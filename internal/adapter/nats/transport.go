// Package nats provides a TransportAdapter implementation that uses
// NATS as the message transport layer. Each agent subscribes to its
// own subject (prefix + peerID) and publishes to remote agents'
// subjects for delivery.
package nats

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/agentanycast/agentanycast-node/internal/adapter"
	"github.com/agentanycast/agentanycast-node/internal/config"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

// validSubjectRe validates NATS subject characters (alphanumeric, dots, underscores, hyphens).
var validSubjectRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// validateNATSSubject checks that a subject string is non-empty and contains
// only valid characters for NATS subjects.
func validateNATSSubject(subject string) error {
	if subject == "" {
		return fmt.Errorf("nats: empty subject")
	}
	if !validSubjectRe.MatchString(subject) {
		return fmt.Errorf("nats: invalid subject %q (must be alphanumeric, dots, underscores, hyphens)", subject)
	}
	return nil
}

// Transport implements adapter.TransportAdapter using NATS as the
// message bus. It subscribes to a per-agent subject and publishes
// envelopes to remote agents' subjects.
type Transport struct {
	conn      *natsgo.Conn
	sub       *natsgo.Subscription
	protocol  adapter.ProtocolAdapter
	cfg       config.NATSConfig
	localID   string // local agent's peer ID for subscription subject
	inCh      chan adapter.InboundMessage
	logger    *slog.Logger
	closeOnce sync.Once
}

// Compile-time interface check.
var _ adapter.TransportAdapter = (*Transport)(nil)

// New creates a new NATS transport adapter. It connects to the broker,
// subscribes to the local agent's subject, and starts delivering
// inbound messages to the Subscribe channel.
func New(ctx context.Context, cfg config.NATSConfig, localID string, protocol adapter.ProtocolAdapter, logger *slog.Logger) (*Transport, error) {
	opts := []natsgo.Option{
		natsgo.Name("agentanycast-" + truncateID(localID)),
		natsgo.MaxReconnects(-1),
		natsgo.ReconnectWait(2 * time.Second),
	}

	if cfg.AuthUser != "" {
		opts = append(opts, natsgo.UserInfo(cfg.AuthUser, cfg.AuthPass))
	}

	if cfg.TLSEnabled {
		tlsCfg, err := buildTLSConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("nats: build TLS config: %w", err)
		}
		opts = append(opts, natsgo.Secure(tlsCfg))
	}

	conn, err := natsgo.Connect(cfg.Broker, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats: connect to %s: %w", cfg.Broker, err)
	}

	t := &Transport{
		conn:     conn,
		protocol: protocol,
		cfg:      cfg,
		localID:  localID,
		inCh:     make(chan adapter.InboundMessage, 256),
		logger:   logger.With("component", "transport.nats"),
	}

	// Subscribe to this agent's subject: prefix + localID.
	subject := cfg.SubjectPrefix + localID
	if err := validateNATSSubject(subject); err != nil {
		conn.Close()
		return nil, fmt.Errorf("nats: invalid subscription subject: %w", err)
	}
	sub, err := conn.Subscribe(subject, t.handleMessage)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("nats: subscribe to %s: %w", subject, err)
	}
	t.sub = sub

	t.logger.Info("nats transport ready", "broker", cfg.Broker, "subject", subject)
	return t, nil
}

// Name returns "nats".
func (t *Transport) Name() string { return "nats" }

// Send delivers an envelope to a remote agent via NATS. The target
// must be in the form "nats://<subject>".
func (t *Transport) Send(ctx context.Context, target string, env *envelope.Envelope) error {
	subject := strings.TrimPrefix(target, "nats://")
	if err := validateNATSSubject(subject); err != nil {
		return fmt.Errorf("nats: invalid target subject: %w", err)
	}

	data, err := t.protocol.Emit(ctx, env)
	if err != nil {
		return fmt.Errorf("nats: emit envelope: %w", err)
	}

	if err := t.conn.Publish(subject, data); err != nil {
		return fmt.Errorf("nats: publish to subject %s (%d bytes): %w", subject, len(data), err)
	}
	return nil
}

// Subscribe returns a channel of inbound messages received via NATS.
// The channel is closed when the transport shuts down.
func (t *Transport) Subscribe(_ context.Context) (<-chan adapter.InboundMessage, error) {
	return t.inCh, nil
}

// MatchesTarget returns true if the target string starts with "nats://".
func (t *Transport) MatchesTarget(target string) bool {
	return strings.HasPrefix(target, "nats://")
}

// LocalAddrs returns the connected broker URL.
func (t *Transport) LocalAddrs() []string {
	if url := t.conn.ConnectedUrl(); url != "" {
		return []string{url}
	}
	return nil
}

// Close shuts down the NATS subscription and connection.
func (t *Transport) Close() error {
	t.closeOnce.Do(func() {
		if t.sub != nil {
			_ = t.sub.Unsubscribe()
		}
		t.conn.Close()
		close(t.inCh)
		t.logger.Info("nats transport closed")
	})
	return nil
}

// handleMessage is the NATS subscription callback. It translates raw
// bytes into an Envelope via the protocol adapter and delivers it to
// the inbound channel.
func (t *Transport) handleMessage(msg *natsgo.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	env, err := t.protocol.Ingest(ctx, msg.Data, map[string]string{
		"transport":    "nats",
		"nats.subject": msg.Subject,
	})
	if err != nil {
		t.logger.Warn("nats: ingest failed", "subject", msg.Subject, "data_len", len(msg.Data), "error", err)
		return
	}

	select {
	case t.inCh <- adapter.InboundMessage{Envelope: env, Source: msg.Subject}:
	default:
		t.logger.Warn("nats: inbound channel full, dropping envelope", "id", env.ID)
	}
}

// truncateID returns the first 8 characters of an ID for display.
func truncateID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// buildTLSConfig creates a TLS configuration from the NATS config.
func buildTLSConfig(cfg config.NATSConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load TLS keypair: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
}
