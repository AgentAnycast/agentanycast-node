package anp

import (
	"encoding/json"
	"testing"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

func TestAgentCardToDescription(t *testing.T) {
	card := &pb.AgentCard{
		Name:        "Test Agent",
		Description: "A test agent",
		Version:     "1.0",
		Skills: []*pb.Skill{
			{Id: "echo", Description: "Echo skill"},
		},
		P2PExtension: &pb.P2PExtension{
			DidKey: "did:key:z6Mk...",
			DidWeb: "did:web:example.com",
		},
	}

	desc := AgentCardToDescription(card, "https://example.com")

	if desc.Name != "Test Agent" {
		t.Fatalf("expected name 'Test Agent', got %q", desc.Name)
	}
	if desc.ID != "https://example.com/agent/ad.json" {
		t.Fatalf("expected ID with /agent/ad.json, got %q", desc.ID)
	}
	// did:web should be preferred over did:key.
	if desc.DID != "did:web:example.com" {
		t.Fatalf("expected did:web, got %q", desc.DID)
	}
	if len(desc.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(desc.Interfaces))
	}
	if desc.Interfaces[0].Protocol != "openrpc" {
		t.Fatalf("expected openrpc protocol, got %q", desc.Interfaces[0].Protocol)
	}
}

func TestAgentCardToDescriptionDidKeyFallback(t *testing.T) {
	card := &pb.AgentCard{
		Name: "Agent",
		P2PExtension: &pb.P2PExtension{
			DidKey: "did:key:z6Mk...",
		},
	}

	desc := AgentCardToDescription(card, "https://example.com")
	if desc.DID != "did:key:z6Mk..." {
		t.Fatalf("expected did:key fallback, got %q", desc.DID)
	}
}

func TestAgentCardToDescriptionNilExtension(t *testing.T) {
	card := &pb.AgentCard{Name: "Agent"}
	desc := AgentCardToDescription(card, "https://example.com")
	if desc.DID != "" {
		t.Fatalf("expected empty DID, got %q", desc.DID)
	}
}

func TestDescriptionToAgentCard(t *testing.T) {
	desc := &AgentDescription{
		Name:        "ANP Agent",
		Description: "An ANP agent",
		Version:     "2.0",
		DID:         "did:web:agent.example.com",
	}

	card := DescriptionToAgentCard(desc)
	if card.Name != "ANP Agent" {
		t.Fatalf("expected 'ANP Agent', got %q", card.Name)
	}
	if card.P2PExtension == nil {
		t.Fatal("expected P2PExtension to be set")
	}
	if card.P2PExtension.DidWeb != "did:web:agent.example.com" {
		t.Fatalf("expected did:web, got %q", card.P2PExtension.DidWeb)
	}
}

func TestDescriptionToAgentCardDidKey(t *testing.T) {
	desc := &AgentDescription{
		Name: "Key Agent",
		DID:  "did:key:z6MkTest",
	}
	card := DescriptionToAgentCard(desc)
	if card.P2PExtension.DidKey != "did:key:z6MkTest" {
		t.Fatalf("expected did:key, got %q", card.P2PExtension.DidKey)
	}
}

func TestDescriptionToAgentCardNoDID(t *testing.T) {
	desc := &AgentDescription{Name: "No DID Agent"}
	card := DescriptionToAgentCard(desc)
	if card.P2PExtension != nil {
		t.Fatal("expected nil P2PExtension when no DID")
	}
}

