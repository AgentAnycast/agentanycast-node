// Package store provides local persistent storage using BoltDB.
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	tasksBucket       = []byte("tasks")
	offlineQueueBucket = []byte("offline_queue")
	metadataBucket    = []byte("metadata")
)

// Store wraps a BoltDB database for task and queue persistence.
type Store struct {
	db *bolt.DB
}

// Open opens or creates the BoltDB database at the given directory.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}

	path := filepath.Join(dir, "agentanycast.db")
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bolt db: %w", err)
	}

	// Create buckets.
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{tasksBucket, offlineQueueBucket, metadataBucket} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create buckets: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// ── Task Operations ────────────────────────────────────────

// PutTask stores a task (protobuf-serialized bytes) by task ID.
func (s *Store) PutTask(taskID string, data []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(tasksBucket).Put([]byte(taskID), data)
	})
}

// GetTask retrieves a task by task ID.
func (s *Store) GetTask(taskID string) ([]byte, error) {
	var data []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(tasksBucket).Get([]byte(taskID))
		if v == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		data = make([]byte, len(v))
		copy(data, v)
		return nil
	})
	return data, err
}

// DeleteTask removes a task by task ID.
func (s *Store) DeleteTask(taskID string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(tasksBucket).Delete([]byte(taskID))
	})
}

// ListTasks returns all stored task IDs.
func (s *Store) ListTasks() ([]string, error) {
	var ids []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(tasksBucket)
		return b.ForEach(func(k, _ []byte) error {
			ids = append(ids, string(k))
			return nil
		})
	})
	return ids, err
}

// ── Offline Queue Operations ───────────────────────────────

// EnqueueMessage stores a message for later delivery.
// key format: "{target_peer_id}/{envelope_id}"
func (s *Store) EnqueueMessage(key string, data []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(offlineQueueBucket).Put([]byte(key), data)
	})
}

// DequeueMessage removes a delivered message from the queue.
func (s *Store) DequeueMessage(key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(offlineQueueBucket).Delete([]byte(key))
	})
}

// ListQueuedMessages returns all queued message keys and data.
func (s *Store) ListQueuedMessages() (map[string][]byte, error) {
	result := make(map[string][]byte)
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(offlineQueueBucket)
		return b.ForEach(func(k, v []byte) error {
			data := make([]byte, len(v))
			copy(data, v)
			result[string(k)] = data
			return nil
		})
	})
	return result, err
}

// ── Metadata Operations ────────────────────────────────────

// SetMetadata stores a metadata key-value pair.
func (s *Store) SetMetadata(key, value string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(metadataBucket).Put([]byte(key), []byte(value))
	})
}

// GetMetadata retrieves a metadata value by key.
func (s *Store) GetMetadata(key string) (string, error) {
	var val string
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(metadataBucket).Get([]byte(key))
		if v == nil {
			return fmt.Errorf("metadata not found: %s", key)
		}
		val = string(v)
		return nil
	})
	return val, err
}

// SetMetadataBytes stores a metadata key with binary value.
func (s *Store) SetMetadataBytes(key string, value []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(metadataBucket).Put([]byte(key), value)
	})
}

// GetMetadataBytes retrieves a binary metadata value by key.
func (s *Store) GetMetadataBytes(key string) ([]byte, error) {
	var data []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(metadataBucket).Get([]byte(key))
		if v == nil {
			return fmt.Errorf("metadata not found: %s", key)
		}
		data = make([]byte, len(v))
		copy(data, v)
		return nil
	})
	return data, err
}

// ListMetadataByPrefix returns all metadata key-value pairs whose keys start with the given prefix.
func (s *Store) ListMetadataByPrefix(prefix string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	pfx := []byte(prefix)
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(metadataBucket)
		c := b.Cursor()
		for k, v := c.Seek(pfx); k != nil; k, v = c.Next() {
			key := string(k)
			if len(key) < len(prefix) || key[:len(prefix)] != prefix {
				break
			}
			data := make([]byte, len(v))
			copy(data, v)
			result[key] = data
		}
		return nil
	})
	return result, err
}
