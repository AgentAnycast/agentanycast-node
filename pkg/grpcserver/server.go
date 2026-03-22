// Package grpcserver implements the NodeService gRPC server
// that the Python SDK uses to control the Go daemon.
package grpcserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
	"github.com/agentanycast/agentanycast-node/internal/a2a"
	"github.com/agentanycast/agentanycast-node/internal/anycast"
	"github.com/agentanycast/agentanycast-node/internal/bridge"
	agentcrypto "github.com/agentanycast/agentanycast-node/internal/crypto"
	"github.com/agentanycast/agentanycast-node/internal/metrics"
	"github.com/agentanycast/agentanycast-node/internal/node"
	"github.com/agentanycast/agentanycast-node/internal/store"
)

// Server implements the NodeService gRPC interface.
type Server struct {
	pb.UnimplementedNodeServiceServer

	host           *node.Host
	engine         *a2a.Engine
	router         *a2a.Router
	store          *store.Store
	logger         *slog.Logger
	startedAt      time.Time
	anycastRouter  *anycast.Router  // v0.2: Anycast routing
	outboundBridge *bridge.OutboundClient // v0.2: HTTP Bridge outbound
	streamMgr      *a2a.StreamManager     // v0.2: Streaming

	mu       sync.RWMutex
	card     *pb.AgentCard
	cardRaw  []byte // serialized card for libp2p exchange

	// Channel for incoming task subscribers
	incomingTaskSubs []chan *a2a.IncomingTaskEvent
	subsMu           sync.Mutex

	// v0.7: MCP Proxy handler for auto-processing incoming tasks.
	mcpProxyHandler MCPProxyHandler
	mcpProxySkills  map[string]bool // skill IDs managed by the proxy
}

// MCPProxyHandler is called when an incoming task targets a skill managed
// by the MCP proxy. It receives the skill/tool name and the JSON arguments,
// and returns the JSON result from the MCP tool call.
type MCPProxyHandler func(ctx context.Context, skillID string, input json.RawMessage) (json.RawMessage, error)

// Config holds configuration for the gRPC server.
type Config struct {
	Host           *node.Host
	Engine         *a2a.Engine
	Router         *a2a.Router
	Store          *store.Store
	Logger         *slog.Logger
	AnycastRouter  *anycast.Router       // v0.2
	OutboundBridge *bridge.OutboundClient // v0.2
	StreamManager  *a2a.StreamManager     // v0.2
}

// New creates a new gRPC server.
func New(cfg Config) *Server {
	s := &Server{
		host:           cfg.Host,
		engine:         cfg.Engine,
		router:         cfg.Router,
		store:          cfg.Store,
		logger:         cfg.Logger,
		startedAt:      time.Now(),
		anycastRouter:  cfg.AnycastRouter,
		outboundBridge: cfg.OutboundBridge,
		streamMgr:      cfg.StreamManager,
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

	srv := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)
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

// RegisterMCPProxyHandler registers a handler for incoming tasks that target
// skills managed by the MCP Remote Proxy. When an incoming task's skill ID
// matches one of the registered proxy skills, the handler is invoked automatically
// instead of forwarding the task to SDK subscribers.
func (s *Server) RegisterMCPProxyHandler(handler MCPProxyHandler, skillIDs []string) {
	s.mcpProxyHandler = handler
	s.mcpProxySkills = make(map[string]bool, len(skillIDs))
	for _, id := range skillIDs {
		s.mcpProxySkills[id] = true
	}
	s.logger.Info("MCP proxy handler registered", "skills", len(skillIDs))
}

// RegisterProxySkill adds a single skill ID to the local agent card.
func (s *Server) RegisterProxySkill(skill *pb.Skill) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.card == nil {
		s.card = &pb.AgentCard{
			Name:            "MCP Proxy Agent",
			Description:     "Auto-generated agent card for MCP proxy",
			Version:         "0.7.0",
			ProtocolVersion: "a2a/0.3",
		}
	}
	s.card.Skills = append(s.card.Skills, skill)

	raw, err := proto.Marshal(s.card)
	if err != nil {
		s.logger.Error("failed to marshal card after skill registration", "error", err)
		return
	}
	s.cardRaw = raw
	s.host.SetSelfCard(raw)
}

