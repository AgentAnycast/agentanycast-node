// Package a2a implements the A2A protocol engine — Task state machine,
// message routing, and Agent Card management.
package a2a

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
	"github.com/agentanycast/agentanycast-node/internal/telemetry"
)

// SendFunc sends serialized bytes to a remote peer over libp2p.
type SendFunc func(ctx context.Context, pid peer.ID, data []byte) error

// PeerCardFunc returns the raw agent card bytes for a peer, if available.
type PeerCardFunc func(pid peer.ID) ([]byte, bool)

// InboundPolicy checks whether an inbound message from a remote peer should
// be allowed. Implementations may enforce ACL rules, rate limits, and audit
// logging. The method returns nil if the message is permitted.
type InboundPolicy interface {
	CheckInbound(source envelope.AgentIdentity, skillID, envelopeID string) error
}

// dedupCacheSize is the max number of envelope IDs to remember for dedup.
const dedupCacheSize = 1024

// Router handles deserialization, routing, and dispatch of A2A messages
// received over the libp2p network.
type Router struct {
	engine     *Engine
	logger     *slog.Logger
	sendFn     SendFunc
	peerCardFn PeerCardFunc
	ackTracker *AckTracker

	// inboundPolicy enforces ACL, rate limiting, and audit on inbound messages.
	// If nil, all inbound messages are accepted (backward-compatible default).
	inboundPolicy InboundPolicy

	mu              sync.RWMutex
	incomingCh      chan *IncomingTaskEvent
	taskUpdateSubs  map[string][]chan *TaskUpdateEvent

	dedupMu    sync.Mutex
	dedupCache map[string]struct{}
	dedupOrder []string
}

// IncomingTaskEvent is emitted when a remote peer sends us a new task.
type IncomingTaskEvent struct {
	Task       *pb.Task
	SenderCard *pb.AgentCard
	SenderPeer peer.ID
}

// TaskUpdateEvent is emitted when a task's status changes.
type TaskUpdateEvent struct {
	TaskID    string
	Status    pb.TaskStatus
	Message   *pb.Message
	Artifacts []*pb.Artifact
}

// NewRouter creates a new A2A message router.
// peerCardFn is optional — if nil, SenderCard will always be nil in IncomingTaskEvent.
// onMaxRetries is optional — if nil, messages that exceed max ACK retries are dropped.
func NewRouter(engine *Engine, logger *slog.Logger, sendFn SendFunc, peerCardFn PeerCardFunc, onMaxRetries MaxRetriesFunc) *Router {
	r := &Router{
		engine:         engine,
		logger:         logger,
		sendFn:         sendFn,
		peerCardFn:     peerCardFn,
		ackTracker:     NewAckTracker(sendFn, logger, onMaxRetries),
		incomingCh:     make(chan *IncomingTaskEvent, 64),
		taskUpdateSubs: make(map[string][]chan *TaskUpdateEvent),
		dedupCache:     make(map[string]struct{}, dedupCacheSize),
	}

	// Wire engine callback to push updates to subscribers.
	engine.SetOnTaskUpdate(func(t *Task) {
		r.mu.RLock()
		subs := r.taskUpdateSubs[t.ID]
		r.mu.RUnlock()

		evt := &TaskUpdateEvent{
			TaskID: t.ID,
			Status: TaskStatusToProto(t.Status),
		}
		for _, ch := range subs {
			select {
			case ch <- evt:
			default:
				r.logger.Warn("dropping task update for slow subscriber", "task_id", t.ID, "status", t.Status)
			}
		}
	})

	return r
}

// SetInboundPolicy configures the inbound policy checker (ACL, rate limiting,
// audit). When set, HandleMessage will reject messages that violate the policy.
func (r *Router) SetInboundPolicy(policy InboundPolicy) {
	r.inboundPolicy = policy
}

// IncomingTasks returns the channel of new incoming task events.
func (r *Router) IncomingTasks() <-chan *IncomingTaskEvent {
	return r.incomingCh
}

