# AgentAnycast Node

P2P daemon for decentralized A2A agent-to-agent communication.

[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-FSL--1.1--ALv2-blue)](LICENSE)

> **AgentAnycast is fully decentralized.** On a local network, it works with zero configuration via mDNS auto-discovery. For cross-network communication, just deploy your own relay with a single command.

## Overview

AgentAnycast Node (`agentanycastd`) is the core daemon that powers the AgentAnycast network. It runs on each machine and handles:

- **Automatic peer discovery** via mDNS on local networks
- **NAT traversal** via circuit relay and hole punching for cross-network communication
- **End-to-end encryption** using Noise_XX (Curve25519 + ChaCha20-Poly1305)
- **A2A task routing** between peers with direct, skill-based, and HTTP bridge addressing
- **Streaming** for chunked artifact delivery
- **HTTP Bridge** for P2P ↔ HTTP A2A interop
- **Prometheus metrics** for observability
- **gRPC API** for language SDKs (Python, etc.) to interact with the daemon

## Quick Start

### Local network -- zero configuration

```bash
# Build
go build -o agentanycastd ./cmd/agentanycastd

# Run -- agents on the same LAN discover each other automatically
./agentanycastd
```

That's it. Agents on the same network find each other via mDNS. No relay, no bootstrap, no configuration needed.

### Cross-network -- deploy your own relay

For agents on different networks (across the internet), you need a relay server. Deploy one with a single command:

```bash
# On any VPS with a public IP (Oracle Cloud free tier works great)
git clone https://github.com/AgentAnycast/agentanycast-relay && cd agentanycast-relay
docker-compose up -d

# Note the RELAY_ADDR from the logs, then tell your nodes about it:
./agentanycastd -bootstrap-peers "/ip4/<RELAY_IP>/tcp/4001/p2p/12D3KooW..."
```

Or via environment variable:

```bash
export AGENTANYCAST_BOOTSTRAP_PEERS="/ip4/<RELAY_IP>/tcp/4001/p2p/12D3KooW..."
./agentanycastd
```

## Configuration

The daemon can be configured via CLI flags, environment variables, or a TOML config file.

**Priority:** CLI flags > environment variables > config file > defaults

### Environment Variables

| Variable | Description | Default |
|---|---|---|
| `AGENTANYCAST_KEY_PATH` | Path to the libp2p identity key file | `~/.agentanycast/key` |
| `AGENTANYCAST_GRPC_LISTEN` | gRPC server listen address | `127.0.0.1:50051` |
| `AGENTANYCAST_LOG_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `AGENTANYCAST_STORE_PATH` | Path to persistent data store | `~/.agentanycast/store` |
| `AGENTANYCAST_BOOTSTRAP_PEERS` | Comma-separated list of relay/bootstrap multiaddrs | (none -- LAN only) |
| `AGENTANYCAST_ENABLE_MDNS` | Enable mDNS local network discovery | `true` |

### Config File

Default location: `~/.agentanycast/config.toml`

```toml
key_path = "~/.agentanycast/key"
grpc_listen = "127.0.0.1:50051"
log_level = "info"
log_format = "json"             # "json" or "text"
store_path = "~/.agentanycast/store"
enable_mdns = true
bootstrap_peers = [
    "/ip4/203.0.113.50/tcp/4001/p2p/12D3KooW..."
]

[bridge]
enabled = false
listen = ":8080"
# tls_cert = "/path/to/cert.pem"
# tls_key = "/path/to/key.pem"
# cors_origins = ["https://app.example.com"]

[anycast]
# registry_addr = "relay.example.com:50052"
enable_dht = false
dht_mode = "auto"               # "auto", "server", or "client"
cache_ttl = "30s"
auto_register = true

[metrics]
enabled = false
listen = ":9090"
```

### CLI Flags

| Flag | Description |
|---|---|
| `-key` | Path to identity key file |
| `-grpc-listen` | gRPC listen address |
| `-log-level` | Log level |
| `-bootstrap-peers` | Comma-separated bootstrap multiaddrs |
| `-bridge-listen` | HTTP bridge listen address (e.g., `:8080`) |
| `-config` | Path to TOML config file |
| `-version` | Print version and exit |

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                      agentanycastd                       │
│                                                          │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐ ┌──────────┐  │
│  │  Engine   │  │  Router  │  │ Offline  │ │ Anycast  │  │
│  │(task FSM) │  │(A2A msg) │  │  Queue   │ │ Router   │  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘ └────┬─────┘  │
│       │              │             │             │        │
│  ┌────┴──────────────┴─────────────┴─────────────┴─────┐ │
│  │                    libp2p Host                      │ │
│  │  mDNS · Noise · TCP/QUIC · Relay · DCUtR · DHT     │ │
│  └──────────────────────┬──────────────────────────────┘ │
│                         │                                │
│  ┌──────────────────────┴──────────────────────────────┐ │
│  │      gRPC Server (16 RPCs for SDK)                  │ │
│  └─────────────────────────────────────────────────────┘ │
│                                                          │
│  ┌───────────────┐  ┌───────────────┐  ┌──────────────┐  │
│  │  HTTP Bridge  │  │   Metrics     │  │  BoltDB      │  │
│  │  (A2A ↔ P2P)  │  │  (Prometheus) │  │  (storage)   │  │
│  └───────────────┘  └───────────────┘  └──────────────┘  │
└──────────────────────────────────────────────────────────┘
```

### Internal Packages

| Package | Responsibility |
|---|---|
| `internal/a2a/` | A2A protocol engine — task state machine, envelope routing, offline queue, streaming |
| `internal/node/` | libp2p host — peer connections, mDNS discovery, DHT, stream multiplexing |
| `internal/crypto/` | Ed25519 key management, Noise_XX integration |
| `internal/nat/` | AutoNAT detection, DCUtR hole punching, Circuit Relay v2 client |
| `internal/store/` | BoltDB persistence — tasks, agent cards, offline message queue |
| `internal/config/` | Configuration — TOML file, environment variables, CLI flags |
| `internal/bridge/` | HTTP Bridge — translates HTTP JSON-RPC ↔ P2P A2A envelopes |
| `internal/anycast/` | Anycast router — skill-based addressing, registry + DHT discovery |
| `internal/metrics/` | Prometheus metrics — connections, tasks, routing, bridge, streaming |
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

### HTTP Bridge

The HTTP Bridge exposes an A2A-compatible HTTP endpoint, letting standard HTTP A2A agents interact with the P2P network:

- `/.well-known/a2a-agent-card` — Agent Card discovery
- `/` — JSON-RPC endpoint for task operations
- Optional TLS and CORS support

### Metrics

When enabled, the daemon exposes Prometheus metrics on a configurable HTTP port:

- `agentanycast_connected_peers` — current peer count
- `agentanycast_tasks_total` — tasks by status
- `agentanycast_task_duration_seconds` — task latency histogram
- `agentanycast_bridge_requests_total` — HTTP bridge requests
- `agentanycast_route_resolutions_total` — anycast resolution count
- `agentanycast_stream_chunks_total` — streaming chunk count
- `agentanycast_offline_queue_size` — queued offline messages

## Disclaimer

This software is provided "as is", without warranty of any kind. This software uses cryptography and may be subject to export controls in certain jurisdictions.

## License

[FSL-1.1-ALv2](LICENSE) -- Functional Source License, Version 1.1, with Apache License, Version 2.0 as the future license. Each release converts to Apache 2.0 two years after its publication date.
