package envelope

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	env := New(TypeTask, "12D3KooWTarget", []byte("payload"))

	if env.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if env.Type != TypeTask {
		t.Fatalf("expected TypeTask, got %s", env.Type)
	}
	if env.Target != "12D3KooWTarget" {
		t.Fatalf("expected target 12D3KooWTarget, got %s", env.Target)
	}
	if string(env.Payload) != "payload" {
		t.Fatalf("expected payload 'payload', got %s", env.Payload)
	}
	if env.Metadata == nil {
		t.Fatal("expected non-nil metadata")
	}
	if env.Timestamp.IsZero() {
		t.Fatal("expected non-zero timestamp")
	}
	if time.Since(env.Timestamp) > time.Second {
		t.Fatal("timestamp too old")
	}
}

func TestSetMeta(t *testing.T) {
	env := &Envelope{}
	env.SetMeta("key", "value")

	if env.Meta("key") != "value" {
		t.Fatalf("expected 'value', got %q", env.Meta("key"))
	}
	if env.Meta("missing") != "" {
		t.Fatalf("expected empty for missing key, got %q", env.Meta("missing"))
	}
}

func TestMetaNilMap(t *testing.T) {
	env := &Envelope{}
	if env.Meta("anything") != "" {
		t.Fatal("expected empty for nil metadata")
	}
}

// BenchmarkEnvelopeMarshal measures the cost of JSON-serializing an Envelope.
func BenchmarkEnvelopeMarshal(b *testing.B) {
	env := New(TypeTask, "12D3KooWTarget", []byte(`{"task_id":"t-1","message":"hello"}`))
	env.SetMeta(MetaKeyProtocol, "a2a")
	env.SetMeta(MetaKeySkill, "summarize")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEnvelopeUnmarshal measures the cost of JSON-deserializing an Envelope.
func BenchmarkEnvelopeUnmarshal(b *testing.B) {
	env := New(TypeTask, "12D3KooWTarget", []byte(`{"task_id":"t-1","message":"hello"}`))
	env.SetMeta(MetaKeyProtocol, "a2a")
	env.SetMeta(MetaKeySkill, "summarize")

	data, err := json.Marshal(env)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out Envelope
		if err := json.Unmarshal(data, &out); err != nil {
			b.Fatal(err)
		}
	}
}
