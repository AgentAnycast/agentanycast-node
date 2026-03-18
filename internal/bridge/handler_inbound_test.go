package bridge

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

type mockTaskSender struct {
	handleFunc func(task *pb.Task) (*pb.Task, error)
}

func (m *mockTaskSender) HandleInboundTask(task *pb.Task) (*pb.Task, error) {
	return m.handleFunc(task)
}

func testInboundLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestInboundHandlerMethodNotAllowed(t *testing.T) {
	handler := NewInboundHandler(nil, testInboundLogger())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestInboundHandlerInvalidJSON(t *testing.T) {
	handler := NewInboundHandler(nil, testInboundLogger())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK { // JSON-RPC errors are 200
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp A2AHTTPResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != -32700 {
		t.Fatalf("expected parse error (-32700), got %+v", resp.Error)
	}
}

func TestInboundHandlerUnknownMethod(t *testing.T) {
	handler := NewInboundHandler(nil, testInboundLogger())

	rpcReq := A2AHTTPRequest{
		JSONRPC: "2.0",
		ID:      "1",
		Method:  "tasks/unknown",
	}
	body, _ := json.Marshal(rpcReq)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var resp A2AHTTPResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("expected method not found (-32601), got %+v", resp.Error)
	}
}

func TestInboundHandlerNilSender(t *testing.T) {
	handler := NewInboundHandler(nil, testInboundLogger())

	params := SendTaskParams{
		Message: A2AMessage{
			Role:  "user",
			Parts: []A2APart{{Type: "text", Text: "Hello"}},
		},
	}
	paramsBytes, _ := json.Marshal(params)

	rpcReq := A2AHTTPRequest{
		JSONRPC: "2.0",
		ID:      "1",
		Method:  "tasks/send",
		Params:  paramsBytes,
	}
	body, _ := json.Marshal(rpcReq)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var resp A2AHTTPResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != -32000 {
		t.Fatalf("expected internal error (-32000) for nil sender, got %+v", resp.Error)
	}
}

func TestInboundHandlerSendTaskSuccess(t *testing.T) {
	sender := &mockTaskSender{
		handleFunc: func(task *pb.Task) (*pb.Task, error) {
			return &pb.Task{
				TaskId: task.TaskId,
				Status: pb.TaskStatus_TASK_STATUS_COMPLETED,
				Messages: task.Messages,
				Artifacts: []*pb.Artifact{
					{
						ArtifactId: "art-1",
						Name:       "output",
						Parts: []*pb.Part{
							{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: "Echo: Hello"}}},
						},
					},
				},
			}, nil
		},
	}

	handler := NewInboundHandler(sender, testInboundLogger())

	params := SendTaskParams{
		ID: "task-42",
		Message: A2AMessage{
			Role:  "user",
			Parts: []A2APart{{Type: "text", Text: "Hello"}},
		},
	}
	paramsBytes, _ := json.Marshal(params)

	rpcReq := A2AHTTPRequest{
		JSONRPC: "2.0",
		ID:      "req-1",
		Method:  "tasks/send",
		Params:  paramsBytes,
	}
	body, _ := json.Marshal(rpcReq)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	respBody, _ := io.ReadAll(w.Body)
	var resp A2AHTTPResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// Verify result contains the task.
	resultBytes, _ := json.Marshal(resp.Result)
	var taskResult A2ATaskResult
	if err := json.Unmarshal(resultBytes, &taskResult); err != nil {
		t.Fatalf("unmarshal task result: %v", err)
	}
	if taskResult.ID != "task-42" {
		t.Fatalf("expected task-42, got %s", taskResult.ID)
	}
	if taskResult.Status != "completed" {
		t.Fatalf("expected completed, got %s", taskResult.Status)
	}
}

func TestInboundHandlerAutoGeneratesTaskID(t *testing.T) {
	var receivedTaskID string
	sender := &mockTaskSender{
		handleFunc: func(task *pb.Task) (*pb.Task, error) {
			receivedTaskID = task.TaskId
			return &pb.Task{
				TaskId: task.TaskId,
				Status: pb.TaskStatus_TASK_STATUS_COMPLETED,
			}, nil
		},
	}

	handler := NewInboundHandler(sender, testInboundLogger())

	params := SendTaskParams{
		// No ID — should auto-generate.
		Message: A2AMessage{
			Role:  "user",
			Parts: []A2APart{{Type: "text", Text: "Hi"}},
		},
	}
	paramsBytes, _ := json.Marshal(params)

	rpcReq := A2AHTTPRequest{JSONRPC: "2.0", ID: "1", Method: "tasks/send", Params: paramsBytes}
	body, _ := json.Marshal(rpcReq)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if receivedTaskID == "" {
		t.Fatal("expected auto-generated task ID")
	}
}

func TestInboundHandlerSetSender(t *testing.T) {
	handler := NewInboundHandler(nil, testInboundLogger())

	sender := &mockTaskSender{
		handleFunc: func(task *pb.Task) (*pb.Task, error) {
			return &pb.Task{TaskId: task.TaskId, Status: pb.TaskStatus_TASK_STATUS_COMPLETED}, nil
		},
	}
	handler.SetSender(sender)

	params := SendTaskParams{
		Message: A2AMessage{Role: "user", Parts: []A2APart{{Type: "text", Text: "Test"}}},
	}
	paramsBytes, _ := json.Marshal(params)
	rpcReq := A2AHTTPRequest{JSONRPC: "2.0", ID: "1", Method: "tasks/send", Params: paramsBytes}
	body, _ := json.Marshal(rpcReq)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var resp A2AHTTPResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error != nil {
		t.Fatalf("expected success after SetSender, got error: %+v", resp.Error)
	}
}

func TestInboundHandlerInvalidParams(t *testing.T) {
	sender := &mockTaskSender{
		handleFunc: func(task *pb.Task) (*pb.Task, error) {
			return task, nil
		},
	}
	handler := NewInboundHandler(sender, testInboundLogger())

	rpcReq := A2AHTTPRequest{
		JSONRPC: "2.0",
		ID:      "1",
		Method:  "tasks/send",
		Params:  json.RawMessage(`"not an object"`),
	}
	body, _ := json.Marshal(rpcReq)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var resp A2AHTTPResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("expected invalid params (-32602), got %+v", resp.Error)
	}
}
