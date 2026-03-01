# Go Implementation Design — Claude Code Proxy

**Date**: 2026-03-01
**Status**: Approved
**Type**: Enhanced rewrite (all features + improvements)

## Summary

Rewrite the Node.js Claude Code Proxy in Go, living in a `go/` subdirectory alongside the existing code. The Go version uses the standard library for HTTP (`net/http`), a single external dependency (`gopkg.in/yaml.v3`), and produces a static binary suitable for minimal Docker images.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| HTTP framework | `net/http` stdlib | Zero dependencies, idiomatic, Go 1.22+ path params |
| Architecture | Flat internal packages | Clean separation without over-abstraction |
| Config format | YAML + env vars + CLI flags | Structured, 12-factor friendly, easy overrides |
| Repo location | `go/` subdirectory | Keep Node.js code for reference |
| Logging | `log/slog` | Structured, stdlib, JSON output option |
| Static assets | `go:embed` | Single binary deployment, no external files |

## Project Layout

```
go/
├── cmd/claude-code-proxy/
│   └── main.go                      # CLI flags, config loading, server startup
├── internal/
│   ├── config/
│   │   └── config.go                # YAML parsing, env vars, flag overrides, defaults
│   ├── proxy/
│   │   └── proxy.go                 # HTTP handler for /v1/messages, streaming
│   ├── oauth/
│   │   └── oauth.go                 # PKCE flow, token exchange, refresh, storage
│   ├── auth/
│   │   └── auth.go                  # Token resolver (priority: header > OAuth > CLI)
│   ├── preset/
│   │   └── preset.go               # Load/cache presets, inject into request body
│   ├── transform/
│   │   └── transform.go            # System prompt injection, TTL strip, sampling filter
│   └── logger/
│       └── logger.go               # slog wrapper with configurable level
├── static/
│   ├── login.html                   # OAuth login page (embedded)
│   └── callback.html               # Success page (embedded)
├── presets/
│   └── pyrite.json                  # Preset (embedded)
├── config.yaml                      # Default configuration
├── go.mod
├── Dockerfile                       # Multi-stage build
├── Makefile
└── README.md
```

## Configuration

### config.yaml

```yaml
server:
  port: 42069
  host: ""              # Auto-detect: 127.0.0.1 native, 0.0.0.0 Docker

logging:
  level: info           # trace, debug, info, warn, error

proxy:
  filter_sampling_params: false
  strip_ttl: true

auth:
  auto_open_browser: true
  fallback_to_claude_code: true
```

### Resolution order (later wins)

1. Built-in defaults
2. `config.yaml` (or `--config path`)
3. Environment variables: `CCP_SERVER_PORT`, `CCP_LOG_LEVEL`, etc.
4. CLI flags: `--port`, `--host`, `--log-level`

## Endpoints

### OAuth routes

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/auth/login` | GET | Serves login.html |
| `/auth/get-url` | GET | Generates PKCE + returns auth URL |
| `/auth/callback` | GET | Handles code+state exchange |
| `/auth/status` | GET | Returns auth status JSON |
| `/auth/logout` | GET | Clears tokens |
| `/health` | GET | Health check |

### Proxy routes

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/v1/messages` | POST | Claude API proxy |
| `/v1/{preset}/messages` | POST | Proxy with preset injection |

All endpoints support OPTIONS with CORS headers.

## OAuth & Authentication

### PKCE flow

Same as Node.js implementation:
- `crypto/rand` + `crypto/sha256` for code_verifier/challenge
- In-memory state map with 10-min TTL, 60s cleanup sweep
- Same OAuth config (client_id, authorize/token URLs, redirect_uri, scopes)

### Token storage

- Path: `~/.claude-code-proxy/tokens.json`
- Format: `{access_token, refresh_token, expires_at}`
- File permissions: `0600`
- Compatible with Node.js version (same path/format)

### Token resolver priority

1. `x-api-key` header from incoming request
2. OAuth tokens (auto-refresh with 1-min buffer)
3. Claude Code CLI credentials (`~/.claude/.credentials.json`)

### Enhancement: concurrent refresh protection

Use `sync.Mutex` + single-flight pattern — multiple simultaneous 401s trigger only one refresh HTTP call.

## Proxy & Streaming

### Request flow

```
Client -> Parse JSON body -> Transform -> Inject Preset -> Build upstream request -> Anthropic API
                                                                                        |
Client <- Stream SSE / Buffer JSON <- Auto-retry on 401 <--------------------------Response
```

### Streaming

`io.Copy` with `http.Flusher` for SSE passthrough. Go handles backpressure natively.

### Request transformations

1. Prepend Claude Code system prompt: `"You are Claude Code, Anthropic's official CLI for Claude."`
2. Strip `ttl` from `cache_control` objects (recursive JSON walk)
3. Filter sampling params when enabled (temperature/top_p conflict for Sonnet 4.5)
4. Inject preset system/suffix when using preset route

### Headers sent upstream

```
Content-Type: application/json
Authorization: Bearer {token}
anthropic-version: 2023-06-01
User-Agent: claude-code-proxy/1.0.0
anthropic-beta: claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14
```

### Enhancement: context cancellation

`context.Context` propagation — client disconnect cancels upstream request immediately.

## Preset System

- Embedded via `go:embed presets/*.json`
- JSON format: `{system, suffix, suffixEt}`
- In-memory cache after first parse
- Route: `/v1/{preset}/messages` using Go 1.22+ `http.ServeMux` path params

## Logging

- `log/slog` with custom TRACE level
- Configurable via config/env/flag
- Debug stream for response body logging with thinking highlight
- Enhancement: optional JSON output for containers (`--log-format json`)

## Docker

### Multi-stage Dockerfile

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY go/ .
RUN go build -o /claude-code-proxy ./cmd/claude-code-proxy

FROM alpine:3.19
COPY --from=builder /claude-code-proxy /usr/local/bin/
ENTRYPOINT ["claude-code-proxy"]
```

~10MB final image (vs ~200MB+ Node.js).

### docker-compose.yml

```yaml
services:
  claude-proxy:
    build:
      context: ./go
    ports:
      - "42069:42069"
    volumes:
      - ~/.claude:/root/.claude
      - ~/.claude-code-proxy:/root/.claude-code-proxy
    environment:
      - CCP_LOG_LEVEL=info
```

## Testing Strategy

- `go test` with `httptest.NewServer` for integration tests
- Table-driven tests for all transformations
- Mock HTTP server for OAuth token exchange
- Test files alongside source: `config_test.go`, `proxy_test.go`, etc.

## Improvements Over Node.js

| Area | Node.js | Go |
|------|---------|-----|
| Binary | Requires Node.js runtime | Single static binary |
| Docker image | ~200MB+ | ~10MB |
| Config | Flat key=value .txt | YAML + env + CLI flags |
| Streaming | Pipe-based | io.Copy + Flusher (native backpressure) |
| Client disconnect | Not handled | context.Context cancellation |
| Token refresh | Basic dedup | sync.Mutex single-flight |
| Logging | Custom Logger class | slog (structured, JSON option) |
| Static files | fs.readFileSync | go:embed (compiled in) |
