package anp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

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
	mu       sync.RWMutex
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

// SetCard updates the agent card used by the handler. Safe to call
// concurrently while the server is running.
func (h *Handler) SetCard(card *pb.AgentCard) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.card = card
}

// getCard returns the current agent card under a read lock.
func (h *Handler) getCard() *pb.AgentCard {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.card
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

	card := h.getCard()
	if card == nil {
		http.Error(w, "no agent card configured", http.StatusNotFound)
		return
	}

	// Derive base URL from the request.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	baseURL := scheme + "://" + r.Host

	desc := AgentCardToDescription(card, baseURL)

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

	card := h.getCard()
	if card == nil {
		http.Error(w, "no agent card configured", http.StatusNotFound)
		return
	}

	spec := SkillsToOpenRPC(card.Skills, card.Name, card.Version)

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

	// Route the RPC call through the task channel with a timeout to prevent blocking.
	resultCh := make(chan *JSONRPCResponse, 1)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	select {
	case h.taskChan <- IncomingANPTask{
		Method:   req.Method,
		Params:   req.Params,
		ResultCh: resultCh,
	}:
	case <-ctx.Done():
		h.writeError(w, req.ID, -32000, "server busy: task queue full")
		return
	}

	// Wait for the result.
	var result *JSONRPCResponse
	select {
	case result = <-resultCh:
	case <-ctx.Done():
		h.writeError(w, req.ID, -32000, "request timed out waiting for result")
		return
	}

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
