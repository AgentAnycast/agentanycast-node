package grpcserver

import (
	"context"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
	"github.com/agentanycast/agentanycast-node/internal/a2a"
	"github.com/agentanycast/agentanycast-node/internal/node"
	"github.com/agentanycast/agentanycast-node/internal/store"
)

const bufSize = 1024 * 1024

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// testSetup creates a minimal in-process gRPC server with a real libp2p host and returns
// a connected client plus cleanup function.
func testSetup(t *testing.T) (pb.NodeServiceClient, *Server, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	logger := testLogger()

	// Create an in-memory libp2p host via the node package constructor.
	priv, _, err := libp2pcrypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	nodeHost, err := node.NewHost(ctx, node.HostConfig{
		PrivateKey:  priv,
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	}, logger)
	if err != nil {
		t.Fatalf("create host: %v", err)
	}

	// Open a temp store.
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	engine := a2a.NewEngine(logger)
	engine.SetStore(st)

	router := a2a.NewRouter(engine, logger, func(_ context.Context, _ peer.ID, _ []byte) error {
		return nil // no-op send for tests
	}, nil, nil)

	srv := New(Config{
		Host:   nodeHost,
		Engine: engine,
		Router: router,
		Store:  st,
		Logger: logger,
	})

	// Create an in-process gRPC server using bufconn.
	lis := bufconn.Listen(bufSize)
	grpcSrv := grpc.NewServer()
	pb.RegisterNodeServiceServer(grpcSrv, srv)

	go func() {
		if err := grpcSrv.Serve(lis); err != nil {
			// Will error on graceful stop, which is expected.
		}
	}()

	// Create a client connection through bufconn.
	conn, err := grpc.NewClient("passthrough://bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}

	client := pb.NewNodeServiceClient(conn)

	cleanup := func() {
		conn.Close()
		grpcSrv.GracefulStop()
		cancel()
		nodeHost.Close()
		st.Close()
	}

	_ = ctx // keep ctx alive

	return client, srv, cleanup
}

func TestGetNodeInfo(t *testing.T) {
	client, _, cleanup := testSetup(t)
	defer cleanup()

	resp, err := client.GetNodeInfo(context.Background(), &pb.GetNodeInfoRequest{})
	if err != nil {
		t.Fatalf("GetNodeInfo: %v", err)
	}

	info := resp.GetNodeInfo()
	if info == nil {
		t.Fatal("NodeInfo is nil")
	}
	if info.PeerId == "" {
		t.Fatal("PeerId should not be empty")
	}
	if info.Version == "" {
		t.Fatal("Version should not be empty")
	}
	if info.StartedAt == nil {
		t.Fatal("StartedAt should not be nil")
	}
	// The host listens on at least one address.
	if len(info.ListenAddresses) == 0 {
		t.Fatal("expected at least one listen address")
	}
}

func TestSetAgentCardAndGetTask(t *testing.T) {
	client, _, cleanup := testSetup(t)
	defer cleanup()

	ctx := context.Background()

	// Set an agent card.
	_, err := client.SetAgentCard(ctx, &pb.SetAgentCardRequest{
		Card: &pb.AgentCard{
			Name:        "test-agent",
			Description: "A test agent",
			Version:     "1.0.0",
		},
	})
	if err != nil {
		t.Fatalf("SetAgentCard: %v", err)
	}

	// Verify GetNodeInfo still works after setting a card.
	resp, err := client.GetNodeInfo(ctx, &pb.GetNodeInfoRequest{})
	if err != nil {
		t.Fatalf("GetNodeInfo after SetAgentCard: %v", err)
	}
	if resp.GetNodeInfo().PeerId == "" {
		t.Fatal("PeerId should not be empty after SetAgentCard")
	}
}

func TestSetAgentCardNilReturnsError(t *testing.T) {
	client, _, cleanup := testSetup(t)
	defer cleanup()

	_, err := client.SetAgentCard(context.Background(), &pb.SetAgentCardRequest{
		Card: nil,
	})
	if err == nil {
		t.Fatal("expected error for nil card, got nil")
	}
}

func TestGetTaskNotFound(t *testing.T) {
	client, _, cleanup := testSetup(t)
	defer cleanup()

	_, err := client.GetTask(context.Background(), &pb.GetTaskRequest{
		TaskId: "nonexistent-task",
	})
	if err == nil {
		t.Fatal("expected NotFound error, got nil")
	}
}

func TestSendTaskFlow(t *testing.T) {
	client, srv, cleanup := testSetup(t)
	defer cleanup()

	ctx := context.Background()

	// Set a card first (required for P2P extension).
	_, err := client.SetAgentCard(ctx, &pb.SetAgentCardRequest{
		Card: &pb.AgentCard{
			Name:    "test-agent",
			Version: "1.0.0",
		},
	})
	if err != nil {
		t.Fatalf("SetAgentCard: %v", err)
	}

	// We need a valid peer ID to send to. Use the server's own peer ID
	// (the send will go through the no-op sendFn).
	nodeInfo, err := client.GetNodeInfo(ctx, &pb.GetNodeInfoRequest{})
	if err != nil {
		t.Fatalf("GetNodeInfo: %v", err)
	}
	selfPeerID := nodeInfo.GetNodeInfo().PeerId

	// Send a task to ourselves (the no-op sendFn will succeed).
	sendResp, err := client.SendTask(ctx, &pb.SendTaskRequest{
		Target: &pb.SendTaskRequest_PeerId{PeerId: selfPeerID},
		Message: &pb.Message{
			MessageId: "msg-1",
			Role:      pb.MessageRole_MESSAGE_ROLE_USER,
			Parts: []*pb.Part{
				{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: "please summarize"}}},
			},
		},
	})
	if err != nil {
		t.Fatalf("SendTask: %v", err)
	}

	sentTask := sendResp.GetTask()
	if sentTask == nil {
		t.Fatal("SendTask response task is nil")
	}
	if sentTask.TaskId == "" {
		t.Fatal("task ID should not be empty")
	}
	if sentTask.Status != pb.TaskStatus_TASK_STATUS_SUBMITTED {
		t.Fatalf("expected SUBMITTED, got %v", sentTask.Status)
	}
	// Verify we can retrieve the task.
	getResp, err := client.GetTask(ctx, &pb.GetTaskRequest{TaskId: sentTask.TaskId})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if getResp.GetTask().TaskId != sentTask.TaskId {
		t.Fatalf("GetTask returned wrong task ID")
	}

	// Update status to WORKING, then COMPLETED.
	_, err = client.UpdateTaskStatus(ctx, &pb.UpdateTaskStatusRequest{
		TaskId: sentTask.TaskId,
		Status: pb.TaskStatus_TASK_STATUS_WORKING,
	})
	if err != nil {
		t.Fatalf("UpdateTaskStatus to WORKING: %v", err)
	}

	_, err = client.CompleteTask(ctx, &pb.CompleteTaskRequest{
		TaskId: sentTask.TaskId,
	})
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	// Verify final state.
	getResp, err = client.GetTask(ctx, &pb.GetTaskRequest{TaskId: sentTask.TaskId})
	if err != nil {
		t.Fatalf("GetTask after complete: %v", err)
	}
	if getResp.GetTask().Status != pb.TaskStatus_TASK_STATUS_COMPLETED {
		t.Fatalf("expected COMPLETED, got %v", getResp.GetTask().Status)
	}

	_ = srv // referenced to keep setup alive
}

