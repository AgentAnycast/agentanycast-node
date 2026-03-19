// Package config handles daemon configuration from file, env, and flags.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultConfigPath returns the default config file location (~/.agentanycast/config.toml).
func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentanycast", "config.toml")
}

// Config holds all daemon configuration.
type Config struct {
	// Node settings
	KeyPath     string   `toml:"key_path"`
	ListenAddrs []string `toml:"listen_addrs"`

	// gRPC settings
	GRPCListen string `toml:"grpc_listen"`

	// Relay settings
	BootstrapPeers    []string `toml:"bootstrap_peers"`
	EnableRelayClient bool     `toml:"enable_relay_client"`
	EnableHolePunching bool    `toml:"enable_hole_punching"`

	// Transport settings
	EnableQUIC         bool `toml:"enable_quic"`          // Enable QUIC transport (default: true)
	EnableWebTransport bool `toml:"enable_webtransport"`  // Enable WebTransport (default: false)

	// Discovery settings
	EnableMDNS bool `toml:"enable_mdns"`

	// Store settings
	StorePath       string `toml:"store_path"`
	OfflineQueueTTL string `toml:"offline_queue_ttl"`

	// Log settings
	LogLevel  string `toml:"log_level"`
	LogFormat string `toml:"log_format"`

	// v0.2: HTTP Bridge settings
	Bridge BridgeConfig `toml:"bridge"`

	// v0.2: Anycast routing settings
	Anycast AnycastConfig `toml:"anycast"`

	// v0.2: Metrics settings
	Metrics MetricsConfig `toml:"metrics"`

	// v0.4: MCP server settings
	MCP MCPConfig `toml:"mcp"`

	// v0.5: ANP (Agent Network Protocol) bridge settings
	ANP ANPConfig `toml:"anp"`

	// v0.5: Decentralized identity settings
	Identity IdentityConfig `toml:"identity"`
}

// BridgeConfig holds HTTP Bridge configuration.
type BridgeConfig struct {
	Enabled     bool     `toml:"enabled"`
	Listen      string   `toml:"listen"`
	TLSCert     string   `toml:"tls_cert"`
	TLSKey      string   `toml:"tls_key"`
	CORSOrigins []string `toml:"cors_origins"`
}

// AnycastConfig holds Anycast routing configuration.
type AnycastConfig struct {
	RoutingStrategy string `toml:"routing_strategy"`
	CacheTTL        string `toml:"cache_ttl"`
	AutoRegister    bool   `toml:"auto_register"`
	RegistryAddr    string `toml:"registry_addr"`
	// v0.5: Multiple relay registry addresses for multi-relay federation.
	RegistryAddrs []string `toml:"registry_addrs"`
	// v0.3: DHT-based discovery (can work alongside RegistryAddr).
	EnableDHT bool   `toml:"enable_dht"`
	DHTMode   string `toml:"dht_mode"` // "auto" (default), "server", "client"
}

