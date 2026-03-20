package anp

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

// AgentCardToDescription converts an A2A AgentCard to an ANP Agent Description.
func AgentCardToDescription(card *pb.AgentCard, baseURL string) *AgentDescription {
	desc := &AgentDescription{
		Context: map[string]string{
			"@vocab": "https://schema.org/",
			"anp":    "https://agentnetworkprotocol.com/ns/",
		},
		Type:        "anp:AgentDescription",
		ID:          baseURL + "/agent/ad.json",
		Name:        card.Name,
		Description: card.Description,
		Version:     card.Version,
		Interfaces: []InterfaceEntry{
			{
				Type:     "anp:OpenRPCInterface",
				Protocol: "openrpc",
				URL:      baseURL + "/agent/interface.json",
			},
		},
	}

	// Prefer did:web if available, then did:key.
	if ext := card.P2PExtension; ext != nil {
		if ext.DidWeb != "" {
			desc.DID = ext.DidWeb
		} else if ext.DidKey != "" {
			desc.DID = ext.DidKey
		}
	}

	return desc
}

// DescriptionToAgentCard converts an ANP Agent Description to an A2A AgentCard.
func DescriptionToAgentCard(desc *AgentDescription) *pb.AgentCard {
	card := &pb.AgentCard{
		Name:        desc.Name,
		Description: desc.Description,
		Version:     desc.Version,
	}

	// Map the DID into the P2P extension.
	if desc.DID != "" {
		card.P2PExtension = &pb.P2PExtension{}
		if len(desc.DID) > 8 && desc.DID[:8] == "did:web:" {
			card.P2PExtension.DidWeb = desc.DID
		} else if len(desc.DID) > 8 && desc.DID[:8] == "did:key:" {
			card.P2PExtension.DidKey = desc.DID
		}
	}

	return card
}

// SkillsToOpenRPC converts A2A Skills to an OpenRPC specification.
func SkillsToOpenRPC(skills []*pb.Skill, agentName, version string) *OpenRPCSpec {
	methods := make([]RPCMethod, 0, len(skills))
	for _, s := range skills {
		method := RPCMethod{
			Name:        s.Id,
			Description: s.Description,
		}

		// Convert input schema to a params definition if present.
		if s.InputSchema != "" {
			var schema any
			if err := json.Unmarshal([]byte(s.InputSchema), &schema); err == nil {
				method.Params = []RPCParam{
					{
						Name:     "input",
						Schema:   schema,
						Required: true,
					},
				}
			}
		}

		// Convert output schema to a result definition if present.
		if s.OutputSchema != "" {
			var schema any
			if err := json.Unmarshal([]byte(s.OutputSchema), &schema); err == nil {
				method.Result = &RPCResult{
					Name:   "result",
					Schema: schema,
				}
			}
		}

		methods = append(methods, method)
	}

	return &OpenRPCSpec{
		OpenRPC: "1.3.2",
		Info: OpenRPCInfo{
			Title:   agentName,
			Version: version,
		},
		Methods: methods,
	}
}

// OpenRPCToSkills extracts A2A Skills from an OpenRPC specification.
func OpenRPCToSkills(spec *OpenRPCSpec) []*pb.Skill {
	skills := make([]*pb.Skill, 0, len(spec.Methods))
	for _, m := range spec.Methods {
		skill := &pb.Skill{
			Id:          m.Name,
			Description: m.Description,
		}

		// Convert params to input schema.
		if len(m.Params) > 0 {
			schemaBytes, err := json.Marshal(m.Params[0].Schema)
			if err == nil {
				skill.InputSchema = string(schemaBytes)
			}
		}

		// Convert result to output schema.
		if m.Result != nil {
			schemaBytes, err := json.Marshal(m.Result.Schema)
			if err == nil {
				skill.OutputSchema = string(schemaBytes)
			}
		}

		skills = append(skills, skill)
	}
	return skills
}

// TaskToJSONRPCRequest converts an A2A task's message to a JSON-RPC request.
func TaskToJSONRPCRequest(skillID string, messageText string, taskID string) *JSONRPCRequest {
	return &JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  skillID,
		Params: map[string]any{
			"input":   messageText,
			"task_id": taskID,
		},
		ID: taskID,
	}
}

// JSONRPCResultToArtifact converts a JSON-RPC result to an A2A Artifact.
func JSONRPCResultToArtifact(result any) *pb.Artifact {
	return &pb.Artifact{
		ArtifactId: uuid.New().String(),
		Name:       "anp-result",
		Parts: []*pb.Part{
			{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: extractTextFromResult(result)}}},
		},
	}
}

// extractTextFromResult extracts a text string from a JSON-RPC result value.
// It handles string results directly, maps with a "text" key, and falls back
// to JSON serialization for other types.
func extractTextFromResult(result any) string {
	switch v := result.(type) {
	case string:
		return v
	case map[string]any:
		if text, ok := v["text"]; ok {
			return fmt.Sprintf("%v", text)
		}
		data, _ := json.Marshal(v)
		return string(data)
	default:
		data, _ := json.Marshal(v)
		return string(data)
	}
}