func TestFailTaskFlow(t *testing.T) {
	client, srv, cleanup := testSetup(t)
	defer cleanup()

	ctx := context.Background()

	// Set a card first.
	_, err := client.SetAgentCard(ctx, &pb.SetAgentCardRequest{
		Card: &pb.AgentCard{
			Name:    "test-agent",
			Version: "1.0.0",
		},
	})
	if err != nil {
		t.Fatalf("SetAgentCard: %v", err)
	}

	nodeInfo, err := client.GetNodeInfo(ctx, &pb.GetNodeInfoRequest{})
	if err != nil {
		t.Fatalf("GetNodeInfo: %v", err)
	}
	selfPeerID := nodeInfo.GetNodeInfo().PeerId

	// Send a task to ourselves.
	sendResp, err := client.SendTask(ctx, &pb.SendTaskRequest{
		Target: &pb.SendTaskRequest_PeerId{PeerId: selfPeerID},
		Message: &pb.Message{
			MessageId: "msg-f1",
			Role:      pb.MessageRole_MESSAGE_ROLE_USER,
			Parts: []*pb.Part{
				{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: "fail me"}}},
			},
		},
	})
	if err != nil {
		t.Fatalf("SendTask: %v", err)
	}

	taskID := sendResp.GetTask().TaskId

	// Update status to WORKING first.
	_, err = client.UpdateTaskStatus(ctx, &pb.UpdateTaskStatusRequest{
		TaskId: taskID,
		Status: pb.TaskStatus_TASK_STATUS_WORKING,
	})
	if err != nil {
		t.Fatalf("UpdateTaskStatus to WORKING: %v", err)
	}

	// Now fail the task.
	_, err = client.FailTask(ctx, &pb.FailTaskRequest{
		TaskId:       taskID,
		ErrorMessage: "something went wrong",
	})
	if err != nil {
		t.Fatalf("FailTask: %v", err)
	}

	// Verify final state.
	getResp, err := client.GetTask(ctx, &pb.GetTaskRequest{TaskId: taskID})
	if err != nil {
		t.Fatalf("GetTask after fail: %v", err)
	}
	if getResp.GetTask().Status != pb.TaskStatus_TASK_STATUS_FAILED {
		t.Fatalf("expected FAILED, got %v", getResp.GetTask().Status)
	}

	_ = srv
}

func TestFailTaskEmptyTaskID(t *testing.T) {
	client, _, cleanup := testSetup(t)
	defer cleanup()

	_, err := client.FailTask(context.Background(), &pb.FailTaskRequest{
		TaskId: "",
	})
	if err == nil {
		t.Fatal("expected error for empty task ID, got nil")
	}
}

