package core

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// AuditEvent represents a single auditable action.
type AuditEvent struct {
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`
	Target   string `json:"target"`
	Skill    string `json:"skill,omitempty"`
	Action   string `json:"action"` // "allow", "deny", "rate_limit"
	Transport string `json:"transport,omitempty"`
	EnvID    string `json:"envelope_id,omitempty"`
}

// AuditLogger writes audit events as JSON Lines to a file.
// A nil *AuditLogger is safe to use — all methods are no-ops.
type AuditLogger struct {
	mu     sync.Mutex
	file   *os.File
	enc    *json.Encoder
	logger *slog.Logger
}

// NewAuditLogger creates an audit logger writing to the specified path.
// Returns (nil, nil) if path is empty (audit disabled).
func NewAuditLogger(path string, logger *slog.Logger) (*AuditLogger, error) {
	if path == "" {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open audit log: %w", err)
	}

	return &AuditLogger{
		file:   f,
		enc:    json.NewEncoder(f),
		logger: logger.With("component", "audit"),
	}, nil
}

// Log writes an audit event. Safe to call on a nil receiver.
func (a *AuditLogger) Log(event AuditEvent) {
	if a == nil {
		return
	}
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.enc.Encode(event); err != nil {
		a.logger.Error("failed to write audit event", "error", err)
	}
}

// Close closes the audit log file. Safe to call on a nil receiver.
func (a *AuditLogger) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.file.Close()
}
