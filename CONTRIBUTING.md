# Contributing

Thank you for your interest in contributing to AgentAnycast!

Please see the [Contributing Guide](https://github.com/AgentAnycast/agentanycast/blob/main/CONTRIBUTING.md) in the main repository for guidelines on:

- Contribution workflow
- Coding standards
- Commit message conventions
- Cross-repository changes

## Node-Specific Guidelines

- This repository is licensed under FSL-1.1-ALv2, so a [CLA](https://github.com/AgentAnycast/agentanycast/blob/main/CLA.md) signature is required
- Run `make lint` and `make test` before submitting
- Follow idiomatic Go patterns — `go vet` and `golangci-lint` are enforced in CI
- Use structured logging via `slog` — no `fmt.Println` in production code
