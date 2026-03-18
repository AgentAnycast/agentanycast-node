package anycast

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multihash"
)

const (
	// dhtReprovideInterval controls how often skills are re-announced to the DHT.
	// DHT provider records expire after ~24h; re-providing every 12h ensures availability.
	dhtReprovideInterval = 12 * time.Hour

	// dhtFindProvidersTimeout is the max time to wait for a FindProviders query.
	dhtFindProvidersTimeout = 15 * time.Second

	// dhtMaxProviders is the maximum number of providers to fetch per skill query.
	dhtMaxProviders = 20
)

// skillMetadata is serialized and stored as a DHT value alongside the provider record.
// Since content routing (Provide/FindProviders) only stores PeerIDs, we store
// extra metadata in the DHT's value store keyed by /agentanycast/meta/{peerID}/{skillID}.
type skillMetadata struct {
	PeerID      string            `json:"peer_id"`
	SkillID     string            `json:"skill_id"`
	Description string            `json:"description,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	AgentName   string            `json:"agent_name,omitempty"`
	AgentDesc   string            `json:"agent_desc,omitempty"`
	Timestamp   int64             `json:"ts"`
}

// agentMeta holds per-agent metadata for re-providing to the DHT.
type agentMeta struct {
	agentName string
	agentDesc string
}

// DHTDiscovery implements DiscoveryProvider using Kademlia DHT content routing.
// Skills are published as CID providers; metadata is stored in the DHT value store.
type DHTDiscovery struct {
	dht    *dht.IpfsDHT
	host   host.Host
	logger *slog.Logger

	mu          sync.RWMutex
	localSkills map[string][]SkillInfo // peerID -> skills
	localMeta   map[string]agentMeta   // peerID -> agent metadata

	closeOnce sync.Once
	cancel    context.CancelFunc
	done      chan struct{}
}

// NewDHTDiscovery creates a DHTDiscovery backed by the given IpfsDHT instance.
// The DHT must already be bootstrapped (connected to bootstrap peers).
func NewDHTDiscovery(d *dht.IpfsDHT, h host.Host, logger *slog.Logger) *DHTDiscovery {
	ctx, cancel := context.WithCancel(context.Background())
	dd := &DHTDiscovery{
		dht:         d,
		host:        h,
		logger:      logger,
		localSkills: make(map[string][]SkillInfo),
		localMeta:   make(map[string]agentMeta),
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	go dd.reprovideLoop(ctx)
	return dd
}

// skillCID deterministically converts a skill_id to a CID for content routing.
func skillCID(skillID string) cid.Cid {
	h := sha256.Sum256([]byte("agentanycast/skill/" + skillID))
	mh, _ := multihash.Encode(h[:], multihash.SHA2_256)
	return cid.NewCidV1(cid.Raw, mh)
}

// metaKey returns the DHT value key for storing skill metadata.
func metaKey(peerID, skillID string) string {
	return "/agentanycast/meta/" + peerID + "/" + skillID
}

func (d *DHTDiscovery) DiscoverBySkill(ctx context.Context, skillID string) ([]AgentEndpoint, error) {
	c := skillCID(skillID)

	findCtx, cancel := context.WithTimeout(ctx, dhtFindProvidersTimeout)
	defer cancel()

	provCh := d.dht.FindProvidersAsync(findCtx, c, dhtMaxProviders)

	endpoints := make([]AgentEndpoint, 0)
	seen := make(map[peer.ID]bool)

	for pi := range provCh {
		if pi.ID == d.host.ID() || seen[pi.ID] {
			continue
		}
		seen[pi.ID] = true

		ep := AgentEndpoint{
			PeerID:   pi.ID,
			LastSeen: time.Now(),
		}

		// Try to fetch metadata from DHT value store.
		key := metaKey(pi.ID.String(), skillID)
		if val, err := d.dht.GetValue(findCtx, key); err == nil {
			var meta skillMetadata
			if json.Unmarshal(val, &meta) == nil {
				ep.AgentName = meta.AgentName
				ep.Description = meta.AgentDesc
				ep.Skills = []SkillInfo{{
					SkillID:     meta.SkillID,
					Description: meta.Description,
					Tags:        meta.Tags,
				}}
			}
		}

		// Fallback: if no metadata, still return the endpoint with PeerID.
		if len(ep.Skills) == 0 {
			ep.Skills = []SkillInfo{{SkillID: skillID}}
		}

		endpoints = append(endpoints, ep)
	}

	d.logger.Debug("DHT discovery completed",
		"skill_id", skillID,
		"providers_found", len(endpoints),
	)
	return endpoints, nil
}

func (d *DHTDiscovery) RegisterSkills(ctx context.Context, peerID string, skills []SkillInfo, agentName, agentDesc string) error {
	d.mu.Lock()
	d.localSkills[peerID] = skills
	d.localMeta[peerID] = agentMeta{agentName: agentName, agentDesc: agentDesc}
	d.mu.Unlock()

	for _, skill := range skills {
		c := skillCID(skill.SkillID)

		// Announce as provider for this skill CID.
		if err := d.dht.Provide(ctx, c, true); err != nil {
			return fmt.Errorf("DHT provide skill %q: %w", skill.SkillID, err)
		}

		// Store metadata in DHT value store.
		meta := skillMetadata{
			PeerID:      peerID,
			SkillID:     skill.SkillID,
			Description: skill.Description,
			Tags:        skill.Tags,
			AgentName:   agentName,
			AgentDesc:   agentDesc,
			Timestamp:   time.Now().Unix(),
		}
		val, err := json.Marshal(meta)
		if err != nil {
			continue
		}
		key := metaKey(peerID, skill.SkillID)
		if err := d.dht.PutValue(ctx, key, val); err != nil {
			d.logger.Debug("DHT PutValue failed (non-fatal)", "key", key, "error", err)
		}
	}

	d.logger.Info("skills registered in DHT",
		"peer_id", peerID,
		"skill_count", len(skills),
	)
	return nil
}

func (d *DHTDiscovery) UnregisterSkills(ctx context.Context, peerID string, skillIDs []string) error {
	d.mu.Lock()
	if len(skillIDs) == 0 {
		delete(d.localSkills, peerID)
	} else {
		existing := d.localSkills[peerID]
		removeSet := make(map[string]bool, len(skillIDs))
		for _, id := range skillIDs {
			removeSet[id] = true
		}
		filtered := existing[:0]
		for _, s := range existing {
			if !removeSet[s.SkillID] {
				filtered = append(filtered, s)
			}
		}
		d.localSkills[peerID] = filtered
	}
	d.mu.Unlock()

	// DHT provider records expire naturally; we just stop re-providing.
	d.logger.Info("skills unregistered from DHT", "peer_id", peerID, "skill_ids", skillIDs)
	return nil
}

func (d *DHTDiscovery) Close() error {
	d.closeOnce.Do(func() {
		d.cancel()
	})
	select {
	case <-d.done:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("DHT discovery shutdown timed out")
	}
}

// reprovideLoop re-announces all registered skills periodically.
func (d *DHTDiscovery) reprovideLoop(ctx context.Context) {
	defer close(d.done)
	ticker := time.NewTicker(dhtReprovideInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.mu.RLock()
			for peerID, skills := range d.localSkills {
				meta := d.localMeta[peerID]
				for _, skill := range skills {
					c := skillCID(skill.SkillID)
					provCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
					if err := d.dht.Provide(provCtx, c, true); err != nil {
						d.logger.Debug("DHT re-provide failed",
							"peer_id", peerID,
							"skill_id", skill.SkillID,
							"error", err,
						)
					} else {
						// Re-store metadata alongside the provider record.
						m := skillMetadata{
							PeerID:      peerID,
							SkillID:     skill.SkillID,
							Description: skill.Description,
							Tags:        skill.Tags,
							AgentName:   meta.agentName,
							AgentDesc:   meta.agentDesc,
							Timestamp:   time.Now().Unix(),
						}
						if val, err := json.Marshal(m); err == nil {
							key := metaKey(peerID, skill.SkillID)
							if err := d.dht.PutValue(provCtx, key, val); err != nil {
								d.logger.Debug("DHT re-provide metadata failed",
									"key", key, "error", err,
								)
							}
						}
					}
					cancel()
				}
			}
			d.mu.RUnlock()
		}
	}
}