func TestCompleteTaskEmptyTaskID(t *testing.T) {
	client, _, cleanup := testSetup(t)
	defer cleanup()

	_, err := client.CompleteTask(context.Background(), &pb.CompleteTaskRequest{
		TaskId: "",
	})
	if err == nil {
		t.Fatal("expected error for empty task ID, got nil")
	}
}

func TestCancelTaskFlow(t *testing.T) {
	client, srv, cleanup := testSetup(t)
	defer cleanup()

	ctx := context.Background()

	// Set a card.
	_, err := client.SetAgentCard(ctx, &pb.SetAgentCardRequest{
		Card: &pb.AgentCard{Name: "test-agent", Version: "1.0.0"},
	})
	if err != nil {
		t.Fatalf("SetAgentCard: %v", err)
	}

	nodeInfo, err := client.GetNodeInfo(ctx, &pb.GetNodeInfoRequest{})
	if err != nil {
		t.Fatalf("GetNodeInfo: %v", err)
	}
	selfPeerID := nodeInfo.GetNodeInfo().PeerId

	// Send a task.
	sendResp, err := client.SendTask(ctx, &pb.SendTaskRequest{
		Target: &pb.SendTaskRequest_PeerId{PeerId: selfPeerID},
		Message: &pb.Message{
			Role: pb.MessageRole_MESSAGE_ROLE_USER,
			Parts: []*pb.Part{
				{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: "cancel me"}}},
			},
		},
	})
	if err != nil {
		t.Fatalf("SendTask: %v", err)
	}

	taskID := sendResp.GetTask().TaskId

	// Cancel the task.
	_, err = client.CancelTask(ctx, &pb.CancelTaskRequest{TaskId: taskID})
	if err != nil {
		t.Fatalf("CancelTask: %v", err)
	}

	// Verify final state.
	getResp, err := client.GetTask(ctx, &pb.GetTaskRequest{TaskId: taskID})
	if err != nil {
		t.Fatalf("GetTask after cancel: %v", err)
	}
	if getResp.GetTask().Status != pb.TaskStatus_TASK_STATUS_CANCELED {
		t.Fatalf("expected CANCELED, got %v", getResp.GetTask().Status)
	}

	_ = srv
}

func TestCancelTaskEmptyTaskID(t *testing.T) {
	client, _, cleanup := testSetup(t)
	defer cleanup()

	_, err := client.CancelTask(context.Background(), &pb.CancelTaskRequest{TaskId: ""})
	if err == nil {
		t.Fatal("expected error for empty task ID")
	}
}

func TestCancelTaskNotFound(t *testing.T) {
	client, _, cleanup := testSetup(t)
	defer cleanup()

	_, err := client.CancelTask(context.Background(), &pb.CancelTaskRequest{TaskId: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestUpdateTaskStatusEmptyTaskID(t *testing.T) {
	client, _, cleanup := testSetup(t)
	defer cleanup()

	_, err := client.UpdateTaskStatus(context.Background(), &pb.UpdateTaskStatusRequest{
		TaskId: "",
		Status: pb.TaskStatus_TASK_STATUS_WORKING,
	})
	if err == nil {
		t.Fatal("expected error for empty task ID")
	}
}

func TestGracefulShutdownWithContext(t *testing.T) {
	// Test that the gRPC server shuts down when context is canceled.
	ctx, cancel := context.WithCancel(context.Background())
	logger := testLogger()

	priv, _, err := libp2pcrypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	nodeHost, err := node.NewHost(ctx, node.HostConfig{
		PrivateKey:  priv,
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	}, logger)
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	defer nodeHost.Close()

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	engine := a2a.NewEngine(logger)
	engine.SetStore(st)

	router := a2a.NewRouter(engine, logger, func(_ context.Context, _ peer.ID, _ []byte) error {
		return nil
	}, nil, nil)

	srv := New(Config{
		Host:   nodeHost,
		Engine: engine,
		Router: router,
		Store:  st,
		Logger: logger,
	})

	// Use a bufconn to serve.
	lis := bufconn.Listen(bufSize)
	grpcSrv := grpc.NewServer()
	pb.RegisterNodeServiceServer(grpcSrv, srv)

	serverDone := make(chan struct{})
	go func() {
		_ = grpcSrv.Serve(lis)
		close(serverDone)
	}()

	// Connect and verify it works.
	conn, err := grpc.NewClient("passthrough://bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	client := pb.NewNodeServiceClient(conn)

	// Verify the server is alive.
	_, err = client.GetNodeInfo(context.Background(), &pb.GetNodeInfoRequest{})
	if err != nil {
		t.Fatalf("GetNodeInfo before shutdown: %v", err)
	}

	// Trigger graceful stop.
	cancel()
	grpcSrv.GracefulStop()

	select {
	case <-serverDone:
		// Good, server shut down.
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down within 10s")
	}
}
