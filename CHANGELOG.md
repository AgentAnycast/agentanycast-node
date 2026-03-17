# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/), and this project adheres to [Semantic Versioning](https://semver.org/).

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

[0.1.0]: https://github.com/AgentAnycast/agentanycast-node/releases/tag/v0.1.0
