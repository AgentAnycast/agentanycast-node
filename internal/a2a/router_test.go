package a2a

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

// fakePeerID returns a deterministic peer ID for testing.
func fakePeerID(t *testing.T) peer.ID {
	t.Helper()
	// Use a well-known base58-encoded peer ID (ed25519 identity multihash).
	pid, err := peer.Decode("12D3KooWDpJ7As7BWAwRMfu1VU2WCqNjvq387JEYKDBj4kx6nXTN")
	if err != nil {
		t.Fatalf("failed to decode test peer ID: %v", err)
	}
	return pid
}

func TestRouterHandleSendTask(t *testing.T) {
	engine := NewEngine(testLogger())

	// Track sent messages (we use a no-op send function here).
	router := NewRouter(engine, testLogger(), func(_ interface{}, _ peer.ID, _ []byte) error {
		return nil
	})

	remotePeer := fakePeerID(t)

	// Build a SEND_TASK envelope.
	env := &pb.A2AEnvelope{
		EnvelopeId: "test-env-1",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_SendTask{
			SendTask: &pb.SendTaskPayload{
				TaskId:        "task-123",
				ContextId:     "ctx-abc",
				TargetSkillId: "summarize",
				Message: &pb.Message{
					MessageId: "msg-1",
					Role:      pb.MessageRole_MESSAGE_ROLE_USER,
					Parts: []*pb.Part{
						{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: "hello"}}},
					},
				},
			},
		},
	}

	data, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	// Handle the message.
	router.HandleMessage(remotePeer, data)

	// The engine should now have the task registered.
	task, err := engine.GetTask("task-123")
	if err != nil {
		t.Fatalf("GetTask after HandleMessage: %v", err)
	}
	if task.Status != StatusSubmitted {
		t.Fatalf("expected SUBMITTED, got %s", task.Status)
	}
	if task.ContextID != "ctx-abc" {
		t.Fatalf("expected context ctx-abc, got %s", task.ContextID)
	}
	if task.TargetSkillID != "summarize" {
		t.Fatalf("expected skill summarize, got %s", task.TargetSkillID)
	}
	if task.OriginatorPeerID != remotePeer.String() {
		t.Fatalf("expected originator %s, got %s", remotePeer, task.OriginatorPeerID)
	}

	// The incoming tasks channel should have received the event.
	select {
	case evt := <-router.IncomingTasks():
		if evt.Task.TaskId != "task-123" {
			t.Fatalf("incoming event task ID = %s, want task-123", evt.Task.TaskId)
		}
		if evt.SenderPeer != remotePeer {
			t.Fatalf("incoming event sender = %s, want %s", evt.SenderPeer, remotePeer)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for incoming task event")
	}
}

func TestRouterHandleTaskStatusUpdate(t *testing.T) {
	engine := NewEngine(testLogger())

	router := NewRouter(engine, testLogger(), func(_ interface{}, _ peer.ID, _ []byte) error {
		return nil
	})

	remotePeer := fakePeerID(t)

	// First, create a task in the engine so we can update it.
	task := engine.CreateTask("ctx-1", "skill-1", remotePeer.String())

	// Transition to WORKING first (required before COMPLETED).
	if err := engine.TransitionTask(task.ID, StatusWorking); err != nil {
		t.Fatalf("transition to WORKING: %v", err)
	}

	// Subscribe to updates for this task.
	updateCh := router.SubscribeTaskUpdates(task.ID)
	defer router.UnsubscribeTaskUpdates(task.ID, updateCh)

	// Build a TASK_STATUS_UPDATE envelope -> COMPLETED (via handleTaskComplete).
	env := &pb.A2AEnvelope{
		EnvelopeId: "test-env-2",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_TASK_STATUS_UPDATE,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_TaskStatusUpdate{
			TaskStatusUpdate: &pb.TaskStatusUpdatePayload{
				TaskId: task.ID,
				Status: pb.TaskStatus_TASK_STATUS_COMPLETED,
			},
		},
	}

	data, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	router.HandleMessage(remotePeer, data)

	// Verify the engine state changed.
	updated, err := engine.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask after status update: %v", err)
	}
	if updated.Status != StatusCompleted {
		t.Fatalf("expected COMPLETED, got %s", updated.Status)
	}

	// Verify the subscriber received the update event.
	select {
	case evt := <-updateCh:
		if evt.TaskID != task.ID {
			t.Fatalf("update event task ID = %s, want %s", evt.TaskID, task.ID)
		}
		if evt.Status != pb.TaskStatus_TASK_STATUS_COMPLETED {
			t.Fatalf("update event status = %v, want COMPLETED", evt.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task update event")
	}
}

func TestRouterHandleInvalidStatusUpdate(t *testing.T) {
	engine := NewEngine(testLogger())

	router := NewRouter(engine, testLogger(), func(_ interface{}, _ peer.ID, _ []byte) error {
		return nil
	})

	remotePeer := fakePeerID(t)

	// Create a task in SUBMITTED state.
	task := engine.CreateTask("ctx-1", "skill-1", remotePeer.String())

	// Try to transition directly to COMPLETED (invalid: must go through WORKING first).
	env := &pb.A2AEnvelope{
		EnvelopeId: "test-env-3",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_TASK_STATUS_UPDATE,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_TaskStatusUpdate{
			TaskStatusUpdate: &pb.TaskStatusUpdatePayload{
				TaskId: task.ID,
				Status: pb.TaskStatus_TASK_STATUS_COMPLETED,
			},
		},
	}

	data, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// This should not crash, just log a warning.
	router.HandleMessage(remotePeer, data)

	// Task should remain in SUBMITTED.
	updated, err := engine.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if updated.Status != StatusSubmitted {
		t.Fatalf("expected SUBMITTED (unchanged), got %s", updated.Status)
	}
}
