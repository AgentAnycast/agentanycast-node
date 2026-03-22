// Package libp2p provides a TransportAdapter implementation that wraps
// the existing internal/node.Host (libp2p peer-to-peer networking).
package libp2p

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/agentanycast/agentanycast-node/internal/adapter"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
	"github.com/agentanycast/agentanycast-node/internal/node"
)

// Transport implements adapter.TransportAdapter using libp2p.
// It wraps node.Host — the existing P2P networking layer — without
// modifying or replacing it.
type Transport struct {
	host     *node.Host
	protocol adapter.ProtocolAdapter
	logger   *slog.Logger
	inCh     chan adapter.InboundMessage
}

// Compile-time interface check.
var _ adapter.TransportAdapter = (*Transport)(nil)

// New creates a new libp2p transport adapter that wraps the given Host.
// The protocol adapter is used to translate raw bytes to/from Envelopes.
func New(host *node.Host, protocol adapter.ProtocolAdapter, logger *slog.Logger) *Transport {
	t := &Transport{
		host:     host,
		protocol: protocol,
		logger:   logger,
		inCh:     make(chan adapter.InboundMessage, 64),
	}
	return t
}

// Name returns "libp2p".
func (t *Transport) Name() string { return "libp2p" }

// Send delivers an envelope to a libp2p peer identified by its PeerID string.
func (t *Transport) Send(ctx context.Context, target string, env *envelope.Envelope) error {
	pid, err := peer.Decode(target)
	if err != nil {
		return fmt.Errorf("libp2p: decode peer ID %q: %w", target, err)
	}

	data, err := t.protocol.Emit(ctx, env)
	if err != nil {
		return fmt.Errorf("libp2p: emit envelope: %w", err)
	}

	return t.host.SendA2AMessage(ctx, pid, data, t.logger)
}

// Subscribe returns a channel of inbound messages. Call RegisterHandler
// first to wire the Host's A2A protocol handler to this channel.
func (t *Transport) Subscribe(_ context.Context) (<-chan adapter.InboundMessage, error) {
	return t.inCh, nil
}

// RegisterHandler wires the Host's A2A protocol stream handler so that
// incoming messages are translated to Envelopes and sent to the
// Subscribe channel. Call this after Host is created but before starting
// the gRPC server.
//
// Note: the existing a2a.Router.HandleMessage path continues to work
// independently — this handler is additive, feeding the Connection Core.
func (t *Transport) RegisterHandler() {
	t.host.RegisterA2AProtocol(func(remotePeer peer.ID, data []byte) {
		env, err := t.protocol.Ingest(context.Background(), data, map[string]string{
			"remote_peer": remotePeer.String(),
			"transport":   "libp2p",
		})
		if err != nil {
			t.logger.Warn("libp2p: ingest failed", "peer", remotePeer, "error", err)
			return
		}
		env.Source = envelope.AgentIdentity{PeerID: remotePeer.String()}

		select {
		case t.inCh <- adapter.InboundMessage{Envelope: env, Source: remotePeer.String()}:
		default:
			t.logger.Warn("libp2p: inbound channel full, dropping envelope", "id", env.ID)
		}
	})
}

// MatchesTarget returns true if the target string is a valid libp2p PeerID.
func (t *Transport) MatchesTarget(target string) bool {
	_, err := peer.Decode(target)
	return err == nil
}

// LocalAddrs returns the multiaddrs this Host is listening on.
func (t *Transport) LocalAddrs() []string {
	addrs := t.host.Addrs()
	strs := make([]string, len(addrs))
	for i, a := range addrs {
		strs[i] = a.String()
	}
	return strs
}

// Close is a no-op — the Host lifecycle is managed by main.go, not
// by the transport adapter.
func (t *Transport) Close() error {
	return nil
}
