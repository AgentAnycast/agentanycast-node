package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/protobuf/proto"

	"github.com/agentanycast/agentanycast-node/internal/crypto"
	"github.com/agentanycast/agentanycast-node/internal/metrics"
	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

// ── Tool Definitions ─────────────────────────────────────────────────

var toolGetNodeInfo = mcp.Tool{
	Name:        "get_node_info",
	Description: "Get this node's identity and network information (PeerID, DID, listen addresses, connected peer count).",
	InputSchema: mcp.ToolInputSchema{
		Type:       "object",
		Properties: map[string]any{},
	},
}

var toolListConnectedPeers = mcp.Tool{
	Name:        "list_connected_peers",
	Description: "List all peers currently connected to this node.",
	InputSchema: mcp.ToolInputSchema{
		Type:       "object",
		Properties: map[string]any{},
	},
}

var toolGetAgentCard = mcp.Tool{
	Name:        "get_agent_card",
	Description: "Get the A2A Agent Card (name, description, skills) for a connected peer or this node.",
	InputSchema: mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]any{
			"peer_id": map[string]any{
				"type":        "string",
				"description": "PeerID of the agent. Use 'self' or omit to get this node's card.",
			},
		},
	},
}

var toolDiscoverAgents = mcp.Tool{
	Name:        "discover_agents",
	Description: "Find AI agents on the P2P network that offer a specific skill. Queries the DHT and relay skill registry.",
	InputSchema: mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]any{
			"skill": map[string]any{
				"type":        "string",
				"description": "The skill to search for (e.g., 'translate', 'summarize', 'code-review').",
			},
		},
		Required: []string{"skill"},
	},
}

var toolSendTask = mcp.Tool{
	Name:        "send_task",
	Description: "Send an encrypted A2A task to a remote AI agent. Target can be a PeerID, skill name (with by_skill=true), or HTTP URL.",
	InputSchema: mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]any{
			"target": map[string]any{
				"type":        "string",
				"description": "Target PeerID, skill name (if by_skill is true), or HTTP URL of the remote agent.",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "The message text to send to the agent.",
			},
			"by_skill": map[string]any{
				"type":        "boolean",
				"description": "If true, treat target as a skill name and use anycast routing to find the best agent.",
			},
		},
		Required: []string{"target", "message"},
	},
}

var toolSendTaskBySkill = mcp.Tool{
	Name:        "send_task_by_skill",
	Description: "Send an encrypted A2A task using anycast routing — the network automatically finds the best agent for the skill.",
	InputSchema: mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]any{
			"skill": map[string]any{
				"type":        "string",
				"description": "The skill to route to (e.g., 'translate', 'summarize').",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "The message text to send to the agent.",
			},
		},
		Required: []string{"skill", "message"},
	},
}

var toolGetTaskStatus = mcp.Tool{
	Name:        "get_task_status",
	Description: "Get the current status and result of a previously sent task.",
	InputSchema: mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "The task ID returned by send_task or send_task_by_skill.",
			},
		},
		Required: []string{"task_id"},
	},
}

// ── Tool Handlers ────────────────────────────────────────────────────

func (s *Server) handleGetNodeInfo(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h := s.config.Host
	addrs := h.Addrs()
	addrStrs := make([]string, len(addrs))
	for i, a := range addrs {
		addrStrs[i] = a.String()
	}

	didKey, _ := crypto.PeerIDToDIDKey(h.ID())

	result := map[string]any{
		"peer_id":          h.ID().String(),
		"did_key":          didKey,
		"listen_addresses": addrStrs,
		"connected_peers":  len(h.ConnectedPeers()),
	}
	metrics.MCPToolCalls.WithLabelValues("get_node_info", "ok").Inc()
	return marshalToolResult(result)
}

func (s *Server) handleListConnectedPeers(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h := s.config.Host
	peers := h.ConnectedPeers()

	peerList := make([]map[string]any, 0, len(peers))
	for _, pid := range peers {
		addrs := h.Peerstore().Addrs(pid)
		addrStrs := make([]string, len(addrs))
		for i, a := range addrs {
			addrStrs[i] = a.String()
		}
		peerList = append(peerList, map[string]any{
			"peer_id":   pid.String(),
			"addresses": addrStrs,
		})
	}

	metrics.MCPToolCalls.WithLabelValues("list_connected_peers", "ok").Inc()
	return marshalToolResult(map[string]any{
		"count": len(peerList),
		"peers": peerList,
	})
}

