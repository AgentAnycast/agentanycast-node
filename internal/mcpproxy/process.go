// Package mcpproxy implements the MCP Remote Proxy — it spawns an arbitrary
// MCP Server subprocess (stdio transport), auto-discovers its tools, and
// makes them accessible over the AgentAnycast P2P network as A2A Skills.
//
// This is the "ngrok for MCP" feature: any MCP-compatible tool server can be
// wrapped and exposed to the decentralized agent network with zero code changes.
package mcpproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
)

// jsonRPCRequest is a JSON-RPC 2.0 request object.
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// jsonRPCResponse is a JSON-RPC 2.0 response object.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// jsonRPCError represents a JSON-RPC 2.0 error.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonRPCError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// Process manages an MCP Server subprocess communicating over stdio.
// It implements the JSON-RPC 2.0 protocol over stdin/stdout pipes,
// serializing writes and matching responses to pending requests via
// a concurrent-safe pending map.
type Process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	mu     sync.Mutex // serializes JSON-RPC writes to stdin
	nextID atomic.Int64

	pending   map[int64]chan jsonRPCResponse
	pendingMu sync.Mutex

	logger *slog.Logger
	done   chan struct{} // closed when the reader goroutine exits
}

// NewProcess spawns the MCP Server command and returns a Process.
// The subprocess is started with stdin/stdout pipes for JSON-RPC communication.
// A background goroutine reads responses from stdout and dispatches them to
// pending request channels.
func NewProcess(command string, args []string, logger *slog.Logger) (*Process, error) {
	cmd := exec.Command(command, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcpproxy: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("mcpproxy: stdout pipe: %w", err)
	}

	// Discard stderr to avoid blocking; the subprocess logger handles its own output.
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("mcpproxy: start %s: %w", command, err)
	}

	p := &Process{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReaderSize(stdout, 64*1024),
		pending: make(map[int64]chan jsonRPCResponse),
		logger:  logger.With("component", "mcpproxy.process"),
		done:    make(chan struct{}),
	}

	// Start background reader that dispatches responses to pending channels.
	go p.readLoop()

	return p, nil
}

// Call sends a JSON-RPC request and waits for the matching response.
// The context can be used to cancel the wait.
func (p *Process) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := p.nextID.Add(1)

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	// Register pending response channel before writing to avoid races.
	ch := make(chan jsonRPCResponse, 1)
	p.pendingMu.Lock()
	p.pending[id] = ch
	p.pendingMu.Unlock()

	defer func() {
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
	}()

	if err := p.writeRequest(req); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("mcpproxy: call %s: %w", method, ctx.Err())
	case <-p.done:
		return nil, fmt.Errorf("mcpproxy: call %s: subprocess exited", method)
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (p *Process) Notify(ctx context.Context, method string, params any) error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return p.writeRequest(req)
}

// Close terminates the subprocess gracefully. It closes stdin first to
// signal the subprocess, then waits for it to exit.
func (p *Process) Close() error {
	p.stdin.Close()
	err := p.cmd.Wait()
	<-p.done // Wait for reader goroutine to finish.
	return err
}

// writeRequest serializes a JSON-RPC request and writes it to stdin.
// Writes are serialized via a mutex to prevent interleaving.
func (p *Process) writeRequest(req jsonRPCRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("mcpproxy: marshal request: %w", err)
	}
	data = append(data, '\n')

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := p.stdin.Write(data); err != nil {
		return fmt.Errorf("mcpproxy: write to stdin: %w", err)
	}
	return nil
}

// readLoop reads JSON-RPC responses from stdout and dispatches them to
// pending request channels. It runs until stdout is closed or an error occurs.
func (p *Process) readLoop() {
	defer close(p.done)

	for {
		line, err := p.stdout.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				p.logger.Warn("subprocess stdout read error", "error", err)
			}
			// Signal all pending callers that the process has exited.
			p.pendingMu.Lock()
			for id, ch := range p.pending {
				close(ch)
				delete(p.pending, id)
			}
			p.pendingMu.Unlock()
			return
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			p.logger.Debug("ignoring non-JSON line from subprocess", "line", string(line))
			continue
		}

		// Notifications (id=0) are ignored — we only handle responses.
		if resp.ID == 0 {
			continue
		}

		p.pendingMu.Lock()
		ch, ok := p.pending[resp.ID]
		p.pendingMu.Unlock()

		if ok {
			ch <- resp
		} else {
			p.logger.Debug("no pending request for response", "id", resp.ID)
		}
	}
}
