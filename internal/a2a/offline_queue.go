package a2a

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/agentanycast/agentanycast-node/internal/store"
)

var offlineTracer = otel.Tracer("agentanycast/a2a/offline")

// OfflineQueue manages queuing and flushing of messages that could not be
// delivered because the target peer was disconnected. Messages are persisted
// in the Store's offline-queue bucket keyed by "{peer_id}/{envelope_id}".
//
// Each stored value is prefixed with an 8-byte big-endian Unix timestamp
// (seconds) used for TTL enforcement.
type OfflineQueue struct {
	store  *store.Store
	logger *slog.Logger
	ttl    time.Duration
}

// NewOfflineQueue creates a new OfflineQueue backed by the given store.
// ttl controls how long queued messages are retained. Zero means no expiry.
func NewOfflineQueue(s *store.Store, logger *slog.Logger, ttl time.Duration) *OfflineQueue {
	return &OfflineQueue{
		store:  s,
		logger: logger,
		ttl:    ttl,
	}
}

// Enqueue persists a message for later delivery to a disconnected peer.
// The message is prefixed with the current timestamp for TTL enforcement.
func (q *OfflineQueue) Enqueue(peerID, envelopeID string, data []byte) error {
	_, span := offlineTracer.Start(context.Background(), "a2a.offline.enqueue",
		trace.WithAttributes(
			attribute.String("peer_id", peerID),
			attribute.String("envelope_id", envelopeID),
		),
	)
	defer span.End()

	key := peerID + "/" + envelopeID
	stamped := q.stampData(data)
	if err := q.store.EnqueueMessage(key, stamped); err != nil {
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
// Expired messages are silently removed. Messages that fail to send remain
// in the queue for a future retry.
func (q *OfflineQueue) FlushQueue(peerID string, sendFn func(data []byte) error) error {
	_, span := offlineTracer.Start(context.Background(), "a2a.offline.flush",
		trace.WithAttributes(attribute.String("peer_id", peerID)),
	)
	defer span.End()

	all, err := q.store.ListQueuedMessages()
	if err != nil {
		return fmt.Errorf("list queued messages: %w", err)
	}

	prefix := peerID + "/"
	flushed := 0
	expired := 0
	var lastErr error

	for key, raw := range all {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		ts, data := q.unstampData(raw)

		// Drop expired messages.
		if q.isExpired(ts) {
			if err := q.store.DequeueMessage(key); err != nil {
				q.logger.Warn("failed to dequeue expired message", "key", key, "error", err)
			}
			expired++
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

	if flushed > 0 || expired > 0 {
		q.logger.Info("flushed offline queue",
			"peer_id", peerID,
			"flushed", flushed,
			"expired", expired,
		)
	}

	return lastErr
}

// QueuedCount returns the number of queued (non-expired) messages for a given peer.
func (q *OfflineQueue) QueuedCount(peerID string) (int, error) {
	all, err := q.store.ListQueuedMessages()
	if err != nil {
		return 0, err
	}

	prefix := peerID + "/"
	count := 0
	for key, raw := range all {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		ts, _ := q.unstampData(raw)
		if !q.isExpired(ts) {
			count++
		}
	}
	return count, nil
}

// stampData prepends an 8-byte Unix timestamp to the message data.
func (q *OfflineQueue) stampData(data []byte) []byte {
	buf := make([]byte, 8+len(data))
	binary.BigEndian.PutUint64(buf, uint64(time.Now().Unix()))
	copy(buf[8:], data)
	return buf
}

// unstampData splits the 8-byte timestamp from the message data.
// Returns zero time and original data if the value is too short (legacy data).
func (q *OfflineQueue) unstampData(raw []byte) (time.Time, []byte) {
	if len(raw) < 8 {
		return time.Time{}, raw
	}
	ts := time.Unix(int64(binary.BigEndian.Uint64(raw[:8])), 0)
	return ts, raw[8:]
}

// isExpired returns true if the message timestamp is older than the TTL.
func (q *OfflineQueue) isExpired(ts time.Time) bool {
	if q.ttl == 0 || ts.IsZero() {
		return false
	}
	return time.Since(ts) > q.ttl
}
