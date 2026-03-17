package a2a

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

const (
	defaultMaxRetries   = 3
	defaultBaseInterval = 2 * time.Second
)

// pendingMessage tracks a sent message awaiting an ACK.
type pendingMessage struct {
	envelopeID string
	target     peer.ID
	data       []byte
	retries    int
	nextRetry  time.Time
}

// AckTracker tracks outgoing messages and retries them if no ACK is received
// within a timeout. It uses exponential backoff (2s, 4s, 8s) with a maximum
// of 3 retries.
type AckTracker struct {
	sendFn SendFunc
	logger *slog.Logger

	mu      sync.Mutex
	pending map[string]*pendingMessage // envelopeID -> pending

	stopCh chan struct{}
	done   chan struct{}
}

// NewAckTracker creates a new AckTracker that retries unacknowledged messages.
func NewAckTracker(sendFn SendFunc, logger *slog.Logger) *AckTracker {
	at := &AckTracker{
		sendFn:  sendFn,
		logger:  logger,
		pending: make(map[string]*pendingMessage),
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
	}
	go at.retryLoop()
	return at
}

// Track registers an outgoing message for ACK tracking. It returns the
// envelope ID used for the message.
func (at *AckTracker) Track(target peer.ID, envelopeID string, data []byte) {
	at.mu.Lock()
	defer at.mu.Unlock()

	at.pending[envelopeID] = &pendingMessage{
		envelopeID: envelopeID,
		target:     target,
		data:       data,
		retries:    0,
		nextRetry:  time.Now().Add(defaultBaseInterval),
	}

	at.logger.Debug("tracking message for ACK",
		"envelope_id", envelopeID,
		"target", target,
	)
}

// Acknowledge removes a message from the pending map when an ACK is received.
// Returns true if the envelope was found and removed.
func (at *AckTracker) Acknowledge(envelopeID string) bool {
	at.mu.Lock()
	defer at.mu.Unlock()

	if _, ok := at.pending[envelopeID]; ok {
		delete(at.pending, envelopeID)
		at.logger.Debug("message acknowledged", "envelope_id", envelopeID)
		return true
	}
	return false
}

// Stop shuts down the retry loop.
func (at *AckTracker) Stop() {
	close(at.stopCh)
	<-at.done
}

// SendAck sends an ACK envelope back to the sender for the given envelope ID.
func SendAck(sendFn SendFunc, ctx interface{}, target peer.ID, acknowledgedEnvelopeID string) error {
	env := &pb.A2AEnvelope{
		EnvelopeId: uuid.New().String(),
		Type:       pb.EnvelopeType_ENVELOPE_TYPE_ACK,
		Timestamp:  timestamppb.Now(),
		Payload: &pb.A2AEnvelope_Ack{
			Ack: &pb.AckPayload{
				AcknowledgedEnvelopeId: acknowledgedEnvelopeID,
			},
		},
	}

	data, err := proto.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal ACK envelope: %w", err)
	}

	return sendFn(ctx, target, data)
}

func (at *AckTracker) retryLoop() {
	defer close(at.done)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-at.stopCh:
			return
		case now := <-ticker.C:
			at.processRetries(now)
		}
	}
}

func (at *AckTracker) processRetries(now time.Time) {
	at.mu.Lock()
	// Collect messages that need retrying or expiring.
	var toRetry []*pendingMessage
	var toRemove []string
	for id, pm := range at.pending {
		if now.Before(pm.nextRetry) {
			continue
		}
		if pm.retries >= defaultMaxRetries {
			at.logger.Warn("message delivery failed after max retries",
				"envelope_id", id,
				"target", pm.target,
				"retries", pm.retries,
			)
			toRemove = append(toRemove, id)
		} else {
			toRetry = append(toRetry, pm)
		}
	}
	for _, id := range toRemove {
		delete(at.pending, id)
	}
	at.mu.Unlock()

	// Send retries outside the lock to avoid blocking.
	for _, pm := range toRetry {
		at.logger.Debug("retrying message send",
			"envelope_id", pm.envelopeID,
			"target", pm.target,
			"retry", pm.retries+1,
		)

		if err := at.sendFn(nil, pm.target, pm.data); err != nil {
			at.logger.Warn("retry send failed",
				"envelope_id", pm.envelopeID,
				"error", err,
			)
		}

		at.mu.Lock()
		if p, ok := at.pending[pm.envelopeID]; ok {
			p.retries++
			// Exponential backoff: 2s, 4s, 8s
			backoff := defaultBaseInterval * (1 << p.retries)
			p.nextRetry = time.Now().Add(backoff)
		}
		at.mu.Unlock()
	}
}
