package a2a

import (
	"context"
	"log/slog"
	"os"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestCreateTask(t *testing.T) {
	e := NewEngine(testLogger())
	task := e.CreateTask("ctx-1", "analyze_csv", "peer-a")

	if task.ID == "" {
		t.Fatal("task ID should not be empty")
	}
	if task.Status != StatusSubmitted {
		t.Fatalf("expected SUBMITTED, got %s", task.Status)
	}
	if task.ContextID != "ctx-1" {
		t.Fatalf("expected ctx-1, got %s", task.ContextID)
	}
}

func TestValidTransitions(t *testing.T) {
	e := NewEngine(testLogger())

	tests := []struct {
		name       string
		transitions []TaskStatus
		wantErr    bool
	}{
		{
			name:        "submitted -> working -> completed",
			transitions: []TaskStatus{StatusWorking, StatusCompleted},
		},
		{
			name:        "submitted -> working -> failed",
			transitions: []TaskStatus{StatusWorking, StatusFailed},
		},
		{
			name:        "submitted -> rejected",
			transitions: []TaskStatus{StatusRejected},
		},
		{
			name:        "submitted -> canceled",
			transitions: []TaskStatus{StatusCanceled},
		},
		{
			name:        "submitted -> working -> input_required -> working -> completed",
			transitions: []TaskStatus{StatusWorking, StatusInputRequired, StatusWorking, StatusCompleted},
		},
		{
			name:        "submitted -> completed is invalid",
			transitions: []TaskStatus{StatusCompleted},
			wantErr:     true,
		},
		{
			name:        "working -> submitted is invalid",
			transitions: []TaskStatus{StatusWorking, StatusSubmitted},
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := e.CreateTask("", "", "peer-a")

			var lastErr error
			for _, status := range tt.transitions {
				lastErr = e.TransitionTask(context.Background(), task.ID, status)
				if lastErr != nil {
					break
				}
			}

			if tt.wantErr && lastErr == nil {
				t.Fatal("expected error but got nil")
			}
			if !tt.wantErr && lastErr != nil {
				t.Fatalf("unexpected error: %v", lastErr)
			}
		})
	}
}

func TestTerminalStateRejectsTransition(t *testing.T) {
	e := NewEngine(testLogger())
	task := e.CreateTask("", "", "peer-a")

	_ = e.TransitionTask(context.Background(), task.ID, StatusWorking)
	_ = e.TransitionTask(context.Background(), task.ID, StatusCompleted)

	err := e.TransitionTask(context.Background(), task.ID, StatusFailed)
	if err == nil {
		t.Fatal("expected error when transitioning from terminal state")
	}
}

func TestOnTaskUpdateCallback(t *testing.T) {
	e := NewEngine(testLogger())

	var called bool
	e.SetOnTaskUpdate(func(task *Task) {
		called = true
	})

	task := e.CreateTask("", "", "peer-a")
	_ = e.TransitionTask(context.Background(), task.ID, StatusWorking)

	if !called {
		t.Fatal("onTaskUpdate callback was not called")
	}
}
