package anp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

// IncomingANPTask represents an ANP JSON-RPC call routed into the A2A engine.
type IncomingANPTask struct {
	Method   string
	Params   interface{}
	ResultCh chan<- *JSONRPCResponse
}

// Handler serves ANP protocol HTTP endpoints:
//   - GET  /agent/ad.json        — Agent Description (JSON-LD)
//   - GET  /agent/interface.json  — OpenRPC interface specification
//   - POST /agent/rpc             — JSON-RPC 2.0 method calls
type Handler struct {
	card     *pb.AgentCard
	taskChan chan<- IncomingANPTask
	logger   *slog.Logger
}

// NewHandler creates a new ANP inbound handler.
func NewHandler(card *pb.AgentCard, taskChan chan<- IncomingANPTask, logger *slog.Logger) *Handler {
	return &Handler{
		card:     card,
		taskChan: taskChan,
		logger:   logger,
	}
}

// SetCard updates the agent card used by the handler. Safe to call while
// the server is running (the card is read per-request, not cached).
func (h *Handler) SetCard(card *pb.AgentCard) {
	h.card = card
}

// ServeHTTP routes requests to the appropriate ANP endpoint.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/agent/ad.json":
		h.serveDescription(w, r)
	case "/agent/interface.json":
		h.serveInterface(w, r)
	case "/agent/rpc":
		h.serveRPC(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) serveDescription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.card == nil {
		http.Error(w, "no agent card configured", http.StatusNotFound)
		return
	}

	// Derive base URL from the request.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	baseURL := scheme + "://" + r.Host

	desc := AgentCardToDescription(h.card, baseURL)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(desc); err != nil {
		h.logger.Error("failed to encode agent description", "error", err)
	}
}

func (h *Handler) serveInterface(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.card == nil {
		http.Error(w, "no agent card configured", http.StatusNotFound)
		return
	}

	spec := SkillsToOpenRPC(h.card.Skills, h.card.Name, h.card.Version)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(spec); err != nil {
		h.logger.Error("failed to encode OpenRPC spec", "error", err)
	}
}

func (h *Handler) serveRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20)) // 16MB max
	if err != nil {
		h.writeError(w, nil, -32700, "failed to read request body")
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.writeError(w, nil, -32700, "invalid JSON")
		return
	}

	if h.taskChan == nil {
		h.writeError(w, req.ID, -32000, "ANP task handler is not configured")
		return
	}

	// Route the RPC call through the task channel.
	resultCh := make(chan *JSONRPCResponse, 1)
	h.taskChan <- IncomingANPTask{
		Method:   req.Method,
		Params:   req.Params,
		ResultCh: resultCh,
	}

	// Wait for the result.
	result := <-resultCh

	respBytes, err := json.Marshal(result)
	if err != nil {
		h.writeError(w, req.ID, -32000, "failed to marshal response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(respBytes); err != nil {
		h.logger.Warn("failed to write response", "error", err)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, id interface{}, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: message},
	}
	respBytes, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC errors are still 200
	if _, err := w.Write(respBytes); err != nil {
		h.logger.Warn("failed to write error response", "error", err)
	}
}
