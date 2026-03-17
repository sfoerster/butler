# Security Policy

## Reporting a Vulnerability

Do not open public issues for suspected vulnerabilities.

Email `cve@stormblest.com` with the subject line `[Butler Security]` and include:

1. A clear description of the issue and impact.
2. Reproduction steps or a minimal proof of concept.
3. Affected versions/commit hashes.
4. Suggested mitigation, if known.

You should receive an acknowledgment within 72 hours. Please allow reasonable time for a fix before public disclosure.

## Scope

Security-sensitive components include:

1. **Authentication and API key handling** -- key validation, constant-time comparison, and rejection of unauthenticated requests (`internal/proxy/proxy.go`).
2. **JWT token handling** -- HMAC-based JWT issuance, validation, and expiration enforcement (`internal/auth/jwt.go`).
3. **OIDC federation** -- OIDC discovery, JWKS fetching/caching, asymmetric token signature validation, issuer/audience verification, role extraction (`internal/auth/oidc.go`).
4. **Model ACL enforcement** -- allowlist/denylist evaluation, deny-by-default logic, and multi-role policy merging (`internal/config/config.go`).
5. **Request body inspection** -- model name extraction, prompt collection, and parameter parsing from incoming request bodies (`internal/proxy/model.go`).
6. **Input filtering** -- regex-based prompt rejection, context length and token caps, and request size limits (`internal/proxy/proxy.go`, `internal/config/config.go`).
7. **Rate limiting** -- per-client, per-user, and global fixed-window rate limiting (`internal/proxy/ratelimit.go`).
8. **Reverse proxy transport** -- upstream request forwarding, header handling, and response streaming (`internal/proxy/proxy.go`).
9. **Configuration loading** -- YAML parsing and `${ENV_VAR}` interpolation of secrets (`internal/config/config.go`).

## Unauthenticated Endpoints

The `/healthz`, `/metrics`, and `/auth/login` endpoints do not require authentication. `/healthz` and `/metrics` are intended for load balancers and monitoring systems. `/auth/login` accepts username/password credentials and returns a JWT token (returns 404 when auth mode is `api_key`). If your deployment requires these to be restricted, place Butler behind a reverse proxy that limits access to these paths.

## JWT Secret

When using `jwt_standalone` or `either` auth mode, the `jwt_secret` must be at least 32 characters. Use a cryptographically random string. Load it via environment variable interpolation (`${JWT_SECRET}`), not hardcoded in config files.

## OIDC Security

When using `oidc` or `either` auth mode with OIDC federation:

1. The `oidc.issuer` must use HTTPS. HTTP issuers are rejected at config validation time.
2. JWKS keys are fetched on startup (fail closed if the provider is unreachable) and refreshed on a configurable interval (default 60 minutes).
3. On unknown `kid` (key rotation), Butler triggers an immediate JWKS refresh, rate-limited to once per 30 seconds to prevent abuse.
4. If a background JWKS refresh fails, Butler continues using cached keys and logs a warning. This prevents transient provider outages from breaking authentication.
5. HMAC (`HS256`) and `none` signing algorithms are rejected — only asymmetric algorithms (RSA, ECDSA) are accepted for OIDC tokens.
6. Issuer (`iss`) and audience (`aud`) claims are validated against the configured values. Token expiration (`exp`) is required.

## Hardening Expectations

1. Butler fails closed -- unauthenticated or unauthorized requests are rejected, never proxied.
2. API keys should be loaded via environment variable interpolation (`${ENV_VAR}`), not hardcoded in config files committed to version control.
3. Bind `listen` to `127.0.0.1` unless Butler is behind a TLS-terminating reverse proxy or firewall. Do not expose Butler directly to the public internet without TLS.
4. Use unique, high-entropy API keys per client. Rotate keys periodically.
5. Restrict model allowlists to the minimum set each client requires. Avoid `"*"` wildcards in production.
