// Package http provides a TransportAdapter implementation for HTTP-based
// A2A agents. It wraps the existing internal/bridge.OutboundClient.
package http

import (
	"context"
	"fmt"
	"strings"

	"github.com/agentanycast/agentanycast-node/internal/adapter"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

// BridgeSender abstracts the outbound HTTP bridge's send capability.
// This avoids a direct import of internal/bridge, keeping the adapter
// package loosely coupled.
type BridgeSender interface {
	SendRaw(ctx context.Context, targetURL string, data []byte) ([]byte, error)
}

// Transport implements adapter.TransportAdapter for HTTP targets.
// It wraps the existing HTTP Bridge's outbound client.
type Transport struct {
	sender BridgeSender
}

// Compile-time interface check.
var _ adapter.TransportAdapter = (*Transport)(nil)

// New creates a new HTTP transport adapter.
// If sender is nil, Send will always return an error (HTTP bridge disabled).
func New(sender BridgeSender) *Transport {
	return &Transport{sender: sender}
}

// Name returns "http".
func (t *Transport) Name() string { return "http" }

// Send delivers an envelope to an HTTP URL target.
func (t *Transport) Send(ctx context.Context, target string, env *envelope.Envelope) error {
	if t.sender == nil {
		return fmt.Errorf("http: bridge not configured")
	}
	_, err := t.sender.SendRaw(ctx, target, env.Payload)
	return err
}

// Subscribe returns nil — HTTP transport is outbound-only in v0.7.
// Inbound HTTP is handled by the bridge server directly.
func (t *Transport) Subscribe(_ context.Context) (<-chan adapter.InboundMessage, error) {
	ch := make(chan adapter.InboundMessage)
	close(ch) // immediately closed — no inbound via this adapter
	return ch, nil
}

// MatchesTarget returns true if the target looks like an HTTP(S) URL.
func (t *Transport) MatchesTarget(target string) bool {
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
}

// LocalAddrs returns nil — HTTP transport is outbound-only.
func (t *Transport) LocalAddrs() []string { return nil }

// Close is a no-op.
func (t *Transport) Close() error { return nil }
