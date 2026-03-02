# Generic LLM Proxy Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Transform claude-code-proxy from a Claude-only proxy into a generic multi-provider LLM proxy with YAML-configured providers, model-based routing, and per-provider auth (api_key or OAuth).

**Architecture:** Providers defined in YAML config. Proxy peeks at `model` field in request body, matches it against provider glob patterns, resolves that provider's credentials, and forwards the raw body to the provider's upstream URL + request path. OAuth flow parameterized per provider.

**Tech Stack:** Go 1.25, net/http stdlib, gopkg.in/yaml.v3, filepath.Match for glob patterns

---

### Task 1: New Config Schema

**Files:**
- Rewrite: `internal/config/config.go`
- Rewrite: `internal/config/config_test.go`
- Rewrite: `config.yaml`

**Step 1: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Server.Port != 42069 {
		t.Errorf("default port = %d, want 42069", cfg.Server.Port)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("default log level = %q, want info", cfg.Logging.Level)
	}
}

func TestLoadProviders(t *testing.T) {
	yaml := `
server:
  port: 8080
providers:
  anthropic:
    models: ["claude-*"]
    upstream: "https://api.anthropic.com"
    auth:
      type: oauth
      client_id: "test-client"
      authorize_url: "https://example.com/auth"
      token_url: "https://example.com/token"
      scopes: "read write"
    headers:
      anthropic-version: "2023-06-01"
  openai:
    models: ["gpt-*", "o1-*"]
    upstream: "https://api.openai.com"
    auth:
      type: api_key
      api_key: "sk-test"
      header_name: "Authorization"
      header_prefix: "Bearer "
    headers: {}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Server.Port)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("providers count = %d, want 2", len(cfg.Providers))
	}

	anth, ok := cfg.Providers["anthropic"]
	if !ok {
		t.Fatal("missing anthropic provider")
	}
	if anth.Auth.Type != "oauth" {
		t.Errorf("anthropic auth type = %q, want oauth", anth.Auth.Type)
	}
	if anth.Headers["anthropic-version"] != "2023-06-01" {
		t.Error("missing anthropic-version header")
	}

	oai, ok := cfg.Providers["openai"]
	if !ok {
		t.Fatal("missing openai provider")
	}
	if oai.Auth.Type != "api_key" {
		t.Errorf("openai auth type = %q, want api_key", oai.Auth.Type)
	}
	if oai.Auth.APIKey != "sk-test" {
		t.Errorf("openai api_key = %q, want sk-test", oai.Auth.APIKey)
	}
}

func TestExpandEnvVars(t *testing.T) {
	os.Setenv("TEST_API_KEY", "my-secret-key")
	defer os.Unsetenv("TEST_API_KEY")

	result := ExpandEnvVars("${TEST_API_KEY}")
	if result != "my-secret-key" {
		t.Errorf("ExpandEnvVars = %q, want my-secret-key", result)
	}

	result = ExpandEnvVars("prefix-${TEST_API_KEY}-suffix")
	if result != "prefix-my-secret-key-suffix" {
		t.Errorf("ExpandEnvVars = %q, want prefix-my-secret-key-suffix", result)
	}

	result = ExpandEnvVars("no-vars-here")
	if result != "no-vars-here" {
		t.Errorf("ExpandEnvVars = %q, want no-vars-here", result)
	}
}

func TestProviderOrder(t *testing.T) {
	yaml := `
providers:
  zz_last:
    models: ["last-*"]
    upstream: "https://last.example.com"
    auth:
      type: api_key
      api_key: "k1"
      header_name: "Authorization"
      header_prefix: "Bearer "
  aa_first:
    models: ["first-*"]
    upstream: "https://first.example.com"
    auth:
      type: api_key
      api_key: "k2"
      header_name: "Authorization"
      header_prefix: "Bearer "
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	// ProviderOrder should preserve YAML insertion order
	if len(cfg.ProviderOrder) != 2 {
		t.Fatalf("ProviderOrder length = %d, want 2", len(cfg.ProviderOrder))
	}
	if cfg.ProviderOrder[0] != "zz_last" {
		t.Errorf("first provider = %q, want zz_last", cfg.ProviderOrder[0])
	}
	if cfg.ProviderOrder[1] != "aa_first" {
		t.Errorf("second provider = %q, want aa_first", cfg.ProviderOrder[1])
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v -count=1`
Expected: FAIL — structs and functions don't exist yet

**Step 3: Write the implementation**

Rewrite `internal/config/config.go`:

```go
package config

import (
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server        ServerConfig               `yaml:"server"`
	Logging       LoggingConfig              `yaml:"logging"`
	Providers     map[string]ProviderConfig  `yaml:"-"`
	ProviderOrder []string                   `yaml:"-"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
}

type ProviderConfig struct {
	Models   []string            `yaml:"models"`
	Upstream string              `yaml:"upstream"`
	Auth     ProviderAuthConfig  `yaml:"auth"`
	Headers  map[string]string   `yaml:"headers"`
}

type ProviderAuthConfig struct {
	Type         string `yaml:"type"`
	APIKey       string `yaml:"api_key"`
	HeaderName   string `yaml:"header_name"`
	HeaderPrefix string `yaml:"header_prefix"`
	ClientID     string `yaml:"client_id"`
	AuthorizeURL string `yaml:"authorize_url"`
	TokenURL     string `yaml:"token_url"`
	Scopes       string `yaml:"scopes"`
}

var envVarRe = regexp.MustCompile(`\$\{([^}]+)\}`)

func ExpandEnvVars(s string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		key := match[2 : len(match)-1]
		if val, ok := os.LookupEnv(key); ok {
			return val
		}
		return match
	})
}

func Defaults() Config {
	return Config{
		Server:    ServerConfig{Port: 42069, Host: ""},
		Logging:   LoggingConfig{Level: "info"},
		Providers: make(map[string]ProviderConfig),
	}
}

func Load(path string) (Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	// First unmarshal server/logging
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}

	// Manually unmarshal providers to preserve order
	var raw struct {
		Providers yaml.Node `yaml:"providers"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return cfg, err
	}

	if raw.Providers.Kind == yaml.MappingNode {
		cfg.Providers = make(map[string]ProviderConfig)
		cfg.ProviderOrder = nil
		for i := 0; i+1 < len(raw.Providers.Content); i += 2 {
			name := raw.Providers.Content[i].Value
			var prov ProviderConfig
			if err := raw.Providers.Content[i+1].Decode(&prov); err != nil {
				return cfg, err
			}
			cfg.Providers[name] = prov
			cfg.ProviderOrder = append(cfg.ProviderOrder, name)
		}
	}

	return cfg, nil
}

