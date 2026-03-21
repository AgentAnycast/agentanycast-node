# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/), and this project adheres to [Semantic Versioning](https://semver.org/).

## [0.7.0] - 2026-03-20

### Added

- OpenTelemetry distributed tracing with W3C Trace Context propagation
- `/health` HTTP endpoint for liveness and readiness checks
- OTLP gRPC exporter for trace data
- `otelgrpc` server and client interceptors for automatic gRPC span creation
- Span annotations for A2A task lifecycle events

## [0.6.0] - 2026-03-20

### Added

- MCP server supporting both stdio and Streamable HTTP transports
- 7 MCP tools: `send_task`, `get_task`, `cancel_task`, `list_peers`, `connect_peer`, `discover_agents`, `get_agent_card`
- Prometheus metrics with 13 counters and gauges covering tasks, peers, streams, and NAT
- `/metrics` HTTP endpoint for Prometheus scraping

## [0.5.0] - 2026-03-19

### Added

- Streaming RPCs: `StreamStart`, `StreamChunk`, `StreamEnd` envelope types
- Chunked artifact delivery for large payloads over libp2p streams
- ANP JSON-RPC bridge for interoperability with ANP-compatible agents

## [0.4.0] - 2026-03-19

### Added

- DID-based peer addressing with `did:key` ↔ PeerID bidirectional conversion
- DHT-based discovery for global peer and skill lookup
- Composite discovery combining mDNS, DHT, and relay registry
- Routing cache with configurable TTL for discovery results

## [0.3.0] - 2026-03-18

### Added

- HTTP Bridge for A2A ↔ P2P interoperability
- Card endpoint serving Agent Card over HTTP (`/.well-known/agent.json`)
- Inbound HTTP handler converting HTTP A2A requests to P2P tasks
- Outbound HTTP handler for sending tasks to standard HTTP-based A2A agents
- Anycast routing engine with skill-based addressing

## [0.2.0] - 2026-03-18

### Added

- `ConnectPeer` and `ListPeers` RPCs for explicit peer management
- Agent Card management with local storage and exchange on connect
- mDNS discovery integration for automatic LAN peer detection

## [0.1.0] - 2026-03-17

### Added

- Core P2P daemon (`agentanycastd`) with libp2p v0.47
- A2A protocol engine with task state machine (submitted → working → completed/failed)
- Message router with ACK tracking and retransmission
- Offline message queue with auto-flush on peer reconnection
- Automatic Agent Card exchange on peer connect
- Ed25519 cryptographic identity with Noise_XX encryption
- NAT traversal: AutoNAT + DCUtR hole-punching + Circuit Relay v2
- mDNS auto-discovery for LAN communication (zero-config)
- BoltDB persistence for tasks, cards, and queued messages
- gRPC server with 13 RPC methods for SDK integration
- Configuration via CLI flags, environment variables, and TOML config file
- CI pipeline with vet, lint, test, and cross-platform build
- Release workflow for automated binary builds (5 platforms)

[0.7.0]: https://github.com/AgentAnycast/agentanycast-node/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/AgentAnycast/agentanycast-node/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/AgentAnycast/agentanycast-node/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/AgentAnycast/agentanycast-node/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/AgentAnycast/agentanycast-node/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/AgentAnycast/agentanycast-node/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/AgentAnycast/agentanycast-node/releases/tag/v0.1.0
