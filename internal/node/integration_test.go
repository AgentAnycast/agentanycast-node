package node

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

// TestIntegrationFullTaskLifecycle tests a complete task flow between two hosts:
// host1 sends a SEND_TASK → host2 receives → host2 sends STATUS_UPDATE(WORKING) →
// host2 sends TASK_COMPLETE → host1 receives completion.
func TestIntegrationFullTaskLifecycle(t *testing.T) {
	host1 := newTestHost(t)
	defer host1.Close()
	host2 := newTestHost(t)
	defer host2.Close()

	// Channel for host1 to receive responses from host2.
	host1Received := make(chan *pb.A2AEnvelope, 10)
	host1.RegisterA2AProtocol(func(remotePeer peer.ID, data []byte) {
		var env pb.A2AEnvelope
		if err := proto.Unmarshal(data, &env); err == nil {
			host1Received <- &env
		}
	})

	// Channel for host2 to receive tasks from host1.
	host2Received := make(chan *pb.A2AEnvelope, 10)
	host2.RegisterA2AProtocol(func(remotePeer peer.ID, data []byte) {
		var env pb.A2AEnvelope
		if err := proto.Unmarshal(data, &env); err == nil {
			host2Received <- &env
		}
	})

	// Connect host1 -> host2.
	host2Info := host2.Peerstore().PeerInfo(host2.ID())
	if err := host1.Connect(context.Background(), host2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Step 1: host1 sends SEND_TASK to host2.
	sendEnv := &pb.A2AEnvelope{
		EnvelopeId: "lifecycle-send-1",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_SendTask{
			SendTask: &pb.SendTaskPayload{
				TaskId:        "lifecycle-task-1",
				ContextId:     "ctx-lifecycle",
				TargetSkillId: "summarize",
				Message: &pb.Message{
					Role: pb.MessageRole_MESSAGE_ROLE_USER,
					Parts: []*pb.Part{
						{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: "Summarize this document"}}},
					},
				},
			},
		},
	}
	sendData, _ := proto.Marshal(sendEnv)
	if err := host1.SendA2AMessage(context.Background(), host2.ID(), sendData, testLogger()); err != nil {
		t.Fatalf("host1 SendA2AMessage: %v", err)
	}

	// Step 2: host2 receives the task.
	select {
	case env := <-host2Received:
		if env.Type != pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK {
			t.Fatalf("host2 got type %v, want SEND_TASK", env.Type)
		}
		payload := env.GetSendTask()
		if payload.TaskId != "lifecycle-task-1" {
			t.Fatalf("task ID = %q, want lifecycle-task-1", payload.TaskId)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host2 timed out waiting for SEND_TASK")
	}

	// Step 3: host2 sends STATUS_UPDATE(WORKING) back to host1.
	workingEnv := &pb.A2AEnvelope{
		EnvelopeId: "lifecycle-working-1",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_TASK_STATUS_UPDATE,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_TaskStatusUpdate{
			TaskStatusUpdate: &pb.TaskStatusUpdatePayload{
				TaskId: "lifecycle-task-1",
				Status: pb.TaskStatus_TASK_STATUS_WORKING,
			},
		},
	}
	workingData, _ := proto.Marshal(workingEnv)
	if err := host2.SendA2AMessage(context.Background(), host1.ID(), workingData, testLogger()); err != nil {
		t.Fatalf("host2 send WORKING: %v", err)
	}

	// Step 4: host1 receives WORKING update.
	select {
	case env := <-host1Received:
		if env.Type != pb.EnvelopeType_ENVELOPE_TYPE_TASK_STATUS_UPDATE {
			t.Fatalf("host1 got type %v, want TASK_STATUS_UPDATE", env.Type)
		}
		payload := env.GetTaskStatusUpdate()
		if payload.Status != pb.TaskStatus_TASK_STATUS_WORKING {
			t.Fatalf("status = %v, want WORKING", payload.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host1 timed out waiting for WORKING update")
	}

	// Step 5: host2 sends TASK_COMPLETE back to host1.
	completeEnv := &pb.A2AEnvelope{
		EnvelopeId: "lifecycle-complete-1",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_TASK_COMPLETE,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_TaskComplete{
			TaskComplete: &pb.TaskCompletePayload{
				TaskId: "lifecycle-task-1",
				Artifacts: []*pb.Artifact{
					{
						ArtifactId: "art-1",
						Name:       "summary",
						Parts: []*pb.Part{
							{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: "The document is about X."}}},
						},
					},
				},
			},
		},
	}
	completeData, _ := proto.Marshal(completeEnv)
	if err := host2.SendA2AMessage(context.Background(), host1.ID(), completeData, testLogger()); err != nil {
		t.Fatalf("host2 send COMPLETE: %v", err)
	}

	// Step 6: host1 receives TASK_COMPLETE.
	select {
	case env := <-host1Received:
		if env.Type != pb.EnvelopeType_ENVELOPE_TYPE_TASK_COMPLETE {
			t.Fatalf("host1 got type %v, want TASK_COMPLETE", env.Type)
		}
		payload := env.GetTaskComplete()
		if payload.TaskId != "lifecycle-task-1" {
			t.Fatalf("complete task ID = %q, want lifecycle-task-1", payload.TaskId)
		}
		if len(payload.Artifacts) != 1 {
			t.Fatalf("expected 1 artifact, got %d", len(payload.Artifacts))
		}
		if payload.Artifacts[0].Name != "summary" {
			t.Fatalf("artifact name = %q, want summary", payload.Artifacts[0].Name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host1 timed out waiting for TASK_COMPLETE")
	}
}

// TestIntegrationCardExchangeWithA2ATask tests card exchange followed by task flow,
// verifying peer card availability after exchange.
func TestIntegrationCardExchangeWithA2ATask(t *testing.T) {
	host1 := newTestHost(t)
	defer host1.Close()
	host2 := newTestHost(t)
	defer host2.Close()

	// Prepare protobuf cards.
	card1 := &pb.AgentCard{Name: "Agent-1", Description: "First agent", Version: "1.0"}
	card2 := &pb.AgentCard{Name: "Agent-2", Description: "Second agent", Version: "2.0"}

	card1Bytes, _ := proto.Marshal(card1)
	card2Bytes, _ := proto.Marshal(card2)

	// Register card protocol on host2.
	host2.RegisterCardProtocol(func() []byte { return card2Bytes })

	// Task received channel.
	taskReceived := make(chan *pb.SendTaskPayload, 1)
	host2.RegisterA2AProtocol(func(remotePeer peer.ID, data []byte) {
		var env pb.A2AEnvelope
		if err := proto.Unmarshal(data, &env); err == nil {
			if p := env.GetSendTask(); p != nil {
				taskReceived <- p
			}
		}
	})

	// Connect host1 -> host2.
	host2Info := host2.Peerstore().PeerInfo(host2.ID())
	if err := host1.Connect(context.Background(), host2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Exchange cards.
	if err := host1.ExchangeCard(context.Background(), host2.ID(), card1Bytes); err != nil {
		t.Fatalf("ExchangeCard: %v", err)
	}

	// Verify both hosts have each other's card.
	if _, ok := host1.GetPeerCard(host2.ID()); !ok {
		t.Fatal("host1 missing host2 card after exchange")
	}
	if _, ok := host2.GetPeerCard(host1.ID()); !ok {
		t.Fatal("host2 missing host1 card after exchange")
	}

	// Now send a task from host1 to host2.
	env := &pb.A2AEnvelope{
		EnvelopeId: "card-then-task-1",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_SendTask{
			SendTask: &pb.SendTaskPayload{
				TaskId:        "task-after-card",
				TargetSkillId: "analyze",
			},
		},
	}
	data, _ := proto.Marshal(env)
	if err := host1.SendA2AMessage(context.Background(), host2.ID(), data, testLogger()); err != nil {
		t.Fatalf("SendA2AMessage: %v", err)
	}

	select {
	case payload := <-taskReceived:
		if payload.TaskId != "task-after-card" {
			t.Fatalf("task ID = %q, want task-after-card", payload.TaskId)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for task after card exchange")
	}
}

// TestIntegrationACKExchange tests that an ACK envelope is properly sent and received.
func TestIntegrationACKExchange(t *testing.T) {
	host1 := newTestHost(t)
	defer host1.Close()
	host2 := newTestHost(t)
	defer host2.Close()

	ackReceived := make(chan string, 1)
	host1.RegisterA2AProtocol(func(remotePeer peer.ID, data []byte) {
		var env pb.A2AEnvelope
		if err := proto.Unmarshal(data, &env); err == nil {
			if env.Type == pb.EnvelopeType_ENVELOPE_TYPE_ACK {
				if ack := env.GetAck(); ack != nil {
					ackReceived <- ack.AcknowledgedEnvelopeId
				}
			}
		}
	})

	host2.RegisterA2AProtocol(func(remotePeer peer.ID, data []byte) {
		// host2 sends ACK back to host1 for any message received.
		var env pb.A2AEnvelope
		if err := proto.Unmarshal(data, &env); err == nil {
			ackEnv := &pb.A2AEnvelope{
				EnvelopeId: "ack-response",
				Type:       pb.EnvelopeType_ENVELOPE_TYPE_ACK,
				Timestamp:  timestamppb.Now(),
				Payload: &pb.A2AEnvelope_Ack{
					Ack: &pb.AckPayload{AcknowledgedEnvelopeId: env.EnvelopeId},
				},
			}
			ackData, _ := proto.Marshal(ackEnv)
			rpid := remotePeer
			_ = host2.SendA2AMessage(context.Background(), rpid, ackData, testLogger())
		}
	})

	// Connect.
	host2Info := host2.Peerstore().PeerInfo(host2.ID())
	if err := host1.Connect(context.Background(), host2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Send a message from host1.
	env := &pb.A2AEnvelope{
		EnvelopeId: "needs-ack-1",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK,
		Payload: &pb.A2AEnvelope_SendTask{
			SendTask: &pb.SendTaskPayload{TaskId: "ack-test-task"},
		},
	}
	data, _ := proto.Marshal(env)
	if err := host1.SendA2AMessage(context.Background(), host2.ID(), data, testLogger()); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Wait for ACK back.
	select {
	case ackID := <-ackReceived:
		if ackID != "needs-ack-1" {
			t.Fatalf("ACK acknowledged %q, want needs-ack-1", ackID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ACK")
	}
}

// TestIntegrationTaskFail tests the failure flow: host1 sends task, host2 sends TASK_FAIL.
func TestIntegrationTaskFail(t *testing.T) {
	host1 := newTestHost(t)
	defer host1.Close()
	host2 := newTestHost(t)
	defer host2.Close()

	host1Received := make(chan *pb.A2AEnvelope, 10)
	host1.RegisterA2AProtocol(func(_ peer.ID, data []byte) {
		var env pb.A2AEnvelope
		if err := proto.Unmarshal(data, &env); err == nil {
			host1Received <- &env
		}
	})

	host2Received := make(chan *pb.A2AEnvelope, 10)
	host2.RegisterA2AProtocol(func(_ peer.ID, data []byte) {
		var env pb.A2AEnvelope
		if err := proto.Unmarshal(data, &env); err == nil {
			host2Received <- &env
		}
	})

	host2Info := host2.Peerstore().PeerInfo(host2.ID())
	if err := host1.Connect(context.Background(), host2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// host1 sends task.
	sendEnv := &pb.A2AEnvelope{
		EnvelopeId: "fail-send-1",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_SendTask{
			SendTask: &pb.SendTaskPayload{TaskId: "fail-task-1"},
		},
	}
	sendData, _ := proto.Marshal(sendEnv)
	if err := host1.SendA2AMessage(context.Background(), host2.ID(), sendData, testLogger()); err != nil {
		t.Fatalf("send task: %v", err)
	}

	// host2 receives.
	select {
	case <-host2Received:
	case <-time.After(5 * time.Second):
		t.Fatal("host2 timed out")
	}

	// host2 sends TASK_FAIL.
	failEnv := &pb.A2AEnvelope{
		EnvelopeId: "fail-response-1",
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_TASK_FAIL,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_TaskFail{
			TaskFail: &pb.TaskFailPayload{
				TaskId:       "fail-task-1",
				ErrorMessage: "processing error: out of memory",
			},
		},
	}
	failData, _ := proto.Marshal(failEnv)
	if err := host2.SendA2AMessage(context.Background(), host1.ID(), failData, testLogger()); err != nil {
		t.Fatalf("send fail: %v", err)
	}

	// host1 receives TASK_FAIL.
	select {
	case env := <-host1Received:
		if env.Type != pb.EnvelopeType_ENVELOPE_TYPE_TASK_FAIL {
			t.Fatalf("type = %v, want TASK_FAIL", env.Type)
		}
		payload := env.GetTaskFail()
		if payload.TaskId != "fail-task-1" {
			t.Fatalf("task ID = %q", payload.TaskId)
		}
		if payload.ErrorMessage != "processing error: out of memory" {
			t.Fatalf("error message = %q", payload.ErrorMessage)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host1 timed out waiting for TASK_FAIL")
	}
}
