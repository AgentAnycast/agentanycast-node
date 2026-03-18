package bridge

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

// TaskSender is the interface for sending tasks into the local A2A engine.
type TaskSender interface {
	// HandleInboundTask processes an HTTP-originated task and returns the result.
	HandleInboundTask(task *pb.Task) (*pb.Task, error)
}

// InboundHandler handles HTTP → P2P inbound translation.
type InboundHandler struct {
	sender TaskSender
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

// ServeHTTP handles A2A JSON-RPC requests from HTTP agents.
func (h *InboundHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
