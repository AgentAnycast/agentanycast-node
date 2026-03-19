// Package mcp implements the MCP (Model Context Protocol) server interface
// for the AgentAnycast daemon. This is the third protocol interface alongside
// gRPC (SDK communication) and HTTP Bridge (A2A HTTP interop).
//
// The MCP server exposes P2P networking capabilities as MCP tools, enabling
// any MCP-compatible AI tool (Claude Desktop, ChatGPT, Cursor, VS Code,
// Gemini CLI, etc.) to directly interact with the AgentAnycast P2P network.
//
// Two transport modes are supported:
//   - stdio: AI tools spawn the daemon and communicate via stdin/stdout
//   - Streamable HTTP: remote clients connect via HTTP (required for ChatGPT)
package mcp

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/agentanycast/agentanycast-node/internal/a2a"
	"github.com/agentanycast/agentanycast-node/internal/anycast"
	"github.com/agentanycast/agentanycast-node/internal/bridge"
	"github.com/agentanycast/agentanycast-node/internal/node"
	"github.com/agentanycast/agentanycast-node/internal/store"
)

// Config holds MCP server dependencies. These are the same internal services
// used by the gRPC server, ensuring consistent behavior across interfaces.
type Config struct {
	Host           *node.Host
	Engine         *a2a.Engine
	Router         *a2a.Router
	Store          *store.Store
	Logger         *slog.Logger
	AnycastRouter  *anycast.Router
	OutboundBridge *bridge.OutboundClient
	StreamManager  *a2a.StreamManager

	// CardProvider returns the current local AgentCard as serialized protobuf bytes.
	// This decouples the MCP server from the gRPC server.
	CardProvider func() []byte
}

// Server wraps the MCP protocol server and provides two transport modes.
type Server struct {
	mcpServer *server.MCPServer
	config    Config
	logger    *slog.Logger
}

// Version is set at build time via ldflags.
var Version = "0.4.0-dev"

// New creates a new MCP server with all tools registered.
func New(cfg Config) *Server {
	mcpSrv := server.NewMCPServer(
		"agentanycast",
		Version,
		server.WithToolCapabilities(true),
	)

	s := &Server{
		mcpServer: mcpSrv,
		config:    cfg,
		logger:    cfg.Logger.With("component", "mcp"),
	}

	s.registerTools()
	return s
}

// ServeStdio runs the MCP server over stdin/stdout for local AI tool integration
// (Claude Desktop, Cursor, VS Code, Gemini CLI, JetBrains, etc.).
// This method blocks until stdin is closed or the context is canceled.
func (s *Server) ServeStdio(ctx context.Context) error {
	s.logger.Info("MCP stdio server starting")
	stdioSrv := server.NewStdioServer(s.mcpServer)
	return stdioSrv.Listen(ctx, os.Stdin, os.Stdout)
}

// ServeHTTP starts the Streamable HTTP transport for remote clients (ChatGPT, etc.).
// Returns the underlying http.Server for lifecycle management by the caller.
func (s *Server) ServeHTTP(addr string) (*http.Server, error) {
	httpTransport := server.NewStreamableHTTPServer(s.mcpServer)

	srv := &http.Server{
		Addr:         addr,
		Handler:      httpTransport,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("MCP HTTP server error", "error", err)
		}
	}()

	s.logger.Info("MCP Streamable HTTP server started", "addr", addr)
	return srv, nil
}

// registerTools registers all MCP tools with the server.
func (s *Server) registerTools() {
	s.mcpServer.AddTool(toolGetNodeInfo, s.handleGetNodeInfo)
	s.mcpServer.AddTool(toolListConnectedPeers, s.handleListConnectedPeers)
	s.mcpServer.AddTool(toolGetAgentCard, s.handleGetAgentCard)
	s.mcpServer.AddTool(toolDiscoverAgents, s.handleDiscoverAgents)
	s.mcpServer.AddTool(toolSendTask, s.handleSendTask)
	s.mcpServer.AddTool(toolSendTaskBySkill, s.handleSendTaskBySkill)
	s.mcpServer.AddTool(toolGetTaskStatus, s.handleGetTaskStatus)
}
