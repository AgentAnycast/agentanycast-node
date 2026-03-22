package mcpproxy

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"
)

// mockMCPServerScript is a shell script that simulates an MCP server.
// It reads JSON-RPC requests from stdin and writes responses to stdout.
const mockMCPServerScript = `#!/bin/sh
# Mock MCP Server — reads JSON-RPC from stdin, writes responses to stdout.
# Supports: initialize, notifications/initialized, tools/list, tools/call.

while IFS= read -r line; do
  id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  method=$(echo "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')

  case "$method" in
    "initialize")
      echo "{\"jsonrpc\":\"2.0\",\"id\":${id},\"result\":{\"protocolVersion\":\"2025-03-26\",\"serverInfo\":{\"name\":\"mock-mcp-server\",\"version\":\"1.0.0\"},\"capabilities\":{\"tools\":{}}}}"
      ;;
    "notifications/initialized")
      # Notification — no response.
      ;;
    "tools/list")
      echo "{\"jsonrpc\":\"2.0\",\"id\":${id},\"result\":{\"tools\":[{\"name\":\"echo\",\"description\":\"Echoes the input back\",\"inputSchema\":{\"type\":\"object\",\"properties\":{\"message\":{\"type\":\"string\"}},\"required\":[\"message\"]}},{\"name\":\"add\",\"description\":\"Adds two numbers\",\"inputSchema\":{\"type\":\"object\",\"properties\":{\"a\":{\"type\":\"number\"},\"b\":{\"type\":\"number\"}},\"required\":[\"a\",\"b\"]}}]}}"
      ;;
    "tools/call")
      tool_name=$(echo "$line" | sed -n 's/.*"name":"\([^"]*\)".*/\1/p')
      case "$tool_name" in
        "echo")
          msg=$(echo "$line" | sed -n 's/.*"message":"\([^"]*\)".*/\1/p')
          echo "{\"jsonrpc\":\"2.0\",\"id\":${id},\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"echo: ${msg}\"}]}}"
          ;;
        "add")
          echo "{\"jsonrpc\":\"2.0\",\"id\":${id},\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"42\"}]}}"
          ;;
        *)
          echo "{\"jsonrpc\":\"2.0\",\"id\":${id},\"error\":{\"code\":-32601,\"message\":\"unknown tool: ${tool_name}\"}}"
          ;;
      esac
      ;;
    *)
      echo "{\"jsonrpc\":\"2.0\",\"id\":${id},\"error\":{\"code\":-32601,\"message\":\"method not found\"}}"
      ;;
  esac
done
`

// writeMockScript writes the mock MCP server script to a temporary file
// and returns its path.
func writeMockScript(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "mock-mcp-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(mockMCPServerScript); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0o755); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestDiscoverTools(t *testing.T) {
	script := writeMockScript(t)
	logger := testLogger()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proc, err := NewProcess("sh", []string{script}, logger)
	if err != nil {
		t.Fatalf("NewProcess: %v", err)
	}
	defer proc.Close()

	tools, err := proc.DiscoverTools(ctx)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	// Verify tool names.
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["echo"] || !names["add"] {
		t.Fatalf("expected tools [echo, add], got %v", names)
	}

	// Verify echo tool has a description.
	for _, tool := range tools {
		if tool.Name == "echo" && tool.Description != "Echoes the input back" {
			t.Fatalf("echo tool description = %q, want %q", tool.Description, "Echoes the input back")
		}
	}
}

func TestToolsToSkills(t *testing.T) {
	tools := []MCPTool{
		{
			Name:        "echo",
			Description: "Echoes the input back",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`),
		},
		{
			Name:        "add",
			Description: "Adds two numbers",
		},
	}

	skills := ToolsToSkills(tools)
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}

	if skills[0].Id != "echo" {
		t.Fatalf("skill[0].Id = %q, want %q", skills[0].Id, "echo")
	}
	if skills[0].Description != "Echoes the input back" {
		t.Fatalf("skill[0].Description = %q, want %q", skills[0].Description, "Echoes the input back")
	}
	if skills[0].InputSchema == "" {
		t.Fatal("skill[0].InputSchema should not be empty")
	}

	if skills[1].Id != "add" {
		t.Fatalf("skill[1].Id = %q, want %q", skills[1].Id, "add")
	}
}

func TestHandleTask(t *testing.T) {
	script := writeMockScript(t)
	logger := testLogger()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proxy, err := New(ctx, Config{
		Command:   "sh",
		Args:      []string{script},
		AgentName: "test-proxy",
		Logger:    logger,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer proxy.Close()

	// Verify tools were discovered.
	if len(proxy.Tools()) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(proxy.Tools()))
	}

	// Call the echo tool.
	args := json.RawMessage(`{"message": "hello"}`)
	result, err := proxy.HandleTask(ctx, "echo", args)
	if err != nil {
		t.Fatalf("HandleTask(echo): %v", err)
	}

	var callResult toolCallResult
	if err := json.Unmarshal(result, &callResult); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(callResult.Content) == 0 {
		t.Fatal("expected at least one content item")
	}
	if callResult.Content[0].Text != "echo: hello" {
		t.Fatalf("echo result = %q, want %q", callResult.Content[0].Text, "echo: hello")
	}
}

func TestHandleTaskUnknownTool(t *testing.T) {
	script := writeMockScript(t)
	logger := testLogger()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proxy, err := New(ctx, Config{
		Command:   "sh",
		Args:      []string{script},
		AgentName: "test-proxy",
		Logger:    logger,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer proxy.Close()

	// Call a nonexistent tool — should return a JSON-RPC error.
	_, err = proxy.HandleTask(ctx, "nonexistent", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
}

func TestProcessClose(t *testing.T) {
	script := writeMockScript(t)
	logger := testLogger()

	proc, err := NewProcess("sh", []string{script}, logger)
	if err != nil {
		t.Fatalf("NewProcess: %v", err)
	}

	// Close should terminate cleanly.
	if err := proc.Close(); err != nil {
		// Some shells exit with non-zero when stdin is closed; that's acceptable.
		t.Logf("Close returned: %v (acceptable for shell scripts)", err)
	}
}

func TestProxySkills(t *testing.T) {
	script := writeMockScript(t)
	logger := testLogger()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proxy, err := New(ctx, Config{
		Command:   "sh",
		Args:      []string{script},
		AgentName: "test-proxy",
		Logger:    logger,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer proxy.Close()

	skills := proxy.Skills()
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}

	// Verify skills have correct IDs.
	ids := map[string]bool{}
	for _, s := range skills {
		ids[s.Id] = true
	}
	if !ids["echo"] || !ids["add"] {
		t.Fatalf("expected skills [echo, add], got %v", ids)
	}
}
