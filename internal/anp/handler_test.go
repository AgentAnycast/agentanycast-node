package anp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testCard() *pb.AgentCard {
	return &pb.AgentCard{
		Name:        "Test Agent",
		Description: "A test agent for ANP",
		Version:     "1.0",
		Skills: []*pb.Skill{
			{Id: "echo", Description: "Echo input back"},
			{Id: "translate", Description: "Translate text", InputSchema: `{"type":"string"}`},
		},
		P2PExtension: &pb.P2PExtension{
			DidKey: "did:key:z6MkTest",
		},
	}
}

func TestHandlerServeDescription(t *testing.T) {
	handler := NewHandler(testCard(), nil, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/agent/ad.json", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var desc AgentDescription
	if err := json.NewDecoder(w.Body).Decode(&desc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if desc.Name != "Test Agent" {
		t.Fatalf("expected 'Test Agent', got %q", desc.Name)
	}
	if desc.DID != "did:key:z6MkTest" {
		t.Fatalf("expected did:key, got %q", desc.DID)
	}
}

func TestHandlerServeDescriptionMethodNotAllowed(t *testing.T) {
	handler := NewHandler(testCard(), nil, testLogger())

	req := httptest.NewRequest(http.MethodPost, "/agent/ad.json", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandlerServeDescriptionNoCard(t *testing.T) {
	handler := NewHandler(nil, nil, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/agent/ad.json", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandlerServeInterface(t *testing.T) {
	handler := NewHandler(testCard(), nil, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/agent/interface.json", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var spec OpenRPCSpec
	if err := json.NewDecoder(w.Body).Decode(&spec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(spec.Methods) != 2 {
		t.Fatalf("expected 2 methods, got %d", len(spec.Methods))
	}
	if spec.Methods[0].Name != "echo" {
		t.Fatalf("expected method 'echo', got %q", spec.Methods[0].Name)
	}
}

func TestHandlerServeInterfaceNoCard(t *testing.T) {
	handler := NewHandler(nil, nil, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/agent/interface.json", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandlerServeRPCSuccess(t *testing.T) {
	taskChan := make(chan IncomingANPTask, 1)
	handler := NewHandler(testCard(), taskChan, testLogger())

	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "echo",
		Params:  map[string]interface{}{"input": "Hello"},
		ID:      "req-1",
	}
	body, _ := json.Marshal(rpcReq)

	// Handle the task in a goroutine.
	go func() {
		task := <-taskChan
		if task.Method != "echo" {
			t.Errorf("expected method 'echo', got %q", task.Method)
		}
		task.ResultCh <- &JSONRPCResponse{
			JSONRPC: "2.0",
			Result:  "Echo: Hello",
			ID:      "req-1",
		}
	}()

	req := httptest.NewRequest(http.MethodPost, "/agent/rpc", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	respBody, _ := io.ReadAll(w.Body)
	var resp JSONRPCResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Result != "Echo: Hello" {
		t.Fatalf("expected 'Echo: Hello', got %v", resp.Result)
	}
}

func TestHandlerServeRPCMethodNotAllowed(t *testing.T) {
	handler := NewHandler(testCard(), nil, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/agent/rpc", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandlerServeRPCInvalidJSON(t *testing.T) {
	handler := NewHandler(testCard(), nil, testLogger())

	req := httptest.NewRequest(http.MethodPost, "/agent/rpc", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK { // JSON-RPC errors are 200
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp JSONRPCResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != -32700 {
		t.Fatalf("expected parse error (-32700), got %+v", resp.Error)
	}
}

func TestHandlerServeRPCNilTaskChan(t *testing.T) {
	handler := NewHandler(testCard(), nil, testLogger())

	rpcReq := JSONRPCRequest{JSONRPC: "2.0", Method: "echo", ID: "1"}
	body, _ := json.Marshal(rpcReq)

	req := httptest.NewRequest(http.MethodPost, "/agent/rpc", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var resp JSONRPCResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != -32000 {
		t.Fatalf("expected internal error (-32000), got %+v", resp.Error)
	}
}

func TestHandlerNotFound(t *testing.T) {
	handler := NewHandler(testCard(), nil, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandlerSetCard(t *testing.T) {
	handler := NewHandler(nil, nil, testLogger())

	// Initially no card.
	req := httptest.NewRequest(http.MethodGet, "/agent/ad.json", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 with no card, got %d", w.Code)
	}

	// Set card and retry.
	handler.SetCard(testCard())
	req = httptest.NewRequest(http.MethodGet, "/agent/ad.json", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after SetCard, got %d", w.Code)
	}
}
