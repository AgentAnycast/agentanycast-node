package mcpproxy

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

// MCPTool represents a tool discovered from an MCP Server subprocess.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// initializeResult is the JSON-RPC result from the MCP initialize call.
type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

// toolsListResult is the JSON-RPC result from the MCP tools/list call.
type toolsListResult struct {
	Tools []MCPTool `json:"tools"`
}

// DiscoverTools performs the MCP initialization handshake and returns all
// tools advertised by the subprocess. The sequence is:
//
//  1. Send initialize request (with client capabilities)
//  2. Receive initialize response (server capabilities)
//  3. Send notifications/initialized notification
//  4. Send tools/list request
//  5. Receive tools list
func (p *Process) DiscoverTools(ctx context.Context) ([]MCPTool, error) {
	// Step 1: Initialize handshake.
	initParams := map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "agentanycast-mcp-proxy",
			"version": "0.7.0",
		},
	}

	initResult, err := p.Call(ctx, "initialize", initParams)
	if err != nil {
		return nil, fmt.Errorf("mcpproxy: initialize: %w", err)
	}

	var initResp initializeResult
	if err := json.Unmarshal(initResult, &initResp); err != nil {
		return nil, fmt.Errorf("mcpproxy: parse initialize result: %w", err)
	}

	p.logger.Info("MCP server initialized",
		"server_name", initResp.ServerInfo.Name,
		"server_version", initResp.ServerInfo.Version,
		"protocol_version", initResp.ProtocolVersion,
	)

	// Step 2: Send initialized notification.
	if err := p.Notify(ctx, "notifications/initialized", nil); err != nil {
		return nil, fmt.Errorf("mcpproxy: send initialized notification: %w", err)
	}

	// Step 3: List tools.
	toolsResult, err := p.Call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("mcpproxy: tools/list: %w", err)
	}

	var toolsResp toolsListResult
	if err := json.Unmarshal(toolsResult, &toolsResp); err != nil {
		return nil, fmt.Errorf("mcpproxy: parse tools/list result: %w", err)
	}

	p.logger.Info("discovered MCP tools", "count", len(toolsResp.Tools))
	return toolsResp.Tools, nil
}

// ToolsToSkills converts discovered MCP tools into A2A Skills for the AgentCard.
// Each tool becomes a skill with its name as the skill ID and its JSON input
// schema serialized into the skill's InputSchema field.
func ToolsToSkills(tools []MCPTool) []*pb.Skill {
	skills := make([]*pb.Skill, 0, len(tools))
	for _, t := range tools {
		skill := &pb.Skill{
			Id:          t.Name,
			Description: t.Description,
		}
		if len(t.InputSchema) > 0 {
			skill.InputSchema = string(t.InputSchema)
		}
		skills = append(skills, skill)
	}
	return skills
}