// forwardIncomingTasks reads from the router's incoming channel and fans out to all subscribers.
// If an MCP proxy handler is registered and the incoming task targets a proxy-managed skill,
// the task is handled automatically without forwarding to SDK subscribers.
func (s *Server) forwardIncomingTasks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-s.router.IncomingTasks():
			if !ok {
				return
			}
			metrics.TasksTotal.WithLabelValues("received", "submitted").Inc()

			// v0.7: Check if this task targets an MCP proxy skill.
			if s.mcpProxyHandler != nil && evt.Task != nil && s.mcpProxySkills[evt.Task.TargetSkillId] {
				go s.handleMCPProxyTask(ctx, evt)
				continue
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

// handleMCPProxyTask processes an incoming task via the MCP proxy handler.
// It extracts the message text as JSON arguments, invokes the proxy handler,
// and sends the result back to the originator.
func (s *Server) handleMCPProxyTask(ctx context.Context, evt *a2a.IncomingTaskEvent) {
	task := evt.Task
	skillID := task.TargetSkillId

	// Mark task as working.
	_ = s.engine.TransitionTask(ctx, task.TaskId, a2a.StatusWorking)

	// Extract message text as the input arguments.
	var input json.RawMessage
	if len(task.Messages) > 0 && len(task.Messages[0].Parts) > 0 {
		for _, part := range task.Messages[0].Parts {
			if tp := part.GetTextPart(); tp != nil {
				input = json.RawMessage(tp.Text)
				break
			}
		}
	}
	if input == nil {
		input = json.RawMessage(`{}`)
	}

	// Call the MCP proxy handler.
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	result, err := s.mcpProxyHandler(callCtx, skillID, input)
	if err != nil {
		s.logger.Error("MCP proxy task failed", "task_id", task.TaskId, "skill", skillID, "error", err)
		_ = s.engine.TransitionTask(ctx, task.TaskId, a2a.StatusFailed)

		// Send failure back to originator.
		if task.OriginatorPeerId != "" && task.OriginatorPeerId != "local" {
			pid, peerErr := peer.Decode(task.OriginatorPeerId)
			if peerErr == nil {
				bgCtx, bgCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer bgCancel()
				_ = s.router.SendFail(bgCtx, pid, task.TaskId, err.Error())
			}
		}
		return
	}

	// Mark as completed and send result back.
	_ = s.engine.TransitionTask(ctx, task.TaskId, a2a.StatusCompleted)

	resultArtifact := &pb.Artifact{
		Parts: []*pb.Part{
			{Content: &pb.Part_TextPart{TextPart: &pb.TextPart{Text: string(result)}}},
		},
	}

	if task.OriginatorPeerId != "" && task.OriginatorPeerId != "local" {
		pid, peerErr := peer.Decode(task.OriginatorPeerId)
		if peerErr == nil {
			bgCtx, bgCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer bgCancel()
			if sendErr := s.router.SendComplete(bgCtx, pid, task.TaskId, []*pb.Artifact{resultArtifact}, nil); sendErr != nil {
				s.logger.Warn("failed to send MCP proxy result to originator",
					"task_id", task.TaskId, "peer", pid, "error", sendErr)
			}
		}
	}

	s.logger.Info("MCP proxy task completed", "task_id", task.TaskId, "skill", skillID)
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

	// Auto-populate did:key from the host's PeerID.
	if didKey, err := agentcrypto.PeerIDToDIDKey(s.host.ID()); err == nil {
		req.Card.P2PExtension.DidKey = didKey
	}

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
	if req.Message == nil {
		return nil, status.Error(codes.InvalidArgument, "message is required")
	}

	switch target := req.Target.(type) {
	case *pb.SendTaskRequest_PeerId:
		return s.sendTaskToPeer(ctx, target.PeerId, req.Message, req.Metadata)

	case *pb.SendTaskRequest_SkillId:
		return s.sendTaskBySkill(ctx, target.SkillId, req.Message, req.Metadata)

	case *pb.SendTaskRequest_Url:
		return s.sendTaskViaHTTPBridge(ctx, target.Url, req.Message, req.Metadata)

	default:
		return nil, status.Error(codes.InvalidArgument, "target (peer_id, skill_id, or url) is required")
	}
}

// sendTaskToPeer sends a task directly to a known PeerID.
func (s *Server) sendTaskToPeer(ctx context.Context, peerIDStr string, msg *pb.Message, metadata map[string]string) (*pb.SendTaskResponse, error) {
	start := time.Now()

	pid, err := peer.Decode(peerIDStr)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid peer_id: %v", err)
	}

	task, err := s.router.SendTask(ctx, pid, msg, "", "")
	if err != nil {
		metrics.TasksTotal.WithLabelValues("sent", "error").Inc()
		return nil, status.Errorf(codes.Internal, "send task: %v", err)
	}

	metrics.TasksTotal.WithLabelValues("sent", "submitted").Inc()
	metrics.TaskDuration.WithLabelValues("sent").Observe(time.Since(start).Seconds())
	return &pb.SendTaskResponse{Task: taskToProto(task, msg)}, nil
}

