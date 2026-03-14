# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- OIDC federation (`auth.mode: oidc`) -- validate tokens from external identity providers (Keycloak, Okta, Entra ID) using JWKS auto-discovery
- Role-to-policy mapping (`role_policies`) -- map OIDC roles/groups to proxy policy without a database
- Configurable role claim path (`oidc.role_claim_path`) for different IdP conventions (e.g. `realm_access.roles`, `groups`)
- JWKS caching with background refresh and rate-limited on-demand refresh for key rotation
- Multi-role merging with most-permissive-wins semantics (union of allowed models, highest rate limit)
- `"unlimited"` rate limit value for role policies
- `either` mode now accepts OIDC as an auth source alongside API keys and JWTs
- JWT standalone authentication (`auth.mode: jwt_standalone`) with built-in `/auth/login` endpoint
- Per-user identity with configurable model ACLs, rate limits, context caps, and prompt filtering
- `either` auth mode accepting both API keys and JWT tokens
- `Subject` abstraction unifying Client and User policy enforcement
- User identity (`"user"` field) in structured log output for JWT-authenticated requests
- Prometheus-compatible `/metrics` endpoint with request counters, rejection counters, and latency histograms (hand-rolled, no external dependencies)
- `/healthz` health check endpoint that verifies upstream Ollama connectivity
- Optional full-prompt logging (`log_prompts: true`) for audit and debugging (disabled by default)
- Per-client rate limiting (`rate_limit` field, e.g. `"60/min"` or `"100/hour"`)
- Global rate limiting (`global_rate_limit` field) to protect Ollama from total overload
- Regex-based prompt rejection (`deny_prompt_patterns`) to block requests matching configurable patterns
- Per-client context length cap (`max_ctx`) to reject requests with `num_ctx` above a threshold
- Per-client token prediction cap (`max_predict`) to enforce a `num_predict` ceiling
- Per-client request size limit (`max_request_bytes`) to reject abnormally large payloads

## [0.1.0] - 2025-03-05

### Added

- Transparent reverse proxy for Ollama native API (`/api/*`) and OpenAI-compatible API (`/v1/*`)
- Streaming support (SSE and chunked responses passed through without buffering)
- API key authentication via `Authorization: Bearer <key>` header
- Per-key model allowlist and denylist with deny-by-default policy
- Structured JSON request/response logging with client identity, model, status, and duration
- YAML configuration with `${ENV_VAR}` interpolation for secrets
- Docker and Docker Compose deployment support
- GitLab CI pipeline (lint, test, build)
