package bridge

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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

var bridgeOutboundTracer = otel.Tracer("agentanycast/bridge/outbound")

// OutboundClient sends A2A tasks to external HTTP A2A agents.
type OutboundClient struct {
	httpClient *http.Client
	logger     *slog.Logger
}

// NewOutboundClient creates a new outbound HTTP bridge client.
func NewOutboundClient(logger *slog.Logger) *OutboundClient {
	return &OutboundClient{
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		logger: logger,
	}
}

// SendTask sends an A2A task to an external HTTP agent at the given URL.
func (c *OutboundClient) SendTask(ctx context.Context, targetURL string, msg *pb.Message, metadata map[string]string) (*pb.Task, error) {
	ctx, span := bridgeOutboundTracer.Start(ctx, "bridge.outbound",
		trace.WithAttributes(attribute.String("target_url", targetURL)),
	)
	defer span.End()

	taskID := uuid.New().String()

	reqBody, err := BuildA2AHTTPRequest(taskID, msg)
	if err != nil {
		return nil, fmt.Errorf("build HTTP request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	c.logger.Debug("outbound bridge request",
		"url", targetURL,
		"task_id", taskID,
	)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request to %s: %w", targetURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, targetURL, string(respBody))
	}

	taskResult, err := ParseA2AHTTPResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("parse A2A response: %w", err)
	}

	// Convert HTTP result back to protobuf.
	return httpTaskResultToProto(taskID, taskResult), nil
}

func httpTaskResultToProto(taskID string, result *A2ATaskResult) *pb.Task {
	now := timestamppb.Now()

	messages := make([]*pb.Message, 0, len(result.Messages))
	for _, m := range result.Messages {
		messages = append(messages, HTTPMessageToProto(m))
	}

	artifacts := make([]*pb.Artifact, 0, len(result.Artifacts))
	for _, a := range result.Artifacts {
		parts := make([]*pb.Part, 0, len(a.Parts))
		for _, p := range a.Parts {
			parts = append(parts, httpPartToProto(p))
		}
		artifacts = append(artifacts, &pb.Artifact{
			ArtifactId: a.ArtifactID,
			Name:       a.Name,
			Parts:      parts,
		})
	}

	id := result.ID
	if id == "" {
		id = taskID
	}

	return &pb.Task{
		TaskId:    id,
		Status:    HTTPStatusToProto(result.Status),
		Messages:  messages,
		Artifacts: artifacts,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// FetchAgentCard fetches the Agent Card from an HTTP A2A agent's well-known endpoint.
func (c *OutboundClient) FetchAgentCard(ctx context.Context, baseURL string) (*A2AAgentCard, error) {
	cardURL := baseURL + "/.well-known/a2a-agent-card"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cardURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch agent card from %s: %w", cardURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, cardURL)
	}

	var card A2AAgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return nil, fmt.Errorf("decode agent card: %w", err)
	}

	return &card, nil
}
