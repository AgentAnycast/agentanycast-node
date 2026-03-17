# AgentAnycast Node

P2P daemon for decentralized A2A agent-to-agent communication.

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-FSL--1.1--ALv2-blue)](LICENSE)

> **AgentAnycast is fully decentralized.** On a local network, it works with zero configuration via mDNS auto-discovery. For cross-network communication, just deploy your own relay with a single command.

## Overview

AgentAnycast Node (`agentanycastd`) is the core daemon that powers the AgentAnycast network. It runs on each machine and handles:

- **Automatic peer discovery** via mDNS on local networks
- **NAT traversal** via circuit relay and hole punching for cross-network communication
- **End-to-end encryption** using Noise_XX (Curve25519 + ChaCha20-Poly1305)
- **A2A task routing** between peers
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
store_path = "~/.agentanycast/store"
enable_mdns = true
bootstrap_peers = [
    "/ip4/203.0.113.50/tcp/4001/p2p/12D3KooW..."
]
```

### CLI Flags

| Flag | Description |
|---|---|
| `-key` | Path to identity key file |
| `-grpc-listen` | gRPC listen address |
| `-log-level` | Log level |
| `-bootstrap-peers` | Comma-separated bootstrap multiaddrs |
| `-config` | Path to TOML config file |
| `-version` | Print version and exit |

## Architecture

```
┌────────────────────────────────────────────────┐
│                 agentanycastd                   │
│                                                │
│  ┌──────────┐  ┌──────────┐  ┌──────────────┐ │
│  │  Engine   │  │  Router  │  │ Offline Queue│ │
│  │(task FSM) │  │(A2A msg) │  │  (retry)     │ │
│  └────┬─────┘  └────┬─────┘  └──────┬───────┘ │
│       │              │               │         │
│  ┌────┴──────────────┴───────────────┴───────┐ │
│  │              libp2p Host                   │ │
│  │  mDNS · Noise · TCP/QUIC · Relay · DCUtR  │ │
│  └────────────────────┬──────────────────────┘ │
│                       │                        │
│  ┌────────────────────┴──────────────────────┐ │
│  │            gRPC Server (SDK API)          │ │
│  └───────────────────────────────────────────┘ │
│                                                │
│  ┌───────────────────────────────────────────┐ │
│  │          BoltDB Store (persistence)       │ │
│  └───────────────────────────────────────────┘ │
└────────────────────────────────────────────────┘
```

- **Engine** -- Task state machine (submitted → working → completed/failed)
- **Router** -- Serializes/deserializes A2A envelopes, routes between peers, handles ACK + retransmission
- **Offline Queue** -- Queues messages for unreachable peers, auto-flushes on reconnection
- **libp2p Host** -- Peer discovery (mDNS), connections, NAT traversal, E2E encryption
- **gRPC Server** -- 13 RPC methods for SDKs to control the daemon
- **Store** -- BoltDB-based persistence for tasks, agent cards, and queued messages
- **Auto Card Exchange** -- Agent Cards are automatically exchanged with newly connected peers

## Disclaimer

This software is provided "as is", without warranty of any kind. This software uses cryptography and may be subject to export controls in certain jurisdictions.

## License

[FSL-1.1-ALv2](LICENSE) -- Functional Source License, Version 1.1, with Apache License, Version 2.0 as the future license. Each release converts to Apache 2.0 two years after its publication date.
