// Package bridge implements the HTTP Bridge for bidirectional translation
// between HTTP A2A agents and P2P AgentAnycast agents.
//
// The bridge is stateless — it translates between A2A HTTP JSON format
// and internal Protobuf representations without caching or persistence.
package bridge

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

// ── A2A HTTP JSON types (matching A2A v0.3 JSON schema) ─────

// A2AHTTPRequest is the JSON body of an A2A HTTP tasks/send request.
type A2AHTTPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// A2AHTTPResponse is the JSON body of an A2A HTTP response.
type A2AHTTPResponse struct {
	JSONRPC string     `json:"jsonrpc"`
	ID      any        `json:"id"`
	Result  any        `json:"result,omitempty"`
	Error   *A2AError  `json:"error,omitempty"`
}

// A2AError is a JSON-RPC error object.
type A2AError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// SendTaskParams are the parameters for a tasks/send request.
type SendTaskParams struct {
	ID            string         `json:"id,omitempty"`
	Message       A2AMessage     `json:"message"`
	TargetSkillID string         `json:"target_skill_id,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// A2AMessage is the JSON representation of an A2A message.
type A2AMessage struct {
	Role  string    `json:"role"`
	Parts []A2APart `json:"parts"`
}

// A2APart is the JSON representation of an A2A message part.
type A2APart struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
	URL      string         `json:"url,omitempty"`
	Filename string         `json:"filename,omitempty"`
}

// A2ATaskResult is the JSON representation of a task result.
type A2ATaskResult struct {
	ID        string        `json:"id"`
	Status    string        `json:"status"`
	Messages  []A2AMessage  `json:"messages,omitempty"`
	Artifacts []A2AArtifact `json:"artifacts,omitempty"`
}

// A2AArtifact is the JSON representation of an artifact.
type A2AArtifact struct {
	ArtifactID string    `json:"artifact_id"`
	Name       string    `json:"name"`
	Parts      []A2APart `json:"parts"`
}

// A2AAgentCard is the JSON representation of an A2A Agent Card.
type A2AAgentCard struct {
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	Version         string     `json:"version"`
	ProtocolVersion string     `json:"protocol_version"`
	Skills          []A2ASkill `json:"skills"`
	URL             string     `json:"url,omitempty"`
}

// A2ASkill is the JSON representation of a skill.
type A2ASkill struct {
	ID           string `json:"id"`
	Description  string `json:"description"`
	InputSchema  string `json:"input_schema,omitempty"`
	OutputSchema string `json:"output_schema,omitempty"`
}

// ── Translation functions ───────────────────────────────────

// HTTPMessageToProto converts an A2A HTTP message to protobuf.
func HTTPMessageToProto(msg A2AMessage) *pb.Message {
	role := pb.MessageRole_MESSAGE_ROLE_USER
	if msg.Role == "agent" {
		role = pb.MessageRole_MESSAGE_ROLE_AGENT
	}

	parts := make([]*pb.Part, 0, len(msg.Parts))
	for _, p := range msg.Parts {
		parts = append(parts, httpPartToProto(p))
	}

	return &pb.Message{
		MessageId: uuid.New().String(),
		Role:      role,
		Parts:     parts,
		CreatedAt: timestamppb.Now(),
	}
}

func httpPartToProto(p A2APart) *pb.Part {
	part := &pb.Part{}
	switch p.Type {
	case "text":
		part.Content = &pb.Part_TextPart{TextPart: &pb.TextPart{Text: p.Text}}
	case "url":
		part.Content = &pb.Part_UrlPart{UrlPart: &pb.UrlPart{Url: p.URL, Filename: p.Filename}}
	default:
		part.Content = &pb.Part_TextPart{TextPart: &pb.TextPart{Text: p.Text}}
	}
	return part
}

// ProtoMessageToHTTP converts a protobuf message to A2A HTTP format.
func ProtoMessageToHTTP(msg *pb.Message) A2AMessage {
	role := "user"
	if msg.Role == pb.MessageRole_MESSAGE_ROLE_AGENT {
		role = "agent"
	}

	parts := make([]A2APart, 0, len(msg.Parts))
	for _, p := range msg.Parts {
		parts = append(parts, protoPartToHTTP(p))
	}

	return A2AMessage{Role: role, Parts: parts}
}

func protoPartToHTTP(p *pb.Part) A2APart {
	switch c := p.Content.(type) {
	case *pb.Part_TextPart:
		return A2APart{Type: "text", Text: c.TextPart.Text}
	case *pb.Part_UrlPart:
		return A2APart{Type: "url", URL: c.UrlPart.Url, Filename: c.UrlPart.Filename}
	default:
		return A2APart{Type: "text", Text: ""}
	}
}

// ProtoTaskToHTTP converts a protobuf Task to A2A HTTP format.
func ProtoTaskToHTTP(task *pb.Task) A2ATaskResult {
	messages := make([]A2AMessage, 0, len(task.Messages))
	for _, m := range task.Messages {
		messages = append(messages, ProtoMessageToHTTP(m))
	}

	artifacts := make([]A2AArtifact, 0, len(task.Artifacts))
	for _, a := range task.Artifacts {
		parts := make([]A2APart, 0, len(a.Parts))
		for _, p := range a.Parts {
			parts = append(parts, protoPartToHTTP(p))
		}
		artifacts = append(artifacts, A2AArtifact{
			ArtifactID: a.ArtifactId,
			Name:       a.Name,
			Parts:      parts,
		})
	}

	return A2ATaskResult{
		ID:        task.TaskId,
		Status:    protoStatusToHTTP(task.Status),
		Messages:  messages,
		Artifacts: artifacts,
	}
}

// ProtoCardToHTTP converts a protobuf AgentCard to A2A HTTP format.
func ProtoCardToHTTP(card *pb.AgentCard, baseURL string) A2AAgentCard {
	skills := make([]A2ASkill, 0, len(card.Skills))
	for _, s := range card.Skills {
		skills = append(skills, A2ASkill{
			ID:           s.Id,
			Description:  s.Description,
			InputSchema:  s.InputSchema,
			OutputSchema: s.OutputSchema,
		})
	}

	return A2AAgentCard{
		Name:            card.Name,
		Description:     card.Description,
		Version:         card.Version,
		ProtocolVersion: card.ProtocolVersion,
		Skills:          skills,
		URL:             baseURL,
	}
}

func protoStatusToHTTP(s pb.TaskStatus) string {
	switch s {
	case pb.TaskStatus_TASK_STATUS_SUBMITTED:
		return "submitted"
	case pb.TaskStatus_TASK_STATUS_WORKING:
		return "working"
	case pb.TaskStatus_TASK_STATUS_INPUT_REQUIRED:
		return "input-required"
	case pb.TaskStatus_TASK_STATUS_COMPLETED:
		return "completed"
	case pb.TaskStatus_TASK_STATUS_FAILED:
		return "failed"
	case pb.TaskStatus_TASK_STATUS_CANCELED:
		return "canceled"
	case pb.TaskStatus_TASK_STATUS_REJECTED:
		return "rejected"
	default:
		return "unknown"
	}
}

// HTTPStatusToProto converts an A2A HTTP status string to protobuf.
func HTTPStatusToProto(status string) pb.TaskStatus {
	switch status {
	case "submitted":
		return pb.TaskStatus_TASK_STATUS_SUBMITTED
	case "working":
		return pb.TaskStatus_TASK_STATUS_WORKING
	case "input-required":
		return pb.TaskStatus_TASK_STATUS_INPUT_REQUIRED
	case "completed":
		return pb.TaskStatus_TASK_STATUS_COMPLETED
	case "failed":
		return pb.TaskStatus_TASK_STATUS_FAILED
	case "canceled":
		return pb.TaskStatus_TASK_STATUS_CANCELED
	case "rejected":
		return pb.TaskStatus_TASK_STATUS_REJECTED
	default:
		return pb.TaskStatus_TASK_STATUS_UNSPECIFIED
	}
}

// BuildA2AHTTPRequest creates an A2A JSON-RPC request for outbound bridge calls.
func BuildA2AHTTPRequest(taskID string, msg *pb.Message) ([]byte, error) {
	params := SendTaskParams{
		ID:      taskID,
		Message: ProtoMessageToHTTP(msg),
	}

	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	req := A2AHTTPRequest{
		JSONRPC: "2.0",
		ID:      taskID,
		Method:  "tasks/send",
		Params:  paramsBytes,
	}

	return json.Marshal(req)
}

// ParseA2AHTTPResponse parses an A2A HTTP JSON-RPC response.
func ParseA2AHTTPResponse(data []byte) (*A2ATaskResult, error) {
	var resp A2AHTTPResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("A2A error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}

	var task A2ATaskResult
	if err := json.Unmarshal(resultBytes, &task); err != nil {
		return nil, fmt.Errorf("unmarshal task result: %w", err)
	}

	return &task, nil
}

// BuildJSONRPCResponse creates a JSON-RPC success response.
func BuildJSONRPCResponse(id any, result any) ([]byte, error) {
	resp := A2AHTTPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	return json.Marshal(resp)
}

// BuildJSONRPCError creates a JSON-RPC error response.
func BuildJSONRPCError(id any, code int, message string) ([]byte, error) {
	resp := A2AHTTPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &A2AError{Code: code, Message: message},
	}
	return json.Marshal(resp)
}

// nowTimestamp returns the current time as a protobuf Timestamp.
func nowTimestamp() *timestamppb.Timestamp {
	return timestamppb.New(time.Now())
}
