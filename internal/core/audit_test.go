package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditLogWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	al, err := NewAuditLogger(path, nil)
	if err != nil {
		t.Fatalf("create audit logger: %v", err)
	}
	defer al.Close()

	al.Log(AuditEvent{
		Source: "did:key:z123",
		Target: "12D3KooWPeer",
		Skill:  "translate",
		Action: "allow",
		EnvID:  "env-001",
	})

	// Close to flush.
	if err := al.Close(); err != nil {
		t.Fatalf("close audit logger: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}

	var event AuditEvent
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("parse audit event: %v", err)
	}

	if event.Source != "did:key:z123" {
		t.Fatalf("expected source did:key:z123, got %q", event.Source)
	}
	if event.Action != "allow" {
		t.Fatalf("expected action allow, got %q", event.Action)
	}
	if event.Timestamp == "" {
		t.Fatal("expected timestamp to be set")
	}
}

func TestAuditLogNil(t *testing.T) {
	// A nil AuditLogger should not panic on any method call.
	var al *AuditLogger

	al.Log(AuditEvent{Action: "allow"})

	if err := al.Close(); err != nil {
		t.Fatalf("unexpected error from nil Close: %v", err)
	}
}

func TestAuditLogFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	al, err := NewAuditLogger(path, nil)
	if err != nil {
		t.Fatalf("create audit logger: %v", err)
	}

	// Write multiple events.
	al.Log(AuditEvent{Source: "a", Action: "allow"})
	al.Log(AuditEvent{Source: "b", Action: "deny"})
	al.Log(AuditEvent{Source: "c", Action: "rate_limit"})
	al.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}

	// JSON Lines format: each line is a valid JSON object.
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	for i, line := range lines {
		var event AuditEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line %d: invalid JSON: %v", i, err)
		}
	}
}

func TestAuditLogDisabled(t *testing.T) {
	// Empty path returns nil logger without error.
	al, err := NewAuditLogger("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if al != nil {
		t.Fatal("expected nil logger for empty path")
	}
}
