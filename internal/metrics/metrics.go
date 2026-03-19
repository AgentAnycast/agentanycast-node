// Package metrics provides Prometheus instrumentation for AgentAnycast.
package metrics

import (
	"context"
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

// Server runs the Prometheus metrics HTTP server.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// NewServer creates a new metrics server.
func NewServer(listen string, logger *slog.Logger) *Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	return &Server{
		httpServer: &http.Server{
			Addr:         listen,
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		logger: logger,
	}
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