// sendTaskBySkill resolves a skill to a peer via Anycast routing, then sends the task.
func (s *Server) sendTaskBySkill(ctx context.Context, skillID string, msg *pb.Message, metadata map[string]string) (*pb.SendTaskResponse, error) {
	start := time.Now()

	if s.anycastRouter == nil {
		return nil, status.Error(codes.Unavailable, "anycast routing is not configured")
	}

	targetPeer, err := s.anycastRouter.Resolve(ctx, skillID)
	if err != nil {
		metrics.RouteResolutions.WithLabelValues("miss").Inc()
		return nil, status.Errorf(codes.NotFound, "no agent found for skill %q: %v", skillID, err)
	}
	metrics.RouteResolutions.WithLabelValues("hit").Inc()

	task, err := s.router.SendTask(ctx, targetPeer, msg, skillID, "")
	if err != nil {
		// Invalidate cache on send failure so we try a different peer next time.
		s.anycastRouter.InvalidateCache(skillID)
		metrics.TasksTotal.WithLabelValues("sent", "error").Inc()
		return nil, status.Errorf(codes.Internal, "send task to %s: %v", targetPeer, err)
	}

	metrics.TasksTotal.WithLabelValues("sent", "submitted").Inc()
	metrics.TaskDuration.WithLabelValues("sent").Observe(time.Since(start).Seconds())
	return &pb.SendTaskResponse{Task: taskToProto(task, msg)}, nil
}

// sendTaskViaHTTPBridge sends a task to an external HTTP A2A agent.
func (s *Server) sendTaskViaHTTPBridge(ctx context.Context, targetURL string, msg *pb.Message, metadata map[string]string) (*pb.SendTaskResponse, error) {
	start := time.Now()

	if s.outboundBridge == nil {
		return nil, status.Error(codes.Unavailable, "HTTP bridge is not configured")
	}

	resultTask, err := s.outboundBridge.SendTask(ctx, targetURL, msg, metadata)
	if err != nil {
		metrics.BridgeRequests.WithLabelValues("outbound", "error").Inc()
		return nil, status.Errorf(codes.Internal, "HTTP bridge to %s: %v", targetURL, err)
	}

	metrics.BridgeRequests.WithLabelValues("outbound", "ok").Inc()
	metrics.TaskDuration.WithLabelValues("sent").Observe(time.Since(start).Seconds())
	return &pb.SendTaskResponse{Task: resultTask}, nil
}

