// Package grpcserver implements the NodeService gRPC server
// that the Python SDK uses to control the Go daemon.
package grpcserver

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
	"github.com/agentanycast/agentanycast-node/internal/a2a"
	"github.com/agentanycast/agentanycast-node/internal/node"
	"github.com/agentanycast/agentanycast-node/internal/store"
)

// Server implements the NodeService gRPC interface.
type Server struct {
	pb.UnimplementedNodeServiceServer

	host      *node.Host
	engine    *a2a.Engine
	router    *a2a.Router
	store     *store.Store
	logger    *slog.Logger
	startedAt time.Time

	mu       sync.RWMutex
	card     *pb.AgentCard
	cardRaw  []byte // serialized card for libp2p exchange

	// Channel for incoming task subscribers
	incomingTaskSubs []chan *a2a.IncomingTaskEvent
	subsMu           sync.Mutex
}

// Config holds configuration for the gRPC server.
type Config struct {
	Host    *node.Host
	Engine  *a2a.Engine
	Router  *a2a.Router
	Store   *store.Store
	Logger  *slog.Logger
}

// New creates a new gRPC server.
func New(cfg Config) *Server {
	s := &Server{
		host:      cfg.Host,
		engine:    cfg.Engine,
		router:    cfg.Router,
		store:     cfg.Store,
		logger:    cfg.Logger,
		startedAt: time.Now(),
	}

	// Recover persisted self card from store if available.
	if raw, err := cfg.Host.GetSelfCard(); err == nil && len(raw) > 0 {
		var card pb.AgentCard
		if err := proto.Unmarshal(raw, &card); err == nil {
			s.card = &card
			s.cardRaw = raw
			cfg.Logger.Info("recovered self agent card from store", "name", card.Name)
		}
	}

	return s
}

// ListenAndServe starts the gRPC server on the given address.
// Address can be "unix:///path/to/sock" or "tcp://host:port".
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	var (
		lis     net.Listener
		err     error
		network string
		address string
	)

	if strings.HasPrefix(addr, "unix://") {
		network = "unix"
		address = addr[7:]
		// Clean up stale socket
		os.Remove(address)
	} else if strings.HasPrefix(addr, "tcp://") {
		network = "tcp"
		address = addr[6:]
	} else {
		// Default: treat as unix path
		network = "unix"
		address = addr
	}

	lis, err = net.Listen(network, address)
	if err != nil {
		return fmt.Errorf("listen %s://%s: %w", network, address, err)
	}

	srv := grpc.NewServer()
	pb.RegisterNodeServiceServer(srv, s)

	// Register health check service
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("agentanycast.v1.NodeService", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, healthSrv)

	s.logger.Info("gRPC server listening", "network", network, "address", address)

	// Start forwarding incoming tasks from router to subscribers
	go s.forwardIncomingTasks(ctx)

	// Graceful shutdown with timeout to prevent hanging on long-lived streams.
	go func() {
		<-ctx.Done()
		s.logger.Info("gRPC server shutting down")
		done := make(chan struct{})
		go func() {
			srv.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			s.logger.Warn("graceful stop timed out, forcing stop")
			srv.Stop()
		}
	}()

	return srv.Serve(lis)
}

// forwardIncomingTasks reads from the router's incoming channel and fans out to all subscribers.
func (s *Server) forwardIncomingTasks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-s.router.IncomingTasks():
			if !ok {
				return
			}
			s.subsMu.Lock()
			for _, ch := range s.incomingTaskSubs {
				select {
				case ch <- evt:
				default:
				}
			}
			s.subsMu.Unlock()
		}
	}
}

// ── Node Management ────────────────────────────────────────

func (s *Server) GetNodeInfo(ctx context.Context, req *pb.GetNodeInfoRequest) (*pb.GetNodeInfoResponse, error) {
	addrs := s.host.Addrs()
	addrStrs := make([]string, len(addrs))
	for i, a := range addrs {
		addrStrs[i] = a.String()
	}

	return &pb.GetNodeInfoResponse{
		NodeInfo: &pb.NodeInfo{
			PeerId:          s.host.ID().String(),
			ListenAddresses: addrStrs,
			ConnectedPeers:  int32(len(s.host.ConnectedPeers())),
			StartedAt:       timestamppb.New(s.startedAt),
			Version:         "0.1.0",
		},
	}, nil
}