func ApplyEnv(cfg *Config) {
	if v := os.Getenv("CCP_SERVER_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("CCP_SERVER_HOST"); v != "" {
		cfg.Server.Host = v
	}
	if v := os.Getenv("CCP_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = strings.ToLower(v)
	}
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v -count=1`
Expected: PASS

**Step 5: Write config.yaml**

Rewrite `config.yaml`:

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
      anthropic-beta: "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14"
      User-Agent: "claude-code-proxy/2.0.0"
```

**Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go config.yaml
git commit -m "feat: new config schema with providers map and env var expansion"
```

---

### Task 2: Parameterized OAuth Manager

**Files:**
- Modify: `internal/oauth/oauth.go`
- Modify: `internal/oauth/oauth_test.go`

**Step 1: Write the failing test**

Add to `internal/oauth/oauth_test.go`:

```go
func TestNewManagerWithConfig(t *testing.T) {
	mgr := NewManager(OAuthConfig{
		Name:         "test-provider",
		ClientID:     "custom-client-id",
		AuthorizeURL: "https://example.com/auth",
		TokenURL:     "https://example.com/token",
		RedirectURI:  "https://example.com/callback",
		Scopes:       "read write",
	})

	if !strings.Contains(mgr.TokenPath, "tokens-test-provider.json") {
		t.Errorf("TokenPath = %q, want to contain tokens-test-provider.json", mgr.TokenPath)
	}
	if mgr.Config.ClientID != "custom-client-id" {
		t.Errorf("ClientID = %q, want custom-client-id", mgr.Config.ClientID)
	}
}

func TestBuildAuthorizationURLWithConfig(t *testing.T) {
	mgr := NewManager(OAuthConfig{
		Name:         "test",
		ClientID:     "my-client",
		AuthorizeURL: "https://auth.example.com/authorize",
		RedirectURI:  DefaultRedirectURI,
		Scopes:       "scope1 scope2",
	})

	pkce := GeneratePKCE()
	url := mgr.BuildAuthorizationURL(pkce)

	if !strings.Contains(url, "https://auth.example.com/authorize") {
		t.Errorf("URL should use configured authorize_url, got %s", url)
	}
	if !strings.Contains(url, "client_id=my-client") {
		t.Errorf("URL should use configured client_id, got %s", url)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -v -count=1 -run "Config"`
Expected: FAIL — `OAuthConfig` and new `NewManager` signature don't exist

**Step 3: Write the implementation**

Modify `internal/oauth/oauth.go`:

- Add `OAuthConfig` struct
- Change `NewManager()` to `NewManager(cfg OAuthConfig) *Manager`
- Add `Config` field to `Manager`
- Make `BuildAuthorizationURL` a method on `Manager` (so it uses config)
- Make `ExchangeCodeForTokens` and `RefreshAccessToken` use `m.Config.ClientID` etc.
- Token path becomes `~/.claude-code-proxy/tokens-{name}.json`

```go
type OAuthConfig struct {
	Name         string
	ClientID     string
	AuthorizeURL string
	TokenURL     string
	RedirectURI  string
	Scopes       string
}

type Manager struct {
	Config    OAuthConfig
	TokenPath string
	TokenURL  string

	mu             sync.Mutex
	refreshPromise chan struct{}
	cachedToken    string
}

func NewManager(cfg OAuthConfig) *Manager {
	home, _ := os.UserHomeDir()
	tokenFile := fmt.Sprintf("tokens-%s.json", cfg.Name)
	if cfg.RedirectURI == "" {
		cfg.RedirectURI = DefaultRedirectURI
	}
	return &Manager{
		Config:    cfg,
		TokenPath: filepath.Join(home, ".claude-code-proxy", tokenFile),
		TokenURL:  cfg.TokenURL,
	}
}

func (m *Manager) BuildAuthorizationURL(pkce PKCE) string {
	params := url.Values{
		"code":                  {"true"},
		"client_id":             {m.Config.ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {m.Config.RedirectURI},
		"scope":                 {m.Config.Scopes},
		"code_challenge":        {pkce.CodeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {pkce.State},
	}
	return m.Config.AuthorizeURL + "?" + params.Encode()
}
```

Update `ExchangeCodeForTokens` and `RefreshAccessToken` to use `m.Config.ClientID` and `m.Config.RedirectURI` instead of constants.

**Step 4: Update existing tests to use new NewManager signature**

All existing tests that call `NewManager()` need updating to `NewManager(OAuthConfig{...})` with default Anthropic values.

**Step 5: Run all tests**

Run: `go test ./internal/oauth/ -v -count=1`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/oauth/oauth.go internal/oauth/oauth_test.go
git commit -m "feat: parameterize OAuth manager with per-provider config"
```

---

### Task 3: Auth Resolver Interface

**Files:**
- Rewrite: `internal/auth/auth.go`
- Rewrite: `internal/auth/auth_test.go`

**Step 1: Write the failing test**

```go
package auth

import (
	"testing"
)

func TestStaticKeyResolver(t *testing.T) {
	r := NewStaticKeyResolver("sk-test-123", "Authorization", "Bearer ")
	token, header, prefix, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if token != "sk-test-123" {
		t.Errorf("token = %q, want sk-test-123", token)
	}
	if header != "Authorization" {
		t.Errorf("header = %q, want Authorization", header)
	}
	if prefix != "Bearer " {
		t.Errorf("prefix = %q, want 'Bearer '", prefix)
	}
}

func TestStaticKeyResolver_CustomHeader(t *testing.T) {
	r := NewStaticKeyResolver("AIza-test", "x-goog-api-key", "")
	token, header, prefix, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if token != "AIza-test" {
		t.Errorf("token = %q, want AIza-test", token)
	}
	if header != "x-goog-api-key" {
		t.Errorf("header = %q, want x-goog-api-key", header)
	}
	if prefix != "" {
		t.Errorf("prefix = %q, want empty", prefix)
	}
}

type mockOAuthMgr struct {
	authenticated bool
	token         string
	err           error
}

func (m *mockOAuthMgr) IsAuthenticated() bool             { return m.authenticated }
func (m *mockOAuthMgr) GetValidAccessToken() (string, error) { return m.token, m.err }

func TestOAuthResolver(t *testing.T) {
	mock := &mockOAuthMgr{authenticated: true, token: "access-tok"}
	r := NewOAuthResolver(mock)
	token, header, prefix, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if token != "access-tok" {
		t.Errorf("token = %q, want access-tok", token)
	}
	if header != "Authorization" {
		t.Errorf("header = %q, want Authorization", header)
	}
	if prefix != "Bearer " {
		t.Errorf("prefix = %q, want 'Bearer '", prefix)
	}
}

func TestOAuthResolver_NotAuthenticated(t *testing.T) {
	mock := &mockOAuthMgr{authenticated: false}
	r := NewOAuthResolver(mock)
	_, _, _, err := r.Resolve()
	if err == nil {
		t.Fatal("expected error when not authenticated")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/ -v -count=1`
Expected: FAIL

**Step 3: Write the implementation**

```go
package auth

import (
	"fmt"
	"log/slog"
)

// Resolver returns (token, headerName, headerPrefix, error)
type Resolver interface {
	Resolve() (token string, headerName string, headerPrefix string, err error)
	ClearCache()
}

// StaticKeyResolver for api_key auth type
type StaticKeyResolver struct {
	apiKey       string
	headerName   string
	headerPrefix string
}

func NewStaticKeyResolver(apiKey, headerName, headerPrefix string) *StaticKeyResolver {
	return &StaticKeyResolver{
		apiKey:       apiKey,
		headerName:   headerName,
		headerPrefix: headerPrefix,
	}
}

func (r *StaticKeyResolver) Resolve() (string, string, string, error) {
	return r.apiKey, r.headerName, r.headerPrefix, nil
}

func (r *StaticKeyResolver) ClearCache() {}

// OAuthTokenProvider is the interface OAuth managers must implement
type OAuthTokenProvider interface {
	IsAuthenticated() bool
	GetValidAccessToken() (string, error)
}

// OAuthResolver for oauth auth type
type OAuthResolver struct {
	oauth OAuthTokenProvider
}

func NewOAuthResolver(oauth OAuthTokenProvider) *OAuthResolver {
	return &OAuthResolver{oauth: oauth}
}

func (r *OAuthResolver) Resolve() (string, string, string, error) {
	if !r.oauth.IsAuthenticated() {
		return "", "", "", fmt.Errorf("not authenticated; visit /auth/login/{provider}")
	}
	tok, err := r.oauth.GetValidAccessToken()
	if err != nil {
		slog.Warn("OAuth token retrieval failed", "error", err)
		return "", "", "", err
	}
	return tok, "Authorization", "Bearer ", nil
}

func (r *OAuthResolver) ClearCache() {}
```

**Step 4: Run tests**

Run: `go test ./internal/auth/ -v -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/auth/auth.go internal/auth/auth_test.go
git commit -m "feat: auth resolver interface with static key and OAuth implementations"
```

---

### Task 4: Provider Router

**Files:**
- Create: `internal/provider/provider.go`
- Create: `internal/provider/provider_test.go`

**Step 1: Write the failing test**

Create `internal/provider/provider_test.go`:

```go
package provider

import (
	"testing"
)

func TestExtractModel(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		model string
	}{
		{"simple", `{"model":"claude-sonnet-4-20250514","messages":[]}`, "claude-sonnet-4-20250514"},
		{"nested", `{"messages":[],"model":"gpt-4o","stream":true}`, "gpt-4o"},
		{"no model", `{"messages":[]}`, ""},
		{"empty", `{}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractModel([]byte(tt.body))
			if got != tt.model {
				t.Errorf("ExtractModel = %q, want %q", got, tt.model)
			}
		})
	}
}

func TestRouterMatch(t *testing.T) {
	r := NewRouter()
	r.Add("anthropic", []string{"claude-*"})
	r.Add("openai", []string{"gpt-*", "o1-*", "o3-*"})
	r.Add("gemini", []string{"gemini-*"})

	tests := []struct {
		model    string
		provider string
		ok       bool
	}{
		{"claude-sonnet-4-20250514", "anthropic", true},
		{"claude-3-5-haiku-20241022", "anthropic", true},
		{"gpt-4o", "openai", true},
		{"o1-preview", "openai", true},
		{"gemini-2.0-flash", "gemini", true},
		{"unknown-model", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			name, ok := r.Match(tt.model)
			if ok != tt.ok {
				t.Errorf("Match ok = %v, want %v", ok, tt.ok)
			}
			if name != tt.provider {
				t.Errorf("Match provider = %q, want %q", name, tt.provider)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -v -count=1`
Expected: FAIL — package doesn't exist

**Step 3: Write the implementation**

Create `internal/provider/provider.go`:

```go
package provider

import (
	"encoding/json"
	"path/filepath"
)

// ExtractModel does a lightweight extraction of the "model" field from JSON bytes.
func ExtractModel(body []byte) string {
	var peek struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &peek); err != nil {
		return ""
	}
	return peek.Model
}

type routeEntry struct {
	name     string
	patterns []string
}

// Router matches model names to provider names using glob patterns.
type Router struct {
	entries []routeEntry
}

func NewRouter() *Router {
	return &Router{}
}

func (r *Router) Add(name string, patterns []string) {
	r.entries = append(r.entries, routeEntry{name: name, patterns: patterns})
}

// Match returns the first provider name whose glob pattern matches the model.
func (r *Router) Match(model string) (string, bool) {
	for _, e := range r.entries {
		for _, pat := range e.patterns {
			if matched, _ := filepath.Match(pat, model); matched {
				return e.name, true
			}
		}
	}
	return "", false
}
```

**Step 4: Run tests**

Run: `go test ./internal/provider/ -v -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/provider/provider.go internal/provider/provider_test.go
git commit -m "feat: provider router with model-based glob matching"
```

---

### Task 5: Proxy Handler with Provider Routing

**Files:**
- Rewrite: `internal/proxy/proxy.go`
- Rewrite: `internal/proxy/proxy_test.go`

**Step 1: Write the failing test**

```go
package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockResolver struct {
	token        string
	headerName   string
	headerPrefix string
}

func (m *mockResolver) Resolve() (string, string, string, error) {
	return m.token, m.headerName, m.headerPrefix, nil
}
func (m *mockResolver) ClearCache() {}

func TestHandler_RoutesToCorrectProvider(t *testing.T) {
	var gotAuth string
	var gotCustomHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCustomHeader = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	h := NewHandler()
	h.AddProvider("anthropic", &ProviderEntry{
		Upstream: upstream.URL,
		Auth:     &mockResolver{token: "oauth-tok", headerName: "Authorization", headerPrefix: "Bearer "},
		Headers:  map[string]string{"anthropic-version": "2023-06-01"},
		Patterns: []string{"claude-*"},
	})

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if gotAuth != "Bearer oauth-tok" {
		t.Errorf("Authorization = %q, want 'Bearer oauth-tok'", gotAuth)
	}
	if gotCustomHeader != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want 2023-06-01", gotCustomHeader)
	}
}

func TestHandler_UnknownModel(t *testing.T) {
	h := NewHandler()
	h.AddProvider("anthropic", &ProviderEntry{
		Upstream: "https://unused",
		Auth:     &mockResolver{token: "t", headerName: "Authorization", headerPrefix: "Bearer "},
		Patterns: []string{"claude-*"},
	})

	body := `{"model":"unknown-model","messages":[]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400 for unknown model", w.Code)
	}
}

func TestHandler_StreamingResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		w.Write([]byte("data: {\"type\":\"text_delta\"}\n\n"))
		flusher.Flush()
	}))
	defer upstream.Close()

	h := NewHandler()
	h.AddProvider("anthropic", &ProviderEntry{
		Upstream: upstream.URL,
		Auth:     &mockResolver{token: "t", headerName: "Authorization", headerPrefix: "Bearer "},
		Patterns: []string{"claude-*"},
	})

	body := `{"model":"claude-sonnet-4-20250514","stream":true,"messages":[]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", w.Header().Get("Content-Type"))
	}
}

func TestHandler_401Retry(t *testing.T) {
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	h := NewHandler()
	h.AddProvider("anthropic", &ProviderEntry{
		Upstream: upstream.URL,
		Auth:     &mockResolver{token: "t", headerName: "Authorization", headerPrefix: "Bearer "},
		Patterns: []string{"claude-*"},
	})

	body := `{"model":"claude-sonnet-4-20250514","messages":[]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestHandler_BodyPassthrough(t *testing.T) {
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	h := NewHandler()
	h.AddProvider("openai", &ProviderEntry{
		Upstream: upstream.URL,
		Auth:     &mockResolver{token: "sk-test", headerName: "Authorization", headerPrefix: "Bearer "},
		Patterns: []string{"gpt-*"},
	})

	reqBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(reqBody))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if receivedBody != reqBody {
		t.Errorf("body modified.\ngot:  %s\nwant: %s", receivedBody, reqBody)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/ -v -count=1`
Expected: FAIL

**Step 3: Write the implementation**

Rewrite `internal/proxy/proxy.go`:

```go
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/anthropics/claude-code-proxy/internal/auth"
	"github.com/anthropics/claude-code-proxy/internal/provider"
)

type ProviderEntry struct {
	Upstream string
	Auth     auth.Resolver
	Headers  map[string]string
	Patterns []string
}

type Handler struct {
	router    *provider.Router
	providers map[string]*ProviderEntry
	client    *http.Client
}

func NewHandler() *Handler {
	return &Handler{
		router:    provider.NewRouter(),
		providers: make(map[string]*ProviderEntry),
		client:    &http.Client{Timeout: 0},
	}
}

func (h *Handler) AddProvider(name string, entry *ProviderEntry) {
	h.providers[name] = entry
	h.router.Add(name, entry.Patterns)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Failed to read request body", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}

	model := provider.ExtractModel(rawBody)
	if model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or empty 'model' field in request body"})
		return
	}

	providerName, ok := h.router.Match(model)
	if !ok {
		slog.Warn("No provider matched", "model", model)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "no provider configured for model: " + model,
		})
		return
	}

	entry := h.providers[providerName]
	slog.Debug("Routing request", "model", model, "provider", providerName, "upstream", entry.Upstream)

	token, headerName, headerPrefix, err := entry.Auth.Resolve()
	if err != nil {
		slog.Error("Authentication failed", "provider", providerName, "error", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	upstreamURL := entry.Upstream + r.URL.Path

	resp, err := h.doUpstreamRequest(context.Background(), upstreamURL, rawBody, token, headerName, headerPrefix, entry.Headers)
	if err != nil {
		slog.Error("Upstream request failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		resp.Body.Close()
		slog.Info("Got 401, refreshing token and retrying", "provider", providerName)
		entry.Auth.ClearCache()

		token, headerName, headerPrefix, err = entry.Auth.Resolve()
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication failed after retry"})
			return
		}

		resp, err = h.doUpstreamRequest(context.Background(), upstreamURL, rawBody, token, headerName, headerPrefix, entry.Headers)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		defer resp.Body.Close()
	}

	for key, vals := range resp.Header {
		for _, val := range vals {
			w.Header().Add(key, val)
		}
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		w.WriteHeader(resp.StatusCode)
		flusher, canFlush := w.(http.Flusher)
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				if canFlush {
					flusher.Flush()
				}
			}
			if err != nil {
				if err != io.EOF {
					slog.Error("Stream read error", "error", err)
				}
				break
			}
		}
	} else {
		w.Header().Del("Content-Encoding")
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream read error"})
			return
		}
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
	}
}

func (h *Handler) doUpstreamRequest(ctx context.Context, url string, body []byte, token, headerName, headerPrefix string, extraHeaders map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerName, headerPrefix+token)

	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	return h.client.Do(req)
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
```

**Step 4: Run tests**

Run: `go test ./internal/proxy/ -v -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/proxy/proxy.go internal/proxy/proxy_test.go
git commit -m "feat: proxy handler with model-based provider routing"
```

---

### Task 6: Rewire main.go

**Files:**
- Rewrite: `cmd/claude-code-proxy/main.go`

**Step 1: Write the implementation**

Key changes:
- Build provider router from `cfg.Providers`
- For each provider with `auth.type == "oauth"`: create `oauth.NewManager(OAuthConfig{...})`, create `auth.NewOAuthResolver(mgr)`
- For each provider with `auth.type == "api_key"`: create `auth.NewStaticKeyResolver(ExpandEnvVars(key), headerName, headerPrefix)`
- OAuth routes become per-provider: `GET /auth/login/{provider}`, `GET /auth/callback/{provider}`
- `GET /auth/status` returns all providers
- Single catch-all: `POST /` → proxy handler
- Login page shows list of OAuth providers

```go
// Build providers
proxyHandler := proxy.NewHandler()
oauthManagers := make(map[string]*oauth.Manager) // only OAuth providers

for _, name := range cfg.ProviderOrder {
	prov := cfg.Providers[name]
	var resolver auth.Resolver

	switch prov.Auth.Type {
	case "oauth":
		mgr := oauth.NewManager(oauth.OAuthConfig{
			Name:         name,
			ClientID:     prov.Auth.ClientID,
			AuthorizeURL: prov.Auth.AuthorizeURL,
			TokenURL:     prov.Auth.TokenURL,
			Scopes:       prov.Auth.Scopes,
		})
		oauthManagers[name] = mgr
		resolver = auth.NewOAuthResolver(mgr)
	case "api_key":
		resolver = auth.NewStaticKeyResolver(
			config.ExpandEnvVars(prov.Auth.APIKey),
			prov.Auth.HeaderName,
			prov.Auth.HeaderPrefix,
		)
	}

	proxyHandler.AddProvider(name, &proxy.ProviderEntry{
		Upstream: prov.Upstream,
		Auth:     resolver,
		Headers:  prov.Headers,
		Patterns: prov.Models,
	})
}

// OAuth routes — per provider
for name, mgr := range oauthManagers {
	provName := name
	provMgr := mgr
	mux.HandleFunc("GET /auth/login/"+provName, func(w http.ResponseWriter, r *http.Request) { ... })
	mux.HandleFunc("GET /auth/get-url/"+provName, func(w http.ResponseWriter, r *http.Request) { ... })
	mux.HandleFunc("GET /auth/callback/"+provName, func(w http.ResponseWriter, r *http.Request) { ... })
}

// Auth status — all providers
mux.HandleFunc("GET /auth/status", func(w http.ResponseWriter, r *http.Request) {
	status := make(map[string]interface{})
	for name, mgr := range oauthManagers {
		isAuth := mgr.IsAuthenticated()
		entry := map[string]interface{}{"authenticated": isAuth, "type": "oauth"}
		if exp := mgr.GetTokenExpiration(); exp != nil {
			entry["expires_at"] = exp.Format(time.RFC3339)
		}
		status[name] = entry
	}
	// api_key providers are always "authenticated"
	for _, name := range cfg.ProviderOrder {
		if _, isOAuth := oauthManagers[name]; !isOAuth {
			status[name] = map[string]interface{}{"authenticated": true, "type": "api_key"}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
})

// Catch-all proxy route
mux.Handle("POST /", proxyHandler)
```

**Step 2: Build and verify**

Run: `go build ./cmd/claude-code-proxy`
Expected: Compiles

**Step 3: Run all tests**

Run: `go test ./... -v -count=1`
Expected: All pass

**Step 4: Commit**

```bash
git add cmd/claude-code-proxy/main.go
git commit -m "feat: rewire main.go with multi-provider routing and per-provider OAuth"
```

---

### Task 7: Delete Unused Packages

**Files:**
- Delete: `internal/preset/`
- Delete: `internal/transform/`
- Delete: `cmd/claude-code-proxy/presets/`

**Step 1: Remove directories**

```bash
rm -rf internal/preset internal/transform cmd/claude-code-proxy/presets
```

**Step 2: Remove embed directive from main.go if still present**

Remove `//go:embed presets/*` and `var presetsFS embed.FS` if still in main.go.

**Step 3: Run all tests**

Run: `go test ./... -v -count=1`
Expected: All pass

**Step 4: Commit**

```bash
git add -u
git commit -m "chore: remove unused preset and transform packages"
```

---

### Task 8: Update Login Page for Multi-Provider

**Files:**
- Modify: `cmd/claude-code-proxy/static/login.html`

**Step 1: Update login.html**

The login page needs to be aware of which provider it's authenticating. The simplest approach: the `/auth/login/{provider}` handler serves the same HTML but with the provider name injected (or the HTML fetches `/auth/get-url/{provider}`).

Update the JavaScript in login.html to extract the provider name from the URL path and call `/auth/get-url/{provider}` instead of `/auth/get-url`.

**Step 2: Verify manually**

Run: `go build -o claude-code-proxy ./cmd/claude-code-proxy && ./claude-code-proxy --port 5577`
Visit: `http://localhost:5577/auth/login/anthropic`

**Step 3: Commit**

```bash
git add cmd/claude-code-proxy/static/login.html
git commit -m "feat: update login page for per-provider OAuth flow"
```

---

### Task 9: Update README

**Files:**
- Modify: `README.md`

Update to document:
- Multi-provider YAML config
- Model-based routing
- Per-provider auth types
- OAuth per-provider login URLs
- Example configs for Anthropic, OpenAI, Gemini

**Step 1: Commit**

```bash
git add README.md
git commit -m "docs: update README for multi-provider proxy"
```

---

### Task 10: Integration Smoke Test

**Step 1: Build and run**

```bash
go build -o claude-code-proxy ./cmd/claude-code-proxy
./claude-code-proxy --port 5577
```

**Step 2: Verify endpoints**

```bash
curl http://localhost:5577/health
curl http://localhost:5577/auth/status
```

**Step 3: Run full test suite**

```bash
go test ./... -v -count=1
```

**Step 4: Final commit and tag**

```bash
git add -A
git commit -m "v0.2.0: generic multi-provider LLM proxy"
git tag v0.2.0
git push origin main v0.2.0
```
