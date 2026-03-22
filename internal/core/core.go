// Package core implements the Connection Core — the central message
// dispatcher for the AgentAnycast Connection Layer. It holds registered
// protocol and transport adapters and routes Envelopes between them.
//
// In v0.7, the Core sits alongside the existing A2A Router and libp2p
// Host wiring. It does not replace them — existing code paths continue
// to work unchanged. The Core provides an alternative, protocol-neutral
// dispatch path for new features (MCP Proxy, future transports).
package core

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/agentanycast/agentanycast-node/internal/adapter"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

var tracer = otel.Tracer("agentanycast/core")

// Core is the central message dispatcher for the Connection Layer.
type Core struct {
	mu         sync.RWMutex
	protocols  map[string]adapter.ProtocolAdapter
	transports []adapter.TransportAdapter
	logger     *slog.Logger

	// Enterprise capabilities (v0.8).
	acl       *ACLManager
	rateLimit *RateLimiter
	audit     *AuditLogger

	// E2E encryption key (v0.8).
	localPrivateKey ed25519.PrivateKey
}

// Config holds configuration for the Connection Core.
type Config struct {
	Logger *slog.Logger
}

// New creates a new Connection Core.
func New(cfg Config) *Core {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Core{
		protocols:  make(map[string]adapter.ProtocolAdapter),
		transports: make([]adapter.TransportAdapter, 0),
		logger:     logger,
	}
}

// RegisterProtocol adds a protocol adapter to the Core.
func (c *Core) RegisterProtocol(pa adapter.ProtocolAdapter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.protocols[pa.Name()] = pa
	c.logger.Info("registered protocol adapter", "protocol", pa.Name())
}

// RegisterTransport adds a transport adapter to the Core.
// Transports are checked in registration order for target matching.
func (c *Core) RegisterTransport(ta adapter.TransportAdapter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.transports = append(c.transports, ta)
	c.logger.Info("registered transport adapter", "transport", ta.Name())
}

// Protocol returns a registered protocol adapter by name.
func (c *Core) Protocol(name string) (adapter.ProtocolAdapter, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	pa, ok := c.protocols[name]
	return pa, ok
}

// Transports returns all registered transport adapters.
func (c *Core) Transports() []adapter.TransportAdapter {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]adapter.TransportAdapter, len(c.transports))
	copy(result, c.transports)
	return result
}

// Send dispatches an envelope to the first transport adapter that
// matches the envelope's target. Returns an error if no transport
// can reach the target.
func (c *Core) Send(ctx context.Context, env *envelope.Envelope) error {
	ctx, span := tracer.Start(ctx, "connection.send",
		trace.WithAttributes(
			attribute.String("envelope.id", env.ID),
			attribute.String("envelope.type", string(env.Type)),
			attribute.String("envelope.target", env.Target),
		),
	)
	defer span.End()

	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, t := range c.transports {
		if t.MatchesTarget(env.Target) {
			span.SetAttributes(attribute.String("transport", t.Name()))
			if err := t.Send(ctx, env.Target, env); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return fmt.Errorf("core: send via %s: %w", t.Name(), err)
			}
			return nil
		}
	}

	err := fmt.Errorf("core: no transport matches target %q", env.Target)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	return err
}

// SetEncryptionKey sets the local Ed25519 private key for E2E encryption.
// When set, Route() will encrypt envelope payloads for recipients that
// provide their public key via the "recipient_pubkey" metadata field.
func (c *Core) SetEncryptionKey(priv ed25519.PrivateKey) {
	c.localPrivateKey = priv
}

// SetACLManager configures the ACL manager for access control enforcement.
func (c *Core) SetACLManager(acl *ACLManager) {
	c.acl = acl
}

// SetRateLimiter configures the per-source rate limiter.
func (c *Core) SetRateLimiter(rl *RateLimiter) {
	c.rateLimit = rl
}

// SetAuditLogger configures the audit logger for security events.
func (c *Core) SetAuditLogger(al *AuditLogger) {
	c.audit = al
}

// Route is the recommended entry point for dispatching envelopes. It wraps
// Send with ACL enforcement, rate limiting, and audit logging.
func (c *Core) Route(ctx context.Context, env *envelope.Envelope) error {
	ctx, span := tracer.Start(ctx, "connection.route",
		trace.WithAttributes(
			attribute.String("envelope.id", env.ID),
			attribute.String("envelope.type", string(env.Type)),
			attribute.String("envelope.target", env.Target),
		),
	)
	defer span.End()

	skillID := env.Meta(envelope.MetaKeySkill)
	sourceID := env.Source.DIDKey
	if sourceID == "" {
		sourceID = env.Source.PeerID
	}

	// 1. ACL check.
	if c.acl != nil {
		if err := c.acl.CheckAccess(env.Source, skillID); err != nil {
			if c.audit != nil {
				c.audit.Log(AuditEvent{
					Source: sourceID,
					Target: env.Target,
					Skill:  skillID,
					Action: "deny",
					EnvID:  env.ID,
				})
			}
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
	}

	// 2. Rate limit check.
	if c.rateLimit != nil {
		if !c.rateLimit.Allow(env.Source) {
			rateLimitErr := fmt.Errorf("core: rate limit exceeded for %s", sourceID)
			if c.audit != nil {
				c.audit.Log(AuditEvent{
					Source: sourceID,
					Target: env.Target,
					Skill:  skillID,
					Action: "rate_limit",
					EnvID:  env.ID,
				})
			}
			span.RecordError(rateLimitErr)
			span.SetStatus(codes.Error, rateLimitErr.Error())
			return rateLimitErr
		}
	}

	// 3. Audit: record allowed access.
	if c.audit != nil {
		c.audit.Log(AuditEvent{
			Source: sourceID,
			Target: env.Target,
			Skill:  skillID,
			Action: "allow",
			EnvID:  env.ID,
		})
	}

	// 3.5. E2E encryption (if keys available and envelope has recipient pubkey).
	if c.localPrivateKey != nil {
		if recipientPubKeyHex := env.Meta("recipient_pubkey"); recipientPubKeyHex != "" {
			recipientPubKey, err := hex.DecodeString(recipientPubKeyHex)
			if err == nil && len(recipientPubKey) == ed25519.PublicKeySize {
				encrypted, err := EncryptPayload(env.Payload, c.localPrivateKey, ed25519.PublicKey(recipientPubKey))
				if err != nil {
					span.RecordError(err)
					span.SetStatus(codes.Error, err.Error())
					return fmt.Errorf("core: encrypt payload: %w", err)
				}
				env.Payload = encrypted
				env.SetMeta("encrypted", "nacl-box")
			}
		}
	}

	// 4. Dispatch via Send.
	return c.Send(ctx, env)
}

// ProtocolNames returns the names of all registered protocol adapters.
func (c *Core) ProtocolNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, 0, len(c.protocols))
	for name := range c.protocols {
		names = append(names, name)
	}
	return names
}

// TransportNames returns the names of all registered transport adapters.
func (c *Core) TransportNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, 0, len(c.transports))
	for _, t := range c.transports {
		names = append(names, t.Name())
	}
	return names
}