// SubscribeTaskUpdates returns a channel that receives updates for a specific task.
func (r *Router) SubscribeTaskUpdates(taskID string) chan *TaskUpdateEvent {
	ch := make(chan *TaskUpdateEvent, 16)
	r.mu.Lock()
	r.taskUpdateSubs[taskID] = append(r.taskUpdateSubs[taskID], ch)
	r.mu.Unlock()
	return ch
}

// UnsubscribeTaskUpdates removes a subscriber channel for a task.
func (r *Router) UnsubscribeTaskUpdates(taskID string, ch chan *TaskUpdateEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	subs := r.taskUpdateSubs[taskID]
	for i, s := range subs {
		if s == ch {
			r.taskUpdateSubs[taskID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	if len(r.taskUpdateSubs[taskID]) == 0 {
		delete(r.taskUpdateSubs, taskID)
	}
	close(ch)
}

// HandleMessage processes an incoming A2A message from a remote peer.
func (r *Router) HandleMessage(remotePeer peer.ID, data []byte) {
	var env pb.A2AEnvelope
	if err := proto.Unmarshal(data, &env); err != nil {
		r.logger.Warn("failed to unmarshal A2AEnvelope", "peer", remotePeer, "error", err)
		return
	}

	// Extract W3C trace context from the envelope.
	ctx := telemetry.ExtractTraceContext(context.Background(), &env)
	tracer := telemetry.Tracer("agentanycast.a2a")
	ctx, span := tracer.Start(ctx, "a2a.handle_message",
		trace.WithAttributes(
			attribute.String("envelope_id", env.EnvelopeId),
			attribute.String("envelope_type", env.Type.String()),
			attribute.String("peer_id", remotePeer.String()),
		),
	)
	defer span.End()

	// Inbound policy check (ACL, rate limiting, audit) — applied to all
	// non-ACK envelopes so enterprise features cover inbound traffic.
	if r.inboundPolicy != nil && env.Type != pb.EnvelopeType_ENVELOPE_TYPE_ACK {
		skillID := extractSkillID(&env)
		source := envelope.AgentIdentity{PeerID: remotePeer.String()}
		if err := r.inboundPolicy.CheckInbound(source, skillID, env.EnvelopeId); err != nil {
			r.logger.Warn("inbound policy rejected message",
				"peer", remotePeer,
				"envelope_id", env.EnvelopeId,
				"error", err,
			)
			span.RecordError(err)
			return
		}
	}

	r.logger.Debug("routing A2A envelope",
		"peer", remotePeer,
		"type", env.Type.String(),
		"envelope_id", env.EnvelopeId,
	)

	// Deduplicate by envelope ID (at-least-once → effectively-once).
	if env.EnvelopeId != "" && env.Type != pb.EnvelopeType_ENVELOPE_TYPE_ACK {
		if r.isDuplicate(env.EnvelopeId) {
			r.logger.Debug("dropping duplicate envelope", "envelope_id", env.EnvelopeId)
			return
		}
	}

	switch env.Type {
	case pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK:
		r.handleSendTask(ctx, remotePeer, &env)
	case pb.EnvelopeType_ENVELOPE_TYPE_TASK_STATUS_UPDATE:
		r.handleTaskStatusUpdate(ctx, remotePeer, &env)
	case pb.EnvelopeType_ENVELOPE_TYPE_TASK_COMPLETE:
		r.handleTaskComplete(ctx, remotePeer, &env)
	case pb.EnvelopeType_ENVELOPE_TYPE_TASK_FAIL:
		r.handleTaskFail(ctx, remotePeer, &env)
	case pb.EnvelopeType_ENVELOPE_TYPE_TASK_CANCEL:
		r.handleTaskCancel(ctx, remotePeer, &env)
	case pb.EnvelopeType_ENVELOPE_TYPE_ACK:
		r.handleAck(remotePeer, &env)
		return // Don't send an ACK for an ACK
	default:
		r.logger.Warn("unknown envelope type", "type", env.Type)
		return
	}

	// Send ACK back to sender for all non-ACK envelope types.
	if err := SendAck(r.sendFn, ctx, remotePeer, env.EnvelopeId); err != nil {
		r.logger.Warn("failed to send ACK", "envelope_id", env.EnvelopeId, "error", err)
	}
}

func (r *Router) handleSendTask(_ context.Context, remotePeer peer.ID, env *pb.A2AEnvelope) {
	payload := env.GetSendTask()
	if payload == nil {
		r.logger.Warn("send_task envelope missing payload")
		return
	}

	taskID := payload.TaskId
	if taskID == "" {
		taskID = uuid.New().String()
	}

	// Register in engine
	task := &Task{
		ID:               taskID,
		ContextID:        payload.ContextId,
		Status:           StatusSubmitted,
		TargetSkillID:    payload.TargetSkillId,
		OriginatorPeerID: remotePeer.String(),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	r.engine.RegisterTask(task)

	// Build proto task for the incoming event
	pbTask := &pb.Task{
		TaskId:           taskID,
		ContextId:        payload.ContextId,
		Status:           pb.TaskStatus_TASK_STATUS_SUBMITTED,
		TargetSkillId:    payload.TargetSkillId,
		OriginatorPeerId: remotePeer.String(),
		CreatedAt:        timestamppb.Now(),
		UpdatedAt:        timestamppb.Now(),
	}
	if payload.Message != nil {
		pbTask.Messages = []*pb.Message{payload.Message}
	}

	// Look up the sender's agent card.
	var senderCard *pb.AgentCard
	if r.peerCardFn != nil {
		if cardData, ok := r.peerCardFn(remotePeer); ok {
			var card pb.AgentCard
			if err := proto.Unmarshal(cardData, &card); err != nil {
				r.logger.Warn("failed to unmarshal sender card", "peer", remotePeer, "error", err)
			} else {
				senderCard = &card
			}
		}
	}

	// Emit to incoming tasks channel
	select {
	case r.incomingCh <- &IncomingTaskEvent{
		Task:       pbTask,
		SenderCard: senderCard,
		SenderPeer: remotePeer,
	}:
	default:
		r.logger.Warn("incoming task channel full, dropping task", "task_id", taskID)
	}

	r.logger.Info("incoming task registered", "task_id", taskID, "from", remotePeer)
}

func (r *Router) handleTaskStatusUpdate(ctx context.Context, remotePeer peer.ID, env *pb.A2AEnvelope) {
	payload := env.GetTaskStatusUpdate()
	if payload == nil {
		return
	}

	newStatus := ProtoToTaskStatus(payload.Status)
	if err := r.engine.TransitionTask(ctx, payload.TaskId, newStatus); err != nil {
		r.logger.Warn("status update rejected", "task_id", payload.TaskId, "error", err)
		return
	}

	// Notify subscribers
	r.notifyUpdate(payload.TaskId, payload.Status, payload.Message, nil)
}

func (r *Router) handleTaskComplete(ctx context.Context, remotePeer peer.ID, env *pb.A2AEnvelope) {
	payload := env.GetTaskComplete()
	if payload == nil {
		return
	}

	if err := r.engine.TransitionTask(ctx, payload.TaskId, StatusCompleted); err != nil {
		r.logger.Warn("complete rejected", "task_id", payload.TaskId, "error", err)
		return
	}

	r.notifyUpdate(payload.TaskId, pb.TaskStatus_TASK_STATUS_COMPLETED, payload.Message, payload.Artifacts)
}

func (r *Router) handleTaskFail(ctx context.Context, remotePeer peer.ID, env *pb.A2AEnvelope) {
	payload := env.GetTaskFail()
	if payload == nil {
		return
	}

	if err := r.engine.TransitionTask(ctx, payload.TaskId, StatusFailed); err != nil {
		r.logger.Warn("fail rejected", "task_id", payload.TaskId, "error", err)
		return
	}

	r.notifyUpdate(payload.TaskId, pb.TaskStatus_TASK_STATUS_FAILED, payload.Message, nil)
}

func (r *Router) handleTaskCancel(ctx context.Context, remotePeer peer.ID, env *pb.A2AEnvelope) {
	payload := env.GetTaskCancel()
	if payload == nil {
		return
	}

	if err := r.engine.TransitionTask(ctx, payload.TaskId, StatusCanceled); err != nil {
		r.logger.Warn("cancel rejected", "task_id", payload.TaskId, "error", err)
		return
	}

	r.notifyUpdate(payload.TaskId, pb.TaskStatus_TASK_STATUS_CANCELED, nil, nil)
}

func (r *Router) handleAck(_ peer.ID, env *pb.A2AEnvelope) {
	payload := env.GetAck()
	if payload == nil {
		return
	}
	ackID := payload.AcknowledgedEnvelopeId
	if r.ackTracker.Acknowledge(ackID) {
		r.logger.Debug("received ACK", "acknowledged_id", ackID)
	} else {
		r.logger.Debug("received ACK for unknown envelope", "acknowledged_id", ackID)
	}
}

// isDuplicate returns true if the envelope ID has been seen before, and records it.
func (r *Router) isDuplicate(envelopeID string) bool {
	r.dedupMu.Lock()
	defer r.dedupMu.Unlock()

	if _, ok := r.dedupCache[envelopeID]; ok {
		return true
	}

	// Evict oldest if at capacity.
	if len(r.dedupOrder) >= dedupCacheSize {
		oldest := r.dedupOrder[0]
		r.dedupOrder = r.dedupOrder[1:]
		delete(r.dedupCache, oldest)
	}

	r.dedupCache[envelopeID] = struct{}{}
	r.dedupOrder = append(r.dedupOrder, envelopeID)
	return false
}

// AckTracker returns the router's AckTracker.
func (r *Router) AckTracker() *AckTracker {
	return r.ackTracker
}

// Close stops the router's background goroutines.
func (r *Router) Close() {
	r.ackTracker.Stop()
}

func (r *Router) notifyUpdate(taskID string, status pb.TaskStatus, msg *pb.Message, artifacts []*pb.Artifact) {
	r.mu.RLock()
	subs := r.taskUpdateSubs[taskID]
	r.mu.RUnlock()

	evt := &TaskUpdateEvent{
		TaskID:    taskID,
		Status:    status,
		Message:   msg,
		Artifacts: artifacts,
	}
	for _, ch := range subs {
		select {
		case ch <- evt:
		default:
			r.logger.Warn("dropping task update for slow subscriber", "task_id", taskID, "status", status)
		}
	}
}

// SendTask creates and sends a task to a remote peer.
func (r *Router) SendTask(ctx context.Context, targetPeer peer.ID, msg *pb.Message, targetSkillID, contextID string) (*Task, error) {
	task := r.engine.CreateTask(contextID, targetSkillID, "local")

	tracer := telemetry.Tracer("agentanycast.a2a")
	spanCtx, span := tracer.Start(ctx, "a2a.send_task",
		trace.WithAttributes(
			attribute.String("task_id", task.ID),
			attribute.String("peer_id", targetPeer.String()),
			attribute.String("skill_id", targetSkillID),
		),
	)
	defer span.End()

	env := &pb.A2AEnvelope{
		EnvelopeId: uuid.New().String(),
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_SEND_TASK,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_SendTask{
			SendTask: &pb.SendTaskPayload{
				TaskId:        task.ID,
				ContextId:     contextID,
				Message:       msg,
				TargetSkillId: targetSkillID,
			},
		},
	}

	// Inject trace context into the envelope for distributed tracing.
	telemetry.InjectTraceContext(spanCtx, env)

	data, err := proto.Marshal(env)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}

	if err := r.sendFn(ctx, targetPeer, data); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("send task to %s: %w", targetPeer, err)
	}

	r.ackTracker.Track(targetPeer, env.EnvelopeId, data)

	r.logger.Info("task sent", "task_id", task.ID, "target", targetPeer)
	return task, nil
}

// SendStatusUpdate sends a task status update to the originator peer.
func (r *Router) SendStatusUpdate(ctx context.Context, targetPeer peer.ID, taskID string, status pb.TaskStatus, msg *pb.Message) error {
	env := &pb.A2AEnvelope{
		EnvelopeId: uuid.New().String(),
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_TASK_STATUS_UPDATE,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_TaskStatusUpdate{
			TaskStatusUpdate: &pb.TaskStatusUpdatePayload{
				TaskId:  taskID,
				Status:  status,
				Message: msg,
			},
		},
	}

	data, err := proto.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal status update: %w", err)
	}

	if err := r.sendFn(ctx, targetPeer, data); err != nil {
		return err
	}
	r.ackTracker.Track(targetPeer, env.EnvelopeId, data)
	return nil
}

// SendComplete sends a task completion message to the originator peer.
func (r *Router) SendComplete(ctx context.Context, targetPeer peer.ID, taskID string, artifacts []*pb.Artifact, msg *pb.Message) error {
	env := &pb.A2AEnvelope{
		EnvelopeId: uuid.New().String(),
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_TASK_COMPLETE,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_TaskComplete{
			TaskComplete: &pb.TaskCompletePayload{
				TaskId:    taskID,
				Artifacts: artifacts,
				Message:   msg,
			},
		},
	}

	data, err := proto.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal complete: %w", err)
	}

	if err := r.sendFn(ctx, targetPeer, data); err != nil {
		return err
	}
	r.ackTracker.Track(targetPeer, env.EnvelopeId, data)
	return nil
}

