# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

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
