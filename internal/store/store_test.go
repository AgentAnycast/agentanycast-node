package store

import (
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestPutGet(t *testing.T) {
	st := openTestStore(t)

	// Write a task.
	if err := st.PutTask("task-1", []byte("hello")); err != nil {
		t.Fatalf("PutTask: %v", err)
	}

	// Read it back.
	data, err := st.GetTask("task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected %q, got %q", "hello", string(data))
	}
}

func TestDelete(t *testing.T) {
	st := openTestStore(t)

	if err := st.PutTask("task-del", []byte("data")); err != nil {
		t.Fatalf("PutTask: %v", err)
	}

	if err := st.DeleteTask("task-del"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	_, err := st.GetTask("task-del")
	if err == nil {
		t.Fatal("expected error after delete, got nil")
	}
}

func TestListByPrefix(t *testing.T) {
	st := openTestStore(t)

	// Store multiple tasks.
	for _, id := range []string{"alpha-1", "alpha-2", "beta-1"} {
		if err := st.PutTask(id, []byte("v")); err != nil {
			t.Fatalf("PutTask(%s): %v", id, err)
		}
	}

	ids, err := st.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}

	if len(ids) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(ids))
	}

	// BoltDB iterates in key order; verify sorted.
	expected := []string{"alpha-1", "alpha-2", "beta-1"}
	for i, want := range expected {
		if ids[i] != want {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], want)
		}
	}
}

func TestBucketIsolation(t *testing.T) {
	st := openTestStore(t)

	// Write to tasks bucket.
	if err := st.PutTask("shared-key", []byte("task-data")); err != nil {
		t.Fatalf("PutTask: %v", err)
	}

	// Write to offline queue bucket with the same key prefix.
	if err := st.EnqueueMessage("shared-key", []byte("queue-data")); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	// Write to metadata bucket.
	if err := st.SetMetadata("shared-key", "meta-data"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	// Read each back and verify they are independent.
	taskData, err := st.GetTask("shared-key")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if string(taskData) != "task-data" {
		t.Fatalf("task data = %q, want %q", string(taskData), "task-data")
	}

	queuedMsgs, err := st.ListQueuedMessages()
	if err != nil {
		t.Fatalf("ListQueuedMessages: %v", err)
	}
	if string(queuedMsgs["shared-key"]) != "queue-data" {
		t.Fatalf("queue data = %q, want %q", string(queuedMsgs["shared-key"]), "queue-data")
	}

	metaVal, err := st.GetMetadata("shared-key")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if metaVal != "meta-data" {
		t.Fatalf("metadata = %q, want %q", metaVal, "meta-data")
	}

	// Delete from one bucket; others remain.
	if err := st.DeleteTask("shared-key"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	// Queue data still present.
	queuedMsgs, err = st.ListQueuedMessages()
	if err != nil {
		t.Fatalf("ListQueuedMessages after delete: %v", err)
	}
	if _, ok := queuedMsgs["shared-key"]; !ok {
		t.Fatal("queue data should still exist after deleting task")
	}

	// Metadata still present.
	metaVal, err = st.GetMetadata("shared-key")
	if err != nil {
		t.Fatalf("GetMetadata after delete: %v", err)
	}
	if metaVal != "meta-data" {
		t.Fatalf("metadata after delete = %q, want %q", metaVal, "meta-data")
	}
}
