package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.LogLevel != "info" {
		t.Fatalf("expected log level 'info', got %q", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Fatalf("expected log format 'json', got %q", cfg.LogFormat)
	}
	if !cfg.EnableRelayClient {
		t.Fatal("expected EnableRelayClient to be true by default")
	}
	if !cfg.EnableHolePunching {
		t.Fatal("expected EnableHolePunching to be true by default")
	}
	if !cfg.EnableMDNS {
		t.Fatal("expected EnableMDNS to be true by default")
	}
	if cfg.OfflineQueueTTL != "24h" {
		t.Fatalf("expected offline queue TTL '24h', got %q", cfg.OfflineQueueTTL)
	}

	// Bridge defaults.
	if cfg.Bridge.Enabled {
		t.Fatal("expected bridge disabled by default")
	}
	if cfg.Bridge.Listen != ":8080" {
		t.Fatalf("expected bridge listen ':8080', got %q", cfg.Bridge.Listen)
	}

	// Anycast defaults.
	if cfg.Anycast.RoutingStrategy != "random" {
		t.Fatalf("expected routing strategy 'random', got %q", cfg.Anycast.RoutingStrategy)
	}
	if cfg.Anycast.CacheTTL != "30s" {
		t.Fatalf("expected cache TTL '30s', got %q", cfg.Anycast.CacheTTL)
	}
	if !cfg.Anycast.AutoRegister {
		t.Fatal("expected auto_register true by default")
	}

	// Metrics defaults.
	if cfg.Metrics.Enabled {
		t.Fatal("expected metrics disabled by default")
	}
	if cfg.Metrics.Listen != ":9090" {
		t.Fatalf("expected metrics listen ':9090', got %q", cfg.Metrics.Listen)
	}
}

func TestLoadConfigFileNotFound(t *testing.T) {
	cfg, err := LoadConfigFile("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	// Should return defaults.
	if cfg.LogLevel != "info" {
		t.Fatalf("expected default log level, got %q", cfg.LogLevel)
	}
}

func TestLoadConfigFileValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
log_level = "debug"
log_format = "text"
enable_mdns = false

[bridge]
enabled = true
listen = ":9999"

[anycast]
routing_strategy = "round_robin"
cache_ttl = "60s"
auto_register = false

[metrics]
enabled = true
listen = ":2112"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}

	if cfg.LogLevel != "debug" {
		t.Fatalf("expected 'debug', got %q", cfg.LogLevel)
	}
	if cfg.LogFormat != "text" {
		t.Fatalf("expected 'text', got %q", cfg.LogFormat)
	}
	if cfg.EnableMDNS {
		t.Fatal("expected EnableMDNS false")
	}
	if !cfg.Bridge.Enabled {
		t.Fatal("expected bridge enabled")
	}
	if cfg.Bridge.Listen != ":9999" {
		t.Fatalf("expected ':9999', got %q", cfg.Bridge.Listen)
	}
	if cfg.Anycast.RoutingStrategy != "round_robin" {
		t.Fatalf("expected 'round_robin', got %q", cfg.Anycast.RoutingStrategy)
	}
	if cfg.Anycast.CacheTTL != "60s" {
		t.Fatalf("expected '60s', got %q", cfg.Anycast.CacheTTL)
	}
	if cfg.Anycast.AutoRegister {
		t.Fatal("expected auto_register false")
	}
	if !cfg.Metrics.Enabled {
		t.Fatal("expected metrics enabled")
	}
	if cfg.Metrics.Listen != ":2112" {
		t.Fatalf("expected ':2112', got %q", cfg.Metrics.Listen)
	}
}

func TestLoadConfigFileInvalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")

	if err := os.WriteFile(path, []byte("not valid toml [[["), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadConfigFile(path)
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func TestApplyEnv(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("AGENTANYCAST_KEY_PATH", "/custom/key")
	t.Setenv("AGENTANYCAST_GRPC_LISTEN", "tcp://localhost:50051")
	t.Setenv("AGENTANYCAST_LOG_LEVEL", "warn")
	t.Setenv("AGENTANYCAST_STORE_PATH", "/custom/data")
	t.Setenv("AGENTANYCAST_BOOTSTRAP_PEERS", "/ip4/1.2.3.4/tcp/4001/p2p/QmA, /ip4/5.6.7.8/tcp/4001/p2p/QmB")
	t.Setenv("AGENTANYCAST_ENABLE_MDNS", "false")

	cfg.ApplyEnv()

	if cfg.KeyPath != "/custom/key" {
		t.Fatalf("expected custom key path, got %q", cfg.KeyPath)
	}
	if cfg.GRPCListen != "tcp://localhost:50051" {
		t.Fatalf("expected custom grpc listen, got %q", cfg.GRPCListen)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("expected warn, got %q", cfg.LogLevel)
	}
	if cfg.StorePath != "/custom/data" {
		t.Fatalf("expected custom store path, got %q", cfg.StorePath)
	}
	if len(cfg.BootstrapPeers) != 2 {
		t.Fatalf("expected 2 bootstrap peers, got %d", len(cfg.BootstrapPeers))
	}
	if cfg.EnableMDNS {
		t.Fatal("expected mDNS disabled via env")
	}
}

func TestApplyEnvNoOverrideWhenEmpty(t *testing.T) {
	cfg := DefaultConfig()
	original := cfg.KeyPath

	// Clear env vars to ensure no override.
	t.Setenv("AGENTANYCAST_KEY_PATH", "")

	cfg.ApplyEnv()

	if cfg.KeyPath != original {
		t.Fatalf("expected key path unchanged, got %q", cfg.KeyPath)
	}
}