func TestSkillsToOpenRPCRoundTrip(t *testing.T) {
	skills := []*pb.Skill{
		{
			Id:           "translate",
			Description:  "Translate text",
			InputSchema:  `{"type":"object","properties":{"text":{"type":"string"}}}`,
			OutputSchema: `{"type":"object","properties":{"result":{"type":"string"}}}`,
		},
		{
			Id:          "echo",
			Description: "Echo input back",
		},
	}

	spec := SkillsToOpenRPC(skills, "Test Agent", "1.0")

	if spec.OpenRPC != "1.3.2" {
		t.Fatalf("expected openrpc 1.3.2, got %q", spec.OpenRPC)
	}
	if spec.Info.Title != "Test Agent" {
		t.Fatalf("expected title 'Test Agent', got %q", spec.Info.Title)
	}
	if len(spec.Methods) != 2 {
		t.Fatalf("expected 2 methods, got %d", len(spec.Methods))
	}

	// Verify first method has params and result.
	m := spec.Methods[0]
	if m.Name != "translate" {
		t.Fatalf("expected method name 'translate', got %q", m.Name)
	}
	if len(m.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(m.Params))
	}
	if m.Result == nil {
		t.Fatal("expected non-nil result for translate method")
	}

	// Second method should have no params/result.
	if len(spec.Methods[1].Params) != 0 {
		t.Fatalf("expected 0 params for echo, got %d", len(spec.Methods[1].Params))
	}

	// Round-trip back to skills.
	roundTripped := OpenRPCToSkills(spec)
	if len(roundTripped) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(roundTripped))
	}
	if roundTripped[0].Id != "translate" {
		t.Fatalf("expected skill ID 'translate', got %q", roundTripped[0].Id)
	}
	if roundTripped[0].Description != "Translate text" {
		t.Fatalf("expected description 'Translate text', got %q", roundTripped[0].Description)
	}
	if roundTripped[0].InputSchema == "" {
		t.Fatal("expected non-empty input schema after round-trip")
	}
	if roundTripped[0].OutputSchema == "" {
		t.Fatal("expected non-empty output schema after round-trip")
	}
}

func TestOpenRPCToSkillsEmpty(t *testing.T) {
	spec := &OpenRPCSpec{Methods: []RPCMethod{}}
	skills := OpenRPCToSkills(spec)
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills, got %d", len(skills))
	}
}

func TestTaskToJSONRPCRequest(t *testing.T) {
	req := TaskToJSONRPCRequest("echo", "Hello, world!", "task-123")

	if req.JSONRPC != "2.0" {
		t.Fatalf("expected jsonrpc 2.0, got %q", req.JSONRPC)
	}
	if req.Method != "echo" {
		t.Fatalf("expected method 'echo', got %q", req.Method)
	}
	if req.ID != "task-123" {
		t.Fatalf("expected ID 'task-123', got %v", req.ID)
	}

	params, ok := req.Params.(map[string]interface{})
	if !ok {
		t.Fatal("expected map params")
	}
	if params["input"] != "Hello, world!" {
		t.Fatalf("expected input 'Hello, world!', got %v", params["input"])
	}
}

func TestJSONRPCResultToArtifactString(t *testing.T) {
	artifact := JSONRPCResultToArtifact("Hello back!")

	if artifact.Name != "anp-result" {
		t.Fatalf("expected name 'anp-result', got %q", artifact.Name)
	}
	if len(artifact.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(artifact.Parts))
	}
	text := artifact.Parts[0].GetTextPart()
	if text == nil || text.Text != "Hello back!" {
		t.Fatalf("expected 'Hello back!', got %v", artifact.Parts[0])
	}
}

func TestJSONRPCResultToArtifactMap(t *testing.T) {
	result := map[string]interface{}{
		"text": "Translated text",
	}
	artifact := JSONRPCResultToArtifact(result)
	text := artifact.Parts[0].GetTextPart()
	if text == nil || text.Text != "Translated text" {
		t.Fatalf("expected 'Translated text', got %v", artifact.Parts[0])
	}
}

func TestJSONRPCResultToArtifactMapNoText(t *testing.T) {
	result := map[string]interface{}{
		"key": "value",
	}
	artifact := JSONRPCResultToArtifact(result)
	text := artifact.Parts[0].GetTextPart()
	if text == nil {
		t.Fatal("expected text part")
	}
	// Should be JSON-encoded.
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(text.Text), &parsed); err != nil {
		t.Fatalf("expected valid JSON, got %q", text.Text)
	}
}

func TestJSONRPCResultToArtifactOtherType(t *testing.T) {
	artifact := JSONRPCResultToArtifact(42)
	text := artifact.Parts[0].GetTextPart()
	if text == nil || text.Text != "42" {
		t.Fatalf("expected '42', got %v", artifact.Parts[0])
	}
}
