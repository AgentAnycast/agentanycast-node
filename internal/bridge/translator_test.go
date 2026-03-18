package bridge

import (
	"encoding/json"
	"testing"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHTTPMessageToProto(t *testing.T) {
	msg := A2AMessage{
		Role: "user",
		Parts: []A2APart{
			{Type: "text", Text: "Hello, agent!"},
			{Type: "url", URL: "https://example.com/doc.pdf", Filename: "doc.pdf"},
		},
	}

	proto := HTTPMessageToProto(msg)
	if proto.Role != pb.MessageRole_MESSAGE_ROLE_USER {
		t.Fatalf("expected USER role, got %v", proto.Role)
	}
	if len(proto.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(proto.Parts))
	}

	textPart := proto.Parts[0].GetTextPart()
	if textPart == nil || textPart.Text != "Hello, agent!" {
		t.Fatalf("unexpected text part: %v", proto.Parts[0])
	}

	urlPart := proto.Parts[1].GetUrlPart()
	if urlPart == nil || urlPart.Url != "https://example.com/doc.pdf" {
		t.Fatalf("unexpected url part: %v", proto.Parts[1])
	}
	if urlPart.Filename != "doc.pdf" {
		t.Fatalf("expected filename doc.pdf, got %s", urlPart.Filename)
	}
}

func TestHTTPMessageToProtoAgentRole(t *testing.T) {
	msg := A2AMessage{Role: "agent", Parts: []A2APart{{Type: "text", Text: "Hi"}}}
	proto := HTTPMessageToProto(msg)
	if proto.Role != pb.MessageRole_MESSAGE_ROLE_AGENT {
		t.Fatalf("expected AGENT role, got %v", proto.Role)
	}
}

func TestProtoMessageToHTTP(t *testing.T) {
	proto := &pb.Message{
		MessageId: "msg-1",
		Role:      pb.MessageRole_MESSAGE_ROLE_AGENT,
		Parts: []*pb.Part{
			{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: "Response text"}}},
			{Content: &pb.Part_UrlPart{UrlPart: &pb.UrlPart{Url: "https://example.com/img.png"}}},
		},
	}

	msg := ProtoMessageToHTTP(proto)
	if msg.Role != "agent" {
		t.Fatalf("expected role 'agent', got %q", msg.Role)
	}
	if len(msg.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(msg.Parts))
	}
	if msg.Parts[0].Type != "text" || msg.Parts[0].Text != "Response text" {
		t.Fatalf("unexpected text part: %+v", msg.Parts[0])
	}
	if msg.Parts[1].Type != "url" || msg.Parts[1].URL != "https://example.com/img.png" {
		t.Fatalf("unexpected url part: %+v", msg.Parts[1])
	}
}

func TestProtoTaskToHTTP(t *testing.T) {
	task := &pb.Task{
		TaskId: "task-123",
		Status: pb.TaskStatus_TASK_STATUS_COMPLETED,
		Messages: []*pb.Message{
			{
				Role:  pb.MessageRole_MESSAGE_ROLE_USER,
				Parts: []*pb.Part{{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: "Hi"}}}},
			},
		},
		Artifacts: []*pb.Artifact{
			{
				ArtifactId: "art-1",
				Name:       "output",
				Parts:      []*pb.Part{{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: "Result"}}}},
			},
		},
	}

	result := ProtoTaskToHTTP(task)
	if result.ID != "task-123" {
		t.Fatalf("expected task-123, got %s", result.ID)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(result.Artifacts))
	}
	if result.Artifacts[0].Name != "output" {
		t.Fatalf("expected artifact name 'output', got %s", result.Artifacts[0].Name)
	}
}

func TestProtoCardToHTTP(t *testing.T) {
	card := &pb.AgentCard{
		Name:            "Test Agent",
		Description:     "A test agent",
		Version:         "1.0",
		ProtocolVersion: "0.3",
		Skills: []*pb.Skill{
			{Id: "echo", Description: "Echo input", InputSchema: "{}", OutputSchema: "{}"},
		},
	}

	result := ProtoCardToHTTP(card, "https://example.com/a2a")
	if result.Name != "Test Agent" {
		t.Fatalf("expected 'Test Agent', got %s", result.Name)
	}
	if result.URL != "https://example.com/a2a" {
		t.Fatalf("expected URL, got %s", result.URL)
	}
	if len(result.Skills) != 1 || result.Skills[0].ID != "echo" {
		t.Fatalf("unexpected skills: %+v", result.Skills)
	}
}