func (s *Server) SetAgentCard(ctx context.Context, req *pb.SetAgentCardRequest) (*pb.SetAgentCardResponse, error) {
	if req.Card == nil {
		return nil, status.Error(codes.InvalidArgument, "card is required")
	}

	// Add P2P extension with our PeerID
	if req.Card.P2PExtension == nil {
		req.Card.P2PExtension = &pb.P2PExtension{}
	}
	req.Card.P2PExtension.PeerId = s.host.ID().String()

	raw, err := proto.Marshal(req.Card)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal card: %v", err)
	}

	s.mu.Lock()
	s.card = req.Card
	s.cardRaw = raw
	s.mu.Unlock()

	// Persist our own card to the store for recovery on restart.
	s.host.SetSelfCard(raw)

	s.logger.Info("agent card set", "name", req.Card.Name)

	// Push card to all connected peers.
	// Use background context since the gRPC request context is short-lived.
	for _, pid := range s.host.ConnectedPeers() {
		go func(pid peer.ID) {
			exCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.host.ExchangeCard(exCtx, pid, raw); err != nil {
				s.logger.Debug("card push failed", "peer", pid, "error", err)
			}
		}(pid)
	}

	return &pb.SetAgentCardResponse{}, nil
}

// GetLocalCardBytes returns the serialized local card for the libp2p card protocol.
func (s *Server) GetLocalCardBytes() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cardRaw
}

// ── Peer Management ────────────────────────────────────────

func (s *Server) ConnectPeer(ctx context.Context, req *pb.ConnectPeerRequest) (*pb.ConnectPeerResponse, error) {
	if req.PeerId == "" {
		return nil, status.Error(codes.InvalidArgument, "peer_id is required")
	}

	pid, err := peer.Decode(req.PeerId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid peer_id: %v", err)
	}

	// Build AddrInfo from provided addresses
	var addrs []multiaddr.Multiaddr
	for _, a := range req.Addresses {
		ma, err := multiaddr.NewMultiaddr(a)
		if err != nil {
			s.logger.Warn("invalid address", "addr", a, "error", err)
			continue
		}
		addrs = append(addrs, ma)
	}

	ai := peer.AddrInfo{
		ID:    pid,
		Addrs: addrs,
	}

	if err := s.host.Connect(ctx, ai); err != nil {
		return nil, status.Errorf(codes.Unavailable, "connect to peer %s: %v", pid, err)
	}

	// Exchange cards after connection
	cardRaw := s.GetLocalCardBytes()
	if cardRaw != nil {
		go func() {
			exCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.host.ExchangeCard(exCtx, pid, cardRaw); err != nil {
				s.logger.Debug("card exchange after connect failed", "peer", pid, "error", err)
			}
		}()
	}

	return &pb.ConnectPeerResponse{
		PeerInfo: &pb.PeerInfo{
			PeerId:      pid.String(),
			ConnectedAt: timestamppb.Now(),
		},
	}, nil
}

func (s *Server) ListPeers(ctx context.Context, req *pb.ListPeersRequest) (*pb.ListPeersResponse, error) {
	peerIDs := s.host.ConnectedPeers()
	peers := make([]*pb.PeerInfo, 0, len(peerIDs))

	for _, pid := range peerIDs {
		pi := &pb.PeerInfo{
			PeerId: pid.String(),
		}
		conns := s.host.Network().ConnsToPeer(pid)
		if len(conns) > 0 {
			pi.Addresses = []string{conns[0].RemoteMultiaddr().String()}
		}
		peers = append(peers, pi)
	}

	return &pb.ListPeersResponse{Peers: peers}, nil
}

