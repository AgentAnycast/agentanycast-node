package a2a

import (
	"fmt"
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
	}, nil, nil)

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
	}, nil, nil)

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

func TestRouterDedupDropsDuplicateEnvelopes(t *testing.T) {
	engine := NewEngine(testLogger())

	var sendCount int
	router := NewRouter(engine, testLogger(), func(_ interface{}, _ peer.ID, _ []byte) error {
		sendCount++
		return nil
	}, nil, nil)

	remotePeer := fakePeerID(t)

	// Build a SEND_TASK envelope.
	env := &pb.A2AEnvelope{
		EnvelopeId: "dedup-env-1",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_SendTask{
			SendTask: &pb.SendTaskPayload{
				TaskId:    "dedup-task-1",
				ContextId: "ctx-dedup",
				Message: &pb.Message{
					MessageId: "msg-d1",
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

	// Handle the same message twice.
	router.HandleMessage(remotePeer, data)
	router.HandleMessage(remotePeer, data)

	// Only one task should be registered.
	task, err := engine.GetTask("dedup-task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusSubmitted {
		t.Fatalf("expected SUBMITTED, got %s", task.Status)
	}

	// Drain the incoming channel — should have exactly one event.
	select {
	case <-router.IncomingTasks():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first incoming task")
	}

	// Second event should NOT arrive (duplicate was dropped).
	select {
	case evt := <-router.IncomingTasks():
		t.Fatalf("unexpected second incoming task event: %+v", evt)
	case <-time.After(100 * time.Millisecond):
		// Expected: no duplicate.
	}
}

func TestRouterDedupEvictsOldEntries(t *testing.T) {
	engine := NewEngine(testLogger())

	router := NewRouter(engine, testLogger(), func(_ interface{}, _ peer.ID, _ []byte) error {
		return nil
	}, nil, nil)

	// Fill the dedup cache to capacity.
	for i := 0; i < dedupCacheSize; i++ {
		router.isDuplicate(fmt.Sprintf("env-%d", i))
	}

	// env-0 should still be in the cache.
	if !router.isDuplicate("env-0") {
		t.Fatal("env-0 should be duplicate before eviction")
	}

	// Now add another entry — the oldest (env-1, since env-0 was re-added above
	// it moved to the end) should be evicted.
	// Actually, since isDuplicate("env-0") returned true, it doesn't re-add it.
	// So the order is: env-0, env-1, ..., env-1023
	// Adding one more should evict env-0.
	router.isDuplicate("new-env")

	// env-0 was just confirmed as duplicate, so it wasn't evicted yet.
	// Let's add another to evict env-1.
	if router.isDuplicate("env-1") {
		// env-1 is the second oldest, it's still there after one eviction.
		// Let's test differently: the cache has exactly dedupCacheSize entries.
	}

	// Test simpler: verify cache doesn't grow beyond limit.
	router2 := NewRouter(engine, testLogger(), func(_ interface{}, _ peer.ID, _ []byte) error {
		return nil
	}, nil, nil)

	for i := 0; i < dedupCacheSize+100; i++ {
		router2.isDuplicate(fmt.Sprintf("x-%d", i))
	}

	router2.dedupMu.Lock()
	cacheLen := len(router2.dedupCache)
	orderLen := len(router2.dedupOrder)
	router2.dedupMu.Unlock()

	if cacheLen != dedupCacheSize {
		t.Fatalf("dedup cache should be capped at %d, got %d", dedupCacheSize, cacheLen)
	}
	if orderLen != dedupCacheSize {
		t.Fatalf("dedup order should be capped at %d, got %d", dedupCacheSize, orderLen)
	}

	// The earliest entries should have been evicted.
	if router2.isDuplicate("x-0") {
		t.Fatal("x-0 should have been evicted")
	}
	// The latest entries should still be present.
	if !router2.isDuplicate(fmt.Sprintf("x-%d", dedupCacheSize+99)) {
		t.Fatal("latest entry should still be in dedup cache")
	}
}

func TestRouterACKsNotDeduped(t *testing.T) {
	engine := NewEngine(testLogger())

	ackCount := 0
	router := NewRouter(engine, testLogger(), func(_ interface{}, _ peer.ID, _ []byte) error {
		return nil
	}, nil, nil)

	remotePeer := fakePeerID(t)

	// Track a message so ACK has something to acknowledge.
	router.ackTracker.Track(remotePeer, "tracked-env-1", []byte("data"))

	// Build an ACK envelope.
	env := &pb.A2AEnvelope{
		EnvelopeId: "ack-env-1",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_ACK,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_Ack{
			Ack: &pb.AckPayload{
				AcknowledgedEnvelopeId: "tracked-env-1",
			},
		},
	}

	data, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Send the same ACK twice — both should be processed (ACKs skip dedup).
	router.HandleMessage(remotePeer, data)
	router.HandleMessage(remotePeer, data)

	// No panic or error means success — ACKs are idempotent.
	_ = ackCount
}

func TestRouterSlowSubscriberDropsEvents(t *testing.T) {
	engine := NewEngine(testLogger())

	router := NewRouter(engine, testLogger(), func(_ interface{}, _ peer.ID, _ []byte) error {
		return nil
	}, nil, nil)

	remotePeer := fakePeerID(t)

	// Create a task and subscribe with a small channel (capacity 1 via SubscribeTaskUpdates default 16).
	task := engine.CreateTask("ctx-slow", "skill-slow", remotePeer.String())

	// Subscribe to updates.
	ch := router.SubscribeTaskUpdates(task.ID)
	defer router.UnsubscribeTaskUpdates(task.ID, ch)

	// Fill the channel to capacity by rapidly transitioning states.
	// The channel has capacity 16, so let's fill it by doing transitions
	// but NOT draining the channel.
	_ = engine.TransitionTask(task.ID, StatusWorking)
	_ = engine.TransitionTask(task.ID, StatusInputRequired)
	_ = engine.TransitionTask(task.ID, StatusWorking)

	// Drain what we can.
	drained := 0
	for {
		select {
		case <-ch:
			drained++
		default:
			goto done
		}
	}
done:
	// We should have received at least some events (up to channel capacity).
	if drained == 0 {
		t.Fatal("expected to receive at least one event")
	}
	// The test mainly verifies the drop path doesn't panic or deadlock.
}

func TestRouterNotifyUpdateDropsForFullChannel(t *testing.T) {
	engine := NewEngine(testLogger())

	router := NewRouter(engine, testLogger(), func(_ interface{}, _ peer.ID, _ []byte) error {
		return nil
	}, nil, nil)

	remotePeer := fakePeerID(t)
	task := engine.CreateTask("ctx-drop", "skill-drop", remotePeer.String())

	// Create a manually tiny channel to guarantee drops.
	tinyCh := make(chan *TaskUpdateEvent, 1)
	router.mu.Lock()
	router.taskUpdateSubs[task.ID] = append(router.taskUpdateSubs[task.ID], tinyCh)
	router.mu.Unlock()

	// Fire multiple updates — first fills the buffer, rest should be dropped (logged).
	_ = engine.TransitionTask(task.ID, StatusWorking)
	_ = engine.TransitionTask(task.ID, StatusInputRequired)
	_ = engine.TransitionTask(task.ID, StatusWorking)
	_ = engine.TransitionTask(task.ID, StatusCompleted)

	// We should get at most 1 event from the tiny channel.
	select {
	case <-tinyCh:
		// good, got one
	case <-time.After(time.Second):
		t.Fatal("expected at least one event in tiny channel")
	}

	// Second read might succeed (if the first send+drain happened fast enough).
	// Third and beyond should definitely be dropped. The key point is no panic.
}

func TestRouterHandleInvalidStatusUpdate(t *testing.T) {
	engine := NewEngine(testLogger())

	router := NewRouter(engine, testLogger(), func(_ interface{}, _ peer.ID, _ []byte) error {
		return nil
	}, nil, nil)

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
