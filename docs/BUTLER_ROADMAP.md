# Butler — Roadmap

**Butler** is an access-control reverse proxy for Ollama. It sits between clients and the Ollama API to handle per-client authorization, model restrictions, input filtering, and usage logging — things Ollama intentionally does not handle. Think of it as a trusted manager for your local LLM infrastructure: you configure it once and it quietly handles access, policy, and observability so you don't have to.

## Problem

Ollama has no authentication or authorization. Any client that can reach port 11434 can use any model with any parameters. This is fine for single-user local use, but breaks down when:

- Multiple services share one Ollama server and you want to restrict which service can use which models
- You expose Ollama to a LAN or homelab and want per-user limits
- You need to log or audit which clients are sending what prompts
- You want to filter or reject prompts that match certain patterns before they reach a model
- You want rate limiting to prevent one runaway service from starving others

## Design Principles

- **Zero changes to Ollama** — proxy sits in front, clients point at the proxy instead of Ollama directly
- **Zero changes to clients** — proxy speaks the same API (both native `/api/*` and OpenAI-compatible `/v1/*`)
- **Config file, not a database** — policy is a YAML/TOML file, version-controlled alongside infra
- **Single static binary** — no runtime dependencies, easy to deploy on any Linux box
- **Fail closed** — if the proxy cannot verify a client, the request is denied

## Architecture

```
Client A ──┐
Client B ──┼──▶ [butler :8080] ──▶ [ollama :11434]
Client C ──┘
            auth / ACL / filter / log
```

Clients connect to Butler. Butler authenticates the request, checks the ACL, optionally inspects the request body, then proxies to Ollama. Responses are streamed back unmodified.

## Features

### Phase 1 — Core Proxy (MVP)

- [x] Transparent reverse proxy for Ollama's native API (`/api/*`) and OpenAI-compatible API (`/v1/*`)
- [x] Streaming support (SSE passthrough for both API styles)
- [x] API key authentication via `Authorization: Bearer <key>` header
- [x] Per-key model allowlist (e.g., key A can use `llama3.2` and `mistral`, key B can only use `llama3.2`)
- [x] Per-key model denylist
- [x] Deny by default — if a key has no allowlist entry for a model, the request is rejected
- [x] Structured request/response logging (JSON lines) with client identity, model, timestamp, duration
- [x] YAML config file for all policy

Example config:

```yaml
listen: "127.0.0.1:8080"
upstream: "http://127.0.0.1:11434"

clients:
  - name: linkedin-copilot
    key: "sk-abc123..."
    allow_models: ["llama3.2", "mistral"]

  - name: open-webui
    key: "sk-def456..."
    allow_models: ["*"]

  - name: dev-sandbox
    key: "sk-ghi789..."
    allow_models: ["llama3.2:1b"]
    rate_limit: "10/min"
```

### Phase 2 — Input Filtering and Rate Limiting

- [ ] Regex-based prompt rejection (block requests matching patterns before they reach the model)
- [ ] Per-key rate limiting (requests per minute/hour)
- [ ] Per-key context length cap (reject requests with `num_ctx` above a threshold)
- [ ] Per-key max tokens cap (`num_predict` ceiling)
- [ ] Global rate limiting (protect Ollama from total overload)
- [ ] Request size limit (reject abnormally large payloads)

### Phase 3 — Observability

- [ ] Prometheus metrics endpoint (`/metrics`) — request count, latency histogram, tokens generated, per-model and per-client breakdowns
- [ ] Health check endpoint (`/healthz`) that also checks upstream Ollama connectivity
- [ ] Optional request body logging (full prompts) with a separate config flag and privacy warning
- [ ] Log rotation / structured log output compatible with journald, Loki, etc.

### Phase 4 — Multi-User Identity and Context Isolation

The MVP uses per-service API keys. This phase adds per-user identity so multiple family members / homelab users can share the same Ollama instance with isolated access.

**Authentication**

