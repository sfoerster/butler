# Quick Start Guide

This guide walks you through setting up Butler with each authentication mode. Pick the one that fits your use case:

- **[API key mode](#api-key-mode)** -- simplest setup, best for service-to-service access control
- **[JWT standalone mode](#jwt-standalone-mode)** -- per-user auth with built-in login, no external IdP needed
- **[OIDC mode](#oidc-mode)** -- federated auth via Keycloak, Okta, Entra ID, or any OIDC provider
- **[Either mode](#either-mode)** -- combine API keys, JWT users, and/or OIDC in a single instance

## Prerequisites

- [Go 1.24+](https://go.dev/dl/) (to build from source) or Docker
- A running [Ollama](https://ollama.com) instance (default: `http://127.0.0.1:11434`)

## Build

```bash
git clone https://github.com/sfoerster/butler.git
cd butler
make build
```

Or with Docker:

```bash
docker build -t butler .
```

## API Key Mode

The default mode. Each client gets a unique API key. Best for service-to-service authentication.

### 1. Create config

```yaml
# butler.yaml
listen: "127.0.0.1:8080"
upstream: "http://127.0.0.1:11434"

clients:
  - name: my-app
    key: "sk-change-me-to-something-random"
    allow_models: ["llama3.2", "mistral"]

  - name: admin
    key: "sk-admin-change-me-too"
    allow_models: ["*"]
    rate_limit: "600/min"
```

For production, use environment variable interpolation for keys:

```yaml
clients:
  - name: my-app
    key: "${MY_APP_KEY}"
    allow_models: ["llama3.2"]
```

### 2. Start Butler

```bash
./butler -config butler.yaml
```

### 3. Test it

```bash
# Should succeed
curl http://localhost:8080/api/chat \
  -H "Authorization: Bearer sk-change-me-to-something-random" \
  -d '{"model": "llama3.2", "messages": [{"role": "user", "content": "Hello"}]}'

# Should fail (wrong key)
curl http://localhost:8080/api/chat \
  -H "Authorization: Bearer wrong-key" \
  -d '{"model": "llama3.2", "messages": [{"role": "user", "content": "Hello"}]}'
# Returns: {"error":"unauthorized"}

# Should fail (model not allowed)
curl http://localhost:8080/api/chat \
  -H "Authorization: Bearer sk-change-me-to-something-random" \
  -d '{"model": "gpt-4", "messages": [{"role": "user", "content": "Hello"}]}'
# Returns: {"error":"model not allowed"}
```

### 4. Add restrictions (optional)

```yaml
clients:
  - name: restricted-app
    key: "${RESTRICTED_KEY}"
    allow_models: ["llama3.2:1b"]
    rate_limit: "10/min"              # Max 10 requests per minute
    max_request_bytes: 1048576        # Max 1 MiB request body
    max_ctx: 2048                     # Max context window
    max_predict: 256                  # Max output tokens
    deny_prompt_patterns:             # Block matching prompts
      - "(?i)ignore.*instructions"
      - "(?i)system.*prompt"
```

## JWT Standalone Mode

Adds per-user identity with a built-in login endpoint. Users are defined in the config file with bcrypt password hashes. No external identity provider needed.

### 1. Generate password hashes

```bash
# Using htpasswd (install: apt install apache2-utils)
htpasswd -nbBC 10 "" 'your-password-here' | cut -d: -f2

# Or using Python
python3 -c "import bcrypt; print(bcrypt.hashpw(b'your-password-here', bcrypt.gensalt(10)).decode())"
```

### 2. Generate a JWT secret

```bash
# Generate a random 64-character secret
openssl rand -base64 48
```

### 3. Create config

```yaml
# butler.yaml
listen: "127.0.0.1:8080"
upstream: "http://127.0.0.1:11434"

auth:
  mode: jwt_standalone
  jwt_secret: "${JWT_SECRET}"     # Set via env var, must be ≥32 characters
  token_expiry: "24h"             # Optional, default: 24h

users:
  - name: alice
    password_hash: "$2b$10$..."   # Paste the bcrypt hash here
    allow_models: ["*"]

  - name: kid1
    password_hash: "$2b$10$..."
    allow_models: ["llama3.2"]
    rate_limit: "20/hour"
    max_ctx: 2048
    max_predict: 256
```

### 4. Start Butler

```bash
export JWT_SECRET="your-64-character-secret-here-replace-this-with-real-one-now"
./butler -config butler.yaml
```

### 5. Test it

```bash
# Login
curl -X POST http://localhost:8080/auth/login \
  -d '{"username": "alice", "password": "your-password-here"}'
# Returns: {"token":"eyJ..."}

# Use the token
TOKEN="eyJ..."  # paste the token from above
curl http://localhost:8080/api/chat \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"model": "llama3.2", "messages": [{"role": "user", "content": "Hello"}]}'
```

## OIDC Mode

Delegates authentication to an external identity provider. Butler validates tokens using JWKS auto-discovery and maps roles from JWT claims to proxy policy. Users are managed entirely in the IdP.

### 1. Configure your identity provider

**Keycloak example:**

1. Create a client called `butler` in your Keycloak realm.
2. Set the client access type to "confidential" or "public" depending on your setup.
3. Ensure the token includes realm roles (default for Keycloak).
4. Note your realm's issuer URL: `https://keycloak.example.com/realms/your-realm`

**Okta example:**

1. Create an application in Okta.
2. Add a `groups` claim to the ID token (Security > API > Authorization Servers > Claims).
3. Note your issuer URL: `https://your-org.okta.com/oauth2/default`

**Entra ID (Azure AD) example:**

1. Register an application in Entra ID.
2. Define app roles in the application manifest.
3. Assign roles to users/groups.
4. Note your issuer URL: `https://login.microsoftonline.com/{tenant-id}/v2.0`

### 2. Create config

```yaml
# butler.yaml
listen: "127.0.0.1:8080"
upstream: "http://127.0.0.1:11434"

auth:
  mode: oidc
  oidc:
    issuer: "https://keycloak.example.com/realms/your-realm"
    client_id: "butler"
    role_claim_path: "realm_access.roles"   # Keycloak default
    # refresh_interval: "60m"               # JWKS refresh interval (default: 60m)

# Map IdP roles to proxy policy
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
    max_predict: 256
    deny_prompt_patterns:
      - "(?i)ignore.*instructions"
```

**Common `role_claim_path` values:**

| Provider | Claim path |
|---|---|
| Keycloak (realm roles) | `realm_access.roles` |
| Keycloak (client roles) | `resource_access.butler.roles` |
| Okta | `groups` |
| Entra ID | `roles` |

### 3. Start Butler

```bash
./butler -config butler.yaml
```

Butler will fetch the OIDC discovery document and JWKS from the issuer on startup. If the provider is unreachable, Butler will refuse to start (fail closed).

### 4. Test it

```bash
# Get a token from your IdP (example using Keycloak direct access grant)
TOKEN=$(curl -s -X POST \
  "https://keycloak.example.com/realms/your-realm/protocol/openid-connect/token" \
  -d "client_id=butler" \
  -d "username=alice" \
  -d "password=hunter2" \
  -d "grant_type=password" | jq -r '.access_token')

# Use the token with Butler
curl http://localhost:8080/api/chat \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"model": "llama3.2", "messages": [{"role": "user", "content": "Hello"}]}'
```

### Multi-role behavior

If a user has multiple roles that match configured policies (e.g. both `operator` and `viewer`), Butler merges them using **most-permissive-wins**:

- **Model access**: union of all roles' allowed models
- **Rate limit**: highest rate wins; `"unlimited"` beats any count
- **Caps** (`max_ctx`, `max_predict`, `max_request_bytes`): highest value wins; `0` (no limit) beats any value
- **Deny lists and patterns**: only applied when exactly one role matches (to avoid conflicting restrictions)

## Either Mode

Combines multiple auth methods on a single Butler instance. Useful when you have both automated services (API keys) and interactive users (JWT or OIDC).

### Example: API keys + OIDC

```yaml
# butler.yaml
listen: "127.0.0.1:8080"
upstream: "http://127.0.0.1:11434"

auth:
  mode: either
  oidc:
    issuer: "https://keycloak.example.com/realms/your-realm"
    client_id: "butler"
    role_claim_path: "realm_access.roles"

# Service accounts (API keys)
clients:
  - name: batch-processor
    key: "${BATCH_KEY}"
    allow_models: ["llama3.2"]
    rate_limit: "600/min"

  - name: monitoring
    key: "${MONITORING_KEY}"
    allow_models: ["*"]

# User roles (OIDC)
role_policies:
  admin:
    allow_models: ["*"]
  viewer:
    allow_models: ["llama3.2:1b"]
    rate_limit: "20/hour"
```

### Example: API keys + JWT standalone

```yaml
# butler.yaml
listen: "127.0.0.1:8080"
upstream: "http://127.0.0.1:11434"

auth:
  mode: either
  jwt_secret: "${JWT_SECRET}"

clients:
  - name: automated-service
    key: "${SERVICE_KEY}"
    allow_models: ["llama3.2"]

users:
  - name: alice
    password_hash: "$2b$10$..."
    allow_models: ["*"]
```

Both API key and JWT token requests are accepted. Butler tries each auth method in order: API key lookup, then JWT validation, then OIDC validation.

## Health Check and Metrics

Butler exposes two unauthenticated endpoints for operational use:

```bash
# Health check (verifies upstream Ollama connectivity)
curl http://localhost:8080/healthz
# Returns: {"status":"healthy"}

# Prometheus metrics
curl http://localhost:8080/metrics
```

These endpoints are suitable for load balancer health checks and monitoring systems. See [SECURITY.md](../SECURITY.md) for guidance on restricting access if needed.

## What's Next

- See the [README](../README.md) for the full configuration reference
- See [SECURITY.md](../SECURITY.md) for hardening guidance
- See [`butler.example.yaml`](../butler.example.yaml) for a fully annotated config template
- See [`BUTLER_ROADMAP.md`](BUTLER_ROADMAP.md) for upcoming features
