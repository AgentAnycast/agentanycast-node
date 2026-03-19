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

// Client is an outbound HTTP client for calling ANP agents.
type Client struct {
	httpClient *http.Client
	logger     *slog.Logger
}

// NewClient creates a new ANP outbound client.
func NewClient(logger *slog.Logger) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		logger: logger,
	}
}

// FetchDescription fetches an ANP agent's Agent Description document.
func (c *Client) FetchDescription(ctx context.Context, baseURL string) (*AgentDescription, error) {
	url := baseURL + "/agent/ad.json"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch agent description from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	var desc AgentDescription
	if err := json.NewDecoder(resp.Body).Decode(&desc); err != nil {
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch interface from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	var spec OpenRPCSpec
	if err := json.NewDecoder(resp.Body).Decode(&spec); err != nil {
		return nil, fmt.Errorf("decode OpenRPC spec: %w", err)
	}

	return &spec, nil
}

// CallMethod calls a JSON-RPC method on an ANP agent.
func (c *Client) CallMethod(ctx context.Context, baseURL, method string, params interface{}) (*JSONRPCResponse, error) {
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

	c.logger.Debug("ANP outbound call",
		"url", url,
		"method", method,
	)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("RPC call to %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20)) // 16MB max
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, url, string(respBody))
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &rpcResp, nil
}

// SendTask is a high-level helper that maps an A2A task to an ANP JSON-RPC
// call and returns the text result.
func (c *Client) SendTask(ctx context.Context, targetURL string, skillID string, messageText string) (string, error) {
	taskID := uuid.New().String()

	params := map[string]interface{}{
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

	// Extract text from the result.
	switch v := resp.Result.(type) {
	case string:
		return v, nil
	case map[string]interface{}:
		if text, ok := v["text"]; ok {
			return fmt.Sprintf("%v", text), nil
		}
		data, _ := json.Marshal(v)
		return string(data), nil
	default:
		data, _ := json.Marshal(v)
		return string(data), nil
	}
}
