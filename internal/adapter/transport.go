package adapter

import (
	"context"

	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

// InboundMessage wraps an envelope received from a remote peer,
// along with transport-level source information.
type InboundMessage struct {
	Envelope *envelope.Envelope
	Source   string // transport-specific source identifier (peer ID, IP, etc.)
}

// TransportAdapter provides a pluggable transport layer for carrying
// envelopes between agents. Each transport (libp2p, NATS, WebSocket,
// HTTP, MQTT) has its own adapter implementation.
type TransportAdapter interface {
	// Name returns the transport identifier (e.g. "libp2p", "nats", "http").
	Name() string

	// Send delivers an envelope to the specified target via this transport.
	Send(ctx context.Context, target string, env *envelope.Envelope) error

	// Subscribe returns a channel of inbound messages received via this
	// transport. The channel is closed when the transport shuts down.
	Subscribe(ctx context.Context) (<-chan InboundMessage, error)

	// MatchesTarget returns true if this transport can reach the given
	// target address. For example, libp2p matches PeerIDs, HTTP matches
	// URLs, NATS matches nats:// subjects.
	MatchesTarget(target string) bool

	// LocalAddrs returns the addresses this transport is listening on.
	LocalAddrs() []string

	// Close shuts down the transport and releases resources.
	Close() error
}