// EffectiveRegistryAddrs returns the list of registry addresses to use,
// merging the single RegistryAddr with the RegistryAddrs list for backward
// compatibility. Duplicates are removed.
func (c *AnycastConfig) EffectiveRegistryAddrs() []string {
	seen := make(map[string]struct{})
	var addrs []string

	// Single address takes precedence (backward compat).
	if c.RegistryAddr != "" {
		seen[c.RegistryAddr] = struct{}{}
		addrs = append(addrs, c.RegistryAddr)
	}
	for _, addr := range c.RegistryAddrs {
		if _, ok := seen[addr]; !ok {
			seen[addr] = struct{}{}
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

// MetricsConfig holds Prometheus metrics configuration.
type MetricsConfig struct {
	Enabled bool   `toml:"enabled"`
	Listen  string `toml:"listen"`
}

// MCPConfig holds MCP (Model Context Protocol) server configuration.
type MCPConfig struct {
	Enabled bool   `toml:"enabled"`
	Listen  string `toml:"listen"` // Streamable HTTP address, e.g. ":3000"
}

// ANPConfig holds ANP (Agent Network Protocol) bridge configuration.
type ANPConfig struct {
	Enabled bool   `toml:"enabled"`
	Listen  string `toml:"listen"` // e.g. ":8090"
}

// IdentityConfig holds decentralized identity configuration for the agent.
type IdentityConfig struct {
	// DIDWeb is the did:web identifier for this agent (e.g., "did:web:example.com:agents:myagent").
	// When set, the daemon serves a DID Document at the corresponding well-known path via the HTTP bridge.
	DIDWeb string `toml:"did_web"`

	// DIDDNSDomain is the domain used for did:dns resolution (e.g., "example.com").
	// The domain's DNS TXT records at _did.<domain> should contain did:key URIs
	// that map to this agent's identity.
	DIDDNSDomain string `toml:"did_dns_domain"`
}

// DefaultConfig returns the default daemon configuration.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".agentanycast")

	return &Config{
		KeyPath: filepath.Join(base, "key"),
		ListenAddrs: []string{
			"/ip4/0.0.0.0/tcp/0",
			"/ip4/0.0.0.0/udp/0/quic-v1",
		},
		GRPCListen:         "unix://" + filepath.Join(base, "daemon.sock"),
		BootstrapPeers:     []string{},
		EnableQUIC:         true,
		EnableWebTransport: false,
		EnableRelayClient:  true,
		EnableHolePunching: true,
		EnableMDNS:         true,
		StorePath:          filepath.Join(base, "data"),
		OfflineQueueTTL:    "24h",
		LogLevel:           "info",
		LogFormat:          "json",
		Bridge: BridgeConfig{
			Enabled:     false,
			Listen:      ":8080",
			CORSOrigins: []string{"*"},
		},
		Anycast: AnycastConfig{
			RoutingStrategy: "random",
			CacheTTL:        "30s",
			AutoRegister:    true,
		},
		Metrics: MetricsConfig{
			Enabled: false,
			Listen:  ":9090",
		},
		MCP: MCPConfig{
			Enabled: false,
			Listen:  ":3000",
		},
		ANP: ANPConfig{
			Enabled: false,
			Listen:  ":8090",
		},
	}
}

// LoadConfigFile loads a TOML config file and returns a Config with defaults
// overridden by file values. If the file does not exist, it returns default
// config without error.
func LoadConfigFile(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}

	return cfg, nil
}

// ApplyEnv overrides config values from environment variables.
func (c *Config) ApplyEnv() {
	if v := os.Getenv("AGENTANYCAST_KEY_PATH"); v != "" {
		c.KeyPath = v
	}
	if v := os.Getenv("AGENTANYCAST_GRPC_LISTEN"); v != "" {
		c.GRPCListen = v
	}
	if v := os.Getenv("AGENTANYCAST_LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}
	if v := os.Getenv("AGENTANYCAST_STORE_PATH"); v != "" {
		c.StorePath = v
	}
	if v := os.Getenv("AGENTANYCAST_BOOTSTRAP_PEERS"); v != "" {
		var peers []string
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				peers = append(peers, p)
			}
		}
		if len(peers) > 0 {
			c.BootstrapPeers = peers
		}
	}
	if v := os.Getenv("AGENTANYCAST_ENABLE_MDNS"); v == "false" || v == "0" {
		c.EnableMDNS = false
	}
	if v := os.Getenv("AGENTANYCAST_REGISTRY_ADDRS"); v != "" {
		var addrs []string
		for _, a := range strings.Split(v, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				addrs = append(addrs, a)
			}
		}
		if len(addrs) > 0 {
			c.Anycast.RegistryAddrs = addrs
		}
	}
	if v := os.Getenv("AGENTANYCAST_MCP_LISTEN"); v != "" {
		c.MCP.Enabled = true
		c.MCP.Listen = v
	}
}