func (s *Server) GetPeerCard(ctx context.Context, req *pb.GetPeerCardRequest) (*pb.GetPeerCardResponse, error) {
	if req.PeerId == "" {
		return nil, status.Error(codes.InvalidArgument, "peer_id is required")
	}

	pid, err := peer.Decode(req.PeerId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid peer_id: %v", err)
	}

	data, ok := s.host.GetPeerCard(pid)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "no card for peer %s", req.PeerId)
	}

	var card pb.AgentCard
	if err := proto.Unmarshal(data, &card); err != nil {
		return nil, status.Errorf(codes.Internal, "unmarshal peer card: %v", err)
	}

	return &pb.GetPeerCardResponse{Card: &card}, nil
}

// ── Task Client Operations ─────────────────────────────────

func (s *Server) SendTask(ctx context.Context, req *pb.SendTaskRequest) (*pb.SendTaskResponse, error) {
	if req.PeerId == "" {
		return nil, status.Error(codes.InvalidArgument, "peer_id is required")
	}
	if req.Message == nil {
		return nil, status.Error(codes.InvalidArgument, "message is required")
	}

	pid, err := peer.Decode(req.PeerId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid peer_id: %v", err)
	}

	task, err := s.router.SendTask(ctx, pid, req.Message, req.TargetSkillId, req.ContextId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "send task: %v", err)
	}

	pbTask := &pb.Task{
		TaskId:           task.ID,
		ContextId:        task.ContextID,
		Status:           a2a.TaskStatusToProto(task.Status),
		TargetSkillId:    task.TargetSkillID,
		OriginatorPeerId: task.OriginatorPeerID,
		Messages:         []*pb.Message{req.Message},
		CreatedAt:        timestamppb.New(task.CreatedAt),
		UpdatedAt:        timestamppb.New(task.UpdatedAt),
	}

	return &pb.SendTaskResponse{Task: pbTask}, nil
}

func (s *Server) GetTask(ctx context.Context, req *pb.GetTaskRequest) (*pb.GetTaskResponse, error) {
	if req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id is required")
	}

	task, err := s.engine.GetTask(req.TaskId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "%v", err)
	}

	return &pb.GetTaskResponse{
		Task: &pb.Task{
			TaskId:           task.ID,
			ContextId:        task.ContextID,
			Status:           a2a.TaskStatusToProto(task.Status),
			TargetSkillId:    task.TargetSkillID,
			OriginatorPeerId: task.OriginatorPeerID,
			CreatedAt:        timestamppb.New(task.CreatedAt),
			UpdatedAt:        timestamppb.New(task.UpdatedAt),
		},
	}, nil
}

func (s *Server) CancelTask(ctx context.Context, req *pb.CancelTaskRequest) (*pb.CancelTaskResponse, error) {
	if req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id is required")
	}

	task, err := s.engine.GetTask(req.TaskId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "%v", err)
	}

	// Send cancel to remote peer (use background context since the RPC ctx is short-lived).
	if task.OriginatorPeerID != "local" {
		pid, err := peer.Decode(task.OriginatorPeerID)
		if err == nil {
			go func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := s.router.SendFail(bgCtx, pid, task.ID, "canceled by local node"); err != nil {
					s.logger.Warn("failed to send cancel to originator", "task_id", task.ID, "peer", pid, "error", err)
				}
			}()
		}
	}

	if err := s.engine.TransitionTask(req.TaskId, a2a.StatusCanceled); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}

	task, err = s.engine.GetTask(req.TaskId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get task after cancel: %v", err)
	}
	return &pb.CancelTaskResponse{
		Task: &pb.Task{
			TaskId:           task.ID,
			ContextId:        task.ContextID,
			Status:           a2a.TaskStatusToProto(task.Status),
			TargetSkillId:    task.TargetSkillID,
			OriginatorPeerId: task.OriginatorPeerID,
			CreatedAt:        timestamppb.New(task.CreatedAt),
			UpdatedAt:        timestamppb.New(task.UpdatedAt),
		},
	}, nil
}

