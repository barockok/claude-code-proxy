# Claude Code Proxy

A generic multi-provider LLM proxy. Routes requests to any LLM provider based on the `model` field, with per-provider auth (OAuth or API key). Written in Go — single static binary, minimal dependencies.

## Quick Start

### Binary

```bash
go build -o claude-code-proxy ./cmd/claude-code-proxy
./claude-code-proxy
```

The server starts on `http://localhost:42069`. For OAuth providers that aren't yet authenticated, it opens the browser automatically.

### Docker

```bash
docker compose up --build
```

### Docker (manual)

```bash
docker build -t claude-code-proxy .
docker run -p 42069:42069 \
  -v ~/.claude-code-proxy:/root/.claude-code-proxy \
  claude-code-proxy
```

## How It Works

1. Client sends `POST /v1/messages` (or any path) with a JSON body containing a `model` field
2. Proxy extracts the `model` value and matches it against provider glob patterns (first match wins)
3. Proxy resolves credentials for the matched provider (OAuth token or static API key)
4. Raw request body is forwarded to `provider.upstream + request.URL.Path`
5. Response streams back to client (SSE streaming supported)
6. On 401: auto-refreshes token and retries once

## Configuration

Providers are configured entirely in YAML — no hardcoded provider logic.

### config.yaml

```yaml
server:
  port: 42069
  host: ""              # Auto-detect: 127.0.0.1 native, 0.0.0.0 Docker

logging:
  level: info           # trace, debug, info, warn, error

providers:
  anthropic:
    models: ["claude-*"]
    upstream: "https://api.anthropic.com"
    auth:
      type: oauth
      client_id: "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
      authorize_url: "https://claude.ai/oauth/authorize"
      token_url: "https://console.anthropic.com/v1/oauth/token"
      scopes: "org:create_api_key user:profile user:inference"
    headers:
      anthropic-version: "2023-06-01"
      anthropic-beta: "claude-code-20250219,oauth-2025-04-20"
      User-Agent: "claude-code-proxy/2.0.0"

  openai:
    models: ["gpt-*", "o1-*", "o3-*", "chatgpt-*"]
    upstream: "https://api.openai.com"
    auth:
      type: api_key
      api_key: "${OPENAI_API_KEY}"
      header_name: "Authorization"
      header_prefix: "Bearer "
    headers: {}

  gemini:
    models: ["gemini-*"]
    upstream: "https://generativelanguage.googleapis.com"
    auth:
      type: api_key
      api_key: "${GEMINI_API_KEY}"
      header_name: "x-goog-api-key"
      header_prefix: ""
    headers: {}
```

### Config Details

- `models`: Glob patterns matched with `filepath.Match` (first match wins, YAML order preserved)
- `upstream`: Base URL. Full URL = `upstream + request path`
- `auth.type`: `api_key` or `oauth`
- `auth.api_key`: Static key or env var reference `${VAR}`
- `auth.header_name`: HTTP header to set (e.g. `Authorization`, `x-goog-api-key`)
- `auth.header_prefix`: Prefix before the key value (e.g. `Bearer `)
- `headers`: Extra headers injected into upstream requests for this provider

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CCP_SERVER_PORT` | `42069` | Server port |
| `CCP_SERVER_HOST` | (auto) | Bind address |
| `CCP_LOG_LEVEL` | `info` | Log level |

API keys can use `${VAR}` syntax in config to reference environment variables.

### CLI Flags

```
--config string   config file path (default "config.yaml")
--port int        server port
--host string     server host
--log-level string  log level
```

## Endpoints

### Authentication

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/auth/login/{provider}` | GET | OAuth login page for a provider |
| `/auth/get-url/{provider}` | GET | Generate authorization URL |
| `/auth/callback/{provider}` | GET | Handle OAuth callback |
| `/auth/status` | GET | Auth status for all providers |
| `/auth/logout/{provider}` | GET | Clear tokens for a provider |
| `/health` | GET | Health check |

### API Proxy

| Endpoint | Method | Description |
|----------|--------|-------------|
| `POST /*` | POST | Catch-all — routes by `model` field |

The proxy reads the `model` field from the request body and routes to the matching provider. Clients can use any path (e.g. `/v1/messages`, `/v1/chat/completions`).

## Auth Types

### OAuth (e.g. Anthropic)

- PKCE flow per provider
- Tokens stored in `~/.claude-code-proxy/tokens-{provider}.json`
- Auto-refresh on expiry
- Login at `http://localhost:42069/auth/login/{provider}`

### API Key (e.g. OpenAI, Gemini)

- Static key from config or env var
- No login needed — always authenticated

## Development

```bash
go build -o claude-code-proxy ./cmd/claude-code-proxy
go test ./... -v
```

## Project Structure

```
├── cmd/claude-code-proxy/
│   ├── main.go          # Entry point, provider wiring, routes
│   └── static/          # Embedded HTML pages (login, callback)
├── internal/
│   ├── config/          # YAML config with provider schema
│   ├── logger/          # slog wrapper with TRACE level
│   ├── oauth/           # Per-provider PKCE flow, token management
│   ├── auth/            # Resolver interface (StaticKey, OAuth)
│   ├── provider/        # Model-based router with glob matching
│   └── proxy/           # HTTP proxy handler with provider routing
├── config.yaml          # Default configuration
├── Dockerfile           # Multi-stage build
└── docker-compose.yml
```