// SendFail sends a task failure message to the originator peer.
func (r *Router) SendFail(ctx context.Context, targetPeer peer.ID, taskID, errorMessage string) error {
	env := &pb.A2AEnvelope{
		EnvelopeId: uuid.New().String(),
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_TASK_FAIL,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_TaskFail{
			TaskFail: &pb.TaskFailPayload{
				TaskId:       taskID,
				ErrorMessage: errorMessage,
			},
		},
	}

	data, err := proto.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal fail: %w", err)
	}

	if err := r.sendFn(ctx, targetPeer, data); err != nil {
		return err
	}
	r.ackTracker.Track(targetPeer, env.EnvelopeId, data)
	return nil
}

// ── Status conversion helpers ─────────────────────────────

// TaskStatusToProto converts internal TaskStatus to proto TaskStatus.
func TaskStatusToProto(s TaskStatus) pb.TaskStatus {
	switch s {
	case StatusSubmitted:
		return pb.TaskStatus_TASK_STATUS_SUBMITTED
	case StatusWorking:
		return pb.TaskStatus_TASK_STATUS_WORKING
	case StatusInputRequired:
		return pb.TaskStatus_TASK_STATUS_INPUT_REQUIRED
	case StatusCompleted:
		return pb.TaskStatus_TASK_STATUS_COMPLETED
	case StatusFailed:
		return pb.TaskStatus_TASK_STATUS_FAILED
	case StatusCanceled:
		return pb.TaskStatus_TASK_STATUS_CANCELED
	case StatusRejected:
		return pb.TaskStatus_TASK_STATUS_REJECTED
	default:
		return pb.TaskStatus_TASK_STATUS_UNSPECIFIED
	}
}

