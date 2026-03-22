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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"encoding/json"

	"github.com/agentanycast/agentanycast-node/internal/a2a"
	a2aadapter "github.com/agentanycast/agentanycast-node/internal/adapter/a2a"
	anpadapter "github.com/agentanycast/agentanycast-node/internal/adapter/anp"
	httpadapter "github.com/agentanycast/agentanycast-node/internal/adapter/http"
	libp2padapter "github.com/agentanycast/agentanycast-node/internal/adapter/libp2p"
	"github.com/agentanycast/agentanycast-node/internal/anp"
	"github.com/agentanycast/agentanycast-node/internal/anycast"
	"github.com/agentanycast/agentanycast-node/internal/bridge"
	"github.com/agentanycast/agentanycast-node/internal/config"
	"github.com/agentanycast/agentanycast-node/internal/core"
	"github.com/agentanycast/agentanycast-node/internal/crypto"
	agentmcp "github.com/agentanycast/agentanycast-node/internal/mcp"
	"github.com/agentanycast/agentanycast-node/internal/mcpproxy"
	"github.com/agentanycast/agentanycast-node/internal/metrics"
	"github.com/agentanycast/agentanycast-node/internal/node"
	"github.com/agentanycast/agentanycast-node/internal/store"
	"github.com/agentanycast/agentanycast-node/internal/telemetry"
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
		flagBridgeListen   = flag.String("bridge-listen", "", "HTTP Bridge listen address (e.g., :8080)")
		flagEnableWebTransport = flag.Bool("enable-webtransport", false, "enable WebTransport (QUIC-based, browser-compatible)")
		flagANPListen          = flag.String("anp-listen", "", "ANP bridge listen address (e.g., :8090)")
		flagMCP                = flag.Bool("mcp", false, "run as MCP server over stdio (for Claude Desktop, Cursor, etc.)")
		flagMCPListen          = flag.String("mcp-listen", "", "MCP Streamable HTTP listen address (e.g., :3000)")
		flagMCPProxy           = flag.String("mcp-proxy", "", "wrap an MCP Server command as P2P agent (e.g., 'uvx mcp-server-filesystem /tmp')")
		flagOTLPEndpoint       = flag.String("otlp-endpoint", "", "OTLP gRPC endpoint for tracing (e.g., localhost:4317)")
		flagVersion            = flag.Bool("version", false, "print version and exit")
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
	if *flagBridgeListen != "" {
		cfg.Bridge.Enabled = true
		cfg.Bridge.Listen = *flagBridgeListen
	}
	if *flagEnableWebTransport {
		cfg.EnableWebTransport = true
	}
	if *flagANPListen != "" {
		cfg.ANP.Enabled = true
		cfg.ANP.Listen = *flagANPListen
	}
	if *flagOTLPEndpoint != "" {
		cfg.Telemetry.Enabled = true
		cfg.Telemetry.OTLPEndpoint = *flagOTLPEndpoint
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

	// ── OpenTelemetry Tracing ────────────────────────────────
	otelShutdown, err := telemetry.Setup(context.Background(), telemetry.Config{
		Enabled:      cfg.Telemetry.Enabled,
		OTLPEndpoint: cfg.Telemetry.OTLPEndpoint,
		SampleRate:   cfg.Telemetry.SampleRate,
		Insecure:     cfg.Telemetry.Insecure,
		ServiceName:  "agentanycast-node",
		Version:      version,
		Logger:       logger,
	})
	if err != nil {
		logger.Error("failed to initialize telemetry", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		otelShutdown(shutdownCtx) //nolint:errcheck
	}()

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
		EnableQUIC:         cfg.EnableQUIC,
		EnableWebTransport: cfg.EnableWebTransport,
		EnableRelayClient:  cfg.EnableRelayClient,
		EnableHolePunching: cfg.EnableHolePunching,
		EnableMDNS:         cfg.EnableMDNS,
		EnableDHT:          cfg.Anycast.EnableDHT,
		DHTMode:            cfg.Anycast.DHTMode,
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

	offlineQueueTTL, err := time.ParseDuration(cfg.OfflineQueueTTL)
	if err != nil {
		logger.Warn("invalid offline_queue_ttl, using default 24h", "value", cfg.OfflineQueueTTL, "error", err)
		offlineQueueTTL = 24 * time.Hour
	}
	offlineQueue := a2a.NewOfflineQueue(st, logger, offlineQueueTTL)

	// v0.2: Stream Manager
	streamMgr := a2a.NewStreamManager(logger)

	router := a2a.NewRouter(engine, logger, func(sendCtx context.Context, pid peer.ID, data []byte) error {
		if err := h.SendA2AMessage(ctx, pid, data, logger); err != nil {
			// Peer unreachable — queue for later delivery.
			envelopeID := a2a.ExtractEnvelopeID(data)
			if qErr := offlineQueue.Enqueue(pid.String(), envelopeID, data); qErr != nil {
				logger.Warn("failed to enqueue offline message", "error", qErr)
			}
			return err
		}
		return nil
	}, h.GetPeerCard, func(target peer.ID, envelopeID string, data []byte) {
		// ACK retries exhausted — fall back to offline queue.
		if qErr := offlineQueue.Enqueue(target.String(), envelopeID, data); qErr != nil {
			logger.Warn("failed to enqueue after max retries", "envelope_id", envelopeID, "error", qErr)
		}
	})
	defer router.Close()

	// ── Anycast Router ──────────────────────────────────────
	var anycastRtr *anycast.Router
	{
		var providers []anycast.DiscoveryProvider

		// v0.5: Multi-relay registry discovery (federation-aware).
		registryAddrs := cfg.Anycast.EffectiveRegistryAddrs()
		if len(registryAddrs) > 1 {
			multiDiscovery, err := anycast.NewMultiRegistryDiscovery(registryAddrs, logger)
			if err != nil {
				logger.Warn("failed to create multi-registry discovery", "error", err)
			} else {
				providers = append(providers, multiDiscovery)
				logger.Info("multi-registry discovery enabled", "addrs", registryAddrs)
			}
		} else if len(registryAddrs) == 1 {
			// v0.2: Single relay registry discovery (backward compat).
			regDiscovery, err := anycast.NewRegistryDiscovery(registryAddrs[0], logger)
			if err != nil {
				logger.Warn("failed to connect to registry", "error", err)
			} else {
				providers = append(providers, regDiscovery)
				logger.Info("registry discovery enabled", "addr", registryAddrs[0])
			}
		}

		// v0.3: DHT-based discovery.
		if cfg.Anycast.EnableDHT && h.DHT != nil {
			dhtDiscovery := anycast.NewDHTDiscovery(h.DHT, h, logger)
			providers = append(providers, dhtDiscovery)
			logger.Info("DHT discovery enabled")
		}

		if len(providers) > 0 {
			var discovery anycast.DiscoveryProvider
			if len(providers) == 1 {
				discovery = providers[0]
			} else {
				discovery = anycast.NewCompositeDiscovery(logger, providers...)
			}

			cacheTTL, _ := time.ParseDuration(cfg.Anycast.CacheTTL)
			if cacheTTL == 0 {
				cacheTTL = 30 * time.Second
			}
			anycastRtr = anycast.NewRouter(anycast.RouterConfig{
				Discovery: discovery,
				Strategy:  &anycast.RandomStrategy{},
				CacheTTL:  cacheTTL,
				Logger:    logger,
			})
			defer anycastRtr.Close()
			logger.Info("anycast router initialized", "providers", len(providers))
		}
	}

	// ── v0.2: HTTP Bridge (outbound client, always available) ─
	outboundBridge := bridge.NewOutboundClient(logger)

	// ── gRPC Server ─────────────────────────────────────────
	grpcSrv := grpcserver.New(grpcserver.Config{
		Host:           h,
		Engine:         engine,
		Router:         router,
		Store:          st,
		Logger:         logger,
		AnycastRouter:  anycastRtr,
		OutboundBridge: outboundBridge,
		StreamManager:  streamMgr,
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

	// ── v0.2: HTTP Bridge Server (optional) ─────────────────
	if cfg.Bridge.Enabled {
		inbound := bridge.NewInboundHandler(grpcSrv, logger)
		baseURL := bridge.BaseURL(cfg.Bridge.Listen, cfg.Bridge.TLSCert != "")
		cardEndpoint := bridge.NewCardEndpoint(grpcSrv.GetLocalCard, baseURL, logger)

		bridgeSrv := bridge.NewServer(bridge.Config{
			Listen:      cfg.Bridge.Listen,
			TLSCert:     cfg.Bridge.TLSCert,
			TLSKey:      cfg.Bridge.TLSKey,
			CORSOrigins: cfg.Bridge.CORSOrigins,
		}, inbound, cardEndpoint, logger)

		go func() {
			if err := bridgeSrv.ListenAndServe(); err != nil {
				logger.Error("HTTP bridge server error", "error", err)
			}
		}()
		defer bridgeSrv.Shutdown(context.Background())

		logger.Info("HTTP bridge enabled", "addr", cfg.Bridge.Listen)
	}

	// ── v0.5: ANP Bridge Server (optional) ──────────────────
	if cfg.ANP.Enabled {
		anpTaskChan := make(chan anp.IncomingANPTask, 64)
		anpSrv := anp.NewServer(cfg.ANP.Listen, grpcSrv.GetLocalCard(), anpTaskChan, logger)

		go func() {
			if err := anpSrv.Start(); err != nil {
				logger.Error("ANP bridge server error", "error", err)
			}
		}()
		defer func() {
			anpSrv.Stop(context.Background())
			close(anpTaskChan) // Unblock the task consumer goroutine.
		}()

		// Process incoming ANP tasks — route through the A2A engine.
		go func() {
			for task := range anpTaskChan {
				go func(t anp.IncomingANPTask) {
					rpcReq := anp.TaskToJSONRPCRequest(t.Method, "", "")
					_ = rpcReq // ANP task processing wired to A2A engine in future iterations.
					t.ResultCh <- &anp.JSONRPCResponse{
						JSONRPC: "2.0",
						Error:   &anp.JSONRPCError{Code: -32601, Message: "method not implemented via ANP bridge yet"},
						ID:      t.Method,
					}
				}(task)
			}
		}()

		logger.Info("ANP bridge enabled", "addr", cfg.ANP.Listen)
	}

	// ── v0.2: Metrics Server (optional) ─────────────────────
	if cfg.Metrics.Enabled {
		metricsSrv := metrics.NewServer(cfg.Metrics.Listen, logger,
			metrics.WithHealthChecker(func() bool {
				return len(h.Network().Peers()) > 0 || len(cfg.BootstrapPeers) == 0
			}),
		)
		go func() {
			if err := metricsSrv.ListenAndServe(); err != nil {
				logger.Error("metrics server error", "error", err)
			}
		}()
		defer metricsSrv.Shutdown(context.Background())
	}

	// ── v0.4: MCP Server ────────────────────────────────────
	mcpSrv := agentmcp.New(agentmcp.Config{
		Host:           h,
		Engine:         engine,
		Router:         router,
		Store:          st,
		Logger:         logger,
		AnycastRouter:  anycastRtr,
		OutboundBridge: outboundBridge,
		StreamManager:  streamMgr,
		CardProvider:   grpcSrv.GetLocalCardBytes,
	})

	// ── v0.7: Connection Core (pluggable adapter architecture) ───
	connectionCore := core.New(core.Config{Logger: logger})

	// Register protocol adapters — wrap existing engines.
	a2aAdapter := a2aadapter.New()
	connectionCore.RegisterProtocol(a2aAdapter)
	connectionCore.RegisterProtocol(anpadapter.New())

	// Register transport adapters — wrap existing networking layers.
	connectionCore.RegisterTransport(libp2padapter.New(h, a2aAdapter, logger))
	if outboundBridge != nil {
		connectionCore.RegisterTransport(httpadapter.New(nil)) // HTTP bridge uses existing outbound client
	}

	_ = connectionCore // Connection Core available for new features (MCP Proxy, etc.)

	// ── v0.7 Phase B: MCP Remote Proxy ───────────────────────
	var mcpProxyInstance *mcpproxy.Proxy
	if *flagMCPProxy != "" {
		proxyArgs := strings.Fields(*flagMCPProxy)
		if len(proxyArgs) == 0 {
			logger.Error("--mcp-proxy requires a command")
			os.Exit(1)
		}

		var err error
		mcpProxyInstance, err = mcpproxy.New(ctx, mcpproxy.Config{
			Command:   proxyArgs[0],
			Args:      proxyArgs[1:],
			AgentName: filepath.Base(proxyArgs[0]),
			Logger:    logger,
		})
		if err != nil {
			logger.Error("failed to start MCP proxy", "error", err)
			os.Exit(1)
		}
		defer mcpProxyInstance.Close()

		// Register each discovered tool as a skill on the agent card.
		for _, skill := range mcpProxyInstance.Skills() {
			grpcSrv.RegisterProxySkill(skill)
		}

		// Register the proxy task handler — incoming tasks targeting proxy skills
		// are automatically forwarded to the MCP subprocess.
		skillIDs := make([]string, 0, len(mcpProxyInstance.Tools()))
		for _, t := range mcpProxyInstance.Tools() {
			skillIDs = append(skillIDs, t.Name)
		}
		grpcSrv.RegisterMCPProxyHandler(func(handlerCtx context.Context, skillID string, input json.RawMessage) (json.RawMessage, error) {
			return mcpProxyInstance.HandleTask(handlerCtx, skillID, input)
		}, skillIDs)

		logger.Info("MCP proxy started", "command", *flagMCPProxy, "tools", len(mcpProxyInstance.Tools()))
	}

	// ── Print Startup Info ───────────────────────────────────
	logger.Info("agentanycastd started",
		"version", version,
		"peer_id", h.ID().String(),
		"addresses", h.Addrs(),
		"grpc_listen", cfg.GRPCListen,
		"bridge_enabled", cfg.Bridge.Enabled,
		"anp_enabled", cfg.ANP.Enabled,
		"mcp_stdio", *flagMCP,
		"mcp_proxy", *flagMCPProxy != "",
		"metrics_enabled", cfg.Metrics.Enabled,
	)

	// Print to stdout for SDK to parse (suppressed in MCP stdio mode
	// because stdout is reserved for JSON-RPC communication).
	if !*flagMCP {
		fmt.Printf("PEER_ID=%s\n", h.ID().String())
		for _, addr := range h.Addrs() {
			fmt.Printf("LISTEN_ADDR=%s/p2p/%s\n", addr, h.ID())
		}
	}

	// ── Start gRPC Server ───────────────────────────────────
	grpcErrCh := make(chan error, 1)
	go func() {
		grpcErrCh <- grpcSrv.ListenAndServe(ctx, cfg.GRPCListen)
	}()

	// ── MCP stdio mode: block on stdin/stdout ────────────────
	if *flagMCP {
		if err := mcpSrv.ServeStdio(ctx); err != nil {
			logger.Error("MCP stdio server error", "error", err)
		}
		cancel()
		return
	}

	// ── MCP HTTP mode (non-blocking, like bridge) ────────────
	if *flagMCPListen != "" || cfg.MCP.Enabled {
		listen := cfg.MCP.Listen
		if *flagMCPListen != "" {
			listen = *flagMCPListen
		}
		mcpHTTPSrv, err := mcpSrv.ServeHTTP(listen)
		if err != nil {
			logger.Error("MCP HTTP server start failed", "error", err)
		} else {
			defer mcpHTTPSrv.Shutdown(context.Background())
			logger.Info("MCP Streamable HTTP enabled", "addr", listen)
		}
	}

	// ── v0.2: Auto-register Skills with Relay Registry ──────
	if anycastRtr != nil && cfg.Anycast.AutoRegister {
		go func() {
			// Wait a bit for bootstrap connections to establish.
			time.Sleep(3 * time.Second)
			card := grpcSrv.GetLocalCard()
			if card != nil && len(card.Skills) > 0 {
				skills := make([]anycast.SkillInfo, 0, len(card.Skills))
				for _, s := range card.Skills {
					skills = append(skills, anycast.SkillInfo{
						SkillID:     s.Id,
						Description: s.Description,
					})
				}
				if err := anycastRtr.RegisterSkills(ctx, h.ID().String(), skills, card.Name, card.Description); err != nil {
					logger.Warn("auto skill registration failed", "error", err)
				} else {
					logger.Info("skills auto-registered with relay", "count", len(skills))
				}
			}
		}()
	}

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

	// v0.2: Unregister skills on shutdown.
	if anycastRtr != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := anycastRtr.UnregisterSkills(shutdownCtx, h.ID().String(), nil); err != nil {
			logger.Warn("skill unregistration failed", "error", err)
		}
	}

	cancel()
}