Two modes: standalone (built-in user management) or federated (delegate to an external OIDC provider like Vinsium's Keycloak).

- [ ] JWT authentication — proxy validates JWTs (HS256/RS256) in `Authorization: Bearer <token>` header
- [ ] **Standalone mode**: built-in `/auth/login` endpoint — accepts username/password, returns a signed JWT (users defined in config)
- [ ] **OIDC mode**: proxy acts as an OIDC relying party — validates tokens issued by an external provider (Keycloak, Okta, Entra ID, etc.) using JWKS discovery
- [ ] OIDC auto-discovery via `/.well-known/openid-configuration` from the issuer URL
- [ ] Role-to-policy mapping — map OIDC roles/groups from JWT claims to proxy policy (e.g., Vinsium `admin` → all models, `viewer` → restricted models)
- [ ] Configurable role claim path (`realm_access.roles` for Keycloak, `groups` for Okta, etc.) — same approach Vinsium uses
- [ ] JWT claims carry user identity (`sub`), allowed models, rate-limit tier, and expiry
- [ ] API keys (Phase 1) remain supported — a key can optionally be scoped to a user identity
- [ ] Configurable auth mode per-listener: `api_key`, `jwt_standalone`, `oidc`, or `either`

**Per-User Policy**

- [ ] Per-user model allowlist/denylist (independent of per-service restrictions — both must pass)
- [ ] Per-user rate limits (e.g., kids get 20 req/hr, adults unlimited)
- [ ] Per-user token budget (daily/monthly ceiling on `eval_count` tokens consumed)
- [ ] Usage tracking by user identity in structured logs (`"user": "alice"` alongside `"client": "linkedin-copilot"`)

**Context Isolation**

Ollama's `/api/chat` is stateless — full message history is sent per-request, so there is no server-side context to leak. However, two areas still need isolation:

- [ ] **`/api/generate` context blobs** — Ollama returns an opaque `context` array that clients pass back for continuation. The proxy can tag these with the user identity and reject mismatched context blobs (prevents user A from resuming user B's conversation).
- [ ] **User-identity header injection** — proxy injects `X-Butler-User: <sub>` header into upstream requests. Downstream clients that maintain server-side state (e.g., a shared Open WebUI instance) can use this to partition conversations per user.
- [ ] **System prompt namespacing** — optionally prepend a per-user system prompt prefix (e.g., `"You are assisting alice."`) so the model's responses are contextually appropriate even if clients don't handle multi-tenancy themselves.
- [ ] **Log isolation** — full-prompt audit logs (Phase 3) are partitioned by user, ensuring one user's prompts are not visible in another's log stream.

**Example config — standalone mode (no external IdP):**

```yaml
listen: "0.0.0.0:8080"
upstream: "http://127.0.0.1:11434"

auth:
  mode: jwt_standalone
  jwt_secret: "${JWT_SECRET}"
  token_expiry: "24h"

users:
  - name: stefan
    password_hash: "$2b$12$..."    # bcrypt
    allow_models: ["*"]
    rate_limit: "unlimited"

  - name: kid1
    password_hash: "$2b$12$..."
    allow_models: ["llama3.2"]
    rate_limit: "20/hr"
    max_tokens_per_day: 50000

clients:
  - name: linkedin-copilot
    key: "sk-abc123..."
    allow_models: ["llama3.2", "mistral"]
```

**Example config — OIDC mode (federated via Vinsium/Keycloak):**

```yaml
listen: "0.0.0.0:8080"
upstream: "http://127.0.0.1:11434"

auth:
  mode: oidc
  oidc:
    issuer: "https://id.vinsium.local/realms/vinsium"
    client_id: "butler"
    # JWKS fetched automatically via .well-known/openid-configuration
    role_claim_path: "realm_access.roles"   # Keycloak default

# Map OIDC roles to proxy policy — users are managed in Keycloak, not here
role_policies:
  admin:
    allow_models: ["*"]
    rate_limit: "unlimited"
  operator:
    allow_models: ["*"]
    rate_limit: "120/hr"
    max_tokens_per_day: 500000
  viewer:
    allow_models: ["llama3.2:1b"]
    rate_limit: "20/hr"
    max_tokens_per_day: 25000

# Per-user overrides (by OIDC `sub` or `preferred_username`) — optional
user_overrides:
  kid1:
    allow_models: ["llama3.2"]
    rate_limit: "10/hr"

clients:
  - name: linkedin-copilot
    key: "sk-abc123..."
    allow_models: ["llama3.2", "mistral"]

  - name: open-webui
    key: "sk-def456..."
    allow_models: ["*"]
    require_user_auth: true         # must have both service key AND user JWT
```

### Phase 5 — Advanced Policy

- [ ] Time-of-day restrictions (e.g., dev-sandbox only available during business hours)
- [ ] Per-key VRAM budget / concurrency limit (prevent one client from loading too many models)
- [ ] Webhook notifications on policy violations (Slack, Discord, generic HTTP)
- [ ] Config hot-reload (SIGHUP or file watch) without restarting the proxy
- [ ] mTLS client authentication as an alternative to API keys

## Tech Choices

- **Language**: Go — single binary, stdlib `net/http/httputil.ReverseProxy` gets the MVP proxy running fast, same language as Ollama itself
- **Repo**: `gitlab.com/sfoerster/butler` — standalone, independent release cycle
- **License**: Apache 2.0 (open-core base); Vinsium integration and enterprise features under proprietary license
- **Config**: YAML with env var interpolation (`${VAR}` syntax) — keys can be inline for dev or env-referenced for production. Mounted as a Docker volume.
- **Logging**: JSON lines to stdout (12-factor style, pipe to whatever you want)
- **Deployment**: Docker-first
  - Multi-stage Dockerfile (build → scratch/distroless)
  - `docker-compose.yml` bundling Butler alongside Ollama for turnkey homelab setup
  - Config and secrets mounted as volumes / injected via env vars
  - Health check via `HEALTHCHECK` directive pointing at `/healthz`
  - Optional: publish to GHCR / Docker Hub for easy pulls

## Competitive Positioning

### LiteLLM Proxy

[LiteLLM](https://github.com/BerriAI/litellm) is the closest existing project — an open-source Python proxy (MIT-licensed core) that routes to 100+ LLM providers including Ollama. It has API key auth, rate limiting, usage tracking, and an enterprise tier with SSO/OIDC (paid license from BerriAI).

**Why it's not the right tool for this problem:**

- **API translation, not transparent proxy** — LiteLLM converts everything to OpenAI-compatible format. Clients cannot use Ollama's native `/api/chat` or `/api/generate` endpoints. This proxy passes them through transparently, supporting both native and OpenAI-compat styles.
- **Cloud-provider-centric** — designed to unify OpenAI, Bedrock, Azure, Vertex API spend. Local Ollama is a side feature, not the focus.
- **Heavy runtime** — Python app requiring PostgreSQL and Redis. Not suitable for a single-container homelab or edge deployment.
- **No compliance story** — no audit trail with tamper detection, no SBOM, no FIPS-capable crypto, no zero-trust networking. Built for SaaS dev teams, not regulated on-prem environments.
- **Enterprise auth is vendor-locked** — SSO/OIDC requires a paid license key from BerriAI. Customers depend on a third party for core security features.

**Differentiation summary:**

| | LiteLLM | Butler |
|---|---|---|
| Primary audience | Dev teams managing cloud LLM API spend | Orgs running local/private LLM infrastructure |
| Architecture | Python + PostgreSQL + Redis | Single Go binary, zero external deps |
| Ollama native API | Translates to OpenAI format | Transparent passthrough |
| Auth (SSO/OIDC) | Paid enterprise license | Built-in, open-core |
| Compliance | Basic logging | Audit trail, SBOM, FIPS-capable |
| Deployment | Multi-container stack | Single container |
| Vinsium integration | None | Native OIDC federation, shared identity |

**Positioning: "LiteLLM is for teams routing to cloud APIs. Butler is for teams running their own models."**

### Other Alternatives

- **Nginx/Caddy + basic auth** — works for simple API key gating, but no model-level ACLs, no per-user policy, no usage tracking, no OIDC. Requires custom Lua/middleware for anything beyond trivial auth.
- **Ollama native auth** — does not exist. Ollama intentionally delegates access control to the deployment layer. If they add it, it will likely be minimal (single shared token), not multi-user with RBAC.
- **Open WebUI's built-in auth** — scoped to Open WebUI only, not a general-purpose proxy for arbitrary Ollama clients.

## Non-Goals

- Not a model registry or model management layer — Ollama handles that
- Not a prompt rewriting / guardrails system — there are dedicated tools for that (e.g., NeMo Guardrails)
- Not a multi-node load balancer — one proxy talks to one Ollama instance
- Not a user-facing UI — this is infrastructure, configured via files

## Context Isolation — Why the Proxy Layer is Enough

A key insight: most Ollama clients are **stateless at the protocol level**. Ollama's `/api/chat` requires the full message array on every request — there is no server-side session. So "context isolation" is not about partitioning hidden state inside Ollama; it's about:

1. **Identity** — knowing *who* made a request (solved by JWT auth in Phase 4)
2. **Authorization** — enforcing what each user can do (solved by per-user policy in Phase 4)
3. **Audit** — logging requests with user identity so prompts don't get mixed in logs
4. **Edge cases** — the `/api/generate` endpoint's `context` continuation blob is the one piece of opaque state that could theoretically leak between users if a client misbehaves

For client apps like linkedin-copilot (a Chrome extension), each user already has fully isolated state in their own browser profile. The proxy just needs to authenticate them and enforce limits — it doesn't need to store or manage their conversation context.

For shared server-side apps (e.g., a single Open WebUI instance used by the family), the proxy injects user identity via `X-Butler-User` and the app is responsible for partitioning its own database. The proxy can additionally require dual auth (`require_user_jwt: true`) — both the service API key and a user JWT must be present.

## Open Questions

- Should Butler support multiple upstream Ollama instances (basic round-robin)? Or keep it single-upstream and let a real load balancer handle that?
- Is there value in a `--dry-run` mode that logs what would be allowed/denied without actually proxying?
- Should Butler handle user registration, or is users-in-config-file sufficient for a homelab/family scenario?
- For dual auth (service key + user JWT), should the JWT be a separate header (e.g., `X-User-Token`) or should both be packed into the `Authorization` header with a compound scheme?
- Should token budgets reset on a calendar boundary (midnight) or on a rolling window?
