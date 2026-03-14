# Butler

An access-control reverse proxy for [Ollama](https://ollama.com). Butler sits between your clients and the Ollama API to handle authentication, model-level authorization, input filtering, rate limiting, and structured logging -- things Ollama intentionally does not provide.

```
Client A ──┐
Client B ──┼──▶ [butler :8080] ──▶ [ollama :11434]
Client C ──┘
            auth / ACL / filter / limit / log
```

## Why

Ollama has no authentication or authorization. Any client that can reach port 11434 can use any model with any parameters. This is fine for single-user local use, but breaks down when:

- Multiple services share one Ollama server and you need to control which service can use which models
- You expose Ollama to a LAN or homelab and want per-client access control
- You need to log or audit which clients are sending what requests
- You want to prevent one runaway service from monopolizing your GPU

Butler gives you per-client API keys, model-level allowlists and denylists, rate limiting, input filtering, and structured JSON request logging -- all configured with a single YAML file.

## Features

**Authentication**

- **API key authentication** via `Authorization: Bearer <key>` header
- **JWT authentication** -- standalone mode with built-in `/auth/login` endpoint for username/password login
- **OIDC federation** -- validate tokens from external identity providers (Keycloak, Okta, Entra ID) using JWKS auto-discovery
- **Flexible auth modes** -- `api_key`, `jwt_standalone`, `oidc`, or `either` (combine all three)

**Authorization and policy**

- **Role-to-policy mapping** -- map OIDC roles/groups to proxy policy (model ACLs, rate limits, caps) via YAML config
- **Per-user policy** -- model ACLs, rate limits, context caps, and prompt filtering per authenticated user
- **Per-key model allowlist and denylist** -- restrict which models each client can access
- **Deny by default** -- if a client has no allowlist entry for a model, the request is rejected
- **Multi-role merging** -- when a user has multiple OIDC roles, policies merge using most-permissive-wins

**Rate limiting and input filtering**

- **Per-client rate limiting** -- cap requests per minute or per hour per client
- **Global rate limiting** -- protect Ollama from total overload across all clients
- **Request size limits** -- reject abnormally large payloads per client
- **Context length cap** -- reject requests with `num_ctx` above a per-client threshold
- **Token prediction cap** -- enforce a `num_predict` ceiling per client
- **Regex prompt rejection** -- block requests matching configurable patterns before they reach the model

**Observability**

- **Prometheus metrics** -- `/metrics` endpoint with request counters, rejection counters, and latency histograms (hand-rolled, no external dependencies)
- **Health check** -- `/healthz` endpoint that verifies upstream Ollama connectivity (for load balancers and orchestrators)
- **Optional prompt logging** -- log full prompt content at INFO level for audit and debugging (`log_prompts: true`)
- **Structured JSON logging** -- every request and response is logged with client/user identity, model, HTTP method, path, status code, and duration

**Operations**

- **Transparent reverse proxy** for both Ollama's native API (`/api/*`) and OpenAI-compatible API (`/v1/*`)
- **Streaming support** -- SSE and chunked responses are passed through without buffering
- **YAML configuration** with `${ENV_VAR}` interpolation for secrets
- **Single static binary** -- no runtime dependencies, no database, no Redis
- **Fail closed** -- unauthenticated or unauthorized requests are rejected, never proxied

## Quick Start

See [`docs/QUICK_START.md`](docs/QUICK_START.md) for step-by-step setup guides covering all authentication modes.

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
global_rate_limit: "600/min"     # Global rate limit across all clients (optional)
log_prompts: false               # Log full prompts at INFO level (default: false)

# Authentication mode (optional, default: api_key)
auth:
  mode: either                   # "api_key", "jwt_standalone", "oidc", or "either"
  jwt_secret: "${JWT_SECRET}"    # Required for jwt_standalone/either with users (≥32 chars)
  token_expiry: "24h"            # JWT token lifetime (default: 24h)

  # OIDC federation (for oidc or either mode)
  oidc:
    issuer: "https://auth.example.com/realms/default"  # Must be HTTPS
    client_id: "butler"                                 # Audience claim
    role_claim_path: "realm_access.roles"               # Dot-separated claim path
    refresh_interval: "60m"                             # JWKS refresh interval (default: 60m)

clients:
  - name: my-app                 # Human-readable client name (for logs)
    key: "${MY_APP_KEY}"         # API key (required, unique per client)
    allow_models: ["llama3.2", "mistral"]  # Models this client can use
    rate_limit: "60/min"         # Per-client rate limit (optional)
    max_request_bytes: 1048576   # Max request body size in bytes (optional)
    max_ctx: 4096                # Max num_ctx value (optional)
    max_predict: 512             # Max num_predict value (optional)
    deny_prompt_patterns:        # Regex patterns to reject prompts (optional)
      - "(?i)ignore.*instructions"

  - name: admin-tool
    key: "${ADMIN_KEY}"
    allow_models: ["*"]          # Wildcard: access to all models

users:
  - name: alice                  # Username for JWT login
    password_hash: "$2b$10$..."  # bcrypt hash of password
    allow_models: ["*"]          # Per-user model allowlist
  - name: kid1
    password_hash: "$2b$10$..."
    allow_models: ["llama3.2"]
    rate_limit: "20/hour"        # Per-user rate limit
    max_ctx: 2048
    max_predict: 256

# Role-to-policy mapping for OIDC mode
role_policies:
  admin:
    allow_models: ["*"]
    rate_limit: "unlimited"
  operator:
    allow_models: ["*"]
    rate_limit: "120/hour"
  viewer:
    allow_models: ["llama3.2:1b"]
    rate_limit: "20/hour"
    max_ctx: 2048
```

See [`butler.example.yaml`](butler.example.yaml) for a full annotated example.

### Authentication modes

| Mode | Accepted auth | Required config |
|---|---|---|
| `api_key` (default) | API keys only | ≥1 client |
| `jwt_standalone` | JWTs only | ≥1 user, `jwt_secret` |
| `oidc` | OIDC tokens only | `oidc` config, ≥1 role policy |
| `either` | API keys, JWTs, and/or OIDC | ≥1 auth source configured |

**API key mode** is the simplest -- each client gets a unique key configured in `butler.yaml`. Best for service-to-service authentication.

**JWT standalone mode** adds user identity. Users authenticate via `POST /auth/login` with `{"username":"...","password":"..."}` and receive a JWT token. Users are defined in the config file with bcrypt password hashes. Best for small teams or family homelabs where you want per-user policy without an external identity provider.

**OIDC mode** delegates authentication to an external identity provider (Keycloak, Okta, Entra ID, etc.). Butler validates tokens using JWKS auto-discovery and maps roles from JWT claims to proxy policy via `role_policies`. Users are managed entirely in the IdP -- no user definitions needed in Butler's config. Best for organizations with existing identity infrastructure.

**Either mode** accepts any combination of the above. A request is authenticated if it matches any configured auth source. This lets you mix service API keys (for automated clients) with user tokens (for interactive users) on the same Butler instance.

### Configuration reference

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `listen` | string | no | `127.0.0.1:8080` | Address and port to listen on |
| `upstream` | string | yes | -- | Ollama server URL |
| `global_rate_limit` | string | no | -- | Rate limit across all clients (e.g. `"600/min"`, `"1000/hour"`) |
| `log_prompts` | bool | no | `false` | Log full prompt content at INFO level (privacy-sensitive) |
| **auth** | | | | |
| `auth.mode` | string | no | `api_key` | Authentication mode: `api_key`, `jwt_standalone`, `oidc`, or `either` |
| `auth.jwt_secret` | string | cond. | -- | JWT signing secret (≥32 chars, required for JWT modes) |
| `auth.token_expiry` | string | no | `24h` | JWT token lifetime (e.g. `"12h"`, `"7d"`) |
| **auth.oidc** | | | | |
| `auth.oidc.issuer` | string | cond. | -- | OIDC issuer URL (must be HTTPS, required for `oidc` mode) |
| `auth.oidc.client_id` | string | cond. | -- | OIDC client ID / audience claim (required for `oidc` mode) |
| `auth.oidc.role_claim_path` | string | cond. | -- | Dot-separated path to roles array in JWT claims |
| `auth.oidc.refresh_interval` | string | no | `60m` | JWKS key refresh interval |
| **clients** | | | | |
| `clients` | list | cond. | -- | At least one client for `api_key`/`either` mode |
| `clients[].name` | string | yes | -- | Client identifier (appears in logs) |
| `clients[].key` | string | yes | -- | API key for authentication (must be unique) |
| `clients[].allow_models` | list | no | `[]` (deny all) | Models this client can access |
| `clients[].deny_models` | list | no | `[]` | Models explicitly denied (checked before allowlist) |
| `clients[].rate_limit` | string | no | -- | Per-client rate limit (e.g. `"60/min"`, `"100/hour"`) |
| `clients[].max_request_bytes` | int | no | `0` (no limit) | Max request body size in bytes |
| `clients[].max_ctx` | int | no | `0` (no limit) | Max `num_ctx` value allowed |
| `clients[].max_predict` | int | no | `0` (no limit) | Max `num_predict` value allowed |
| `clients[].deny_prompt_patterns` | list | no | `[]` | Regex patterns; prompts matching any pattern are rejected |
| **users** | | | | |
| `users` | list | cond. | -- | At least one user for `jwt_standalone` mode |
| `users[].name` | string | yes | -- | Username for login and logs |
| `users[].password_hash` | string | yes | -- | bcrypt hash of password |
| `users[].allow_models` | list | no | `[]` (deny all) | Models this user can access |
| `users[].deny_models` | list | no | `[]` | Models explicitly denied |
| `users[].rate_limit` | string | no | -- | Per-user rate limit |
| `users[].max_request_bytes` | int | no | `0` (no limit) | Max request body size in bytes |
| `users[].max_ctx` | int | no | `0` (no limit) | Max `num_ctx` value allowed |
| `users[].max_predict` | int | no | `0` (no limit) | Max `num_predict` value allowed |
| `users[].deny_prompt_patterns` | list | no | `[]` | Regex patterns; prompts matching any pattern are rejected |
| **role_policies** | | | | |
| `role_policies` | map | cond. | -- | Role-to-policy mapping (required for `oidc` mode) |
| `role_policies.<role>.allow_models` | list | no | `[]` | Models this role can access |
| `role_policies.<role>.deny_models` | list | no | `[]` | Models explicitly denied (single-role only) |
| `role_policies.<role>.rate_limit` | string | no | -- | Per-role rate limit (e.g. `"120/hour"`, `"unlimited"`) |
| `role_policies.<role>.max_request_bytes` | int | no | `0` | Max request body size |
| `role_policies.<role>.max_ctx` | int | no | `0` | Max `num_ctx` value |
| `role_policies.<role>.max_predict` | int | no | `0` | Max `num_predict` value |
| `role_policies.<role>.deny_prompt_patterns` | list | no | `[]` | Regex patterns to reject prompts (single-role only) |

### Model matching rules

- `"*"` -- matches all models
- `"llama3.2"` -- matches `llama3.2`, `llama3.2:latest`, `llama3.2:7b`, or any other tag
- `"llama3.2:7b"` -- matches only the exact string `llama3.2:7b`

Evaluation order: **denylist first**, then allowlist. If a model matches no allowlist entry, the request is denied.

### Multi-role merging (OIDC)

When a user's OIDC token carries multiple roles that match configured policies, Butler merges them using **most-permissive-wins**:

| Field | Merge rule |
|---|---|
| `allow_models` | Union of all roles' models. If any role has `"*"`, result is `"*"`. |
| `rate_limit` | Most permissive wins. `"unlimited"` beats any count. Higher count beats lower. |
| `max_request_bytes` | Most permissive (highest value, or `0`=no limit wins). |
| `max_ctx` | Most permissive (highest value, or `0`=no limit wins). |
| `max_predict` | Most permissive (highest value, or `0`=no limit wins). |
| `deny_models` | Only applied when exactly one role matches. |
| `deny_prompt_patterns` | Only applied when exactly one role matches. |

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

### JWT authentication

```bash
# Login to get a JWT token
curl -X POST http://localhost:8080/auth/login \
  -d '{"username": "alice", "password": "hunter2"}'
# Returns: {"token":"eyJ..."}

# Use the token for requests
curl http://localhost:8080/api/chat \
  -H "Authorization: Bearer eyJ..." \
  -d '{"model": "llama3.2", "messages": [{"role": "user", "content": "Hello"}]}'
```

### OIDC authentication

Users authenticate with the external identity provider (e.g. Keycloak, Okta, Entra ID) and use the issued token directly with Butler:

```bash
# Use an OIDC token from your identity provider
curl http://localhost:8080/api/chat \
  -H "Authorization: Bearer eyJ..." \
  -d '{"model": "llama3.2", "messages": [{"role": "user", "content": "Hello"}]}'
```

Butler validates the token signature against the provider's JWKS, verifies `iss`, `aud`, and `exp`, extracts the user's roles from the configured claim path, and maps them to policy via `role_policies`.

**Common `role_claim_path` values by provider:**

| Provider | Claim path | Notes |
|---|---|---|
| Keycloak | `realm_access.roles` | Realm-level roles |
| Keycloak | `resource_access.butler.roles` | Client-specific roles |
| Okta | `groups` | Groups claim (must be added to token) |
| Entra ID | `roles` | App roles |

**JWKS caching behavior:**

- Keys are fetched on startup. Butler **fails closed** if the provider is unreachable at startup.
- Keys are refreshed in the background every `refresh_interval` (default: 60 minutes).
- On unknown `kid` (key rotation), Butler triggers an immediate refresh (rate-limited to once per 30 seconds).
- If a background refresh fails, Butler keeps using cached keys and logs a warning.

### Error responses

All errors are returned as JSON:

| Status | Body | Meaning |
|---|---|---|
| `401` | `{"error":"unauthorized"}` | Missing or invalid API key / JWT / OIDC token |
| `401` | `{"error":"invalid credentials"}` | Wrong username or password (`/auth/login`) |
| `403` | `{"error":"model not allowed"}` | Client not authorized for the requested model |
| `403` | `{"error":"prompt rejected"}` | Prompt matches a denied pattern |
| `400` | `{"error":"bad request"}` | Malformed request body |
| `400` | `{"error":"num_ctx N exceeds limit of M"}` | `num_ctx` exceeds per-client cap |
| `400` | `{"error":"num_predict N exceeds limit of M"}` | `num_predict` exceeds per-client cap |
| `413` | `{"error":"request too large"}` | Request body exceeds per-client size limit |
| `413` | `{"error":"request body too large"}` | Request body exceeds inspection limit (8 MiB) |
| `429` | `{"error":"rate limit exceeded"}` | Per-client or global rate limit exceeded |
| `502` | `{"error":"upstream unavailable"}` | Cannot reach Ollama |

### Supported endpoints

Butler proxies all Ollama endpoints transparently. Model-level ACL is enforced on endpoints that carry a model name in the request body:

**Inference** (checks `model` field):
`/api/chat`, `/api/generate`, `/api/embeddings`, `/api/embed`, `/v1/chat/completions`, `/v1/completions`, `/v1/embeddings`

**Management** (checks `name` field):
`/api/show`, `/api/pull`, `/api/push`, `/api/delete`, `/api/create`

**Passthrough** (auth only, no model check):
`/api/tags`, `/api/ps`, `/api/version`, `/v1/models`, and any other path

**Unauthenticated** (no auth required):
`/healthz` (health check with upstream connectivity), `/metrics` (Prometheus metrics), `/auth/login` (JWT login, returns 404 in `api_key`/`oidc` mode)

## Logging

Butler emits structured JSON logs to stdout ([12-factor](https://12factor.net/logs) style). Every proxied request produces two log lines -- one on entry and one on completion:

```json
{"time":"2025-03-05T10:30:00Z","level":"INFO","msg":"request","client":"my-app","model":"llama3.2","method":"POST","path":"/api/chat","remote":"192.168.1.50:43210"}
{"time":"2025-03-05T10:30:02Z","level":"INFO","msg":"response","client":"my-app","model":"llama3.2","path":"/api/chat","status":200,"duration_ms":2045}
```

For JWT and OIDC authenticated requests, a `"user"` field is added to identify the individual user:

```json
{"time":"2025-03-05T10:30:00Z","level":"INFO","msg":"request","client":"alice","model":"llama3.2","method":"POST","path":"/api/chat","remote":"192.168.1.50:43210","user":"alice"}
```

Denied requests are logged at `WARN` level:

```json
{"time":"2025-03-05T10:30:05Z","level":"WARN","msg":"unauthorized request","path":"/api/chat","remote":"192.168.1.99:51234"}
{"time":"2025-03-05T10:30:06Z","level":"WARN","msg":"model denied","client":"restricted-service","model":"llama3.2:70b","path":"/api/generate"}
{"time":"2025-03-05T10:30:07Z","level":"WARN","msg":"client rate limit exceeded","client":"dev-sandbox","path":"/api/generate"}
{"time":"2025-03-05T10:30:08Z","level":"WARN","msg":"prompt rejected","client":"dev-sandbox","pattern":"(?i)ignore.*instructions","path":"/api/generate"}
```

When `log_prompts: true` is set, Butler also logs the full prompt content at INFO level:

```json
{"time":"2025-03-05T10:30:00Z","level":"INFO","msg":"prompts","client":"my-app","model":"llama3.2","path":"/api/chat","prompts":["Hello, how are you?"]}
```

Butler logs to stdout. Log rotation is the responsibility of your log collector (journald, Loki, Datadog, logrotate, etc.).

## Development

### Prerequisites

- Go 1.24+
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
cmd/butler/main.go               Entry point
internal/auth/jwt.go             JWT issuance, validation, bcrypt password check
internal/auth/oidc.go            OIDC discovery, JWKS fetch/cache, token validation, role extraction
internal/config/config.go        YAML config loading, validation, model ACL, rate specs, Subject
internal/proxy/proxy.go          Reverse proxy, auth, rate limiting, input filtering, response logging
internal/proxy/login.go          /auth/login POST handler
internal/proxy/model.go          Request body inspection (model, prompts, num_ctx, num_predict)
internal/proxy/ratelimit.go      Fixed-window rate limiter
internal/proxy/metrics.go        Hand-rolled Prometheus metrics collector
internal/proxy/health.go         /healthz health check handler
```

### Running tests

```bash
go test -race -cover ./...
```

Current coverage: ~92% for `config`, ~79% for `auth`, ~94% for `proxy`.

### CI/CD

CI runs on both GitHub Actions (`.github/workflows/ci.yml`) and GitLab CI (`.gitlab-ci.yml`):

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

See [`docs/BUTLER_ROADMAP.md`](docs/BUTLER_ROADMAP.md) for the full roadmap.

**Completed:** Core proxy, input filtering, rate limiting, observability, JWT standalone auth, per-user policy, OIDC federation with JWKS auto-discovery and role-to-policy mapping.

**Upcoming:**

- Per-user token budgets (daily/monthly ceiling on tokens consumed)
- Context isolation (`/api/generate` context blob tagging, `X-Butler-User` header injection)
- Time-of-day restrictions, config hot-reload, mTLS, webhook notifications

## License

Apache 2.0
