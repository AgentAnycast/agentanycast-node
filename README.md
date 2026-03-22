# AgentAnycast Node

P2P daemon for decentralized A2A agent-to-agent communication.

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-FSL--1.1--ALv2-blue)](LICENSE)

> **AgentAnycast is fully decentralized.** On a local network, it works with zero configuration via mDNS auto-discovery. For cross-network communication, just deploy your own relay with a single command.

## Overview

AgentAnycast Node (`agentanycastd`) is the core daemon that powers the AgentAnycast network. It runs on each machine and handles:

- **Connection Layer** — Protocol-neutral Envelope-based message routing with pluggable transport adapters
- **Multi-Transport** — libp2p (default), NATS, HTTP bridge; select via `--nats-broker` flag
- **Automatic peer discovery** via mDNS on local networks
- **NAT traversal** via circuit relay, hole punching, and QUIC
- **E2E Encryption** — NaCl box (X25519 + XSalsa20-Poly1305) encryption at the Envelope layer, transport-agnostic
- **A2A task routing** with direct, skill-based, and HTTP bridge addressing
- **Enterprise Capabilities** — Skill-based ACL, per-peer rate limiting, audit logging (JSON Lines)
- **Streaming** for chunked artifact delivery
- **HTTP Bridge** for P2P ↔ HTTP A2A interop
- **MCP Server** for AI tool integration (Claude, Cursor, ChatGPT, etc.)
- **MCP Remote Proxy** — `--mcp-proxy "command"` wraps any MCP Server as a P2P-accessible agent
- **ANP Bridge** for Agent Network Protocol interop
- **OpenTelemetry** — Distributed tracing with W3C Trace Context propagation, OTLP exporter
- **Prometheus metrics** for observability
- **gRPC API** for language SDKs (Python, TypeScript) to interact with the daemon

## Quick Start

### Local network -- zero configuration

```bash
# Build
go build -o agentanycastd ./cmd/agentanycastd

# Run -- agents on the same LAN discover each other automatically
./agentanycastd
```

### Cross-network -- deploy your own relay

```bash
# On any VPS with a public IP
git clone https://github.com/AgentAnycast/agentanycast-relay && cd agentanycast-relay
docker-compose up -d

# Note the RELAY_ADDR from the logs, then:
./agentanycastd -bootstrap-peers "/ip4/<RELAY_IP>/tcp/4001/p2p/12D3KooW..."
```

### MCP mode -- use as an AI tool

```bash
# stdio mode (Claude Desktop, Cursor, VS Code, Gemini CLI)
./agentanycastd -mcp

# Streamable HTTP mode (ChatGPT, remote clients)
./agentanycastd -mcp-listen :3000
```

## Configuration

**Priority:** CLI flags > environment variables > config file > defaults

### CLI Flags

