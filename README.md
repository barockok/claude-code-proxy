# Claude Code Proxy

An OAuth-based API proxy that routes Claude API requests through a Claude MAX subscription. Written in Go — single static binary, minimal dependencies.

## Quick Start

### Binary

```bash
go build -o claude-code-proxy ./cmd/claude-code-proxy
./claude-code-proxy
```

The server starts on `http://localhost:42069` and opens the browser for OAuth authentication.

### Docker

```bash
docker compose up --build
```

### Docker (manual)

```bash
docker build -t claude-code-proxy .
docker run -p 42069:42069 \
  -v ~/.claude:/root/.claude \
  -v ~/.claude-code-proxy:/root/.claude-code-proxy \
  claude-code-proxy
```

## Configuration

Configuration is loaded in this order (later wins):

1. Built-in defaults
2. `config.yaml` (or `--config path`)
3. Environment variables
4. CLI flags

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

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CCP_SERVER_PORT` | `42069` | Server port |
| `CCP_SERVER_HOST` | (auto) | Bind address |
| `CCP_LOG_LEVEL` | `info` | Log level |
| `CCP_PROXY_FILTER_SAMPLING_PARAMS` | `false` | Filter temperature/top_p conflicts |
| `CCP_PROXY_STRIP_TTL` | `true` | Remove TTL from cache_control |
| `CCP_AUTH_AUTO_OPEN_BROWSER` | `true` | Auto-open browser for auth |
| `CCP_AUTH_FALLBACK_TO_CLAUDE_CODE` | `true` | Use Claude Code CLI as fallback |

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
| `/auth/login` | GET | OAuth login page |
| `/auth/get-url` | GET | Generate authorization URL |
| `/auth/callback` | GET | Handle OAuth callback |
| `/auth/status` | GET | Check auth status |
| `/auth/logout` | GET | Clear tokens |
| `/health` | GET | Health check |

### API Proxy

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/messages` | POST | Claude API proxy |
| `/v1/{preset}/messages` | POST | Proxy with preset injection |

## Authentication Priority

1. `x-api-key` request header (direct API key)
2. OAuth tokens (`~/.claude-code-proxy/tokens.json`)
3. Claude Code CLI credentials (`~/.claude/.credentials.json`)

## Presets

Presets are JSON files in `presets/` embedded into the binary. They inject system prompts and suffixes into requests.

Use a preset by routing through `/v1/{name}/messages` (e.g., `/v1/pyrite/messages`).

## Development

```bash
make build    # Build binary
make run      # Build and run
make test     # Run all tests
make clean    # Remove binary
```

## Project Structure

```
├── cmd/claude-code-proxy/
│   ├── main.go          # Entry point, CLI flags, routes
│   ├── static/          # Embedded HTML pages
│   └── presets/         # Embedded preset JSON files
├── internal/
│   ├── config/          # YAML + env + flag config
│   ├── logger/          # slog wrapper with TRACE level
│   ├── oauth/           # PKCE flow, token management
│   ├── auth/            # Token resolver (header > OAuth > CLI)
│   ├── transform/       # Request body transformations
│   ├── preset/          # Preset loading and injection
│   └── proxy/           # Core HTTP proxy handler
├── config.yaml          # Default configuration
├── Dockerfile           # Multi-stage build
├── Makefile
└── docker-compose.yml
```
