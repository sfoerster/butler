# Contributing to Butler

## Development Setup

1. Use Go 1.23+.
2. Install [golangci-lint](https://golangci-lint.run/) v2.

## Required Checks

Run these before opening a pull request:

```bash
make lint       # golangci-lint run ./...
make test       # go test -race -cover ./...
go fmt ./...
```

Or equivalently:

```bash
golangci-lint run ./...
go test -race -cover ./...
go fmt ./...
```

## Makefile Targets

| Target | Description |
|---|---|
| `make build` | Build the `butler` binary |
| `make test` | Run tests with race detector and coverage |
| `make lint` | Run golangci-lint |
| `make run` | Build and run with `butler.yaml` |
| `make clean` | Remove the binary |

## Pull Request Standards

1. Keep changes scoped and reviewable.
2. Include tests for behavior changes and regressions.
3. Update docs when commands, config, or API behavior changes.
4. At least one reviewer approval is required before merge.
5. Do not merge failing CI.

## Contributor IP Terms

1. Contributions must be original work you are authorized to submit.
2. Before a first merged contribution, complete the project's contributor IP agreement with the maintainer.
3. Every commit in a pull request must include a `Signed-off-by:` trailer (Developer Certificate of Origin).
4. Do not submit code copied from third-party sources unless license compatibility and attribution have been reviewed.

## Security and Secrets

1. Never commit real credentials, API keys, or tokens.
2. Use `${ENV_VAR}` syntax in config files for secrets.
3. Follow `SECURITY.md` for vulnerability reporting.