| Flag | Description |
|---|---|
| `-key` | Path to identity key file |
| `-grpc-listen` | gRPC listen address (unix:// or tcp://) |
| `-log-level` | Log level (`debug`, `info`, `warn`, `error`) |
| `-bootstrap-peers` | Comma-separated bootstrap multiaddrs |
| `-bridge-listen` | HTTP bridge listen address (e.g., `:8080`) |
| `-enable-webtransport` | Enable WebTransport (QUIC-based, browser-compatible) |
| `-mcp` | Run as MCP server over stdio |
| `-mcp-listen` | MCP Streamable HTTP listen address (e.g., `:3000`) |
| `-anp-listen` | ANP bridge listen address (e.g., `:8090`) |
| `-nats-broker` | NATS broker URL (e.g., `nats://broker.example.com:4222`) |
| `-mcp-proxy` | Wrap an MCP Server command as a P2P-accessible agent |
| `-otlp-endpoint` | OTLP collector endpoint for distributed tracing |
| `-metrics-listen` | Prometheus metrics listen address (e.g., `:9090`) |
| `-config` | Path to TOML config file |
| `-version` | Print version and exit |

### Environment Variables

| Variable | Description | Default |
|---|---|---|
| `AGENTANYCAST_KEY_PATH` | Path to the libp2p identity key file | `~/.agentanycast/key` |
| `AGENTANYCAST_GRPC_LISTEN` | gRPC server listen address | `unix://~/.agentanycast/daemon.sock` |
| `AGENTANYCAST_LOG_LEVEL` | Log level | `info` |
| `AGENTANYCAST_STORE_PATH` | Path to persistent data store | `~/.agentanycast/data` |
| `AGENTANYCAST_BOOTSTRAP_PEERS` | Comma-separated relay/bootstrap multiaddrs | (none) |
| `AGENTANYCAST_ENABLE_MDNS` | Enable mDNS local network discovery | `true` |
| `AGENTANYCAST_REGISTRY_ADDRS` | Comma-separated registry addresses (multi-relay federation) | (none) |
| `AGENTANYCAST_MCP_LISTEN` | MCP Streamable HTTP address | (none) |

### Config File

Default location: `~/.agentanycast/config.toml`

```toml
key_path = "~/.agentanycast/key"
grpc_listen = "unix://~/.agentanycast/daemon.sock"
log_level = "info"
log_format = "json"
store_path = "~/.agentanycast/data"
enable_mdns = true
enable_quic = true
enable_webtransport = false
enable_relay_client = true
enable_hole_punching = true
offline_queue_ttl = "24h"
bootstrap_peers = ["/ip4/203.0.113.50/tcp/4001/p2p/12D3KooW..."]

[bridge]
enabled = false
listen = ":8080"
# tls_cert = "/path/to/cert.pem"
# tls_key = "/path/to/key.pem"
# cors_origins = ["*"]

[anycast]
routing_strategy = "random"
cache_ttl = "30s"
auto_register = true
# registry_addr = "relay.example.com:50052"
# registry_addrs = ["relay1:50052", "relay2:50052"]  # federation
enable_dht = false
dht_mode = "auto"   # "auto", "server", or "client"

[metrics]
enabled = false
listen = ":9090"

[mcp]
enabled = false
listen = ":3000"

[anp]
enabled = false
listen = ":8090"

# NATS Transport
[transport.nats]
enabled = true
broker = "nats://broker.example.com:4222"
subject_prefix = "agent."

# Enterprise Policy
[policy]
acl_rules = [
  { source = "*", skill = "*", allow = true },
]

[policy.rate_limits]
default_rps = 100

audit_log_path = "/var/log/agentanycast-audit.jsonl"

# OpenTelemetry
[otel]
enabled = false
otlp_endpoint = "localhost:4317"

[identity]
# did_web = "did:web:example.com:agents:myagent"
# did_dns_domain = "example.com"
```

## Architecture

```
┌──────────────────────────────────────────────────────────────────────┐
│                           agentanycastd                              │
│                                                                      │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────┐     │
│  │  Engine   │  │  Router  │  │ Offline  │  │ Anycast Router   │     │
│  │(task FSM) │  │(A2A msg) │  │  Queue   │  │(skill discovery) │     │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────────────┘     │
│       │              │             │              │                   │
│  ┌────┴──────────────┴─────────────┴──────────────┴──────────────┐   │
│  │         Envelope Layer (E2E NaCl box encryption)              │   │
│  ├───────────────────────────────────────────────────────────────┤   │
│  │                    Transport Adapters                         │   │
│  │  libp2p (mDNS · TCP · QUIC · WebTransport · DHT)             │   │
│  │  NATS · HTTP Bridge · Circuit Relay v2 · DCUtR                │   │
│  └──────────────────────┬───────────────────────────────────────┘   │
│                         │                                            │
│  ┌──────────────────────┴──────────────────────────────────────┐     │
│  │                gRPC Server (16 RPCs for SDKs)               │     │
│  └─────────────────────────────────────────────────────────────┘     │
│                                                                      │
│  ┌────────────┐ ┌────────────┐ ┌──────────┐ ┌────────┐ ┌──────────┐ │
│  │ HTTP Bridge│ │ MCP Server │ │ANP Bridge│ │  OTel  │ │MCP Proxy │ │
│  │ (A2A↔P2P) │ │(stdio/HTTP)│ │(ANP↔A2A) │ │(trace) │ │(wrap cmd)│ │
│  └────────────┘ └────────────┘ └──────────┘ └────────┘ └──────────┘ │
│                                                                      │
│  ┌────────┐ ┌──────┐ ┌─────────┐ ┌──────────────────┐               │
│  │Metrics │ │BoltDB│ │  ACL /  │ │   Audit Logger   │               │
│  │(Prom.) │ │(stor)│ │RateLimit│ │   (JSON Lines)   │               │
│  └────────┘ └──────┘ └─────────┘ └──────────────────┘               │
└──────────────────────────────────────────────────────────────────────┘
```

### Internal Packages

| Package | Responsibility |
|---|---|
| `internal/a2a/` | A2A protocol engine — task state machine, envelope routing, offline queue, streaming |
| `internal/node/` | libp2p host — peer connections, mDNS, DHT, TCP/QUIC/WebTransport |
| `internal/crypto/` | Ed25519 key management, DID conversion (did:key, did:web, did:dns), Verifiable Credentials |
| `internal/nat/` | AutoNAT detection, DCUtR hole punching, Circuit Relay v2 client |
| `internal/store/` | BoltDB persistence — tasks, agent cards, offline message queue |
| `internal/config/` | Configuration — TOML file, environment variables, CLI flags |
| `internal/bridge/` | HTTP Bridge — translates HTTP JSON-RPC ↔ P2P A2A envelopes |
| `internal/anycast/` | Anycast router — skill-based addressing, registry + multi-registry federation + DHT discovery |
| `internal/envelope/` | Envelope layer — protocol-neutral message routing, E2E NaCl box encryption |
| `internal/transport/` | Pluggable transport adapters — libp2p, NATS, HTTP bridge |
| `internal/metrics/` | Prometheus metrics — connections, tasks, routing, bridge, streaming, MCP |
| `internal/mcp/` | MCP Server — exposes P2P capabilities as MCP tools (stdio + Streamable HTTP) |
| `internal/mcpproxy/` | MCP Remote Proxy — wraps external MCP Server commands as P2P agents |
| `internal/anp/` | ANP Bridge — translates ANP HTTP ↔ A2A P2P (JSON-RPC 2.0 + JSON-LD) |
| `internal/policy/` | Enterprise policy — skill-based ACL, per-peer rate limiting, audit logging |
| `internal/otel/` | OpenTelemetry — distributed tracing, W3C Trace Context, OTLP exporter |
| `pkg/grpcserver/` | gRPC server — 16 RPC methods for SDKs |

### gRPC API (16 RPCs)

| Group | Methods |
|---|---|
| **Node** | `GetNodeInfo`, `SetAgentCard` |
| **Peers** | `ConnectPeer`, `ListPeers`, `GetPeerCard` |
| **Task Client** | `SendTask` (peer_id / skill_id / url), `GetTask`, `CancelTask`, `SubscribeTaskUpdates` |
| **Task Server** | `SubscribeIncomingTasks`, `UpdateTaskStatus`, `CompleteTask`, `FailTask` |
| **Streaming** | `SubscribeTaskStream`, `SendStreamingArtifact` |
| **Discovery** | `Discover` |

### MCP Server

The daemon can run as an MCP (Model Context Protocol) server, exposing P2P capabilities as tools for AI assistants:

| Tool | Description |
|---|---|
| `toolGetNodeInfo` | Get local node information |
| `toolListConnectedPeers` | List connected peers |
| `toolGetAgentCard` | Get local or remote agent card |
| `toolDiscoverAgents` | Discover agents by skill |
| `toolSendTask` | Send task to peer by ID |
| `toolSendTaskBySkill` | Send task by skill (anycast) |
| `toolGetTaskStatus` | Get task status |

Two transport modes:
- **stdio** — for local AI tool integration (Claude Desktop, Cursor, VS Code, Gemini CLI, JetBrains)
- **Streamable HTTP** — for remote clients (ChatGPT)

### MCP Remote Proxy

Wrap any MCP Server as a P2P-accessible agent with a single flag:

```bash
# Make a local MCP server discoverable over the P2P network
./agentanycastd --mcp-proxy "npx -y @modelcontextprotocol/server-filesystem /home/user"
```

The proxy auto-generates an Agent Card from the MCP server's tool list, registers skills with the relay, and bridges incoming A2A tasks to MCP tool calls.

### HTTP Bridge

Exposes an A2A-compatible HTTP endpoint for P2P ↔ HTTP interop:

- `GET /.well-known/a2a-agent-card` — Agent Card discovery
- `POST /` — JSON-RPC endpoint for task operations
- Optional TLS and CORS support

### ANP Bridge

Exposes an ANP-compatible HTTP endpoint for Agent Network Protocol interop:

- `GET /agent/ad.json` — Agent Description (JSON-LD)
- `GET /agent/interface.json` — OpenRPC specification
- `POST /agent/rpc` — JSON-RPC 2.0 endpoint

### Metrics

Prometheus metrics on configurable HTTP port (default `:9090`):

- `agentanycast_connected_peers` — current peer count (gauge)
- `agentanycast_connections_total` — connection events by direction (counter)
- `agentanycast_connections_by_transport` — connections by transport type: tcp/quic/webtransport (counter)
- `agentanycast_tasks_total` — tasks by direction and status (counter)
- `agentanycast_task_duration_seconds` — task latency histogram
- `agentanycast_route_resolutions_total` — anycast resolution by result (counter)
- `agentanycast_bridge_requests_total` — HTTP bridge requests (counter)
- `agentanycast_stream_chunks_total` — streaming chunks by direction (counter)
- `agentanycast_messages_total` — A2A messages by envelope type (counter)
- `agentanycast_offline_queue_size` — queued offline messages (gauge)
- `agentanycast_mcp_tool_calls_total` — MCP tool calls by tool and status (counter)
- `agentanycast_acl_decisions_total` — ACL allow/deny decisions (counter)
- `agentanycast_rate_limit_rejections_total` — rate-limited requests (counter)

### OpenTelemetry

Distributed tracing with W3C Trace Context propagation. Spans cover task lifecycle, transport hops, and envelope encryption. Configure with:

```bash
./agentanycastd --otlp-endpoint localhost:4317
```

## Disclaimer

This software is provided "as is", without warranty of any kind. This software uses cryptography and may be subject to export controls in certain jurisdictions.

## License

[FSL-1.1-ALv2](LICENSE) -- Functional Source License, Version 1.1, with Apache License, Version 2.0 as the future license. Each release converts to Apache 2.0 two years after its publication date.