func (s *Server) handleGetAgentCard(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	peerIDStr := stringArg(args, "peer_id")

	// Self card
	if peerIDStr == "" || peerIDStr == "self" {
		cardBytes := s.config.CardProvider()
		if cardBytes == nil {
			metrics.MCPToolCalls.WithLabelValues("get_agent_card", "error").Inc()
			return mcp.NewToolResultError("no agent card set for this node"), nil
		}
		var card pb.AgentCard
		if err := proto.Unmarshal(cardBytes, &card); err != nil {
			metrics.MCPToolCalls.WithLabelValues("get_agent_card", "error").Inc()
			return mcp.NewToolResultError("failed to parse local card: " + err.Error()), nil
		}
		metrics.MCPToolCalls.WithLabelValues("get_agent_card", "ok").Inc()
		return marshalToolResult(agentCardToMap(&card))
	}

	// Peer card
	pid, err := peer.Decode(peerIDStr)
	if err != nil {
		metrics.MCPToolCalls.WithLabelValues("get_agent_card", "error").Inc()
		return mcp.NewToolResultError("invalid peer_id: " + err.Error()), nil
	}

	cardBytes, ok := s.config.Host.GetPeerCard(pid)
	if !ok {
		metrics.MCPToolCalls.WithLabelValues("get_agent_card", "error").Inc()
		return mcp.NewToolResultError(fmt.Sprintf("no card available for peer %s", peerIDStr)), nil
	}

	var card pb.AgentCard
	if err := proto.Unmarshal(cardBytes, &card); err != nil {
		metrics.MCPToolCalls.WithLabelValues("get_agent_card", "error").Inc()
		return mcp.NewToolResultError("failed to parse peer card: " + err.Error()), nil
	}

	metrics.MCPToolCalls.WithLabelValues("get_agent_card", "ok").Inc()
	return marshalToolResult(agentCardToMap(&card))
}

func (s *Server) handleDiscoverAgents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	skill := stringArg(args, "skill")
	if skill == "" {
		metrics.MCPToolCalls.WithLabelValues("discover_agents", "error").Inc()
		return mcp.NewToolResultError("'skill' parameter is required"), nil
	}

	if s.config.AnycastRouter == nil {
		metrics.MCPToolCalls.WithLabelValues("discover_agents", "error").Inc()
		return mcp.NewToolResultError("discovery is not available (no anycast router configured)"), nil
	}

	endpoints, err := s.config.AnycastRouter.Discover(ctx, skill)
	if err != nil {
		metrics.MCPToolCalls.WithLabelValues("discover_agents", "error").Inc()
		return mcp.NewToolResultError("discovery failed: " + err.Error()), nil
	}

	agents := make([]map[string]any, 0, len(endpoints))
	for _, ep := range endpoints {
		skills := make([]map[string]string, 0, len(ep.Skills))
		for _, sk := range ep.Skills {
			skills = append(skills, map[string]string{
				"skill_id":    sk.SkillID,
				"description": sk.Description,
			})
		}
		agents = append(agents, map[string]any{
			"peer_id":    ep.PeerID.String(),
			"agent_name": ep.AgentName,
			"skills":     skills,
		})
	}

	metrics.MCPToolCalls.WithLabelValues("discover_agents", "ok").Inc()
	return marshalToolResult(map[string]any{
		"skill":       skill,
		"agent_count": len(agents),
		"agents":      agents,
	})
}

func (s *Server) handleSendTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	target := stringArg(args, "target")
	message := stringArg(args, "message")
	bySkill := boolArg(args, "by_skill")

	if target == "" {
		metrics.MCPToolCalls.WithLabelValues("send_task", "error").Inc()
		return mcp.NewToolResultError("'target' parameter is required"), nil
	}
	if message == "" {
		metrics.MCPToolCalls.WithLabelValues("send_task", "error").Inc()
		return mcp.NewToolResultError("'message' parameter is required"), nil
	}

	msg := &pb.Message{
		Role: pb.MessageRole_MESSAGE_ROLE_USER,
		Parts: []*pb.Part{
			{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: message}}},
		},
	}

	// Route by type
	if bySkill {
		return s.sendBySkill(ctx, target, msg)
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return s.sendByURL(ctx, target, msg)
	}
	return s.sendByPeerID(ctx, target, msg)
}

func (s *Server) handleSendTaskBySkill(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	skill := stringArg(args, "skill")
	message := stringArg(args, "message")

	if skill == "" || message == "" {
		metrics.MCPToolCalls.WithLabelValues("send_task_by_skill", "error").Inc()
		return mcp.NewToolResultError("'skill' and 'message' parameters are required"), nil
	}

	msg := &pb.Message{
		Role: pb.MessageRole_MESSAGE_ROLE_USER,
		Parts: []*pb.Part{
			{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: message}}},
		},
	}
	return s.sendBySkill(ctx, skill, msg)
}

