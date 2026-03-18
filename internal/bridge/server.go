package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Config holds HTTP Bridge configuration.
type Config struct {
	Listen      string   // e.g., ":8080"
	TLSCert     string   // optional TLS certificate path
	TLSKey      string   // optional TLS key path
	CORSOrigins []string // CORS allowed origins
}

// Server is the HTTP Bridge server.
type Server struct {
	httpServer *http.Server
	config     Config
	logger     *slog.Logger
}

// NewServer creates a new HTTP Bridge server.
func NewServer(cfg Config, inbound *InboundHandler, cardEndpoint *CardEndpoint, logger *slog.Logger) *Server {
	mux := http.NewServeMux()

	// A2A well-known endpoint for card discovery.
	mux.Handle("/.well-known/a2a-agent-card", cardEndpoint)

	// A2A JSON-RPC endpoint for task operations.
	mux.Handle("/", inbound)

	var handler http.Handler = mux
	if len(cfg.CORSOrigins) > 0 {
		handler = corsMiddleware(cfg.CORSOrigins, mux)
	}

	return &Server{
		httpServer: &http.Server{
			Addr:         cfg.Listen,
			Handler:      handler,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
		config: cfg,
		logger: logger,
	}
}

// ListenAndServe starts the HTTP Bridge server.
func (s *Server) ListenAndServe() error {
	s.logger.Info("HTTP bridge started", "addr", s.config.Listen)

	if s.config.TLSCert != "" && s.config.TLSKey != "" {
		return s.httpServer.ListenAndServeTLS(s.config.TLSCert, s.config.TLSKey)
	}
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("HTTP bridge shutting down")
	return s.httpServer.Shutdown(ctx)
}

// Addr returns the configured listen address.
func (s *Server) Addr() string {
	return s.config.Listen
}

func corsMiddleware(origins []string, next http.Handler) http.Handler {
	allowAll := len(origins) == 1 && origins[0] == "*"
	originSet := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		originSet[o] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}

		if allowAll {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if _, ok := originSet[origin]; ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// BaseURL computes the public base URL from the listen address.
func BaseURL(listen string, useTLS bool) string {
	scheme := "http"
	if useTLS {
		scheme = "https"
	}

	addr := listen
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}

	return fmt.Sprintf("%s://%s", scheme, addr)
}
