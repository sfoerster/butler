# Butler

An access-control reverse proxy for [Ollama](https://ollama.com). Butler sits between your clients and the Ollama API to handle authentication, model-level authorization, and structured logging -- things Ollama intentionally does not provide.

```
Client A ──┐
Client B ──┼──▶ [butler :8080] ──▶ [ollama :11434]
Client C ──┘
            auth / ACL / log
```

## Why

Ollama has no authentication or authorization. Any client that can reach port 11434 can use any model with any parameters. This is fine for single-user local use, but breaks down when:

- Multiple services share one Ollama server and you need to control which service can use which models
- You expose Ollama to a LAN or homelab and want per-client access control
- You need to log or audit which clients are sending what requests
- You want to prevent one runaway service from monopolizing your GPU

Butler gives you per-client API keys, model-level allowlists and denylists, and structured JSON request logging -- all configured with a single YAML file.

## Features

- **Transparent reverse proxy** for both Ollama's native API (`/api/*`) and OpenAI-compatible API (`/v1/*`)
- **Streaming support** -- SSE and chunked responses are passed through without buffering
- **API key authentication** via `Authorization: Bearer <key>` header
- **Per-key model allowlist** -- restrict which models each client can access
- **Per-key model denylist** -- explicitly block specific models per client
- **Deny by default** -- if a client has no allowlist entry for a model, the request is rejected
- **Structured JSON logging** -- every request and response is logged with client identity, model, HTTP method, path, status code, and duration
- **YAML configuration** with `${ENV_VAR}` interpolation for secrets
- **Single static binary** -- no runtime dependencies, no database, no Redis
- **Fail closed** -- unauthenticated or unauthorized requests are rejected, never proxied

## Quick Start

### From source

```bash
# Build
make build

# Create config
cp butler.example.yaml butler.yaml
# Edit butler.yaml with your keys and upstream address

# Run
./butler -config butler.yaml
```

### With Docker Compose

The included `docker-compose.yml` runs Butler alongside Ollama as a turnkey stack:

```bash
# Create your config
cp butler.example.yaml butler.yaml
# Edit butler.yaml -- set listen to 0.0.0.0:8080 and upstream to http://ollama:11434

# Start both services
docker compose up -d
```

Butler listens on port 8080. Clients connect to Butler instead of Ollama directly.

### Docker only

```bash
docker build -t butler .

docker run -d \
  -p 8080:8080 \
  -v $(pwd)/butler.yaml:/etc/butler/butler.yaml:ro \
  butler
```

## Configuration

Butler is configured with a single YAML file. Secrets can be referenced as `${ENV_VAR}` and will be expanded at load time.

```yaml
# butler.yaml
listen: "127.0.0.1:8080"        # Address to listen on (default: 127.0.0.1:8080)
upstream: "http://127.0.0.1:11434"  # Ollama address (required)

clients:
  - name: my-app                 # Human-readable client name (for logs)
    key: "${MY_APP_KEY}"         # API key (required, unique per client)
    allow_models: ["llama3.2", "mistral"]  # Models this client can use

  - name: admin-tool
    key: "${ADMIN_KEY}"
    allow_models: ["*"]          # Wildcard: access to all models

  - name: restricted-service
    key: "${RESTRICTED_KEY}"
    allow_models: ["llama3.2:1b"]  # Specific model tag only
    deny_models: ["llama3.2:70b"] # Explicitly deny specific models
```

See [`butler.example.yaml`](butler.example.yaml) for a full annotated example.

### Configuration reference

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `listen` | string | no | `127.0.0.1:8080` | Address and port to listen on |
| `upstream` | string | yes | -- | Ollama server URL |
| `clients` | list | yes | -- | At least one client must be defined |
| `clients[].name` | string | yes | -- | Client identifier (appears in logs) |
| `clients[].key` | string | yes | -- | API key for authentication (must be unique) |
| `clients[].allow_models` | list | no | `[]` (deny all) | Models this client can access |
| `clients[].deny_models` | list | no | `[]` | Models explicitly denied (checked before allowlist) |

### Model matching rules

- `"*"` -- matches all models
- `"llama3.2"` -- matches `llama3.2`, `llama3.2:latest`, `llama3.2:7b`, or any other tag
- `"llama3.2:7b"` -- matches only the exact string `llama3.2:7b`

Evaluation order: **denylist first**, then allowlist. If a model matches no allowlist entry, the request is denied.

## Usage

Point your clients at Butler instead of Ollama and include an API key:

```bash
# Ollama native API
curl http://localhost:8080/api/chat \
  -H "Authorization: Bearer sk-my-api-key" \
  -d '{"model": "llama3.2", "messages": [{"role": "user", "content": "Hello"}]}'

# OpenAI-compatible API
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-my-api-key" \
  -d '{"model": "llama3.2", "messages": [{"role": "user", "content": "Hello"}]}'

# List models (auth required, no model ACL check)
curl http://localhost:8080/api/tags \
  -H "Authorization: Bearer sk-my-api-key"
```

### Error responses

All errors are returned as JSON:

| Status | Body | Meaning |
|---|---|---|
| `401` | `{"error":"unauthorized"}` | Missing or invalid API key |
| `403` | `{"error":"model not allowed"}` | Client not authorized for the requested model |
| `400` | `{"error":"bad request"}` | Malformed request body |
| `413` | `{"error":"request body too large"}` | Model-bearing request body exceeds inspection limit |
| `502` | `{"error":"upstream unavailable"}` | Cannot reach Ollama |

### Supported endpoints

Butler proxies all Ollama endpoints transparently. Model-level ACL is enforced on endpoints that carry a model name in the request body:

**Inference** (checks `model` field):
`/api/chat`, `/api/generate`, `/api/embeddings`, `/api/embed`, `/v1/chat/completions`, `/v1/completions`, `/v1/embeddings`

**Management** (checks `name` field):
`/api/show`, `/api/pull`, `/api/push`, `/api/delete`, `/api/create`

**Passthrough** (auth only, no model check):
`/api/tags`, `/api/ps`, `/api/version`, `/v1/models`, and any other path

## Logging

Butler emits structured JSON logs to stdout ([12-factor](https://12factor.net/logs) style). Every proxied request produces two log lines -- one on entry and one on completion:

```json
{"time":"2025-03-05T10:30:00Z","level":"INFO","msg":"request","client":"my-app","model":"llama3.2","method":"POST","path":"/api/chat","remote":"192.168.1.50:43210"}
{"time":"2025-03-05T10:30:02Z","level":"INFO","msg":"response","client":"my-app","model":"llama3.2","path":"/api/chat","status":200,"duration_ms":2045}
```

Denied requests are logged at `WARN` level:

```json
{"time":"2025-03-05T10:30:05Z","level":"WARN","msg":"unauthorized request","path":"/api/chat","remote":"192.168.1.99:51234"}
{"time":"2025-03-05T10:30:06Z","level":"WARN","msg":"model denied","client":"restricted-service","model":"llama3.2:70b","path":"/api/generate"}
```

Pipe logs to any collector that accepts JSON lines (journald, Loki, Datadog, etc.).

## Development

### Prerequisites

- Go 1.23+
- [golangci-lint](https://golangci-lint.run/) v2 (for linting)

### Build and test

```bash
make build       # Build the butler binary
make test        # Run tests with race detector and coverage
make lint        # Run golangci-lint
make run         # Build and run with butler.yaml
make clean       # Remove the binary
```

### Project structure

```
cmd/butler/main.go              Entry point
internal/config/config.go       YAML config loading, validation, model ACL
internal/config/config_test.go  Config and ACL unit tests
internal/proxy/proxy.go         Reverse proxy, auth middleware, response logging
internal/proxy/model.go         Model name extraction from request bodies
internal/proxy/proxy_test.go    Proxy integration tests
```

### Running tests

```bash
go test -race -cover ./...
```

Current coverage: ~96% for `config`, ~83% for `proxy`.

### CI/CD

The `.gitlab-ci.yml` pipeline runs three stages:

1. **lint** -- `golangci-lint run ./...` (parallel with test)
2. **test** -- `go test -race -cover ./...` (parallel with lint)
3. **build** -- produces a static binary artifact

## Design Principles

- **Zero changes to Ollama** -- Butler is a proxy, not a patch. Ollama runs unmodified.
- **Zero changes to clients** -- Butler speaks the same API. Clients just change the host and add a `Bearer` token.
- **Config file, not a database** -- policy is a YAML file you can version-control alongside your infrastructure.
- **Single static binary** -- no runtime dependencies. The Docker image is built `FROM scratch`.
- **Fail closed** -- if Butler cannot verify a client, the request is denied. No anonymous access, no fallback.

## Roadmap

See [`docs/BUTLER_ROADMAP.md`](docs/BUTLER_ROADMAP.md) for the full roadmap. Upcoming phases:

- **Phase 2** -- Input filtering (regex prompt rejection), per-key rate limiting, context length and token caps
- **Phase 3** -- Observability (Prometheus `/metrics`, `/healthz` health check, optional full-prompt logging)
- **Phase 4** -- Multi-user identity (JWT auth, OIDC federation, per-user policy and token budgets)
- **Phase 5** -- Advanced policy (time-of-day restrictions, config hot-reload, mTLS, webhook notifications)

## License

Apache 2.0
