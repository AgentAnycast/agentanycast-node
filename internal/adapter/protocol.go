// Package adapter defines the pluggable interfaces for the AgentAnycast
// Connection Layer. Protocol adapters translate wire formats; transport
// adapters carry envelopes across networks.
package adapter

import (
	"context"

	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

// Endpoint describes a protocol-specific network listener (e.g., HTTP
// server, WebSocket listener) that a protocol adapter may expose.
type Endpoint struct {
	Protocol string // e.g. "a2a", "anp", "mcp"
	Address  string // e.g. ":8080", "stdio"
}

// ProtocolAdapter translates between a specific wire protocol and the
// internal Envelope format. Each protocol (A2A, ANP, MCP, custom) has
// its own adapter implementation.
type ProtocolAdapter interface {
	// Name returns the adapter's protocol identifier (e.g. "a2a", "anp").
	Name() string

	// Ingest translates raw protocol bytes into an Envelope.
	// The metadata map may carry transport-level context (e.g., remote peer ID).
	Ingest(ctx context.Context, raw []byte, metadata map[string]string) (*envelope.Envelope, error)

	// Emit translates an Envelope back into protocol-specific bytes
	// suitable for sending over a transport.
	Emit(ctx context.Context, env *envelope.Envelope) ([]byte, error)

	// Endpoints returns any protocol-specific network endpoints this
	// adapter serves (may be empty if the protocol uses the transport
	// layer's endpoints).
	Endpoints() []Endpoint
}
