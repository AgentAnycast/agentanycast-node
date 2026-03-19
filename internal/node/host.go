// Package node manages the libp2p host lifecycle and peer connections.
package node

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/libp2p/go-libp2p/p2p/muxer/yamux"
	libp2pquic "github.com/libp2p/go-libp2p/p2p/transport/quic"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	libp2pwebtransport "github.com/libp2p/go-libp2p/p2p/transport/webtransport"

	"github.com/agentanycast/agentanycast-node/internal/metrics"
	"github.com/multiformats/go-multiaddr"
)

// HostConfig holds parameters for creating the libp2p host.
type HostConfig struct {
	PrivateKey         libp2pcrypto.PrivKey
	ListenAddrs        []string
	BootstrapPeers     []string
	EnableQUIC         bool
	EnableWebTransport bool
	EnableRelayClient  bool
	EnableHolePunching bool
	EnableMDNS         bool
	// v0.3: DHT discovery
	EnableDHT bool
	DHTMode   string // "auto" (default), "server", "client"
}

// Host wraps a libp2p host with AgentAnycast-specific logic.
type Host struct {
	host.Host
	logger *slog.Logger
	mu     sync.RWMutex
	// peerCards stores the agent cards received from connected peers.
	peerCards map[peer.ID][]byte
	// OnPeerConnected is called when a new peer connects.
	// It can be set by the caller to trigger actions such as offline queue flushing.
	OnPeerConnected func(peer.ID)
	// store is used for persisting agent cards across restarts.
	store cardStore
	// DHT is the Kademlia DHT instance (nil if DHT is disabled).
	DHT *dht.IpfsDHT
}

// cardStore is the subset of store.Store used for card persistence.
type cardStore interface {
	SetMetadataBytes(key string, value []byte) error
	GetMetadataBytes(key string) ([]byte, error)
	ListMetadataByPrefix(prefix string) (map[string][]byte, error)
}

// NewHost creates and starts a libp2p host with the given configuration.
func NewHost(ctx context.Context, cfg HostConfig, logger *slog.Logger) (*Host, error) {
	listenAddrs := make([]multiaddr.Multiaddr, 0, len(cfg.ListenAddrs))
	for _, addr := range cfg.ListenAddrs {
		ma, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			return nil, fmt.Errorf("parse listen addr %q: %w", addr, err)
		}
		listenAddrs = append(listenAddrs, ma)
	}

	opts := []libp2p.Option{
		libp2p.Identity(cfg.PrivateKey),
		libp2p.ListenAddrs(listenAddrs...),
		libp2p.Security(noise.ID, noise.New),
		libp2p.Muxer(yamux.ID, yamux.DefaultTransport),
		libp2p.Transport(tcp.NewTCPTransport),
		libp2p.NATPortMap(),
		libp2p.EnableAutoNATv2(),
	}

	if cfg.EnableQUIC {
		opts = append(opts, libp2p.Transport(libp2pquic.NewTransport))
		logger.Info("QUIC transport enabled")
	}

	if cfg.EnableWebTransport {
		opts = append(opts, libp2p.Transport(libp2pwebtransport.New))
		// Add WebTransport listener address.
		wtMA, err := multiaddr.NewMultiaddr("/ip4/0.0.0.0/udp/0/quic-v1/webtransport")
		if err == nil {
			listenAddrs = append(listenAddrs, wtMA)
			// Override the listen addrs option to include the WebTransport address.
			opts[1] = libp2p.ListenAddrs(listenAddrs...)
		} else {
			logger.Warn("failed to parse WebTransport listen address", "error", err)
		}
		logger.Info("WebTransport enabled")
	}

	if cfg.EnableRelayClient {
		opts = append(opts, libp2p.EnableRelay())
	}

	if cfg.EnableHolePunching {
		opts = append(opts, libp2p.EnableHolePunching())
	}

	// Enable DHT-based content routing if configured.
	var kadDHT *dht.IpfsDHT
	if cfg.EnableDHT {
		opts = append(opts, libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			dhtMode := dht.ModeAutoServer
			switch cfg.DHTMode {
			case "server":
				dhtMode = dht.ModeServer
			case "client":
				dhtMode = dht.ModeClient
			}
			var err error
			kadDHT, err = dht.New(ctx, h, dht.Mode(dhtMode))
			return kadDHT, err
		}))
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create libp2p host: %w", err)
	}

	bh := &Host{
		Host:      h,
		logger:    logger,
		peerCards: make(map[peer.ID][]byte),
		DHT:       kadDHT,
	}

	// Register connection notification handler.
	h.Network().Notify(&network.NotifyBundle{
		ConnectedF: func(n network.Network, c network.Conn) {
			remotePeer := c.RemotePeer()
			transport := metrics.ClassifyTransport(c.RemoteMultiaddr())
			bh.logger.Info("peer connected",
				"peer", remotePeer.String(),
				"addr", c.RemoteMultiaddr().String(),
				"transport", transport,
			)
			metrics.ConnectionsByTransport.WithLabelValues(transport).Inc()
			if bh.OnPeerConnected != nil {
				go bh.OnPeerConnected(remotePeer)
			}
		},
		DisconnectedF: func(n network.Network, c network.Conn) {
			bh.logger.Info("peer disconnected",
				"peer", c.RemotePeer().String(),
			)
		},
	})

	// Connect to bootstrap peers.
	for _, addr := range cfg.BootstrapPeers {
		go bh.connectBootstrap(ctx, addr)
	}

	// Start mDNS discovery if enabled.
	if cfg.EnableMDNS {
		mdnsSvc := mdns.NewMdnsService(h, "", &mdnsNotifee{host: bh, ctx: ctx})
		if err := mdnsSvc.Start(); err != nil {
			logger.Warn("failed to start mDNS discovery", "error", err)
		} else {
			logger.Info("mDNS discovery started")
		}
	}

	return bh, nil
}

