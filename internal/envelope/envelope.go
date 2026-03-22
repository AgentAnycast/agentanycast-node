// Package envelope defines the protocol-neutral message unit that flows
// through the AgentAnycast Connection Core. Protocol adapters translate
// their native formats to/from Envelope. Transport adapters carry
// Envelopes over the wire.
package envelope

import (
	"time"

	"github.com/google/uuid"
)

// Type classifies what the envelope carries.
type Type string

const (
	TypeTask         Type = "TASK"
	TypeTaskUpdate   Type = "TASK_UPDATE"
	TypeTaskComplete Type = "TASK_COMPLETE"
	TypeTaskFail     Type = "TASK_FAIL"
	TypeTaskCancel   Type = "TASK_CANCEL"
	TypeACK          Type = "ACK"
	TypeStreamStart  Type = "STREAM_START"
	TypeStreamChunk  Type = "STREAM_CHUNK"
	TypeStreamEnd    Type = "STREAM_END"
	TypeCard         Type = "CARD"
	TypeCustom       Type = "CUSTOM"
)

// MetaKeyProtocol is the metadata key that identifies which protocol
// adapter produced or should consume this envelope.
const MetaKeyProtocol = "protocol"

// MetaKeyTraceParent is the W3C Trace Context traceparent header.
const MetaKeyTraceParent = "traceparent"

// MetaKeyTraceState is the W3C Trace Context tracestate header.
const MetaKeyTraceState = "tracestate"

// MetaKeySkill identifies the target skill for routing and access control.
const MetaKeySkill = "skill"

// AgentIdentity identifies the source or target of an envelope.
type AgentIdentity struct {
	PeerID string
	DIDKey string
}

// Envelope is the protocol-neutral message unit flowing through the
// Connection Core. It carries enough metadata for routing and tracing
// while keeping the actual protocol payload opaque ([]byte).
type Envelope struct {
	// ID is a globally unique identifier for this envelope.
	ID string

	// Type classifies the envelope (TASK, ACK, STREAM, etc.).
	Type Type

	// Source identifies the sender.
	Source AgentIdentity

	// Target identifies the destination. The format is transport-dependent:
	// a libp2p PeerID, an HTTP URL, a skill name, a NATS subject, etc.
	Target string

	// Payload carries the protocol-specific serialized data.
	// In v0.7 this is the raw bytes from the originating protocol
	// (e.g., serialized pb.A2AEnvelope). Future versions may
	// normalize this to a canonical format.
	Payload []byte

	// Metadata holds protocol-specific and tracing key-value pairs.
	Metadata map[string]string

	// Timestamp records when the envelope was created.
	Timestamp time.Time
}

// New creates an Envelope with a generated UUID and current timestamp.
func New(typ Type, target string, payload []byte) *Envelope {
	return &Envelope{
		ID:        uuid.New().String(),
		Type:      typ,
		Target:    target,
		Payload:   payload,
		Metadata:  make(map[string]string),
		Timestamp: time.Now(),
	}
}

// SetMeta sets a metadata key-value pair, initializing the map if needed.
func (e *Envelope) SetMeta(key, value string) {
	if e.Metadata == nil {
		e.Metadata = make(map[string]string)
	}
	e.Metadata[key] = value
}

// Meta returns a metadata value by key, or empty string if not present.
func (e *Envelope) Meta(key string) string {
	if e.Metadata == nil {
		return ""
	}
	return e.Metadata[key]
}
