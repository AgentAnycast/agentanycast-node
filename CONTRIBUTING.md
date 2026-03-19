# Contributing

Thank you for your interest in contributing to AgentAnycast!

Please see the [Contributing Guide](https://github.com/AgentAnycast/agentanycast/blob/main/CONTRIBUTING.md) in the main repository for guidelines on:

- Development workflow (fork → branch → PR → squash merge)
- Coding standards and commit message conventions
- Cross-repository changes
- CLA requirements

## Node-Specific Guidelines

- This repository is licensed under FSL-1.1-ALv2, so a [CLA](https://github.com/AgentAnycast/agentanycast/blob/main/CLA.md) signature is required
- Run `make lint` and `make test` before submitting
- Follow idiomatic Go patterns — `go vet` and `golangci-lint` are enforced in CI
- Use structured logging via `slog` — no `fmt.Println` in production code

## Required CI Checks

All of the following must pass before a PR can be merged:

- **vet** — `go vet ./...`
- **lint** — `golangci-lint run`
- **test** — Unit tests with race detection
- **build** — Cross-platform build verification