// ProtoToTaskStatus converts proto TaskStatus to internal TaskStatus.
func ProtoToTaskStatus(s pb.TaskStatus) TaskStatus {
	switch s {
	case pb.TaskStatus_TASK_STATUS_SUBMITTED:
		return StatusSubmitted
	case pb.TaskStatus_TASK_STATUS_WORKING:
		return StatusWorking
	case pb.TaskStatus_TASK_STATUS_INPUT_REQUIRED:
		return StatusInputRequired
	case pb.TaskStatus_TASK_STATUS_COMPLETED:
		return StatusCompleted
	case pb.TaskStatus_TASK_STATUS_FAILED:
		return StatusFailed
	case pb.TaskStatus_TASK_STATUS_CANCELED:
		return StatusCanceled
	case pb.TaskStatus_TASK_STATUS_REJECTED:
		return StatusRejected
	default:
		return StatusUnspecified
	}
}

// extractSkillID extracts the target skill ID from an A2A envelope payload.
func extractSkillID(env *pb.A2AEnvelope) string {
	if p := env.GetSendTask(); p != nil {
		return p.TargetSkillId
	}
	return ""
}

// ExtractEnvelopeID attempts to extract the envelope ID from serialized A2A
// envelope data. If unmarshalling fails, it falls back to a UUID.
func ExtractEnvelopeID(data []byte) string {
	var env pb.A2AEnvelope
	if err := proto.Unmarshal(data, &env); err == nil && env.EnvelopeId != "" {
		return env.EnvelopeId
	}
	return uuid.New().String()
}
