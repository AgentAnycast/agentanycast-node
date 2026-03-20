// Package metrics provides Prometheus instrumentation for AgentAnycast.
package metrics

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/multiformats/go-multiaddr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus metric collectors for the daemon.
var (
	// Connection metrics
	ConnectedPeers = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agentanycast",
		Name:      "connected_peers",
		Help:      "Number of currently connected peers.",
	})

	ConnectionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agentanycast",
		Name:      "connections_total",
		Help:      "Total number of peer connections established.",
	}, []string{"direction"}) // "inbound" or "outbound"

	// Task metrics
	TasksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agentanycast",
		Name:      "tasks_total",
		Help:      "Total number of tasks processed.",
	}, []string{"direction", "status"}) // direction: "sent"/"received", status: terminal state

	TaskDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "agentanycast",
		Name:      "task_duration_seconds",
		Help:      "Duration of task processing in seconds.",
		Buckets:   prometheus.ExponentialBuckets(0.01, 2, 15),
	}, []string{"direction"})

	// Routing metrics
	RouteResolutions = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agentanycast",
		Name:      "route_resolutions_total",
		Help:      "Total number of anycast route resolutions.",
	}, []string{"result"}) // "hit", "miss", "error"

	// Bridge metrics
	BridgeRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agentanycast",
		Name:      "bridge_requests_total",
		Help:      "Total number of HTTP bridge requests.",
	}, []string{"direction", "status"}) // direction: "inbound"/"outbound"

	// Streaming metrics
	StreamChunksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agentanycast",
		Name:      "stream_chunks_total",
		Help:      "Total number of stream chunks processed.",
	}, []string{"direction"})

	// Message metrics
	MessagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agentanycast",
		Name:      "messages_total",
		Help:      "Total number of A2A messages processed.",
	}, []string{"type"}) // envelope type

	OfflineQueueSize = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agentanycast",
		Name:      "offline_queue_size",
		Help:      "Number of messages in the offline queue.",
	})

	// Transport metrics
	ConnectionsByTransport = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agentanycast",
		Name:      "connections_by_transport",
		Help:      "Total number of connections established by transport type.",
	}, []string{"transport"}) // "tcp", "quic", "webtransport"

	// MCP metrics
	MCPToolCalls = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agentanycast",
		Name:      "mcp_tool_calls_total",
		Help:      "Total number of MCP tool invocations.",
	}, []string{"tool", "status"}) // tool: tool name, status: "ok" / "error"
)

// HealthResponse is the JSON structure returned by the /health endpoint.
type HealthResponse struct {
	Status        string  `json:"status"`
	Version       string  `json:"version"`
	UptimeSeconds float64 `json:"uptime_seconds"`
}

// HealthChecker is called to determine whether the node is healthy.
// It should return true if the node is connected and ready to serve.
type HealthChecker func() bool

// Server runs the Prometheus metrics and health HTTP server.
type Server struct {
	httpServer   *http.Server
	logger       *slog.Logger
	startTime    time.Time
	version      string
	healthCheck  HealthChecker
}

// NewServer creates a new metrics server with /metrics and /health endpoints.
// healthCheck is optional — when nil the health endpoint always reports healthy.
func NewServer(listen string, logger *slog.Logger, opts ...ServerOption) *Server {
	s := &Server{
		logger:    logger,
		startTime: time.Now(),
		version:   "dev",
	}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("GET /health", s.handleHealth)

	s.httpServer = &http.Server{
		Addr:         listen,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return s
}

// ServerOption configures the metrics Server.
type ServerOption func(*Server)

// WithHealthChecker sets a function that determines node health.
func WithHealthChecker(fn HealthChecker) ServerOption {
	return func(s *Server) {
		s.healthCheck = fn
	}
}

// SetVersion sets the version string reported in health responses.
func (s *Server) SetVersion(v string) {
	s.version = v
}

// ListenAndServe starts the metrics server.
func (s *Server) ListenAndServe() error {
	s.logger.Info("metrics server started", "addr", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	healthy := s.healthCheck == nil || s.healthCheck()
	statusStr := "healthy"
	httpCode := http.StatusOK
	if !healthy {
		statusStr = "unhealthy"
		httpCode = http.StatusServiceUnavailable
	}

	resp := HealthResponse{
		Status:        statusStr,
		Version:       s.version,
		UptimeSeconds: time.Since(s.startTime).Seconds(),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// ClassifyTransport returns a transport label ("tcp", "quic", or "webtransport")
// based on the remote multiaddr of a connection.
func ClassifyTransport(addr multiaddr.Multiaddr) string {
	if addr == nil {
		return "unknown"
	}
	str := addr.String()
	if strings.Contains(str, "/webtransport") {
		return "webtransport"
	}
	if strings.Contains(str, "/quic-v1") || strings.Contains(str, "/quic") {
		return "quic"
	}
	if strings.Contains(str, "/tcp/") {
		return "tcp"
	}
	return "unknown"
}
