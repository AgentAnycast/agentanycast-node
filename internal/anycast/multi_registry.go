package anycast

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	pb "github.com/agentanycast/agentanycast-proto/gen/go/agentanycast/v1"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// MultiRegistryDiscovery queries multiple relay registry addresses in parallel
// and merges results. This enables multi-relay federation where nodes can
// discover agents across a federation of relay servers.
type MultiRegistryDiscovery struct {
	providers []DiscoveryProvider
	logger    *slog.Logger
}

// NewMultiRegistryDiscovery creates a discovery provider that queries multiple
// relay gRPC addresses in parallel and deduplicates results.
func NewMultiRegistryDiscovery(addrs []string, logger *slog.Logger) (*MultiRegistryDiscovery, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("at least one registry address is required")
	}

	providers := make([]DiscoveryProvider, 0, len(addrs))
	for _, addr := range addrs {
		conn, err := grpc.NewClient(addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			logger.Warn("failed to connect to registry",
				"addr", addr,
				"error", err,
			)
			continue
		}

		providers = append(providers, &registryClient{
			conn:   conn,
			client: pb.NewRegistryServiceClient(conn),
			addr:   addr,
			logger: logger,
		})
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("failed to connect to any registry address")
	}

	return &MultiRegistryDiscovery{
		providers: providers,
		logger:    logger,
	}, nil
}

func (m *MultiRegistryDiscovery) DiscoverBySkill(ctx context.Context, skillID string) ([]AgentEndpoint, error) {
	type result struct {
		endpoints []AgentEndpoint
		err       error
	}

	results := make([]result, len(m.providers))
	var wg sync.WaitGroup
	for i, p := range m.providers {
		wg.Add(1)
		go func(idx int, prov DiscoveryProvider) {
			defer wg.Done()
			eps, err := prov.DiscoverBySkill(ctx, skillID)
			results[idx] = result{endpoints: eps, err: err}
		}(i, p)
	}
	wg.Wait()

	// Merge and deduplicate by PeerID.
	seen := make(map[string]bool)
	var merged []AgentEndpoint
	var lastErr error

	for _, r := range results {
		if r.err != nil {
			lastErr = r.err
			continue
		}
		for _, ep := range r.endpoints {
			key := ep.PeerID.String()
			if !seen[key] {
				seen[key] = true
				merged = append(merged, ep)
			}
		}
	}

	if len(merged) > 0 {
		return merged, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return merged, nil
}

func (m *MultiRegistryDiscovery) RegisterSkills(ctx context.Context, peerID string, skills []SkillInfo, agentName, agentDesc string) error {
	// Register with all registries; succeed if at least one accepts.
	var firstErr error
	anyOK := false
	for _, p := range m.providers {
		if err := p.RegisterSkills(ctx, peerID, skills, agentName, agentDesc); err != nil {
			m.logger.Warn("multi-registry: registration failed", "error", err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			anyOK = true
		}
	}
	if anyOK {
		return nil
	}
	return firstErr
}

func (m *MultiRegistryDiscovery) UnregisterSkills(ctx context.Context, peerID string, skillIDs []string) error {
	var firstErr error
	anyOK := false
	for _, p := range m.providers {
		if err := p.UnregisterSkills(ctx, peerID, skillIDs); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			anyOK = true
		}
	}
	if anyOK {
		return nil
	}
	return firstErr
}

func (m *MultiRegistryDiscovery) Close() error {
	var firstErr error
	for _, p := range m.providers {
		if err := p.Close(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// registryClient is a DiscoveryProvider backed by a single gRPC registry connection.
type registryClient struct {
	conn   *grpc.ClientConn
	client pb.RegistryServiceClient
	addr   string
	logger *slog.Logger
}

func (c *registryClient) DiscoverBySkill(ctx context.Context, skillID string) ([]AgentEndpoint, error) {
	resp, err := c.client.DiscoverBySkill(ctx, &pb.DiscoverBySkillRequest{
		SkillId:   skillID,
		Federated: true, // request federated results from each relay
	})
	if err != nil {
		return nil, fmt.Errorf("discover by skill %q from %s: %w", skillID, c.addr, err)
	}

	endpoints := make([]AgentEndpoint, 0, len(resp.Agents))
	for _, agent := range resp.Agents {
		pid, err := peer.Decode(agent.PeerId)
		if err != nil {
			c.logger.Warn("invalid peer ID in registry response",
				"addr", c.addr,
				"peer_id", agent.PeerId,
				"error", err,
			)
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

func (c *registryClient) RegisterSkills(ctx context.Context, peerID string, skills []SkillInfo, agentName, agentDesc string) error {
	pbSkills := make([]*pb.SkillInfo, 0, len(skills))
	for _, s := range skills {
		pbSkills = append(pbSkills, &pb.SkillInfo{
			SkillId:     s.SkillID,
			Description: s.Description,
			Tags:        s.Tags,
		})
	}

	_, err := c.client.RegisterSkills(ctx, &pb.RegisterSkillsRequest{
		PeerId:           peerID,
		Skills:           pbSkills,
		AgentName:        agentName,
		AgentDescription: agentDesc,
	})
	if err != nil {
		return fmt.Errorf("register skills at %s: %w", c.addr, err)
	}

	c.logger.Info("skills registered with relay",
		"addr", c.addr,
		"peer_id", peerID,
		"skill_count", len(skills),
	)
	return nil
}

func (c *registryClient) UnregisterSkills(ctx context.Context, peerID string, skillIDs []string) error {
	_, err := c.client.UnregisterSkills(ctx, &pb.UnregisterSkillsRequest{
		PeerId:   peerID,
		SkillIds: skillIDs,
	})
	if err != nil {
		return fmt.Errorf("unregister skills at %s: %w", c.addr, err)
	}
	return nil
}

func (c *registryClient) Close() error {
	return c.conn.Close()
}
