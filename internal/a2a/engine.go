// Package a2a implements the A2A protocol engine — Task state machine,
// message routing, and Agent Card management.
package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/agentanycast/agentanycast-node/internal/store"
	"github.com/agentanycast/agentanycast-node/internal/telemetry"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TaskStatus mirrors the A2A protocol task statuses.
type TaskStatus int

const (
	StatusUnspecified   TaskStatus = 0
	StatusSubmitted     TaskStatus = 1
	StatusWorking       TaskStatus = 2
	StatusInputRequired TaskStatus = 3
	StatusCompleted     TaskStatus = 4
	StatusFailed        TaskStatus = 5
	StatusCanceled      TaskStatus = 6
	StatusRejected      TaskStatus = 7
)

func (s TaskStatus) String() string {
	switch s {
	case StatusSubmitted:
		return "SUBMITTED"
	case StatusWorking:
		return "WORKING"
	case StatusInputRequired:
		return "INPUT_REQUIRED"
	case StatusCompleted:
		return "COMPLETED"
	case StatusFailed:
		return "FAILED"
	case StatusCanceled:
		return "CANCELED"
	case StatusRejected:
		return "REJECTED"
	default:
		return "UNSPECIFIED"
	}
}

// IsTerminal returns true if the status is a final state.
func (s TaskStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCanceled, StatusRejected:
		return true
	}
	return false
}

// Task represents an A2A task with its current state.
type Task struct {
	ID              string
	ContextID       string
	Status          TaskStatus
	TargetSkillID   string
	OriginatorPeerID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// validTransitions defines the allowed state transitions.
var validTransitions = map[TaskStatus][]TaskStatus{
	StatusSubmitted:     {StatusWorking, StatusRejected, StatusCanceled},
	StatusWorking:       {StatusCompleted, StatusFailed, StatusCanceled, StatusInputRequired},
	StatusInputRequired: {StatusWorking, StatusCanceled},
}

// Engine manages the lifecycle of A2A tasks.
type Engine struct {
	mu       sync.RWMutex
	tasks    map[string]*Task
	logger   *slog.Logger
	store    *store.Store

	// Callbacks
	onTaskUpdate func(task *Task)
}

// NewEngine creates a new A2A protocol engine.
func NewEngine(logger *slog.Logger) *Engine {
	return &Engine{
		tasks:  make(map[string]*Task),
		logger: logger,
	}
}

// SetStore attaches a persistent store to the engine.
// When set, tasks are persisted on every state change and can be recovered on startup.
func (e *Engine) SetStore(s *store.Store) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.store = s
}

// persistTask serializes and persists a task to the store.
// Must be called with e.mu held or after the in-memory update is complete.
func (e *Engine) persistTask(task *Task) {
	if e.store == nil {
		return
	}
	data, err := json.Marshal(task)
	if err != nil {
		e.logger.Warn("failed to marshal task for persistence", "task_id", task.ID, "error", err)
		return
	}
	if err := e.store.PutTask(task.ID, data); err != nil {
		e.logger.Warn("failed to persist task", "task_id", task.ID, "error", err)
	}
}

// RecoverTasks loads all persisted tasks from the store into memory.
// Should be called once at startup after SetStore.
func (e *Engine) RecoverTasks() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.store == nil {
		return nil
	}

	ids, err := e.store.ListTasks()
	if err != nil {
		return fmt.Errorf("list stored tasks: %w", err)
	}

	recovered := 0
	for _, id := range ids {
		data, err := e.store.GetTask(id)
		if err != nil {
			e.logger.Warn("failed to load task from store", "task_id", id, "error", err)
			continue
		}
		var task Task
		if err := json.Unmarshal(data, &task); err != nil {
			e.logger.Warn("failed to unmarshal stored task", "task_id", id, "error", err)
			continue
		}
		e.tasks[task.ID] = &task
		recovered++
	}

	e.logger.Info("recovered tasks from store", "count", recovered)
	return nil
}

// SetOnTaskUpdate sets the callback invoked on every task state change.
func (e *Engine) SetOnTaskUpdate(fn func(task *Task)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onTaskUpdate = fn
}

// CreateTask creates a new task in SUBMITTED state.
func (e *Engine) CreateTask(contextID, targetSkillID, originatorPeerID string) *Task {
	task := &Task{
		ID:               uuid.New().String(),
		ContextID:        contextID,
		Status:           StatusSubmitted,
		TargetSkillID:    targetSkillID,
		OriginatorPeerID: originatorPeerID,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	e.mu.Lock()
	e.tasks[task.ID] = task
	e.mu.Unlock()

	e.persistTask(task)

	e.logger.Info("task created", "task_id", task.ID, "status", task.Status)
	return task
}

// RegisterTask registers an externally created task (e.g. received from network).
func (e *Engine) RegisterTask(task *Task) {
	e.mu.Lock()
	e.tasks[task.ID] = task
	e.mu.Unlock()

	e.persistTask(task)
}

// TransitionTask attempts to move a task to a new status.
// Returns an error if the transition is not allowed.
func (e *Engine) TransitionTask(ctx context.Context, taskID string, newStatus TaskStatus) error { //nolint:cyclop
	ctx, span := telemetry.Tracer("agentanycast.a2a").Start(ctx, "a2a.task.transition",
		trace.WithAttributes(
			attribute.String("task_id", taskID),
			attribute.String("to_status", newStatus.String()),
		),
	)
	defer span.End()
	_ = ctx // available for future child spans
	e.mu.Lock()
	defer e.mu.Unlock()

	task, ok := e.tasks[taskID]
	if !ok {
		return fmt.Errorf("task not found: %s", taskID)
	}

	if task.Status.IsTerminal() {
		return fmt.Errorf("task %s is in terminal state %s", taskID, task.Status)
	}

	allowed, ok := validTransitions[task.Status]
	if !ok {
		return fmt.Errorf("no transitions defined from state %s", task.Status)
	}

	valid := false
	for _, s := range allowed {
		if s == newStatus {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid transition: %s -> %s for task %s", task.Status, newStatus, taskID)
	}

	oldStatus := task.Status
	span.SetAttributes(attribute.String("from_status", oldStatus.String()))
	task.Status = newStatus
	task.UpdatedAt = time.Now()

	// Persist to store while still holding the lock.
	if e.store != nil {
		data, err := json.Marshal(task)
		if err != nil {
			e.logger.Warn("failed to marshal task for persistence", "task_id", taskID, "error", err)
		} else if err := e.store.PutTask(taskID, data); err != nil {
			e.logger.Warn("failed to persist task", "task_id", taskID, "error", err)
		}
	}

	e.logger.Info("task status changed",
		"task_id", taskID,
		"from", oldStatus,
		"to", newStatus,
	)

	if e.onTaskUpdate != nil {
		e.onTaskUpdate(task)
	}

	return nil
}

// GetTask returns a task by ID.
func (e *Engine) GetTask(taskID string) (*Task, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	task, ok := e.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	return task, nil
}

// ListTasks returns all tasks.
func (e *Engine) ListTasks() []*Task {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tasks := make([]*Task, 0, len(e.tasks))
	for _, t := range e.tasks {
		tasks = append(tasks, t)
	}
	return tasks
}
