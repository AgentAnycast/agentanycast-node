// Package anycast implements capability-based routing for AgentAnycast.
//
// It provides the Anycast Router that resolves skill-based addressing
// to concrete peer endpoints using pluggable discovery providers and
// routing strategies.
package anycast

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// AgentEndpoint describes a routable agent endpoint.
type AgentEndpoint struct {
	PeerID      peer.ID
	Skills      []SkillInfo
	AgentName   string
	Description string
	LastSeen    time.Time
}

// SkillInfo describes a single skill.
type SkillInfo struct {
	SkillID     string
	Description string
	Tags        map[string]string
}

// DiscoveryProvider defines the interface for agent discovery.
// v0.2: RegistryDiscovery (queries Relay's centralized registry)
// v0.3: DHTDiscovery (queries Kademlia DHT)
type DiscoveryProvider interface {
	// DiscoverBySkill returns agents that offer the given skill.
	DiscoverBySkill(ctx context.Context, skillID string) ([]AgentEndpoint, error)

	// RegisterSkills registers the local node's skills.
	RegisterSkills(ctx context.Context, peerID string, skills []SkillInfo, agentName, agentDesc string) error

	// UnregisterSkills removes the local node's skills.
	UnregisterSkills(ctx context.Context, peerID string, skillIDs []string) error

	// Close releases resources.
	Close() error
}

// RegistryDiscovery implements DiscoveryProvider by querying the Relay's gRPC RegistryService.
type RegistryDiscovery struct {
	conn   *grpc.ClientConn
	client pb.RegistryServiceClient
	logger *slog.Logger
}

// NewRegistryDiscovery creates a discovery provider that queries the given Relay registry address.
func NewRegistryDiscovery(registryAddr string, logger *slog.Logger) (*RegistryDiscovery, error) {
	conn, err := grpc.NewClient(registryAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to registry at %s: %w", registryAddr, err)
	}

	return &RegistryDiscovery{
		conn:   conn,
		client: pb.NewRegistryServiceClient(conn),
		logger: logger,
	}, nil
}

func (d *RegistryDiscovery) DiscoverBySkill(ctx context.Context, skillID string) ([]AgentEndpoint, error) {
	resp, err := d.client.DiscoverBySkill(ctx, &pb.DiscoverBySkillRequest{
		SkillId: skillID,
	})
	if err != nil {
		return nil, fmt.Errorf("discover by skill %q: %w", skillID, err)
	}

	endpoints := make([]AgentEndpoint, 0, len(resp.Agents))
	for _, agent := range resp.Agents {
		pid, err := peer.Decode(agent.PeerId)
		if err != nil {
			d.logger.Warn("invalid peer ID in registry response", "peer_id", agent.PeerId, "error", err)
			continue
		}

		skills := make([]SkillInfo, 0, len(agent.Skills))
		for _, s := range agent.Skills {
			skills = append(skills, SkillInfo{
				SkillID:     s.SkillId,
				Description: s.Description,
				Tags:        s.Tags,
			})
		}

		endpoints = append(endpoints, AgentEndpoint{
			PeerID:      pid,
			Skills:      skills,
			AgentName:   agent.AgentName,
			Description: agent.AgentDescription,
			LastSeen:    agent.RegisteredAt.AsTime(),
		})
	}

	return endpoints, nil
}

func (d *RegistryDiscovery) RegisterSkills(ctx context.Context, peerID string, skills []SkillInfo, agentName, agentDesc string) error {
	pbSkills := make([]*pb.SkillInfo, 0, len(skills))
	for _, s := range skills {
		pbSkills = append(pbSkills, &pb.SkillInfo{
			SkillId:     s.SkillID,
			Description: s.Description,
			Tags:        s.Tags,
		})
	}

	_, err := d.client.RegisterSkills(ctx, &pb.RegisterSkillsRequest{
		PeerId:           peerID,
		Skills:           pbSkills,
		AgentName:        agentName,
		AgentDescription: agentDesc,
	})
	if err != nil {
		return fmt.Errorf("register skills: %w", err)
	}

	d.logger.Info("skills registered with relay",
		"peer_id", peerID,
		"skill_count", len(skills),
	)
	return nil
}

func (d *RegistryDiscovery) UnregisterSkills(ctx context.Context, peerID string, skillIDs []string) error {
	_, err := d.client.UnregisterSkills(ctx, &pb.UnregisterSkillsRequest{
		PeerId:   peerID,
		SkillIds: skillIDs,
	})
	if err != nil {
		return fmt.Errorf("unregister skills: %w", err)
	}
	return nil
}

func (d *RegistryDiscovery) Close() error {
	return d.conn.Close()
}
