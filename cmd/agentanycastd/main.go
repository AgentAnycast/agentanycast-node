// agentanycastd is the AgentAnycast daemon — a P2P runtime for the A2A protocol.
//
// It runs as a long-lived background process, managing libp2p networking,
// Noise E2E encryption, NAT traversal, and the A2A protocol engine.
// The Python SDK communicates with it via gRPC over a Unix Domain Socket.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/agentanycast/agentanycast-node/internal/a2a"
	"github.com/agentanycast/agentanycast-node/internal/config"
	"github.com/agentanycast/agentanycast-node/internal/crypto"
	"github.com/agentanycast/agentanycast-node/internal/node"
	"github.com/agentanycast/agentanycast-node/internal/store"
	"github.com/agentanycast/agentanycast-node/pkg/grpcserver"
)

var version = "dev"

func main() {
	var (
		flagConfigPath     = flag.String("config", config.DefaultConfigPath(), "path to TOML config file")
		flagKeyPath        = flag.String("key", "", "path to Ed25519 private key file")
		flagGRPCListen     = flag.String("grpc-listen", "", "gRPC listen address (unix:// or tcp://)")
		flagLogLevel       = flag.String("log-level", "", "log level (debug/info/warn/error)")
		flagBootstrapPeers = flag.String("bootstrap-peers", "", "comma-separated bootstrap peer multiaddrs")
		flagVersion        = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *flagVersion {
		fmt.Println("agentanycastd", version)
		os.Exit(0)
	}

	// ── Configuration (priority: CLI flags > env vars > config file > defaults) ──
	cfg, err := config.LoadConfigFile(*flagConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config file: %v\n", err)
		os.Exit(1)
	}
	cfg.ApplyEnv()

	if *flagKeyPath != "" {
		cfg.KeyPath = *flagKeyPath
	}
	if *flagGRPCListen != "" {
		cfg.GRPCListen = *flagGRPCListen
	}
	if *flagLogLevel != "" {
		cfg.LogLevel = *flagLogLevel
	}
	if *flagBootstrapPeers != "" {
		var peers []string
		for _, p := range strings.Split(*flagBootstrapPeers, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				peers = append(peers, p)
			}
		}
		if len(peers) > 0 {
			cfg.BootstrapPeers = peers
		}
	}

	// ── Logger ───────────────────────────────────────────────
	logLevel := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}

	var handler slog.Handler
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)

	// ── Key Management ───────────────────────────────────────
	privKey, err := crypto.LoadOrGenerateKey(cfg.KeyPath)
	if err != nil {
		logger.Error("failed to load/generate key", "error", err)
		os.Exit(1)
	}

	// ── Store ────────────────────────────────────────────────
	st, err := store.Open(cfg.StorePath)
	if err != nil {
		logger.Error("failed to open store", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	// ── libp2p Host ──────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h, err := node.NewHost(ctx, node.HostConfig{
		PrivateKey:         privKey,
		ListenAddrs:        cfg.ListenAddrs,
		BootstrapPeers:     cfg.BootstrapPeers,
		EnableRelayClient:  cfg.EnableRelayClient,
		EnableHolePunching: cfg.EnableHolePunching,
		EnableMDNS:         cfg.EnableMDNS,
	}, logger)
	if err != nil {
		logger.Error("failed to create host", "error", err)
		os.Exit(1)
	}
	defer h.Close()

	// Attach store for card persistence and recover previously stored cards.
	h.SetStore(st)
	if err := h.RecoverCards(); err != nil {
		logger.Warn("card recovery failed", "error", err)
	}

	// ── A2A Engine + Router + Offline Queue ──────────────────
	engine := a2a.NewEngine(logger)
	engine.SetStore(st)

	// Recover any persisted tasks from a prior crash.
	if err := engine.RecoverTasks(); err != nil {
		logger.Warn("task recovery failed", "error", err)
	}

	offlineQueue := a2a.NewOfflineQueue(st, logger)

	router := a2a.NewRouter(engine, logger, func(sendCtx interface{}, pid peer.ID, data []byte) error {
		if err := h.SendA2AMessage(ctx, pid, data, logger); err != nil {
			// Peer unreachable — queue for later delivery
			envelopeID := fmt.Sprintf("%d", time.Now().UnixNano())
			if qErr := offlineQueue.Enqueue(pid.String(), envelopeID, data); qErr != nil {
				logger.Warn("failed to enqueue offline message", "error", qErr)
			}
			return err
		}
		return nil
	})

	// ── gRPC Server ─────────────────────────────────────────
	grpcSrv := grpcserver.New(grpcserver.Config{
		Host:   h,
		Engine: engine,
		Router: router,
		Store:  st,
		Logger: logger,
	})

	// Register Card exchange protocol — returns serialized card from gRPC server state.
	h.RegisterCardProtocol(func() []byte {
		return grpcSrv.GetLocalCardBytes()
	})

	// Register A2A message protocol — routes through the router.
	h.RegisterA2AProtocol(func(remotePeer peer.ID, data []byte) {
		router.HandleMessage(remotePeer, data)
	})

	// Wire peer-connected callback: auto card exchange + offline queue flush.
	h.OnPeerConnected = func(pid peer.ID) {
		// Automatically exchange agent cards with newly connected peers.
		if card := grpcSrv.GetLocalCardBytes(); card != nil {
			if err := h.ExchangeCard(ctx, pid, card); err != nil {
				logger.Debug("auto card exchange failed", "peer", pid, "error", err)
			} else {
				logger.Info("auto card exchange completed", "peer", pid)
			}
		}
		// Flush any queued offline messages for this peer.
		if err := offlineQueue.FlushQueue(pid.String(), func(data []byte) error {
			return h.SendA2AMessage(ctx, pid, data, logger)
		}); err != nil {
			logger.Warn("offline queue flush failed", "peer", pid, "error", err)
		}
	}

	// ── Print Startup Info ───────────────────────────────────
	logger.Info("agentanycastd started",
		"version", version,
		"peer_id", h.ID().String(),
		"addresses", h.Addrs(),
		"grpc_listen", cfg.GRPCListen,
	)

	// Print to stdout for SDK to parse.
	fmt.Printf("PEER_ID=%s\n", h.ID().String())
	for _, addr := range h.Addrs() {
		fmt.Printf("LISTEN_ADDR=%s/p2p/%s\n", addr, h.ID())
	}

	// ── Start gRPC Server ───────────────────────────────────
	grpcErrCh := make(chan error, 1)
	go func() {
		grpcErrCh <- grpcSrv.ListenAndServe(ctx, cfg.GRPCListen)
	}()

	// ── Wait for Shutdown ────────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("shutting down", "signal", sig)
	case err := <-grpcErrCh:
		if err != nil {
			logger.Error("gRPC server error", "error", err)
		}
	}

	cancel()
}