// mdnsNotifee handles peers discovered via mDNS.
type mdnsNotifee struct {
	host *Host
	ctx  context.Context
}

// HandlePeerFound connects to a peer discovered via mDNS.
func (n *mdnsNotifee) HandlePeerFound(pi peer.AddrInfo) {
	if pi.ID == n.host.ID() {
		return
	}
	n.host.logger.Info("mDNS: discovered peer", "peer", pi.ID)
	if err := n.host.Connect(n.ctx, pi); err != nil {
		n.host.logger.Debug("mDNS: failed to connect to discovered peer", "peer", pi.ID, "error", err)
	}
}

func (h *Host) connectBootstrap(ctx context.Context, addrStr string) {
	ma, err := multiaddr.NewMultiaddr(addrStr)
	if err != nil {
		h.logger.Warn("invalid bootstrap addr", "addr", addrStr, "error", err)
		return
	}

	pi, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		h.logger.Warn("invalid bootstrap peer info", "addr", addrStr, "error", err)
		return
	}

	if err := h.Connect(ctx, *pi); err != nil {
		h.logger.Warn("failed to connect to bootstrap peer", "peer", pi.ID, "error", err)
		return
	}

	h.logger.Info("connected to bootstrap peer", "peer", pi.ID)
}

// SetStore attaches a persistent store for card persistence.
func (h *Host) SetStore(s cardStore) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.store = s
}

// SetPeerCard stores a peer's agent card data in memory and persists it to the store.
func (h *Host) SetPeerCard(pid peer.ID, data []byte) {
	h.mu.Lock()
	h.peerCards[pid] = data
	st := h.store
	h.mu.Unlock()

	if st != nil {
		if err := st.SetMetadataBytes("card:"+pid.String(), data); err != nil {
			h.logger.Warn("failed to persist peer card", "peer", pid, "error", err)
		}
	}
}

// SetSelfCard persists the local node's agent card to the store.
func (h *Host) SetSelfCard(data []byte) {
	h.mu.RLock()
	st := h.store
	h.mu.RUnlock()

	if st != nil {
		if err := st.SetMetadataBytes("card:self", data); err != nil {
			h.logger.Warn("failed to persist self card", "error", err)
		}
	}
}

// GetSelfCard loads the local node's agent card from the store.
func (h *Host) GetSelfCard() ([]byte, error) {
	h.mu.RLock()
	st := h.store
	h.mu.RUnlock()

	if st == nil {
		return nil, fmt.Errorf("no store configured")
	}
	return st.GetMetadataBytes("card:self")
}

// RecoverCards loads persisted peer cards from the store into memory.
func (h *Host) RecoverCards() error {
	h.mu.RLock()
	st := h.store
	h.mu.RUnlock()

	if st == nil {
		return nil
	}

	entries, err := st.ListMetadataByPrefix("card:")
	if err != nil {
		return fmt.Errorf("list stored cards: %w", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	recovered := 0
	for key, data := range entries {
		// Skip the self card — it's handled separately via gRPC server.
		suffix := key[len("card:"):]
		if suffix == "self" {
			continue
		}
		pid, err := peer.Decode(suffix)
		if err != nil {
			h.logger.Warn("invalid peer ID in stored card", "key", key, "error", err)
			continue
		}
		h.peerCards[pid] = data
		recovered++
	}

	if recovered > 0 {
		h.logger.Info("recovered peer cards from store", "count", recovered)
	}
	return nil
}

// GetPeerCard retrieves a peer's agent card data.
func (h *Host) GetPeerCard(pid peer.ID) ([]byte, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	data, ok := h.peerCards[pid]
	return data, ok
}

// ConnectedPeers returns the list of currently connected peer IDs.
func (h *Host) ConnectedPeers() []peer.ID {
	return h.Network().Peers()
}
