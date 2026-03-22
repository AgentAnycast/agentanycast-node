package envelope

import (
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