func (s *Server) SubscribeTaskUpdates(req *pb.SubscribeTaskUpdatesRequest, stream pb.NodeService_SubscribeTaskUpdatesServer) error {
	if req.TaskId == "" {
		return status.Error(codes.InvalidArgument, "task_id is required")
	}

	ch := s.router.SubscribeTaskUpdates(req.TaskId)
	defer s.router.UnsubscribeTaskUpdates(req.TaskId, ch)

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&pb.SubscribeTaskUpdatesResponse{
				TaskId:    evt.TaskID,
				Status:    evt.Status,
				Message:   evt.Message,
				Artifacts: evt.Artifacts,
			}); err != nil {
				return err
			}
		}
	}
}

// ── Task Server Operations ─────────────────────────────────

func (s *Server) SubscribeIncomingTasks(req *pb.SubscribeIncomingTasksRequest, stream pb.NodeService_SubscribeIncomingTasksServer) error {
	ch := make(chan *a2a.IncomingTaskEvent, 16)

	s.subsMu.Lock()
	s.incomingTaskSubs = append(s.incomingTaskSubs, ch)
	s.subsMu.Unlock()

	defer func() {
		s.subsMu.Lock()
		for i, c := range s.incomingTaskSubs {
			if c == ch {
				s.incomingTaskSubs = append(s.incomingTaskSubs[:i], s.incomingTaskSubs[i+1:]...)
				break
			}
		}
		s.subsMu.Unlock()
		close(ch)
	}()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&pb.SubscribeIncomingTasksResponse{
				Task:       evt.Task,
				SenderCard: evt.SenderCard,
			}); err != nil {
				return err
			}
		}
	}
}

func (s *Server) UpdateTaskStatus(ctx context.Context, req *pb.UpdateTaskStatusRequest) (*pb.UpdateTaskStatusResponse, error) {
	if req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id is required")
	}

	newStatus := a2a.ProtoToTaskStatus(req.Status)
	if err := s.engine.TransitionTask(req.TaskId, newStatus); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}

	// Send status update to originator peer
	task, err := s.engine.GetTask(req.TaskId)
	if err == nil && task.OriginatorPeerID != "local" {
		pid, err := peer.Decode(task.OriginatorPeerID)
		if err == nil {
			go func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := s.router.SendStatusUpdate(bgCtx, pid, req.TaskId, req.Status, req.Message); err != nil {
					s.logger.Warn("failed to send status update to originator", "task_id", req.TaskId, "peer", pid, "error", err)
				}
			}()
		}
	}

	return &pb.UpdateTaskStatusResponse{}, nil
}

func (s *Server) CompleteTask(ctx context.Context, req *pb.CompleteTaskRequest) (*pb.CompleteTaskResponse, error) {
	if req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id is required")
	}

	if err := s.engine.TransitionTask(req.TaskId, a2a.StatusCompleted); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}

	// Send completion to originator peer
	task, err := s.engine.GetTask(req.TaskId)
	if err == nil && task.OriginatorPeerID != "local" {
		pid, err := peer.Decode(task.OriginatorPeerID)
		if err == nil {
			go func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := s.router.SendComplete(bgCtx, pid, req.TaskId, req.Artifacts, req.Message); err != nil {
					s.logger.Warn("failed to send completion to originator", "task_id", req.TaskId, "peer", pid, "error", err)
				}
			}()
		}
	}

	return &pb.CompleteTaskResponse{}, nil
}

func (s *Server) FailTask(ctx context.Context, req *pb.FailTaskRequest) (*pb.FailTaskResponse, error) {
	if req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id is required")
	}

	if err := s.engine.TransitionTask(req.TaskId, a2a.StatusFailed); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}

	// Send failure to originator peer
	task, err := s.engine.GetTask(req.TaskId)
	if err == nil && task.OriginatorPeerID != "local" {
		pid, err := peer.Decode(task.OriginatorPeerID)
		if err == nil {
			go func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := s.router.SendFail(bgCtx, pid, req.TaskId, req.ErrorMessage); err != nil {
					s.logger.Warn("failed to send failure to originator", "task_id", req.TaskId, "peer", pid, "error", err)
				}
			}()
		}
	}

	return &pb.FailTaskResponse{}, nil
}
