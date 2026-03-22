package mcpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

// Config holds MCP proxy configuration.
type Config struct {
	// Command is the MCP Server executable (e.g., "uvx", "npx", "python").
	Command string

	// Args are the command-line arguments for the MCP Server
	// (e.g., ["mcp-server-filesystem", "/tmp"]).
	Args []string

	// AgentName is the display name for the generated Agent Card.
	// If empty, it is derived from the command name.
	AgentName string

	// Logger is the structured logger for the proxy.
	Logger *slog.Logger
}

// Proxy wraps an MCP Server subprocess and exposes its tools as A2A Skills
// on the AgentAnycast P2P network. It handles the full lifecycle: subprocess
// management, tool discovery, and task-to-tool-call translation.
type Proxy struct {
	process *Process
	tools   []MCPTool
	config  Config
	logger  *slog.Logger
}

// New creates and initializes the MCP proxy. It spawns the MCP Server
// subprocess, performs the initialization handshake, and discovers all
// available tools. Returns an error if the subprocess fails to start
// or the handshake fails.
func New(ctx context.Context, cfg Config) (*Proxy, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	logger := cfg.Logger.With("component", "mcpproxy")

	proc, err := NewProcess(cfg.Command, cfg.Args, cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("mcpproxy: spawn subprocess: %w", err)
	}

	tools, err := proc.DiscoverTools(ctx)
	if err != nil {
		proc.Close()
		return nil, fmt.Errorf("mcpproxy: discover tools: %w", err)
	}

	logger.Info("MCP proxy initialized",
		"command", cfg.Command,
		"agent_name", cfg.AgentName,
		"tools", len(tools),
	)

	return &Proxy{
		process: proc,
		tools:   tools,
		config:  cfg,
		logger:  logger,
	}, nil
}

// Tools returns the MCP tools discovered from the subprocess.
func (p *Proxy) Tools() []MCPTool {
	return p.tools
}

// Skills returns the A2A Skills derived from the discovered MCP tools.
func (p *Proxy) Skills() []*pb.Skill {
	return ToolsToSkills(p.tools)
}

// toolCallResult is the JSON-RPC result from an MCP tools/call request.
type toolCallResult struct {
	Content []toolCallContent `json:"content"`
	IsError bool              `json:"isError,omitempty"`
}

// toolCallContent is a single content item in a tool call result.
type toolCallContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// HandleTask processes an incoming A2A task by calling the appropriate MCP tool
// on the subprocess. The toolName identifies which tool to invoke, and arguments
// is the JSON-encoded input for the tool.
//
// Returns the raw JSON result from the MCP tool call.
func (p *Proxy) HandleTask(ctx context.Context, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	// Parse arguments — if empty or null, use empty object.
	var args any
	if len(arguments) == 0 || string(arguments) == "null" {
		args = map[string]any{}
	} else {
		if err := json.Unmarshal(arguments, &args); err != nil {
			return nil, fmt.Errorf("mcpproxy: invalid arguments JSON: %w", err)
		}
	}

	p.logger.Debug("calling MCP tool", "tool", toolName)

	result, err := p.process.Call(ctx, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": args,
	})
	if err != nil {
		return nil, fmt.Errorf("mcpproxy: tools/call %s: %w", toolName, err)
	}

	return result, nil
}

// Close terminates the MCP Server subprocess.
func (p *Proxy) Close() error {
	p.logger.Info("shutting down MCP proxy", "command", p.config.Command)
	return p.process.Close()
}
