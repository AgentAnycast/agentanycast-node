package bridge

import (
	"encoding/json"
	"log/slog"
	"net/http"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

// CardProvider returns the local node's current AgentCard.
type CardProvider func() *pb.AgentCard

// CardEndpoint serves the Agent Card at /.well-known/a2a-agent-card.
type CardEndpoint struct {
	getCard CardProvider
	baseURL string
	logger  *slog.Logger
}

// NewCardEndpoint creates the well-known card endpoint handler.
func NewCardEndpoint(getCard CardProvider, baseURL string, logger *slog.Logger) *CardEndpoint {
	return &CardEndpoint{
		getCard: getCard,
		baseURL: baseURL,
		logger:  logger,
	}
}

// ServeHTTP handles GET requests for the Agent Card.
func (e *CardEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	card := e.getCard()
	if card == nil {
		http.Error(w, "no agent card configured", http.StatusNotFound)
		return
	}

	httpCard := ProtoCardToHTTP(card, e.baseURL)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(httpCard); err != nil {
		e.logger.Error("failed to encode agent card", "error", err)
	}
}