func (s *Server) handleGetTaskStatus(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	taskID := stringArg(args, "task_id")
	if taskID == "" {
		metrics.MCPToolCalls.WithLabelValues("get_task_status", "error").Inc()
		return mcp.NewToolResultError("'task_id' parameter is required"), nil
	}

	task, err := s.config.Engine.GetTask(taskID)
	if err != nil {
		metrics.MCPToolCalls.WithLabelValues("get_task_status", "error").Inc()
		return mcp.NewToolResultError("task not found: " + err.Error()), nil
	}

	metrics.MCPToolCalls.WithLabelValues("get_task_status", "ok").Inc()
	return marshalToolResult(map[string]any{
		"task_id": task.ID,
		"status":  task.Status.String(),
	})
}

// ── Send helpers ─────────────────────────────────────────────────────

func (s *Server) sendByPeerID(ctx context.Context, peerIDStr string, msg *pb.Message) (*mcp.CallToolResult, error) {
	pid, err := peer.Decode(peerIDStr)
	if err != nil {
		metrics.MCPToolCalls.WithLabelValues("send_task", "error").Inc()
		return mcp.NewToolResultError("invalid peer_id: " + err.Error()), nil
	}

	task, err := s.config.Router.SendTask(ctx, pid, msg, "", "")
	if err != nil {
		metrics.MCPToolCalls.WithLabelValues("send_task", "error").Inc()
		return mcp.NewToolResultError("failed to send task: " + err.Error()), nil
	}

	metrics.MCPToolCalls.WithLabelValues("send_task", "ok").Inc()
	return marshalToolResult(map[string]any{
		"task_id": task.ID,
		"status":  task.Status.String(),
		"target":  peerIDStr,
		"mode":    "direct",
	})
}

func (s *Server) sendBySkill(ctx context.Context, skill string, msg *pb.Message) (*mcp.CallToolResult, error) {
	if s.config.AnycastRouter == nil {
		metrics.MCPToolCalls.WithLabelValues("send_task_by_skill", "error").Inc()
		return mcp.NewToolResultError("anycast routing is not available"), nil
	}

	pid, err := s.config.AnycastRouter.Resolve(ctx, skill)
	if err != nil {
		metrics.MCPToolCalls.WithLabelValues("send_task_by_skill", "error").Inc()
		return mcp.NewToolResultError("no agents found for skill '" + skill + "': " + err.Error()), nil
	}

	task, err := s.config.Router.SendTask(ctx, pid, msg, skill, "")
	if err != nil {
		metrics.MCPToolCalls.WithLabelValues("send_task_by_skill", "error").Inc()
		return mcp.NewToolResultError("failed to send task: " + err.Error()), nil
	}

	metrics.MCPToolCalls.WithLabelValues("send_task_by_skill", "ok").Inc()
	return marshalToolResult(map[string]any{
		"task_id": task.ID,
		"status":  task.Status.String(),
		"target":  pid.String(),
		"skill":   skill,
		"mode":    "anycast",
	})
}

func (s *Server) sendByURL(ctx context.Context, url string, msg *pb.Message) (*mcp.CallToolResult, error) {
	if s.config.OutboundBridge == nil {
		metrics.MCPToolCalls.WithLabelValues("send_task", "error").Inc()
		return mcp.NewToolResultError("HTTP bridge is not available"), nil
	}

	task, err := s.config.OutboundBridge.SendTask(ctx, url, msg, nil)
	if err != nil {
		metrics.MCPToolCalls.WithLabelValues("send_task", "error").Inc()
		return mcp.NewToolResultError("HTTP bridge request failed: " + err.Error()), nil
	}

	metrics.MCPToolCalls.WithLabelValues("send_task", "ok").Inc()
	return marshalToolResult(map[string]any{
		"task_id": task.TaskId,
		"status":  task.Status.String(),
		"target":  url,
		"mode":    "http_bridge",
	})
}

// ── Helpers ──────────────────────────────────────────────────────────

// marshalToolResult serializes a value to JSON and wraps it in a tool result.
func marshalToolResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError("internal error: " + err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// parseArgs extracts the arguments map from a CallToolRequest.
func parseArgs(req mcp.CallToolRequest) map[string]any {
	if req.Params.Arguments == nil {
		return map[string]any{}
	}
	if m, ok := req.Params.Arguments.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func boolArg(args map[string]any, key string) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// agentCardToMap converts a protobuf AgentCard to a JSON-friendly map.
func agentCardToMap(card *pb.AgentCard) map[string]any {
	skills := make([]map[string]any, 0, len(card.Skills))
	for _, sk := range card.Skills {
		skills = append(skills, map[string]any{
			"id":          sk.Id,
			"description": sk.Description,
		})
	}

	result := map[string]any{
		"name":             card.Name,
		"description":      card.Description,
		"version":          card.Version,
		"protocol_version": card.ProtocolVersion,
		"skills":           skills,
	}

	if p2p := card.P2PExtension; p2p != nil {
		result["peer_id"] = p2p.PeerId
		result["did_key"] = p2p.DidKey
		result["supported_transports"] = p2p.SupportedTransports
		result["relay_addresses"] = p2p.RelayAddresses
	}

	return result
}
