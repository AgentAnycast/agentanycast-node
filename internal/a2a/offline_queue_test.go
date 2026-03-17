package a2a

import (
	"fmt"
	"testing"

	"github.com/agentanycast/agentanycast-node/internal/store"
)

func openTestQueue(t *testing.T) *OfflineQueue {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewOfflineQueue(st, testLogger())
}

func TestEnqueueAndFlush(t *testing.T) {
	q := openTestQueue(t)
	peerID := "peer-abc"

	// Enqueue 3 messages.
	for i := 0; i < 3; i++ {
		err := q.Enqueue(peerID, fmt.Sprintf("env-%d", i), []byte(fmt.Sprintf("data-%d", i)))
		if err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	// Verify count.
	count, err := q.QueuedCount(peerID)
	if err != nil {
		t.Fatalf("QueuedCount: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 queued, got %d", count)
	}

	// Flush all with a successful send function.
	var sent []string
	err = q.FlushQueue(peerID, func(data []byte) error {
		sent = append(sent, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("FlushQueue: %v", err)
	}

	if len(sent) != 3 {
		t.Fatalf("expected 3 sent, got %d", len(sent))
	}

	// After flush, count should be 0.
	count, err = q.QueuedCount(peerID)
	if err != nil {
		t.Fatalf("QueuedCount after flush: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 queued after flush, got %d", count)
	}
}

func TestFlushPartialFailure(t *testing.T) {
	q := openTestQueue(t)
	peerID := "peer-partial"

	// Enqueue 3 messages.
	for i := 0; i < 3; i++ {
		err := q.Enqueue(peerID, fmt.Sprintf("env-%d", i), []byte(fmt.Sprintf("data-%d", i)))
		if err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	// Flush with a send function that fails for message containing "data-1".
	callCount := 0
	err := q.FlushQueue(peerID, func(data []byte) error {
		callCount++
		if string(data) == "data-1" {
			return fmt.Errorf("send failed")
		}
		return nil
	})

	// FlushQueue returns the last error encountered.
	if err == nil {
		t.Fatal("expected error from partial flush, got nil")
	}

	// The failed message should still be in the queue.
	count, err := q.QueuedCount(peerID)
	if err != nil {
		t.Fatalf("QueuedCount: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 remaining after partial flush, got %d", count)
	}
}

func TestQueuedCount(t *testing.T) {
	q := openTestQueue(t)

	// Enqueue for two different peers.
	for i := 0; i < 3; i++ {
		if err := q.Enqueue("peer-A", fmt.Sprintf("e%d", i), []byte("x")); err != nil {
			t.Fatalf("Enqueue peer-A: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := q.Enqueue("peer-B", fmt.Sprintf("e%d", i), []byte("y")); err != nil {
			t.Fatalf("Enqueue peer-B: %v", err)
		}
	}

	countA, err := q.QueuedCount("peer-A")
	if err != nil {
		t.Fatalf("QueuedCount peer-A: %v", err)
	}
	if countA != 3 {
		t.Fatalf("peer-A: expected 3, got %d", countA)
	}

	countB, err := q.QueuedCount("peer-B")
	if err != nil {
		t.Fatalf("QueuedCount peer-B: %v", err)
	}
	if countB != 2 {
		t.Fatalf("peer-B: expected 2, got %d", countB)
	}

	// Unknown peer should have count 0.
	countC, err := q.QueuedCount("peer-C")
	if err != nil {
		t.Fatalf("QueuedCount peer-C: %v", err)
	}
	if countC != 0 {
		t.Fatalf("peer-C: expected 0, got %d", countC)
	}
}
