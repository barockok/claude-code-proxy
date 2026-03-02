# Generic LLM Proxy Design

## Overview

Extend claude-code-proxy from a Claude-only proxy into a generic multi-provider LLM proxy. Providers are configured entirely in YAML — no hardcoded provider logic. The proxy reads the `model` field from the request body, matches it against provider patterns, resolves that provider's credentials, and forwards the raw request body to the provider's upstream endpoint.

## Design Decisions

- **Fully generic**: No hardcoded providers. Everything defined in YAML config.
- **Model-based routing**: Proxy peeks at the `model` field to select a provider.
- **Transparent passthrough**: No body parsing or format translation beyond model extraction.
- **Single catch-all endpoint**: Clients send `POST /*`, proxy resolves provider from model.
- **Per-provider auth**: Each provider defines its auth type — `api_key` (static) or `oauth` (PKCE flow).

## Config Schema

```yaml
server:
  port: 42069
  host: ""

logging:
  level: info

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

- `models`: Glob patterns matched with `filepath.Match` (first match wins, order from YAML preserved)
- `upstream`: Base URL. Full URL = `upstream + request.URL.Path`
- `auth.type`: `api_key` or `oauth`
- `auth.api_key`: Static key or env var reference `${VAR}`
- `auth.header_name`: HTTP header to set (e.g. `Authorization`, `x-goog-api-key`)
- `auth.header_prefix`: Prefix before the key value (e.g. `Bearer `)
- `auth.client_id`, `authorize_url`, `token_url`, `scopes`: OAuth PKCE config
- `headers`: Extra headers injected into upstream requests for this provider
- Env var expansion (`${VAR}`) in all string values

## Request Flow

```
Client POST /*
  -> Read raw body
  -> Lightweight JSON peek: extract "model" field only
  -> Match model against providers (first match wins)
  -> Resolve auth token for matched provider
  -> Forward raw body to: provider.upstream + request.URL.Path
  -> Inject provider's configured headers + auth header
  -> Stream response back to client
  -> On 401: refresh token, retry once
```

If no provider matches: return `400` with error listing available model patterns.

## Auth Per Provider

### api_key type
- Expand env vars at startup
- Inject as: `header_name: header_prefix + api_key`

### oauth type
- Each OAuth provider gets its own `oauth.Manager` instance
- Token file per provider: `~/.claude-code-proxy/tokens-{provider_name}.json`
- Reuses existing PKCE flow, parameterized per provider
- OAuth routes become per-provider:
  - `GET /auth/login/{provider}` — provider-specific login page
  - `GET /auth/callback/{provider}` — OAuth callback
- `GET /auth/status` — returns auth status for all providers

### Client-side
- Clients don't need credentials — proxy handles everything
- `x-api-key` from clients is ignored

## Package Changes

### `internal/config/config.go`
- Remove old `ProxyConfig` and `AuthConfig`
- Add `ProviderConfig` with `Models`, `Upstream`, `Auth`, `Headers`
- Add `ProviderAuthConfig` with `Type`, `APIKey`, `HeaderName`, `HeaderPrefix`, `ClientID`, `AuthorizeURL`, `TokenURL`, `Scopes`
- Add env var expansion function for `${VAR}` in string values

### `internal/provider/provider.go` (new, replaces `internal/proxy`)
- `Router` — holds all providers, matches model to provider
- `Provider` — wraps a single provider config with its auth resolver
- `ExtractModel(rawBody []byte) string` — lightweight model extraction
- `Handler.ServeHTTP` — catch-all HTTP handler

### `internal/auth/auth.go`
- `Resolver` becomes an interface with two implementations:
  - `StaticKeyResolver` — for `api_key` type
  - `OAuthResolver` — wraps `oauth.Manager` for OAuth type

### `internal/oauth/oauth.go`
- `NewManager` takes config struct instead of hardcoded constants
- Token path: `~/.claude-code-proxy/tokens-{name}.json`

### `cmd/claude-code-proxy/main.go`
- Build provider router from config
- OAuth routes per-provider: `/auth/login/{provider}`, `/auth/callback/{provider}`
- `/auth/status` returns all providers
- Single catch-all: `POST /` -> provider router

### Removed packages
- `internal/preset/` — unused since transparent proxy
- `internal/transform/` — unused since transparent proxy
