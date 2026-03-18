package a2a

import (
	"fmt"
	"log/slog"
	"sync"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
)

// StreamManager manages active streaming artifact sessions.
// Each task can have at most one active stream at a time.
type StreamManager struct {
	mu       sync.RWMutex
	streams  map[string]*StreamSession // keyed by task_id
	logger   *slog.Logger
}

// StreamSession tracks the state of an active artifact stream for a task.
type StreamSession struct {
	TaskID       string
	ArtifactID   string
	ArtifactName string
	MediaType    string
	TotalChunks  int32
	Received     int32
	Subscribers  []chan *pb.SubscribeTaskStreamResponse
	mu           sync.Mutex
}

// NewStreamManager creates a new StreamManager.
func NewStreamManager(logger *slog.Logger) *StreamManager {
	return &StreamManager{
		streams: make(map[string]*StreamSession),
		logger:  logger,
	}
}

// Subscribe registers a subscriber for streaming events on a task.
// Returns a channel that receives stream events, and a cleanup function.
func (m *StreamManager) Subscribe(taskID string) (<-chan *pb.SubscribeTaskStreamResponse, func()) {
	ch := make(chan *pb.SubscribeTaskStreamResponse, 64)

	m.mu.Lock()
	session, ok := m.streams[taskID]
	if !ok {
		session = &StreamSession{
			TaskID: taskID,
		}
		m.streams[taskID] = session
	}
	m.mu.Unlock()

	session.mu.Lock()
	session.Subscribers = append(session.Subscribers, ch)
	session.mu.Unlock()

	cleanup := func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		for i, sub := range session.Subscribers {
			if sub == ch {
				session.Subscribers = append(session.Subscribers[:i], session.Subscribers[i+1:]...)
				break
			}
		}
		close(ch)
	}

	return ch, cleanup
}

// HandleStreamStart processes a StreamStart message.
func (m *StreamManager) HandleStreamStart(start *pb.StreamStart) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.streams[start.TaskId]
	if !ok {
		session = &StreamSession{TaskID: start.TaskId}
		m.streams[start.TaskId] = session
	}

	session.ArtifactID = start.ArtifactId
	session.ArtifactName = start.ArtifactName
	session.MediaType = start.MediaType
	session.TotalChunks = start.TotalChunks
	session.Received = 0

	m.logger.Debug("stream started",
		"task_id", start.TaskId,
		"artifact_id", start.ArtifactId,
		"total_chunks", start.TotalChunks,
	)

	// Notify subscribers.
	session.broadcast(&pb.SubscribeTaskStreamResponse{
		Event: &pb.SubscribeTaskStreamResponse_StreamStart{StreamStart: start},
	})

	return nil
}

// HandleStreamChunk processes a StreamChunk message.
func (m *StreamManager) HandleStreamChunk(chunk *pb.StreamChunk) error {
	m.mu.RLock()
	session, ok := m.streams[chunk.TaskId]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no active stream for task %s", chunk.TaskId)
	}

	session.mu.Lock()
	session.Received++
	session.mu.Unlock()

	session.broadcast(&pb.SubscribeTaskStreamResponse{
		Event: &pb.SubscribeTaskStreamResponse_StreamChunk{StreamChunk: chunk},
	})

	return nil
}

// HandleStreamEnd processes a StreamEnd message.
func (m *StreamManager) HandleStreamEnd(end *pb.StreamEnd) error {
	m.mu.Lock()
	session, ok := m.streams[end.TaskId]
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("no active stream for task %s", end.TaskId)
	}

	m.logger.Debug("stream ended",
		"task_id", end.TaskId,
		"artifact_id", end.ArtifactId,
		"reason", end.Reason.String(),
	)

	session.broadcast(&pb.SubscribeTaskStreamResponse{
		Event: &pb.SubscribeTaskStreamResponse_StreamEnd{StreamEnd: end},
	})

	// Clean up the session.
	m.mu.Lock()
	delete(m.streams, end.TaskId)
	m.mu.Unlock()

	return nil
}

func (s *StreamSession) broadcast(resp *pb.SubscribeTaskStreamResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ch := range s.Subscribers {
		select {
		case ch <- resp:
		default:
			// Subscriber is slow — drop the message.
		}
	}
}
