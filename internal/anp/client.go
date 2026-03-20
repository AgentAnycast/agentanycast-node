package anp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithHTTPClient sets a custom http.Client for outbound requests.
func WithHTTPClient(c *http.Client) ClientOption {
	return func(cl *Client) {
		cl.httpClient = c
	}
}

// WithTimeout sets the HTTP client timeout for outbound requests.
func WithTimeout(d time.Duration) ClientOption {
	return func(cl *Client) {
		cl.httpClient.Timeout = d
	}
}

// Client is an outbound HTTP client for calling ANP agents.
type Client struct {
	httpClient *http.Client
	logger     *slog.Logger
}

// NewClient creates a new ANP outbound client.
func NewClient(logger *slog.Logger, opts ...ClientOption) *Client {
	c := &Client{
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		logger: logger,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// FetchDescription fetches an ANP agent's Agent Description document.
func (c *Client) FetchDescription(ctx context.Context, baseURL string) (*AgentDescription, error) {
	url := baseURL + "/agent/ad.json"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch agent description from %s: %w", url, err)
	}
	defer resp.Body.Close()

	c.logger.Debug("ANP call completed",
		"url", url,
		"method", "FetchDescription",
		"status", resp.StatusCode,
		"duration", time.Since(start),
	)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	var desc AgentDescription
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&desc); err != nil {
		return nil, fmt.Errorf("decode agent description: %w", err)
	}

	return &desc, nil
}

// FetchInterface fetches an ANP agent's OpenRPC interface specification.
func (c *Client) FetchInterface(ctx context.Context, baseURL string) (*OpenRPCSpec, error) {
	url := baseURL + "/agent/interface.json"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch interface from %s: %w", url, err)
	}
	defer resp.Body.Close()

	c.logger.Debug("ANP call completed",
		"url", url,
		"method", "FetchInterface",
		"status", resp.StatusCode,
		"duration", time.Since(start),
	)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	var spec OpenRPCSpec
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&spec); err != nil {
		return nil, fmt.Errorf("decode OpenRPC spec: %w", err)
	}

	return &spec, nil
}

// CallMethod calls a JSON-RPC method on an ANP agent.
func (c *Client) CallMethod(ctx context.Context, baseURL, method string, params any) (*JSONRPCResponse, error) {
	url := baseURL + "/agent/rpc"

	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      uuid.New().String(),
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("RPC call to %s: %w", url, err)
	}
	defer resp.Body.Close()

	c.logger.Debug("ANP call completed",
		"url", url,
		"method", method,
		"status", resp.StatusCode,
		"duration", time.Since(start),
	)

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, url, string(errBody))
	}

	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &rpcResp, nil
}

// SendTask is a high-level helper that maps an A2A task to an ANP JSON-RPC
// call and returns the text result.
func (c *Client) SendTask(ctx context.Context, targetURL string, skillID string, messageText string) (string, error) {
	taskID := uuid.New().String()

	params := map[string]any{
		"input":   messageText,
		"task_id": taskID,
	}

	resp, err := c.CallMethod(ctx, targetURL, skillID, params)
	if err != nil {
		return "", fmt.Errorf("ANP call to %s/%s: %w", targetURL, skillID, err)
	}

	if resp.Error != nil {
		return "", fmt.Errorf("ANP error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	return extractTextFromResult(resp.Result), nil
}
