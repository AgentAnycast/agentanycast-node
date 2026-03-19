package anp

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

// Server is the ANP bridge HTTP server.
type Server struct {
	httpServer *http.Server
	handler    *Handler
	logger     *slog.Logger
}

// NewServer creates a new ANP bridge server.
func NewServer(addr string, card *pb.AgentCard, taskChan chan<- IncomingANPTask, logger *slog.Logger) *Server {
	handler := NewHandler(card, taskChan, logger)

	return &Server{
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      handler,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
		handler: handler,
		logger:  logger,
	}
}

// Start begins listening and serving ANP HTTP requests.
func (s *Server) Start() error {
	s.logger.Info("ANP bridge started", "addr", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("ANP bridge shutting down")
	return s.httpServer.Shutdown(ctx)
}

// Addr returns the configured listen address.
func (s *Server) Addr() string {
	return s.httpServer.Addr
}

// Handler returns the underlying ANP handler, useful for updating the card
// after server creation.
func (s *Server) Handler() *Handler {
	return s.handler
}
