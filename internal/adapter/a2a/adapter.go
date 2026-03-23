// Package a2a provides a ProtocolAdapter implementation for the A2A protocol.
// It wraps the existing internal/a2a package (Engine, Router, StreamManager)
// behind the adapter.ProtocolAdapter interface without modifying the
// underlying implementation.
package a2a

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
	"github.com/agentanycast/agentanycast-node/internal/adapter"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

// envelopeTypeMap maps pb.EnvelopeType to the internal envelope.Type.
var envelopeTypeMap = map[pb.EnvelopeType]envelope.Type{
	pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK:          envelope.TypeTask,
	pb.EnvelopeType_ENVELOPE_TYPE_TASK_STATUS_UPDATE:  envelope.TypeTaskUpdate,
	pb.EnvelopeType_ENVELOPE_TYPE_TASK_COMPLETE:       envelope.TypeTaskComplete,
	pb.EnvelopeType_ENVELOPE_TYPE_TASK_FAIL:           envelope.TypeTaskFail,
	pb.EnvelopeType_ENVELOPE_TYPE_TASK_CANCEL:         envelope.TypeTaskCancel,
	pb.EnvelopeType_ENVELOPE_TYPE_ACK:                 envelope.TypeACK,
	pb.EnvelopeType_ENVELOPE_TYPE_STREAM_START:        envelope.TypeStreamStart,
	pb.EnvelopeType_ENVELOPE_TYPE_STREAM_CHUNK:        envelope.TypeStreamChunk,
	pb.EnvelopeType_ENVELOPE_TYPE_STREAM_END:          envelope.TypeStreamEnd,
}

// reverseTypeMap maps internal envelope.Type back to pb.EnvelopeType.
var reverseTypeMap map[envelope.Type]pb.EnvelopeType

func init() {
	reverseTypeMap = make(map[envelope.Type]pb.EnvelopeType, len(envelopeTypeMap))
	for k, v := range envelopeTypeMap {
		reverseTypeMap[v] = k
	}
}

// Adapter implements adapter.ProtocolAdapter for the A2A protocol.
// It translates between serialized pb.A2AEnvelope bytes and the
// protocol-neutral envelope.Envelope type.
//
// In v0.7, the translation uses payload passthrough: the Envelope's
// Payload field carries the original serialized pb.A2AEnvelope bytes
// unchanged. This avoids double-serialization overhead and keeps the
// refactor behavior-preserving.
type Adapter struct{}

// Compile-time interface check.
var _ adapter.ProtocolAdapter = (*Adapter)(nil)

// New creates a new A2A protocol adapter.
func New() *Adapter {
	return &Adapter{}
}

// Name returns "a2a".
func (a *Adapter) Name() string { return "a2a" }

// Ingest translates raw serialized pb.A2AEnvelope bytes into an Envelope.
// It extracts the envelope ID, type, and trace context from the proto
// while keeping the full serialized bytes as the opaque Payload.
func (a *Adapter) Ingest(ctx context.Context, raw []byte, metadata map[string]string) (*envelope.Envelope, error) {
	var pbEnv pb.A2AEnvelope
	if err := proto.Unmarshal(raw, &pbEnv); err != nil {
		return nil, fmt.Errorf("a2a: unmarshal envelope (%d bytes): %w", len(raw), err)
	}

	envType, ok := envelopeTypeMap[pbEnv.Type]
	if !ok {
		envType = envelope.TypeCustom
	}

	env := &envelope.Envelope{
		ID:       pbEnv.EnvelopeId,
		Type:     envType,
		Payload:  raw, // passthrough: carry the original bytes
		Metadata: make(map[string]string),
	}

	// Copy trace context into metadata for the Connection Core.
	if pbEnv.TraceParent != "" {
		env.Metadata[envelope.MetaKeyTraceParent] = pbEnv.TraceParent
	}
	if pbEnv.TraceState != "" {
		env.Metadata[envelope.MetaKeyTraceState] = pbEnv.TraceState
	}

	// Mark the originating protocol.
	env.Metadata[envelope.MetaKeyProtocol] = "a2a"

	// Copy transport-level metadata (e.g., remote peer ID).
	for k, v := range metadata {
		if _, exists := env.Metadata[k]; !exists {
			env.Metadata[k] = v
		}
	}

	// Set timestamp from proto if present.
	if pbEnv.Timestamp != nil {
		env.Timestamp = pbEnv.Timestamp.AsTime()
	}

	return env, nil
}

// Emit translates an Envelope back into serialized A2A protocol bytes.
// In v0.7 passthrough mode, this simply returns the original Payload
// (which is already a serialized pb.A2AEnvelope).
func (a *Adapter) Emit(ctx context.Context, env *envelope.Envelope) ([]byte, error) {
	if env.Payload == nil {
		return nil, fmt.Errorf("a2a: envelope %s has nil payload", env.ID)
	}
	return env.Payload, nil
}

// Endpoints returns nil — the A2A protocol uses the transport layer's
// endpoints (libp2p streams) rather than its own.
func (a *Adapter) Endpoints() []adapter.Endpoint {
	return nil
}
