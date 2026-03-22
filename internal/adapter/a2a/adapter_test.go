package a2a

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

func TestAdapterName(t *testing.T) {
	a := New()
	if a.Name() != "a2a" {
		t.Fatalf("expected 'a2a', got %q", a.Name())
	}
}

func TestAdapterEndpoints(t *testing.T) {
	a := New()
	if eps := a.Endpoints(); eps != nil {
		t.Fatalf("expected nil endpoints, got %v", eps)
	}
}

func TestIngestSendTask(t *testing.T) {
	a := New()

	pbEnv := &pb.A2AEnvelope{
		EnvelopeId:  "test-id-123",
		Type:        pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK,
		Timestamp:   timestamppb.Now(),
		TraceParent: "00-abc123-def456-01",
		TraceState:  "vendor=state",
	}
	raw, err := proto.Marshal(pbEnv)
	if err != nil {
		t.Fatal(err)
	}

	env, err := a.Ingest(context.Background(), raw, map[string]string{"remote_peer": "peer1"})
	if err != nil {
		t.Fatal(err)
	}

	if env.ID != "test-id-123" {
		t.Fatalf("expected ID 'test-id-123', got %q", env.ID)
	}
	if env.Type != envelope.TypeTask {
		t.Fatalf("expected TypeTask, got %s", env.Type)
	}
	if env.Meta(envelope.MetaKeyTraceParent) != "00-abc123-def456-01" {
		t.Fatalf("trace_parent mismatch: %q", env.Meta(envelope.MetaKeyTraceParent))
	}
	if env.Meta(envelope.MetaKeyTraceState) != "vendor=state" {
		t.Fatalf("trace_state mismatch: %q", env.Meta(envelope.MetaKeyTraceState))
	}
	if env.Meta(envelope.MetaKeyProtocol) != "a2a" {
		t.Fatalf("expected protocol 'a2a', got %q", env.Meta(envelope.MetaKeyProtocol))
	}
	if env.Meta("remote_peer") != "peer1" {
		t.Fatalf("expected remote_peer 'peer1', got %q", env.Meta("remote_peer"))
	}
}

func TestIngestAllTypes(t *testing.T) {
	a := New()

	tests := []struct {
		pbType  pb.EnvelopeType
		envType envelope.Type
	}{
		{pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK, envelope.TypeTask},
		{pb.EnvelopeType_ENVELOPE_TYPE_TASK_STATUS_UPDATE, envelope.TypeTaskUpdate},
		{pb.EnvelopeType_ENVELOPE_TYPE_TASK_COMPLETE, envelope.TypeTaskComplete},
		{pb.EnvelopeType_ENVELOPE_TYPE_TASK_FAIL, envelope.TypeTaskFail},
		{pb.EnvelopeType_ENVELOPE_TYPE_TASK_CANCEL, envelope.TypeTaskCancel},
		{pb.EnvelopeType_ENVELOPE_TYPE_ACK, envelope.TypeACK},
		{pb.EnvelopeType_ENVELOPE_TYPE_STREAM_START, envelope.TypeStreamStart},
		{pb.EnvelopeType_ENVELOPE_TYPE_STREAM_CHUNK, envelope.TypeStreamChunk},
		{pb.EnvelopeType_ENVELOPE_TYPE_STREAM_END, envelope.TypeStreamEnd},
	}

	for _, tt := range tests {
		raw, _ := proto.Marshal(&pb.A2AEnvelope{
			EnvelopeId: "id",
			Type:       tt.pbType,
		})
		env, err := a.Ingest(context.Background(), raw, nil)
		if err != nil {
			t.Fatalf("Ingest(%s): %v", tt.pbType, err)
		}
		if env.Type != tt.envType {
			t.Fatalf("Ingest(%s): expected %s, got %s", tt.pbType, tt.envType, env.Type)
		}
	}
}

func TestIngestInvalidBytes(t *testing.T) {
	a := New()
	_, err := a.Ingest(context.Background(), []byte("not protobuf"), nil)
	if err == nil {
		t.Fatal("expected error for invalid protobuf bytes")
	}
}

func TestEmitPassthrough(t *testing.T) {
	a := New()

	original := []byte("serialized-proto-bytes")
	env := &envelope.Envelope{Payload: original}

	out, err := a.Emit(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(original) {
		t.Fatalf("expected passthrough, got %q", out)
	}
}

func TestEmitNilPayload(t *testing.T) {
	a := New()
	_, err := a.Emit(context.Background(), &envelope.Envelope{})
	if err == nil {
		t.Fatal("expected error for nil payload")
	}
}

func TestIngestEmitRoundTrip(t *testing.T) {
	a := New()

	pbEnv := &pb.A2AEnvelope{
		EnvelopeId: "roundtrip-id",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_TASK_COMPLETE,
		Timestamp:  timestamppb.Now(),
	}
	raw, _ := proto.Marshal(pbEnv)

	env, err := a.Ingest(context.Background(), raw, nil)
	if err != nil {
		t.Fatal(err)
	}

	out, err := a.Emit(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}

	// Verify round-trip: raw bytes are preserved.
	if string(out) != string(raw) {
		t.Fatal("round-trip failed: output bytes differ from input")
	}
}
