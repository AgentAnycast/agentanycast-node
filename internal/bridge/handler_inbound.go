package bridge

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

var bridgeInboundTracer = otel.Tracer("agentanycast/bridge/inbound")

// TaskSender is the interface for sending tasks into the local A2A engine.
type TaskSender interface {
	// HandleInboundTask processes an HTTP-originated task and returns the result.
	HandleInboundTask(task *pb.Task) (*pb.Task, error)
}

// InboundPolicyChecker enforces access control on inbound HTTP bridge requests.
type InboundPolicyChecker interface {
	CheckInbound(source envelope.AgentIdentity, skillID, envelopeID string) error
}

// InboundHandler handles HTTP → P2P inbound translation.
type InboundHandler struct {
	sender TaskSender
	policy InboundPolicyChecker
	logger *slog.Logger
}

// NewInboundHandler creates a new inbound handler.
func NewInboundHandler(sender TaskSender, logger *slog.Logger) *InboundHandler {
	return &InboundHandler{
		sender: sender,
		logger: logger,
	}
}

// SetSender sets the TaskSender after construction (for wiring order issues).
func (h *InboundHandler) SetSender(sender TaskSender) {
	h.sender = sender
}

// SetInboundPolicy configures the inbound policy checker for ACL, rate
// limiting, and audit on HTTP bridge requests.
func (h *InboundHandler) SetInboundPolicy(policy InboundPolicyChecker) {
	h.policy = policy
}

// ServeHTTP handles A2A JSON-RPC requests from HTTP agents.
func (h *InboundHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, span := bridgeInboundTracer.Start(r.Context(), "bridge.inbound",
		trace.WithAttributes(
			attribute.String("http.method", r.Method),
			attribute.String("http.path", r.URL.Path),
		),
	)
	defer span.End()

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20)) // 16MB max
	if err != nil {
		h.writeError(w, nil, -32700, "failed to read request body")
		return
	}

	var req A2AHTTPRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.writeError(w, nil, -32700, "invalid JSON")
		return
	}

	switch req.Method {
	case "tasks/send":
		h.handleSendTask(w, req)
	default:
		h.writeError(w, req.ID, -32601, "method not found: "+req.Method)
	}
}

func (h *InboundHandler) handleSendTask(w http.ResponseWriter, req A2AHTTPRequest) {
	if h.sender == nil {
		h.writeError(w, req.ID, -32000, "bridge task sender is not configured")
		return
	}

	var params SendTaskParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		h.writeError(w, req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	// Inbound policy check (ACL, rate limiting, audit).
	if h.policy != nil {
		source := envelope.AgentIdentity{} // HTTP bridge: anonymous source
		if err := h.policy.CheckInbound(source, params.TargetSkillID, ""); err != nil {
			h.logger.Warn("inbound policy rejected bridge request", "skill", params.TargetSkillID, "error", err)
			h.writeError(w, req.ID, -32000, "access denied: "+err.Error())
			return
		}
	}

	// Convert HTTP message to protobuf task.
	taskID := params.ID
	if taskID == "" {
		taskID = uuid.New().String()
	}

	protoMsg := HTTPMessageToProto(params.Message)
	now := timestamppb.Now()

	task := &pb.Task{
		TaskId:        taskID,
		Status:        pb.TaskStatus_TASK_STATUS_SUBMITTED,
		Messages:      []*pb.Message{protoMsg},
		TargetSkillId: params.TargetSkillID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Send into the local A2A engine.
	result, err := h.sender.HandleInboundTask(task)
	if err != nil {
		h.logger.Error("inbound task failed", "task_id", taskID, "error", err)
		h.writeError(w, req.ID, -32000, "internal error: "+err.Error())
		return
	}

	// Convert result back to HTTP format.
	taskResult := ProtoTaskToHTTP(result)
	respBody, err := BuildJSONRPCResponse(req.ID, taskResult)
	if err != nil {
		h.writeError(w, req.ID, -32000, "failed to marshal response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(respBody); err != nil {
		h.logger.Warn("failed to write response", "error", err)
	}
}

func (h *InboundHandler) writeError(w http.ResponseWriter, id any, code int, message string) {
	respBody, _ := BuildJSONRPCError(id, code, message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC errors are still 200
	if _, err := w.Write(respBody); err != nil {
		h.logger.Warn("failed to write error response", "error", err)
	}
}
