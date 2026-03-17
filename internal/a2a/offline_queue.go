package a2a

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/agentanycast/agentanycast-node/internal/store"
)

// OfflineQueue manages queuing and flushing of messages that could not be
// delivered because the target peer was disconnected. Messages are persisted
// in the Store's offline-queue bucket keyed by "{peer_id}/{envelope_id}".
type OfflineQueue struct {
	store  *store.Store
	logger *slog.Logger
}

// NewOfflineQueue creates a new OfflineQueue backed by the given store.
func NewOfflineQueue(s *store.Store, logger *slog.Logger) *OfflineQueue {
	return &OfflineQueue{
		store:  s,
		logger: logger,
	}
}

// Enqueue persists a message for later delivery to a disconnected peer.
func (q *OfflineQueue) Enqueue(peerID, envelopeID string, data []byte) error {
	key := peerID + "/" + envelopeID
	if err := q.store.EnqueueMessage(key, data); err != nil {
		return fmt.Errorf("enqueue message %s: %w", key, err)
	}
	q.logger.Info("message queued for offline delivery",
		"peer_id", peerID,
		"envelope_id", envelopeID,
	)
	return nil
}

// FlushQueue sends all queued messages for a given peer using the provided
// send function. Successfully delivered messages are dequeued from the store.
// Messages that fail to send remain in the queue for a future retry.
func (q *OfflineQueue) FlushQueue(peerID string, sendFn func(data []byte) error) error {
	all, err := q.store.ListQueuedMessages()
	if err != nil {
		return fmt.Errorf("list queued messages: %w", err)
	}

	prefix := peerID + "/"
	flushed := 0
	var lastErr error

	for key, data := range all {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		if err := sendFn(data); err != nil {
			q.logger.Warn("failed to flush queued message",
				"key", key,
				"error", err,
			)
			lastErr = err
			continue
		}

		if err := q.store.DequeueMessage(key); err != nil {
			q.logger.Warn("failed to dequeue delivered message",
				"key", key,
				"error", err,
			)
			lastErr = err
			continue
		}

		flushed++
	}

	if flushed > 0 {
		q.logger.Info("flushed offline queue",
			"peer_id", peerID,
			"flushed", flushed,
		)
	}

	return lastErr
}

// QueuedCount returns the number of queued messages for a given peer.
func (q *OfflineQueue) QueuedCount(peerID string) (int, error) {
	all, err := q.store.ListQueuedMessages()
	if err != nil {
		return 0, err
	}

	prefix := peerID + "/"
	count := 0
	for key := range all {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}
	return count, nil
}