func taskToProto(task *a2a.Task, msg *pb.Message) *pb.Task {
	return &pb.Task{
		TaskId:           task.ID,
		ContextId:        task.ContextID,
		Status:           a2a.TaskStatusToProto(task.Status),
		TargetSkillId:    task.TargetSkillID,
		OriginatorPeerId: task.OriginatorPeerID,
		Messages:         []*pb.Message{msg},
		CreatedAt:        timestamppb.New(task.CreatedAt),
		UpdatedAt:        timestamppb.New(task.UpdatedAt),
	}
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

	if err := s.engine.TransitionTask(ctx, req.TaskId, a2a.StatusCanceled); err != nil {
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
	if err := s.engine.TransitionTask(ctx, req.TaskId, newStatus); err != nil {
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

	if err := s.engine.TransitionTask(ctx, req.TaskId, a2a.StatusCompleted); err != nil {
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

	if err := s.engine.TransitionTask(ctx, req.TaskId, a2a.StatusFailed); err != nil {
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

// ── Streaming (v0.2) ──────────────────────────────────────

func (s *Server) SubscribeTaskStream(req *pb.SubscribeTaskStreamRequest, stream pb.NodeService_SubscribeTaskStreamServer) error {
	if req.TaskId == "" {
		return status.Error(codes.InvalidArgument, "task_id is required")
	}
	if s.streamMgr == nil {
		return status.Error(codes.Unavailable, "streaming is not configured")
	}

	ch, cleanup := s.streamMgr.Subscribe(req.TaskId)
	defer cleanup()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(evt); err != nil {
				return err
			}
		}
	}
}

func (s *Server) SendStreamingArtifact(stream pb.NodeService_SendStreamingArtifactServer) error {
	if s.streamMgr == nil {
		return status.Error(codes.Unavailable, "streaming is not configured")
	}

	for {
		req, err := stream.Recv()
		if err != nil {
			if err.Error() == "EOF" {
				return stream.SendAndClose(&pb.SendStreamingArtifactResponse{})
			}
			return err
		}

		switch evt := req.Event.(type) {
		case *pb.SendStreamingArtifactRequest_StreamStart:
			if err := s.streamMgr.HandleStreamStart(evt.StreamStart); err != nil {
				return status.Errorf(codes.Internal, "stream start: %v", err)
			}
		case *pb.SendStreamingArtifactRequest_StreamChunk:
			if err := s.streamMgr.HandleStreamChunk(evt.StreamChunk); err != nil {
				return status.Errorf(codes.Internal, "stream chunk: %v", err)
			}
		case *pb.SendStreamingArtifactRequest_StreamEnd:
			if err := s.streamMgr.HandleStreamEnd(evt.StreamEnd); err != nil {
				return status.Errorf(codes.Internal, "stream end: %v", err)
			}
		}
	}
}

// ── Discovery (v0.2) ────────────────────────────────────────

func (s *Server) Discover(ctx context.Context, req *pb.DiscoverRequest) (*pb.DiscoverResponse, error) {
	if req.SkillId == "" {
		return nil, status.Error(codes.InvalidArgument, "skill_id is required")
	}
	if s.anycastRouter == nil {
		return nil, status.Error(codes.Unavailable, "anycast routing is not configured")
	}

	endpoints, err := s.anycastRouter.Discover(ctx, req.SkillId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "discover: %v", err)
	}

	agents := make([]*pb.DiscoveredAgent, 0, len(endpoints))
	for _, ep := range endpoints {
		skills := make([]*pb.SkillInfo, 0, len(ep.Skills))
		for _, sk := range ep.Skills {
			skills = append(skills, &pb.SkillInfo{
				SkillId:     sk.SkillID,
				Description: sk.Description,
				Tags:        sk.Tags,
			})
		}
		agents = append(agents, &pb.DiscoveredAgent{
			PeerId:           ep.PeerID.String(),
			AgentName:        ep.AgentName,
			AgentDescription: ep.Description,
			Skills:           skills,
		})
	}

	return &pb.DiscoverResponse{Agents: agents}, nil
}

// GetLocalCard returns the current local AgentCard protobuf (for bridge/card endpoint).
func (s *Server) GetLocalCard() *pb.AgentCard {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.card
}

// HandleInboundTask implements bridge.TaskSender — processes an HTTP-originated task
// through the local A2A engine and returns the result.
func (s *Server) HandleInboundTask(task *pb.Task) (*pb.Task, error) {
	if task == nil || len(task.Messages) == 0 {
		return nil, fmt.Errorf("invalid inbound task: no messages")
	}

	// Create an internal task via the A2A engine.
	msg := task.Messages[0]
	internalTask, err := s.router.SendTask(context.Background(), s.host.ID(), msg, task.TargetSkillId, "")
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}

	// Return the task in its initial state. The caller will get updates via
	// the task status subscription if needed.
	return &pb.Task{
		TaskId:        internalTask.ID,
		ContextId:     internalTask.ContextID,
		Status:        a2a.TaskStatusToProto(internalTask.Status),
		TargetSkillId: internalTask.TargetSkillID,
		Messages:      task.Messages,
		CreatedAt:     timestamppb.New(internalTask.CreatedAt),
		UpdatedAt:     timestamppb.New(internalTask.UpdatedAt),
	}, nil
}
