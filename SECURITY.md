# Security Policy

## Reporting a Vulnerability

Do not open public issues for suspected vulnerabilities.

Email sfoerster@gmail.com with the subject line `[Butler Security]` and include:

1. A clear description of the issue and impact.
2. Reproduction steps or a minimal proof of concept.
3. Affected versions/commit hashes.
4. Suggested mitigation, if known.

You should receive an acknowledgment within 72 hours. Please allow reasonable time for a fix before public disclosure.

## Scope

Security-sensitive components include:

1. **Authentication and API key handling** -- key validation, constant-time comparison, and rejection of unauthenticated requests (`internal/proxy/proxy.go`).
2. **Model ACL enforcement** -- allowlist/denylist evaluation and deny-by-default logic (`internal/config/config.go`).
3. **Request body inspection** -- model name extraction, prompt collection, and parameter parsing from incoming request bodies (`internal/proxy/model.go`).
4. **Input filtering** -- regex-based prompt rejection, context length and token caps, and request size limits (`internal/proxy/proxy.go`, `internal/config/config.go`).
5. **Rate limiting** -- per-client and global fixed-window rate limiting (`internal/proxy/ratelimit.go`).
6. **Reverse proxy transport** -- upstream request forwarding, header handling, and response streaming (`internal/proxy/proxy.go`).
7. **Configuration loading** -- YAML parsing and `${ENV_VAR}` interpolation of secrets (`internal/config/config.go`).

## Hardening Expectations

1. Butler fails closed -- unauthenticated or unauthorized requests are rejected, never proxied.
2. API keys should be loaded via environment variable interpolation (`${ENV_VAR}`), not hardcoded in config files committed to version control.
3. Bind `listen` to `127.0.0.1` unless Butler is behind a TLS-terminating reverse proxy or firewall. Do not expose Butler directly to the public internet without TLS.
4. Use unique, high-entropy API keys per client. Rotate keys periodically.
5. Restrict model allowlists to the minimum set each client requires. Avoid `"*"` wildcards in production.