func TestStatusConversionRoundTrip(t *testing.T) {
	statuses := map[string]pb.TaskStatus{
		"submitted":      pb.TaskStatus_TASK_STATUS_SUBMITTED,
		"working":        pb.TaskStatus_TASK_STATUS_WORKING,
		"input-required": pb.TaskStatus_TASK_STATUS_INPUT_REQUIRED,
		"completed":      pb.TaskStatus_TASK_STATUS_COMPLETED,
		"failed":         pb.TaskStatus_TASK_STATUS_FAILED,
		"canceled":       pb.TaskStatus_TASK_STATUS_CANCELED,
		"rejected":       pb.TaskStatus_TASK_STATUS_REJECTED,
	}

	for httpStatus, protoStatus := range statuses {
		// HTTP → Proto
		got := HTTPStatusToProto(httpStatus)
		if got != protoStatus {
			t.Errorf("HTTPStatusToProto(%q) = %v, want %v", httpStatus, got, protoStatus)
		}

		// Proto → HTTP
		gotHTTP := protoStatusToHTTP(protoStatus)
		if gotHTTP != httpStatus {
			t.Errorf("protoStatusToHTTP(%v) = %q, want %q", protoStatus, gotHTTP, httpStatus)
		}
	}

	// Unknown values.
	if HTTPStatusToProto("unknown-status") != pb.TaskStatus_TASK_STATUS_UNSPECIFIED {
		t.Error("expected UNSPECIFIED for unknown HTTP status")
	}
	if protoStatusToHTTP(pb.TaskStatus_TASK_STATUS_UNSPECIFIED) != "unknown" {
		t.Error("expected 'unknown' for UNSPECIFIED proto status")
	}
}

func TestBuildA2AHTTPRequest(t *testing.T) {
	msg := &pb.Message{
		Role:  pb.MessageRole_MESSAGE_ROLE_USER,
		Parts: []*pb.Part{{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: "Hello"}}}},
	}

	data, err := BuildA2AHTTPRequest("task-1", msg)
	if err != nil {
		t.Fatalf("BuildA2AHTTPRequest: %v", err)
	}

	var req A2AHTTPRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if req.JSONRPC != "2.0" {
		t.Fatalf("expected jsonrpc 2.0, got %s", req.JSONRPC)
	}
	if req.Method != "tasks/send" {
		t.Fatalf("expected method tasks/send, got %s", req.Method)
	}
}

func TestParseA2AHTTPResponse(t *testing.T) {
	taskResult := A2ATaskResult{
		ID:     "task-1",
		Status: "completed",
	}
	resp := A2AHTTPResponse{
		JSONRPC: "2.0",
		ID:      "task-1",
		Result:  taskResult,
	}
	data, _ := json.Marshal(resp)

	result, err := ParseA2AHTTPResponse(data)
	if err != nil {
		t.Fatalf("ParseA2AHTTPResponse: %v", err)
	}
	if result.ID != "task-1" {
		t.Fatalf("expected task-1, got %s", result.ID)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed, got %s", result.Status)
	}
}

func TestParseA2AHTTPResponseError(t *testing.T) {
	resp := A2AHTTPResponse{
		JSONRPC: "2.0",
		ID:      "req-1",
		Error:   &A2AError{Code: -32000, Message: "internal error"},
	}
	data, _ := json.Marshal(resp)

	_, err := ParseA2AHTTPResponse(data)
	if err == nil {
		t.Fatal("expected error for A2A error response")
	}
}

func TestParseA2AHTTPResponseInvalidJSON(t *testing.T) {
	_, err := ParseA2AHTTPResponse([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestBuildJSONRPCResponse(t *testing.T) {
	data, err := BuildJSONRPCResponse("req-1", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("BuildJSONRPCResponse: %v", err)
	}

	var resp A2AHTTPResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.JSONRPC != "2.0" {
		t.Fatalf("expected 2.0, got %s", resp.JSONRPC)
	}
	if resp.Error != nil {
		t.Fatal("expected no error in success response")
	}
}

func TestBuildJSONRPCError(t *testing.T) {
	data, err := BuildJSONRPCError("req-1", -32601, "method not found")
	if err != nil {
		t.Fatalf("BuildJSONRPCError: %v", err)
	}

	var resp A2AHTTPResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error in response")
	}
	if resp.Error.Code != -32601 {
		t.Fatalf("expected code -32601, got %d", resp.Error.Code)
	}
}

func TestNowTimestamp(t *testing.T) {
	ts := nowTimestamp()
	if ts == nil {
		t.Fatal("expected non-nil timestamp")
	}
	// Just verify it's a valid recent timestamp.
	_ = ts.AsTime()
}

func TestHTTPPartToProtoDefaultsToText(t *testing.T) {
	// Unknown part type should default to text.
	part := httpPartToProto(A2APart{Type: "unknown", Text: "fallback"})
	textPart := part.GetTextPart()
	if textPart == nil || textPart.Text != "fallback" {
		t.Fatalf("expected fallback text part, got %v", part)
	}
}

func TestProtoPartToHTTPNilContent(t *testing.T) {
	// Part with no content set should return empty text.
	part := protoPartToHTTP(&pb.Part{})
	if part.Type != "text" {
		t.Fatalf("expected text type for nil content, got %s", part.Type)
	}
}

func TestProtoMessageToHTTPUserRole(t *testing.T) {
	msg := &pb.Message{
		Role:      pb.MessageRole_MESSAGE_ROLE_USER,
		Parts:     []*pb.Part{},
		CreatedAt: timestamppb.Now(),
	}
	result := ProtoMessageToHTTP(msg)
	if result.Role != "user" {
		t.Fatalf("expected user role, got %s", result.Role)
	}
}
