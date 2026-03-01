# Go Implementation Plan — Claude Code Proxy

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Rewrite claude-code-proxy in Go with all features plus enhancements (YAML config, CLI flags, env vars, context cancellation, single-flight token refresh, structured logging).

**Architecture:** Flat internal packages under `go/internal/` — config, logger, oauth, auth, transform, preset, proxy. Single entry point at `go/cmd/claude-code-proxy/main.go`. Static assets embedded via `go:embed`.

**Tech Stack:** Go 1.22+, `net/http` stdlib, `log/slog`, `gopkg.in/yaml.v3`, `go:embed`

---

### Task 1: Project Scaffolding

**Files:**
- Create: `go/go.mod`
- Create: `go/cmd/claude-code-proxy/main.go` (minimal — just prints "starting")
- Create: `go/config.yaml`

**Step 1: Create directory structure**

```bash
mkdir -p go/cmd/claude-code-proxy go/internal/{config,logger,oauth,auth,transform,preset,proxy} go/static go/presets
```

**Step 2: Initialize go module**

```bash
cd go && go mod init github.com/anthropics/claude-code-proxy && go get gopkg.in/yaml.v3
```

**Step 3: Create minimal main.go**

Create `go/cmd/claude-code-proxy/main.go`:

```go
package main

import "fmt"

func main() {
	fmt.Println("claude-code-proxy starting...")
}
```

**Step 4: Create default config.yaml**

Create `go/config.yaml`:

```yaml
server:
  port: 42069
  host: "" # Auto-detect: 127.0.0.1 native, 0.0.0.0 Docker

logging:
  level: info # trace, debug, info, warn, error

proxy:
  filter_sampling_params: false
  strip_ttl: true

auth:
  auto_open_browser: true
  fallback_to_claude_code: true
```

**Step 5: Verify it compiles**

Run: `cd go && go build ./cmd/claude-code-proxy`
Expected: builds successfully

**Step 6: Commit**

```bash
git add go/
git commit -m "feat(go): scaffold Go project with module and config"
```

---

### Task 2: Logger Package

**Files:**
- Create: `go/internal/logger/logger.go`
- Test: `go/internal/logger/logger_test.go`

**Step 1: Write the failing test**

Create `go/internal/logger/logger_test.go`:

```go
package logger

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"trace", LevelTrace},
		{"TRACE", LevelTrace},
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"invalid", slog.LevelInfo}, // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseLevel(tt.input)
			if got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestInit(t *testing.T) {
	var buf bytes.Buffer
	Init("debug", &buf)

	slog.Info("test message", "key", "value")
	output := buf.String()

	if !strings.Contains(output, "test message") {
		t.Errorf("expected log output to contain 'test message', got: %s", output)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	Init("warn", &buf)

	slog.Info("should not appear")
	slog.Warn("should appear")

	output := buf.String()
	if strings.Contains(output, "should not appear") {
		t.Error("info message should be filtered at warn level")
	}
	if !strings.Contains(output, "should appear") {
		t.Error("warn message should appear at warn level")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd go && go test ./internal/logger/ -v`
Expected: FAIL (package doesn't exist yet)

**Step 3: Write implementation**

Create `go/internal/logger/logger.go`:

```go
package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// LevelTrace is a custom level below Debug.
const LevelTrace = slog.Level(-8)

// ParseLevel converts a string log level name to slog.Level.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "trace":
		return LevelTrace
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Init configures the default slog logger with the given level.
// If w is nil, os.Stderr is used.
func Init(level string, w io.Writer) {
	if w == nil {
		w = os.Stderr
	}

	lvl := ParseLevel(level)
	opts := &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Replace the custom trace level name
			if a.Key == slog.LevelKey {
				if a.Value.Any().(slog.Level) == LevelTrace {
					a.Value = slog.StringValue("TRACE")
				}
			}
			return a
		},
	}

	handler := slog.NewTextHandler(w, opts)
	slog.SetDefault(slog.New(handler))
}
```

**Step 4: Run test to verify it passes**

Run: `cd go && go test ./internal/logger/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add go/internal/logger/
git commit -m "feat(go): add logger package with slog and custom TRACE level"
```

---

### Task 3: Config Package

**Files:**
- Create: `go/internal/config/config.go`
- Test: `go/internal/config/config_test.go`

**Step 1: Write the failing test**

Create `go/internal/config/config_test.go`:

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
	if cfg.Proxy.StripTTL != true {
		t.Error("default strip_ttl should be true")
	}
	if cfg.Auth.FallbackToClaudeCode != true {
		t.Error("default fallback_to_claude_code should be true")
	}
}

func TestLoadFromYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(yamlPath, []byte(`
server:
  port: 8080
  host: "0.0.0.0"
logging:
  level: debug
proxy:
  filter_sampling_params: true
  strip_ttl: false
auth:
  auto_open_browser: false
  fallback_to_claude_code: false
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("host = %q, want 0.0.0.0", cfg.Server.Host)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("log level = %q, want debug", cfg.Logging.Level)
	}
	if cfg.Proxy.FilterSamplingParams != true {
		t.Error("filter_sampling_params should be true")
	}
	if cfg.Proxy.StripTTL != false {
		t.Error("strip_ttl should be false")
	}
	if cfg.Auth.AutoOpenBrowser != false {
		t.Error("auto_open_browser should be false")
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load("/nonexistent/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 42069 {
		t.Errorf("should fall back to default port, got %d", cfg.Server.Port)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("CCP_SERVER_PORT", "9999")
	t.Setenv("CCP_LOG_LEVEL", "error")
	t.Setenv("CCP_PROXY_FILTER_SAMPLING_PARAMS", "true")

	cfg := Defaults()
	ApplyEnv(&cfg)

	if cfg.Server.Port != 9999 {
		t.Errorf("env port = %d, want 9999", cfg.Server.Port)
	}
	if cfg.Logging.Level != "error" {
		t.Errorf("env log level = %q, want error", cfg.Logging.Level)
	}
	if cfg.Proxy.FilterSamplingParams != true {
		t.Error("env filter_sampling_params should be true")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd go && go test ./internal/config/ -v`
Expected: FAIL

**Step 3: Write implementation**

Create `go/internal/config/config.go`:

```go
package config

import (
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration.
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Logging LoggingConfig `yaml:"logging"`
	Proxy   ProxyConfig   `yaml:"proxy"`
	Auth    AuthConfig    `yaml:"auth"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
}

type ProxyConfig struct {
	FilterSamplingParams bool `yaml:"filter_sampling_params"`
	StripTTL             bool `yaml:"strip_ttl"`
}

type AuthConfig struct {
	AutoOpenBrowser      bool `yaml:"auto_open_browser"`
	FallbackToClaudeCode bool `yaml:"fallback_to_claude_code"`
}

// Defaults returns a Config with default values.
func Defaults() Config {
	return Config{
		Server: ServerConfig{
			Port: 42069,
			Host: "",
		},
		Logging: LoggingConfig{
			Level: "info",
		},
		Proxy: ProxyConfig{
			FilterSamplingParams: false,
			StripTTL:             true,
		},
		Auth: AuthConfig{
			AutoOpenBrowser:      true,
			FallbackToClaudeCode: true,
		},
	}
}

// Load reads a YAML config file and merges it with defaults.
// If the file doesn't exist, defaults are returned without error.
func Load(path string) (Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// ApplyEnv overrides config values from environment variables.
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
	if v := os.Getenv("CCP_PROXY_FILTER_SAMPLING_PARAMS"); v != "" {
		cfg.Proxy.FilterSamplingParams = v == "true" || v == "1"
	}
	if v := os.Getenv("CCP_PROXY_STRIP_TTL"); v != "" {
		cfg.Proxy.StripTTL = v == "true" || v == "1"
	}
	if v := os.Getenv("CCP_AUTH_AUTO_OPEN_BROWSER"); v != "" {
		cfg.Auth.AutoOpenBrowser = v == "true" || v == "1"
	}
	if v := os.Getenv("CCP_AUTH_FALLBACK_TO_CLAUDE_CODE"); v != "" {
		cfg.Auth.FallbackToClaudeCode = v == "true" || v == "1"
	}
}
```

**Step 4: Run test to verify it passes**

Run: `cd go && go test ./internal/config/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add go/internal/config/
git commit -m "feat(go): add config package with YAML, env var overrides"
```

---

### Task 4: OAuth Package

**Files:**
- Create: `go/internal/oauth/oauth.go`
- Test: `go/internal/oauth/oauth_test.go`

**Step 1: Write the failing test**

Create `go/internal/oauth/oauth_test.go`:

```go
package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratePKCE(t *testing.T) {
	pkce := GeneratePKCE()

	if len(pkce.CodeVerifier) == 0 {
		t.Error("code_verifier should not be empty")
	}
	if len(pkce.CodeChallenge) == 0 {
		t.Error("code_challenge should not be empty")
	}
	if len(pkce.State) == 0 {
		t.Error("state should not be empty")
	}

	// Verify uniqueness
	pkce2 := GeneratePKCE()
	if pkce.State == pkce2.State {
		t.Error("two PKCE generations should produce different states")
	}
}

func TestBuildAuthorizationURL(t *testing.T) {
	pkce := PKCE{
		CodeChallenge: "test_challenge",
		State:         "test_state",
	}

	url := BuildAuthorizationURL(pkce)

	if url == "" {
		t.Fatal("URL should not be empty")
	}

	// Should contain required params
	for _, param := range []string{"client_id=", "response_type=code", "code_challenge=test_challenge", "state=test_state"} {
		if !contains(url, param) {
			t.Errorf("URL missing param %q: %s", param, url)
		}
	}
}

func TestSaveAndLoadTokens(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "tokens.json")

	mgr := &Manager{TokenPath: tokenPath}

	tokens := &Tokens{
		AccessToken:  "test_access",
		RefreshToken: "test_refresh",
		ExpiresAt:    1234567890000,
	}

	if err := mgr.SaveTokens(tokens); err != nil {
		t.Fatal(err)
	}

	loaded, err := mgr.LoadTokens()
	if err != nil {
		t.Fatal(err)
	}

	if loaded.AccessToken != "test_access" {
		t.Errorf("access_token = %q, want test_access", loaded.AccessToken)
	}
	if loaded.RefreshToken != "test_refresh" {
		t.Errorf("refresh_token = %q, want test_refresh", loaded.RefreshToken)
	}
}

func TestLoadTokensMissing(t *testing.T) {
	mgr := &Manager{TokenPath: "/nonexistent/tokens.json"}
	tokens, err := mgr.LoadTokens()
	if err != nil {
		t.Fatal(err)
	}
	if tokens != nil {
		t.Error("should return nil for missing file")
	}
}

func TestIsAuthenticated(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "tokens.json")
	mgr := &Manager{TokenPath: tokenPath}

	if mgr.IsAuthenticated() {
		t.Error("should not be authenticated without tokens")
	}

	mgr.SaveTokens(&Tokens{
		AccessToken:  "a",
		RefreshToken: "r",
		ExpiresAt:    9999999999999,
	})

	if !mgr.IsAuthenticated() {
		t.Error("should be authenticated after saving tokens")
	}
}

func TestLogout(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "tokens.json")
	mgr := &Manager{TokenPath: tokenPath}

	mgr.SaveTokens(&Tokens{AccessToken: "a", RefreshToken: "r", ExpiresAt: 1})

	if err := mgr.Logout(); err != nil {
		t.Fatal(err)
	}

	if mgr.IsAuthenticated() {
		t.Error("should not be authenticated after logout")
	}
}

func TestExchangeCodeForTokens(t *testing.T) {
	// Mock token server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)

		if body["grant_type"] != "authorization_code" {
			t.Errorf("grant_type = %q, want authorization_code", body["grant_type"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "new_access",
			"refresh_token": "new_refresh",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	mgr := &Manager{
		TokenPath: filepath.Join(dir, "tokens.json"),
		TokenURL:  server.URL,
	}

	tokens, err := mgr.ExchangeCodeForTokens("test_code", "test_verifier", "test_state")
	if err != nil {
		t.Fatal(err)
	}

	if tokens.AccessToken != "new_access" {
		t.Errorf("access_token = %q, want new_access", tokens.AccessToken)
	}
}

func TestRefreshAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)

		if body["grant_type"] != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", body["grant_type"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "refreshed_access",
			"refresh_token": "refreshed_refresh",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "tokens.json")
	mgr := &Manager{
		TokenPath: tokenPath,
		TokenURL:  server.URL,
	}

	// Save existing tokens first
	mgr.SaveTokens(&Tokens{
		AccessToken:  "old_access",
		RefreshToken: "old_refresh",
		ExpiresAt:    1, // expired
	})

	resp, err := mgr.RefreshAccessToken()
	if err != nil {
		t.Fatal(err)
	}

	if resp.AccessToken != "refreshed_access" {
		t.Errorf("access_token = %q, want refreshed_access", resp.AccessToken)
	}

	// Verify tokens were saved
	loaded, _ := mgr.LoadTokens()
	if loaded.AccessToken != "refreshed_access" {
		t.Error("refreshed tokens should be persisted")
	}
}

func TestFilePermissions(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("skipping permission test in CI")
	}

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "tokens.json")
	mgr := &Manager{TokenPath: tokenPath}

	mgr.SaveTokens(&Tokens{AccessToken: "a", RefreshToken: "r", ExpiresAt: 1})

	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatal(err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file permissions = %o, want 600", perm)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

**Step 2: Run test to verify it fails**

Run: `cd go && go test ./internal/oauth/ -v`
Expected: FAIL

**Step 3: Write implementation**

Create `go/internal/oauth/oauth.go`:

```go
package oauth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const (
	DefaultClientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	DefaultAuthorizeURL = "https://claude.ai/oauth/authorize"
	DefaultTokenURL    = "https://console.anthropic.com/v1/oauth/token"
	DefaultRedirectURI = "https://console.anthropic.com/oauth/code/callback"
	DefaultScope       = "org:create_api_key user:profile user:inference"
)

// PKCE holds the PKCE parameters for an OAuth flow.
type PKCE struct {
	CodeVerifier  string
	CodeChallenge string
	State         string
}

// Tokens represents stored OAuth tokens.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

// TokenResponse is the response from the token endpoint.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// Manager manages OAuth tokens.
type Manager struct {
	TokenPath string
	TokenURL  string // overridable for testing

	mu             sync.Mutex
	refreshPromise chan struct{}
	cachedToken    string
}

// NewManager creates a new OAuth manager with the default token path.
func NewManager() *Manager {
	home, _ := os.UserHomeDir()
	return &Manager{
		TokenPath: filepath.Join(home, ".claude-code-proxy", "tokens.json"),
		TokenURL:  DefaultTokenURL,
	}
}

// GeneratePKCE creates a new PKCE code verifier, challenge, and state.
func GeneratePKCE() PKCE {
	verifier := make([]byte, 32)
	rand.Read(verifier)
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifier)

	hash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(hash[:])

	stateBytes := make([]byte, 32)
	rand.Read(stateBytes)
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	return PKCE{
		CodeVerifier:  codeVerifier,
		CodeChallenge: codeChallenge,
		State:         state,
	}
}

// BuildAuthorizationURL constructs the OAuth authorization URL.
func BuildAuthorizationURL(pkce PKCE) string {
	params := url.Values{
		"code":                  {"true"},
		"client_id":             {DefaultClientID},
		"response_type":         {"code"},
		"redirect_uri":          {DefaultRedirectURI},
		"scope":                 {DefaultScope},
		"code_challenge":        {pkce.CodeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {pkce.State},
	}
	return DefaultAuthorizeURL + "?" + params.Encode()
}

// ExchangeCodeForTokens exchanges an authorization code for tokens.
func (m *Manager) ExchangeCodeForTokens(code, codeVerifier, state string) (*TokenResponse, error) {
	payload := map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"state":         state,
		"client_id":     DefaultClientID,
		"code_verifier": codeVerifier,
		"redirect_uri":  DefaultRedirectURI,
	}
	return m.makeTokenRequest(payload)
}

// RefreshAccessToken refreshes the access token using the stored refresh token.
// Uses single-flight pattern to prevent concurrent refreshes.
func (m *Manager) RefreshAccessToken() (*TokenResponse, error) {
	m.mu.Lock()

	// If a refresh is already in progress, wait for it
	if m.refreshPromise != nil {
		ch := m.refreshPromise
		m.mu.Unlock()
		<-ch
		// After waiting, load the freshly saved tokens
		tokens, err := m.LoadTokens()
		if err != nil || tokens == nil {
			return nil, fmt.Errorf("refresh completed but failed to load tokens")
		}
		return &TokenResponse{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken}, nil
	}

	m.refreshPromise = make(chan struct{})
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		close(m.refreshPromise)
		m.refreshPromise = nil
		m.mu.Unlock()
	}()

	tokens, err := m.LoadTokens()
	if err != nil || tokens == nil || tokens.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	payload := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": tokens.RefreshToken,
		"client_id":     DefaultClientID,
	}

	resp, err := m.makeTokenRequest(payload)
	if err != nil {
		return nil, err
	}

	slog.Info("Successfully refreshed access token")

	newRefresh := resp.RefreshToken
	if newRefresh == "" {
		newRefresh = tokens.RefreshToken
	}

	newTokens := &Tokens{
		AccessToken:  resp.AccessToken,
		RefreshToken: newRefresh,
		ExpiresAt:    time.Now().UnixMilli() + int64(resp.ExpiresIn)*1000,
	}
	if err := m.SaveTokens(newTokens); err != nil {
		return nil, err
	}

	m.cachedToken = resp.AccessToken
	return resp, nil
}

// GetValidAccessToken returns a valid access token, refreshing if needed.
func (m *Manager) GetValidAccessToken() (string, error) {
	tokens, err := m.LoadTokens()
	if err != nil || tokens == nil {
		return "", fmt.Errorf("no authentication tokens found")
	}

	// Check if token is expired or expiring soon (1 minute buffer)
	if tokens.ExpiresAt <= time.Now().UnixMilli()+60000 {
		slog.Info("Access token expired or expiring soon, refreshing...")
		resp, err := m.RefreshAccessToken()
		if err != nil {
			return "", err
		}
		return resp.AccessToken, nil
	}

	return tokens.AccessToken, nil
}

// LoadTokens reads tokens from disk.
func (m *Manager) LoadTokens() (*Tokens, error) {
	data, err := os.ReadFile(m.TokenPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var tokens Tokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, err
	}
	return &tokens, nil
}

// SaveTokens writes tokens to disk with secure permissions.
func (m *Manager) SaveTokens(tokens *Tokens) error {
	dir := filepath.Dir(m.TokenPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(m.TokenPath, data, 0o600); err != nil {
		return err
	}

	// Explicit chmod for non-Windows systems
	if runtime.GOOS != "windows" {
		os.Chmod(m.TokenPath, 0o600)
	}

	slog.Info("Tokens saved successfully")
	return nil
}

// IsAuthenticated checks if valid tokens exist.
func (m *Manager) IsAuthenticated() bool {
	tokens, err := m.LoadTokens()
	if err != nil || tokens == nil {
		return false
	}
	return tokens.AccessToken != "" && tokens.RefreshToken != ""
}

// GetTokenExpiration returns the token expiration time.
func (m *Manager) GetTokenExpiration() *time.Time {
	tokens, err := m.LoadTokens()
	if err != nil || tokens == nil || tokens.ExpiresAt == 0 {
		return nil
	}
	t := time.UnixMilli(tokens.ExpiresAt)
	return &t
}

// Logout deletes stored tokens.
func (m *Manager) Logout() error {
	m.cachedToken = ""
	if err := os.Remove(m.TokenPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	slog.Info("Tokens deleted successfully")
	return nil
}

func (m *Manager) makeTokenRequest(payload map[string]string) (*TokenResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	tokenURL := m.TokenURL
	if tokenURL == "" {
		tokenURL = DefaultTokenURL
	}

	req, err := http.NewRequest("POST", tokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokenResp, nil
}
```

**Step 4: Run test to verify it passes**

Run: `cd go && go test ./internal/oauth/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add go/internal/oauth/
git commit -m "feat(go): add OAuth package with PKCE, token exchange, refresh"
```

---

### Task 5: Auth Package (Token Resolver)

**Files:**
- Create: `go/internal/auth/auth.go`
- Test: `go/internal/auth/auth_test.go`

**Step 1: Write the failing test**

Create `go/internal/auth/auth_test.go`:

```go
package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropics/claude-code-proxy/internal/oauth"
)

func TestResolveFromHeader(t *testing.T) {
	r := &Resolver{OAuthMgr: &oauth.Manager{TokenPath: "/nonexistent"}}
	token, err := r.Resolve("sk-ant-my-api-key")
	if err != nil {
		t.Fatal(err)
	}
	if token != "Bearer sk-ant-my-api-key" {
		t.Errorf("token = %q, want Bearer sk-ant-my-api-key", token)
	}
}

func TestResolveFromHeaderAlreadyBearer(t *testing.T) {
	r := &Resolver{OAuthMgr: &oauth.Manager{TokenPath: "/nonexistent"}}
	token, err := r.Resolve("Bearer sk-ant-test")
	if err != nil {
		t.Fatal(err)
	}
	if token != "Bearer sk-ant-test" {
		t.Errorf("token = %q, want Bearer sk-ant-test", token)
	}
}

func TestResolveFromOAuth(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "tokens.json")

	mgr := &oauth.Manager{TokenPath: tokenPath}
	mgr.SaveTokens(&oauth.Tokens{
		AccessToken:  "oauth_token",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(time.Hour).UnixMilli(),
	})

	r := &Resolver{OAuthMgr: mgr, FallbackToClaudeCode: false}
	token, err := r.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if token != "Bearer oauth_token" {
		t.Errorf("token = %q, want Bearer oauth_token", token)
	}
}

func TestResolveFromClaudeCodeCredentials(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, ".claude", ".credentials.json")
	os.MkdirAll(filepath.Dir(credPath), 0o755)

	creds := map[string]interface{}{
		"claudeAiOauth": map[string]interface{}{
			"accessToken":  "cli_token",
			"refreshToken": "cli_refresh",
			"expiresAt":    float64(time.Now().Add(time.Hour).UnixMilli()),
		},
	}
	data, _ := json.Marshal(creds)
	os.WriteFile(credPath, data, 0o644)

	oauthMgr := &oauth.Manager{TokenPath: filepath.Join(dir, "nonexistent", "tokens.json")}
	r := &Resolver{
		OAuthMgr:             oauthMgr,
		FallbackToClaudeCode: true,
		ClaudeCredPath:       credPath,
	}

	token, err := r.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if token != "Bearer cli_token" {
		t.Errorf("token = %q, want Bearer cli_token", token)
	}
}

func TestResolveNoAuth(t *testing.T) {
	mgr := &oauth.Manager{TokenPath: "/nonexistent/tokens.json"}
	r := &Resolver{OAuthMgr: mgr, FallbackToClaudeCode: false}

	_, err := r.Resolve("")
	if err == nil {
		t.Error("expected error when no auth available")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd go && go test ./internal/auth/ -v`
Expected: FAIL

**Step 3: Write implementation**

Create `go/internal/auth/auth.go`:

```go
package auth

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anthropics/claude-code-proxy/internal/oauth"
)

// Resolver resolves authentication tokens in priority order:
// 1. x-api-key header value
// 2. OAuth tokens (auto-refresh)
// 3. Claude Code CLI credentials (fallback)
type Resolver struct {
	OAuthMgr             *oauth.Manager
	FallbackToClaudeCode bool
	ClaudeCredPath       string // override for testing; empty = default

	mu          sync.Mutex
	cachedToken string
}

// Resolve returns a Bearer token string. If apiKeyHeader is non-empty, it's
// used directly. Otherwise OAuth tokens are tried, then Claude Code credentials.
func (r *Resolver) Resolve(apiKeyHeader string) (string, error) {
	if apiKeyHeader != "" {
		slog.Debug("Using x-api-key header as token")
		token := apiKeyHeader
		if !strings.HasPrefix(token, "Bearer ") {
			token = "Bearer " + token
		}
		r.mu.Lock()
		r.cachedToken = token
		r.mu.Unlock()
		return token, nil
	}

	r.mu.Lock()
	cached := r.cachedToken
	r.mu.Unlock()

	if cached != "" {
		// Verify OAuth tokens are still valid
		if r.OAuthMgr.IsAuthenticated() {
			tok, err := r.OAuthMgr.GetValidAccessToken()
			if err == nil {
				bearer := "Bearer " + tok
				r.mu.Lock()
				r.cachedToken = bearer
				r.mu.Unlock()
				return bearer, nil
			}
		}
	}

	// Try OAuth
	if r.OAuthMgr.IsAuthenticated() {
		slog.Debug("Using OAuth tokens")
		tok, err := r.OAuthMgr.GetValidAccessToken()
		if err == nil {
			bearer := "Bearer " + tok
			r.mu.Lock()
			r.cachedToken = bearer
			r.mu.Unlock()
			return bearer, nil
		}
		slog.Warn("OAuth token retrieval failed", "error", err)
	}

	// Fallback to Claude Code credentials
	if r.FallbackToClaudeCode {
		slog.Debug("Falling back to Claude Code credentials")
		tok, err := r.loadClaudeCodeToken()
		if err == nil {
			r.mu.Lock()
			r.cachedToken = tok
			r.mu.Unlock()
			return tok, nil
		}
		slog.Warn("Claude Code credential fallback failed", "error", err)
	}

	return "", fmt.Errorf("no authentication tokens found; please authenticate at /auth/login")
}

// ClearCache clears the cached token so the next Resolve re-reads from storage.
func (r *Resolver) ClearCache() {
	r.mu.Lock()
	r.cachedToken = ""
	r.mu.Unlock()
}

func (r *Resolver) credentialsPath() string {
	if r.ClaudeCredPath != "" {
		return r.ClaudeCredPath
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", ".credentials.json")
}

func (r *Resolver) loadClaudeCodeToken() (string, error) {
	data, err := os.ReadFile(r.credentialsPath())
	if err != nil {
		return "", fmt.Errorf("failed to read Claude Code credentials: %w", err)
	}

	var creds struct {
		ClaudeAiOauth struct {
			AccessToken  string  `json:"accessToken"`
			RefreshToken string  `json:"refreshToken"`
			ExpiresAt    float64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}

	if err := json.Unmarshal(data, &creds); err != nil {
		return "", fmt.Errorf("failed to parse credentials: %w", err)
	}

	return "Bearer " + creds.ClaudeAiOauth.AccessToken, nil
}
```

**Step 4: Run test to verify it passes**

Run: `cd go && go test ./internal/auth/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add go/internal/auth/
git commit -m "feat(go): add auth package with token resolution priority"
```

---

### Task 6: Transform Package

**Files:**
- Create: `go/internal/transform/transform.go`
- Test: `go/internal/transform/transform_test.go`

**Step 1: Write the failing test**

Create `go/internal/transform/transform_test.go`:

```go
package transform

import (
	"encoding/json"
	"testing"
)

func TestInjectSystemPrompt_NoExisting(t *testing.T) {
	body := map[string]interface{}{
		"messages": []interface{}{},
	}

	InjectSystemPrompt(body)

	system, ok := body["system"].([]interface{})
	if !ok {
		t.Fatal("system should be an array")
	}
	if len(system) != 1 {
		t.Fatalf("system length = %d, want 1", len(system))
	}

	entry := system[0].(map[string]interface{})
	if entry["text"] != ClaudeCodeSystemPrompt {
		t.Errorf("system text = %q, want %q", entry["text"], ClaudeCodeSystemPrompt)
	}
}

func TestInjectSystemPrompt_ExistingArray(t *testing.T) {
	body := map[string]interface{}{
		"system": []interface{}{
			map[string]interface{}{"type": "text", "text": "existing"},
		},
	}

	InjectSystemPrompt(body)

	system := body["system"].([]interface{})
	if len(system) != 2 {
		t.Fatalf("system length = %d, want 2", len(system))
	}

	first := system[0].(map[string]interface{})
	if first["text"] != ClaudeCodeSystemPrompt {
		t.Error("claude code prompt should be first")
	}
}

func TestInjectSystemPrompt_ExistingString(t *testing.T) {
	body := map[string]interface{}{
		"system": "existing prompt",
	}

	InjectSystemPrompt(body)

	system := body["system"].([]interface{})
	if len(system) != 2 {
		t.Fatalf("system length = %d, want 2", len(system))
	}
}

func TestStripTTL(t *testing.T) {
	body := map[string]interface{}{
		"system": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "hello",
				"cache_control": map[string]interface{}{
					"type": "ephemeral",
					"ttl":  float64(300),
				},
			},
		},
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "hi",
						"cache_control": map[string]interface{}{
							"type": "ephemeral",
							"ttl":  float64(600),
						},
					},
				},
			},
		},
	}

	StripTTL(body)

	// Check system
	sys := body["system"].([]interface{})
	cc := sys[0].(map[string]interface{})["cache_control"].(map[string]interface{})
	if _, hasTTL := cc["ttl"]; hasTTL {
		t.Error("ttl should be stripped from system cache_control")
	}
	if cc["type"] != "ephemeral" {
		t.Error("cache_control type should be preserved")
	}

	// Check messages
	msgs := body["messages"].([]interface{})
	content := msgs[0].(map[string]interface{})["content"].([]interface{})
	mcc := content[0].(map[string]interface{})["cache_control"].(map[string]interface{})
	if _, hasTTL := mcc["ttl"]; hasTTL {
		t.Error("ttl should be stripped from message cache_control")
	}
}

func TestStripTTL_NoCacheControl(t *testing.T) {
	body := map[string]interface{}{
		"system": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
		},
	}
	StripTTL(body) // should not panic
}

func TestFilterSamplingParams_BothPresent_BothDefault(t *testing.T) {
	body := map[string]interface{}{
		"temperature": 1.0,
		"top_p":       1.0,
	}
	FilterSamplingParams(body)

	if _, ok := body["top_p"]; ok {
		t.Error("top_p should be removed when both are default")
	}
	if _, ok := body["temperature"]; !ok {
		t.Error("temperature should be kept when both are default")
	}
}

func TestFilterSamplingParams_BothPresent_OnlyTopPDefault(t *testing.T) {
	body := map[string]interface{}{
		"temperature": 0.7,
		"top_p":       1.0,
	}
	FilterSamplingParams(body)

	if _, ok := body["top_p"]; ok {
		t.Error("top_p should be removed")
	}
	if body["temperature"] != 0.7 {
		t.Error("temperature should be preserved")
	}
}

func TestFilterSamplingParams_BothPresent_OnlyTempDefault(t *testing.T) {
	body := map[string]interface{}{
		"temperature": 1.0,
		"top_p":       0.9,
	}
	FilterSamplingParams(body)

	if _, ok := body["temperature"]; ok {
		t.Error("temperature should be removed")
	}
	if body["top_p"] != 0.9 {
		t.Error("top_p should be preserved")
	}
}

func TestFilterSamplingParams_BothPresent_BothNonDefault(t *testing.T) {
	body := map[string]interface{}{
		"temperature": 0.5,
		"top_p":       0.8,
	}
	FilterSamplingParams(body)

	if _, ok := body["top_p"]; ok {
		t.Error("top_p should be removed (prefer temperature)")
	}
	if body["temperature"] != 0.5 {
		t.Error("temperature should be preserved")
	}
}

func TestFilterSamplingParams_OnlyTopP_Default(t *testing.T) {
	body := map[string]interface{}{
		"top_p": 1.0,
	}
	FilterSamplingParams(body)

	if _, ok := body["top_p"]; ok {
		t.Error("top_p=1.0 should be removed when alone")
	}
}

func TestFilterSamplingParams_OnlyTemperature_Default(t *testing.T) {
	body := map[string]interface{}{
		"temperature": 1.0,
	}
	FilterSamplingParams(body)

	if _, ok := body["temperature"]; ok {
		t.Error("temperature=1.0 should be removed when alone")
	}
}

func TestFilterSamplingParams_OnlyNonDefault(t *testing.T) {
	body := map[string]interface{}{
		"temperature": 0.7,
	}
	FilterSamplingParams(body)

	if body["temperature"] != 0.7 {
		t.Error("non-default temperature should be preserved")
	}
}

func TestProcessRequestBody(t *testing.T) {
	raw := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hello"}],"temperature":1.0,"top_p":1.0}`

	var body map[string]interface{}
	json.Unmarshal([]byte(raw), &body)

	ProcessRequestBody(body, true, true)

	// Should have system prompt injected
	sys, ok := body["system"].([]interface{})
	if !ok || len(sys) == 0 {
		t.Error("system prompt should be injected")
	}

	// With filtering on, top_p should be removed
	if _, ok := body["top_p"]; ok {
		t.Error("top_p should be filtered")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd go && go test ./internal/transform/ -v`
Expected: FAIL

**Step 3: Write implementation**

Create `go/internal/transform/transform.go`:

```go
package transform

import "log/slog"

// ClaudeCodeSystemPrompt is the required system prompt prefix.
const ClaudeCodeSystemPrompt = "You are Claude Code, Anthropic's official CLI for Claude."

// InjectSystemPrompt prepends the Claude Code system prompt to the request body.
func InjectSystemPrompt(body map[string]interface{}) {
	prompt := map[string]interface{}{
		"type": "text",
		"text": ClaudeCodeSystemPrompt,
	}

	existing, exists := body["system"]
	if !exists {
		body["system"] = []interface{}{prompt}
		return
	}

	switch v := existing.(type) {
	case []interface{}:
		body["system"] = append([]interface{}{prompt}, v...)
	default:
		body["system"] = []interface{}{prompt, v}
	}
}

// StripTTL removes ttl fields from cache_control objects in system and messages.
func StripTTL(body map[string]interface{}) {
	if sys, ok := body["system"].([]interface{}); ok {
		stripTTLFromContentArray(sys)
	}

	if msgs, ok := body["messages"].([]interface{}); ok {
		for _, msg := range msgs {
			if m, ok := msg.(map[string]interface{}); ok {
				if content, ok := m["content"].([]interface{}); ok {
					stripTTLFromContentArray(content)
				}
			}
		}
	}
}

func stripTTLFromContentArray(items []interface{}) {
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		cc, ok := m["cache_control"].(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasTTL := cc["ttl"]; hasTTL {
			delete(cc, "ttl")
			slog.Debug("Removed ttl from cache_control")
		}
	}
}

// FilterSamplingParams resolves conflicts between temperature and top_p.
func FilterSamplingParams(body map[string]interface{}) {
	tempVal, hasTemp := body["temperature"]
	topPVal, hasTopP := body["top_p"]

	tempF, tempIsFloat := toFloat64(tempVal)
	topPF, topPIsFloat := toFloat64(topPVal)

	if hasTemp && hasTopP {
		tempIsDefault := tempIsFloat && tempF == 1.0
		topPIsDefault := topPIsFloat && topPF == 1.0

		switch {
		case tempIsDefault && topPIsDefault:
			delete(body, "top_p")
			slog.Debug("Removed top_p=1.0 (both default, keeping temperature)")
		case topPIsDefault:
			delete(body, "top_p")
			slog.Debug("Removed default top_p, keeping temperature")
		case tempIsDefault:
			delete(body, "temperature")
			slog.Debug("Removed default temperature, keeping top_p")
		default:
			delete(body, "top_p")
			slog.Debug("Removed top_p (prefer temperature when both non-default)")
		}
	} else if hasTopP && topPIsFloat && topPF == 1.0 {
		delete(body, "top_p")
		slog.Debug("Removed default top_p=1.0")
	} else if hasTemp && tempIsFloat && tempF == 1.0 {
		delete(body, "temperature")
		slog.Debug("Removed default temperature=1.0")
	}
}

// ProcessRequestBody applies all transformations to a request body.
func ProcessRequestBody(body map[string]interface{}, stripTTL, filterSampling bool) {
	InjectSystemPrompt(body)
	if stripTTL {
		StripTTL(body)
	}
	if filterSampling {
		FilterSamplingParams(body)
	}
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
```

**Step 4: Run test to verify it passes**

Run: `cd go && go test ./internal/transform/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add go/internal/transform/
git commit -m "feat(go): add transform package for request body transformations"
```

---

### Task 7: Preset Package

**Files:**
- Create: `go/internal/preset/preset.go`
- Test: `go/internal/preset/preset_test.go`
- Copy: `go/presets/pyrite.json` (from `server/presets/pyrite.json`)

**Step 1: Copy existing preset**

```bash
cp server/presets/pyrite.json go/presets/pyrite.json
```

**Step 2: Write the failing test**

Create `go/internal/preset/preset_test.go`:

```go
package preset

import (
	"embed"
	"testing"
)

//go:embed testdata/*.json
var testFS embed.FS

func TestLoad(t *testing.T) {
	// Create test preset file
	mgr := NewManager(testFS, "testdata")

	p, err := mgr.Load("test")
	if err != nil {
		t.Fatal(err)
	}

	if p.System == "" {
		t.Error("system should not be empty")
	}
}

func TestLoadMissing(t *testing.T) {
	mgr := NewManager(testFS, "testdata")
	_, err := mgr.Load("nonexistent")
	if err == nil {
		t.Error("should error on missing preset")
	}
}

func TestLoadCaches(t *testing.T) {
	mgr := NewManager(testFS, "testdata")
	p1, _ := mgr.Load("test")
	p2, _ := mgr.Load("test")

	if p1 != p2 {
		t.Error("second load should return cached pointer")
	}
}

func TestApply_NoThinking(t *testing.T) {
	mgr := NewManager(testFS, "testdata")
	p, _ := mgr.Load("test")

	body := map[string]interface{}{
		"system": []interface{}{
			map[string]interface{}{"type": "text", "text": "existing"},
		},
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "hello",
			},
		},
	}

	Apply(body, p)

	system := body["system"].([]interface{})
	if len(system) != 2 {
		t.Fatalf("system length = %d, want 2", len(system))
	}

	// Should have suffix message after user message
	msgs := body["messages"].([]interface{})
	if len(msgs) != 2 {
		t.Fatalf("messages length = %d, want 2 (original + suffix)", len(msgs))
	}
}

func TestApply_WithThinking(t *testing.T) {
	mgr := NewManager(testFS, "testdata")
	p, _ := mgr.Load("test")

	body := map[string]interface{}{
		"system": []interface{}{},
		"thinking": map[string]interface{}{
			"type": "enabled",
		},
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "hello",
			},
		},
	}

	Apply(body, p)

	msgs := body["messages"].([]interface{})
	if len(msgs) < 2 {
		t.Fatal("should have injected suffixEt message")
	}

	// The suffix message content should be from suffixEt
	suffixMsg := msgs[1].(map[string]interface{})
	content := suffixMsg["content"].([]interface{})
	textBlock := content[0].(map[string]interface{})
	if textBlock["text"] != "thinking suffix" {
		t.Errorf("should use suffixEt when thinking enabled, got %q", textBlock["text"])
	}
}
```

Also create test data:

Create `go/internal/preset/testdata/test.json`:

```json
{
  "system": "Test system prompt",
  "suffix": "regular suffix",
  "suffixEt": "thinking suffix"
}
```

**Step 3: Run test to verify it fails**

Run: `cd go && go test ./internal/preset/ -v`
Expected: FAIL

**Step 4: Write implementation**

Create `go/internal/preset/preset.go`:

```go
package preset

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
)

// Preset represents a preset configuration.
type Preset struct {
	System   string `json:"system"`
	Suffix   string `json:"suffix"`
	SuffixEt string `json:"suffixEt"`
}

// Manager loads and caches presets from an embedded filesystem.
type Manager struct {
	fs     fs.FS
	prefix string
	cache  map[string]*Preset
	mu     sync.RWMutex
}

// NewManager creates a preset manager reading from the given filesystem and path prefix.
func NewManager(fsys fs.FS, prefix string) *Manager {
	return &Manager{
		fs:     fsys,
		prefix: prefix,
		cache:  make(map[string]*Preset),
	}
}

// Load reads a preset by name, caching after first load.
func (m *Manager) Load(name string) (*Preset, error) {
	m.mu.RLock()
	if p, ok := m.cache[name]; ok {
		m.mu.RUnlock()
		return p, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if p, ok := m.cache[name]; ok {
		return p, nil
	}

	path := m.prefix + "/" + name + ".json"
	data, err := fs.ReadFile(m.fs, path)
	if err != nil {
		return nil, fmt.Errorf("preset %q not found: %w", name, err)
	}

	var p Preset
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("failed to parse preset %q: %w", name, err)
	}

	m.cache[name] = &p
	slog.Debug("Loaded preset", "name", name)
	return &p, nil
}

// Apply injects a preset's system prompt and suffix into the request body.
func Apply(body map[string]interface{}, p *Preset) {
	if p.System != "" {
		systemPrompt := map[string]interface{}{
			"type": "text",
			"text": p.System,
		}
		if sys, ok := body["system"].([]interface{}); ok {
			body["system"] = append(sys, systemPrompt)
		}
	}

	// Choose suffix based on thinking mode
	hasThinking := false
	if thinking, ok := body["thinking"].(map[string]interface{}); ok {
		if thinkingType, ok := thinking["type"].(string); ok && thinkingType == "enabled" {
			hasThinking = true
		}
	}

	suffix := p.Suffix
	if hasThinking {
		suffix = p.SuffixEt
	}

	if suffix == "" {
		return
	}

	msgs, ok := body["messages"].([]interface{})
	if !ok || len(msgs) == 0 {
		return
	}

	// Find last user message index
	lastUserIdx := -1
	for i, msg := range msgs {
		if m, ok := msg.(map[string]interface{}); ok {
			if m["role"] == "user" {
				lastUserIdx = i
			}
		}
	}

	if lastUserIdx == -1 {
		return
	}

	suffixMsg := map[string]interface{}{
		"role": "user",
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": suffix,
			},
		},
	}

	// Insert after last user message
	newMsgs := make([]interface{}, 0, len(msgs)+1)
	newMsgs = append(newMsgs, msgs[:lastUserIdx+1]...)
	newMsgs = append(newMsgs, suffixMsg)
	newMsgs = append(newMsgs, msgs[lastUserIdx+1:]...)
	body["messages"] = newMsgs

	slog.Debug("Applied preset suffix")
}
```

**Step 5: Run test to verify it passes**

Run: `cd go && go test ./internal/preset/ -v`
Expected: PASS

**Step 6: Commit**

```bash
git add go/internal/preset/ go/presets/
git commit -m "feat(go): add preset package with embed support and caching"
```

---

### Task 8: Proxy Package (Core Handler + Streaming)

**Files:**
- Create: `go/internal/proxy/proxy.go`
- Test: `go/internal/proxy/proxy_test.go`

**Step 1: Write the failing test**

Create `go/internal/proxy/proxy_test.go`:

```go
package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/claude-code-proxy/internal/config"
)

// mockAuthResolver always returns a fixed token.
type mockAuthResolver struct{ token string }

func (m *mockAuthResolver) Resolve(apiKey string) (string, error) {
	if apiKey != "" {
		return "Bearer " + apiKey, nil
	}
	return m.token, nil
}
func (m *mockAuthResolver) ClearCache() {}

func TestProxyHandler_BufferedResponse(t *testing.T) {
	// Mock upstream Anthropic API
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q, want Bearer test-token", r.Header.Get("Authorization"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("missing anthropic-version header")
		}

		// Verify body was transformed
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		system, ok := body["system"].([]interface{})
		if !ok || len(system) == 0 {
			t.Error("system prompt should be injected")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"content": []map[string]string{{"type": "text", "text": "hello"}},
		})
	}))
	defer upstream.Close()

	cfg := config.Defaults()
	h := NewHandler(&cfg, &mockAuthResolver{token: "Bearer test-token"}, nil)
	h.UpstreamURL = upstream.URL

	reqBody := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["content"] == nil {
		t.Error("response should contain content")
	}
}

func TestProxyHandler_StreamingResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)

		flusher, _ := w.(http.Flusher)
		for _, event := range []string{
			`data: {"type":"content_block_start"}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
			`data: {"type":"message_stop"}`,
		} {
			w.Write([]byte(event + "\n\n"))
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	cfg := config.Defaults()
	h := NewHandler(&cfg, &mockAuthResolver{token: "Bearer test"}, nil)
	h.UpstreamURL = upstream.URL

	reqBody := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "text_delta") {
		t.Error("streaming response should contain text_delta events")
	}
}

func TestProxyHandler_401Retry(t *testing.T) {
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

	cfg := config.Defaults()
	h := NewHandler(&cfg, &mockAuthResolver{token: "Bearer test"}, nil)
	h.UpstreamURL = upstream.URL

	reqBody := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if attempts != 2 {
		t.Errorf("should retry on 401, got %d attempts", attempts)
	}
	if w.Code != 200 {
		t.Errorf("final status = %d, want 200 after retry", w.Code)
	}
}

func TestProxyHandler_PresetRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)

		w.Header().Set("Content-Type", "application/json")
		// Return the system array length so we can verify preset was applied
		system := body["system"].([]interface{})
		json.NewEncoder(w).Encode(map[string]int{"system_count": len(system)})
	}))
	defer upstream.Close()

	cfg := config.Defaults()
	h := NewHandler(&cfg, &mockAuthResolver{token: "Bearer test"}, nil)
	h.UpstreamURL = upstream.URL

	// Without preset manager, preset routes should still work (just no preset applied)
	reqBody := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/somename/messages", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd go && go test ./internal/proxy/ -v`
Expected: FAIL

**Step 3: Write implementation**

Create `go/internal/proxy/proxy.go`:

```go
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/anthropics/claude-code-proxy/internal/config"
	"github.com/anthropics/claude-code-proxy/internal/preset"
	"github.com/anthropics/claude-code-proxy/internal/transform"
)

const (
	DefaultUpstreamURL = "https://api.anthropic.com/v1/messages"
	Version            = "2023-06-01"
	BetaHeader         = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14"
	UserAgent          = "claude-code-proxy/1.0.0"
)

var presetRouteRe = regexp.MustCompile(`^/v1/(\w+)/messages$`)

// AuthResolver resolves authentication tokens.
type AuthResolver interface {
	Resolve(apiKeyHeader string) (string, error)
	ClearCache()
}

// Handler handles proxy requests to the Anthropic API.
type Handler struct {
	cfg       *config.Config
	auth      AuthResolver
	presetMgr *preset.Manager
	client    *http.Client

	// UpstreamURL is the Anthropic API URL. Overridable for testing.
	UpstreamURL string
}

// NewHandler creates a new proxy handler.
func NewHandler(cfg *config.Config, auth AuthResolver, presetMgr *preset.Manager) *Handler {
	return &Handler{
		cfg:         cfg,
		auth:        auth,
		presetMgr:   presetMgr,
		client:      &http.Client{},
		UpstreamURL: DefaultUpstreamURL,
	}
}

// ServeHTTP handles POST /v1/messages and /v1/{preset}/messages.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Parse preset name from URL
	var presetName string
	path := r.URL.Path
	if path == "/v1/messages" {
		// standard route
	} else if m := presetRouteRe.FindStringSubmatch(path); m != nil {
		presetName = m[1]
		slog.Debug("Detected preset", "name", presetName)
	} else {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	// Read and parse body
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Failed to read request body", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		slog.Error("Invalid JSON body", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	slog.Debug("Incoming request", "bytes", len(rawBody))

	// Get auth token
	apiKey := r.Header.Get("x-api-key")
	token, err := h.auth.Resolve(apiKey)
	if err != nil {
		slog.Error("Authentication failed", "error", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	// Transform request body
	transform.ProcessRequestBody(body, h.cfg.Proxy.StripTTL, h.cfg.Proxy.FilterSamplingParams)

	// Apply preset if requested
	if presetName != "" && h.presetMgr != nil {
		p, err := h.presetMgr.Load(presetName)
		if err != nil {
			slog.Warn("Failed to load preset", "name", presetName, "error", err)
		} else {
			preset.Apply(body, p)
		}
	}

	// Make upstream request
	resp, err := h.doUpstreamRequest(r.Context(), body, token)
	if err != nil {
		slog.Error("Upstream request failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	// Handle 401 with retry
	if resp.StatusCode == 401 {
		resp.Body.Close()
		slog.Info("Got 401, refreshing token and retrying")
		h.auth.ClearCache()

		token, err = h.auth.Resolve(apiKey)
		if err != nil {
			slog.Warn("Token refresh failed", "error", err)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication failed after retry"})
			return
		}

		resp, err = h.doUpstreamRequest(r.Context(), body, token)
		if err != nil {
			slog.Error("Retry upstream request failed", "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		defer resp.Body.Close()
	}

	// Copy response headers
	for key, vals := range resp.Header {
		for _, val := range vals {
			w.Header().Add(key, val)
		}
	}

	contentType := resp.Header.Get("Content-Type")

	if strings.Contains(contentType, "text/event-stream") {
		// Streaming response — flush chunks as they arrive
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
		slog.Debug("Streaming response sent to client")
	} else {
		// Buffered response
		w.Header().Del("Content-Encoding")
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.Error("Failed to read upstream response", "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream read error"})
			return
		}

		slog.Debug("Non-streaming response", "status", resp.StatusCode, "bytes", len(respBody))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		slog.Debug("Buffered response sent to client")
	}
}

func (h *Handler) doUpstreamRequest(ctx context.Context, body map[string]interface{}, token string) (*http.Response, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to encode body: %w", err)
	}

	slog.Debug("Outgoing request to upstream", "bytes", len(encoded))

	req, err := http.NewRequestWithContext(ctx, "POST", h.UpstreamURL, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	req.Header.Set("anthropic-version", Version)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("anthropic-beta", BetaHeader)

	return h.client.Do(req)
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
```

**Step 4: Run test to verify it passes**

Run: `cd go && go test ./internal/proxy/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add go/internal/proxy/
git commit -m "feat(go): add proxy package with streaming, 401 retry, context cancellation"
```

---

### Task 9: Server Wiring (main.go + routes + static files)

**Files:**
- Modify: `go/cmd/claude-code-proxy/main.go`
- Copy: `go/static/login.html` (from `server/static/login.html`)
- Copy: `go/static/callback.html` (from `server/static/callback.html`)

**Step 1: Copy static files**

```bash
cp server/static/login.html go/static/login.html
cp server/static/callback.html go/static/callback.html
```

**Step 2: Write the full main.go**

Replace `go/cmd/claude-code-proxy/main.go`:

```go
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anthropics/claude-code-proxy/internal/auth"
	"github.com/anthropics/claude-code-proxy/internal/config"
	"github.com/anthropics/claude-code-proxy/internal/logger"
	"github.com/anthropics/claude-code-proxy/internal/oauth"
	"github.com/anthropics/claude-code-proxy/internal/proxy"
)

//go:embed all:../../static
var staticFS embed.FS

//go:embed all:../../presets
var presetsFS embed.FS

// pkceState stores PKCE state with creation time.
type pkceState struct {
	CodeVerifier string
	CreatedAt    time.Time
}

var (
	pkceStates   = make(map[string]pkceState)
	pkceMu       sync.Mutex
	pkceExpiryMs = 10 * time.Minute
)

func cleanupExpiredPKCE() {
	pkceMu.Lock()
	defer pkceMu.Unlock()
	now := time.Now()
	for state, data := range pkceStates {
		if now.Sub(data.CreatedAt) > pkceExpiryMs {
			delete(pkceStates, state)
		}
	}
}

func isRunningInDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return false
	}
	s := string(data)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd")
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		slog.Debug("Failed to open browser", "error", err)
	}
}

func main() {
	// CLI flags
	configPath := flag.String("config", "config.yaml", "path to config file")
	port := flag.Int("port", 0, "server port (overrides config)")
	host := flag.String("host", "", "server host (overrides config)")
	logLevel := flag.String("log-level", "", "log level: trace, debug, info, warn, error")
	flag.Parse()

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Apply env overrides
	config.ApplyEnv(&cfg)

	// Apply CLI flag overrides
	if *port != 0 {
		cfg.Server.Port = *port
	}
	if *host != "" {
		cfg.Server.Host = *host
	}
	if *logLevel != "" {
		cfg.Logging.Level = *logLevel
	}

	// Initialize logger
	logger.Init(cfg.Logging.Level, nil)

	slog.Info("Config loaded", "port", cfg.Server.Port, "log_level", cfg.Logging.Level)

	// Auto-detect host
	serverHost := cfg.Server.Host
	if serverHost == "" {
		if isRunningInDocker() {
			serverHost = "0.0.0.0"
		} else {
			serverHost = "127.0.0.1"
		}
	}

	// Initialize OAuth manager
	oauthMgr := oauth.NewManager()

	// Initialize auth resolver
	authResolver := &auth.Resolver{
		OAuthMgr:             oauthMgr,
		FallbackToClaudeCode: cfg.Auth.FallbackToClaudeCode,
	}

	// Initialize preset manager
	presetMgr := preset.NewManagerFromEmbed(presetsFS)

	// Initialize proxy handler
	proxyHandler := proxy.NewHandler(&cfg, authResolver, presetMgr)

	// PKCE cleanup ticker
	ticker := time.NewTicker(60 * time.Second)
	go func() {
		for range ticker.C {
			cleanupExpiredPKCE()
		}
	}()

	// Build router
	mux := http.NewServeMux()

	// CORS middleware wrapper
	corsHandler := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, x-api-key")

			if r.Method == http.MethodOptions {
				w.WriteHeader(200)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	// OAuth routes
	mux.HandleFunc("GET /auth/login", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFS.ReadFile("static/login.html")
		if err != nil {
			http.Error(w, "Not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	})

	mux.HandleFunc("GET /auth/get-url", func(w http.ResponseWriter, r *http.Request) {
		pkce := oauth.GeneratePKCE()

		pkceMu.Lock()
		pkceStates[pkce.State] = pkceState{
			CodeVerifier: pkce.CodeVerifier,
			CreatedAt:    time.Now(),
		}
		pkceMu.Unlock()

		authURL := oauth.BuildAuthorizationURL(pkce)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"url":   authURL,
			"state": pkce.State,
		})
		slog.Info("Generated OAuth authorization URL")
	})

	mux.HandleFunc("GET /auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		code := q.Get("code")
		state := q.Get("state")

		// Handle manual code entry: code#state
		if manualCode := q.Get("manual_code"); manualCode != "" {
			parts := strings.SplitN(manualCode, "#", 2)
			if len(parts) != 2 {
				http.Error(w, "Invalid code format. Expected: code#state", 400)
				return
			}
			code = parts[0]
			state = parts[1]
		}

		if code == "" || state == "" {
			http.Error(w, "Missing authorization code or state", 400)
			return
		}

		pkceMu.Lock()
		pkceData, ok := pkceStates[state]
		if ok {
			delete(pkceStates, state)
		}
		pkceMu.Unlock()

		if !ok {
			http.Error(w, "Invalid or expired state. Please start again.", 400)
			return
		}

		tokens, err := oauthMgr.ExchangeCodeForTokens(code, pkceData.CodeVerifier, state)
		if err != nil {
			slog.Error("OAuth callback error", "error", err)
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(500)
			fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>Authentication Failed</title></head><body><h1>Authentication Failed</h1><p>Error: %s</p><p><a href="/auth/login">Try again</a></p></body></html>`, err.Error())
			return
		}

		tokenData := &oauth.Tokens{
			AccessToken:  tokens.AccessToken,
			RefreshToken: tokens.RefreshToken,
			ExpiresAt:    time.Now().UnixMilli() + int64(tokens.ExpiresIn)*1000,
		}
		if err := oauthMgr.SaveTokens(tokenData); err != nil {
			slog.Error("Failed to save tokens", "error", err)
			http.Error(w, "Failed to save tokens", 500)
			return
		}

		data, _ := staticFS.ReadFile("static/callback.html")
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
		slog.Info("OAuth authentication successful")
	})

	mux.HandleFunc("GET /auth/status", func(w http.ResponseWriter, r *http.Request) {
		isAuth := oauthMgr.IsAuthenticated()
		exp := oauthMgr.GetTokenExpiration()

		var expiresAt *string
		if exp != nil {
			s := exp.Format(time.RFC3339)
			expiresAt = &s
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"authenticated": isAuth,
			"expires_at":    expiresAt,
		})
	})

	mux.HandleFunc("GET /auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if err := oauthMgr.Logout(); err != nil {
			slog.Error("Logout error", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to logout"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Logged out successfully",
		})
		slog.Info("User logged out")
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "ok",
			"server":    "claude-code-proxy",
			"timestamp": time.Now().UnixMilli(),
		})
	})

	// Proxy routes
	mux.HandleFunc("POST /v1/messages", proxyHandler.ServeHTTP)
	mux.HandleFunc("POST /v1/{preset}/messages", proxyHandler.ServeHTTP)

	// Start server
	addr := fmt.Sprintf("%s:%d", serverHost, cfg.Server.Port)
	server := &http.Server{
		Addr:    addr,
		Handler: corsHandler(mux),
	}

	// Display auth status
	isAuth := oauthMgr.IsAuthenticated()
	exp := oauthMgr.GetTokenExpiration()

	slog.Info("claude-code-proxy server starting", "addr", addr)
	slog.Info("")
	slog.Info("Authentication Status:")
	if isAuth && exp != nil {
		slog.Info(fmt.Sprintf("  Authenticated until %s", exp.Local().Format(time.RFC1123)))
	} else {
		slog.Info("  Not authenticated")
		authURL := fmt.Sprintf("http://localhost:%d/auth/login", cfg.Server.Port)
		slog.Info(fmt.Sprintf("  Visit %s to authenticate", authURL))

		if cfg.Auth.AutoOpenBrowser && !isRunningInDocker() {
			slog.Info("  Opening browser for authentication...")
			go func() {
				time.Sleep(1 * time.Second)
				openBrowser(authURL)
			}()
		}
	}
	slog.Info("")

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		slog.Info("Shutting down...")
		ticker.Stop()
		server.Close()
	}()

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("Server failed", "error", err)
		os.Exit(1)
	}
}
```

Note: The above `main.go` references a `preset.NewManagerFromEmbed` that we need to add as a convenience constructor. Update `go/internal/preset/preset.go` to add:

```go
// NewManagerFromEmbed creates a preset manager from an embedded FS with "presets" prefix.
func NewManagerFromEmbed(fsys fs.FS) *Manager {
	return NewManager(fsys, "presets")
}
```

**Step 3: Verify it compiles**

Run: `cd go && go build ./cmd/claude-code-proxy`
Expected: builds successfully

**Step 4: Commit**

```bash
git add go/cmd/ go/static/ go/internal/preset/preset.go
git commit -m "feat(go): wire up server with all routes, CORS, static files, graceful shutdown"
```

---

### Task 10: Dockerfile & Makefile

**Files:**
- Create: `go/Dockerfile`
- Create: `go/Makefile`
- Create: `go/docker-compose.yml`

**Step 1: Create Dockerfile**

Create `go/Dockerfile`:

```dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /claude-code-proxy ./cmd/claude-code-proxy

FROM alpine:3.19
RUN apk --no-cache add ca-certificates

COPY --from=builder /claude-code-proxy /usr/local/bin/claude-code-proxy

ENTRYPOINT ["claude-code-proxy"]
```

**Step 2: Create Makefile**

Create `go/Makefile`:

```makefile
.PHONY: build run test clean docker-build docker-run

BINARY=claude-code-proxy

build:
	go build -o $(BINARY) ./cmd/claude-code-proxy

run: build
	./$(BINARY)

test:
	go test ./... -v

clean:
	rm -f $(BINARY)

docker-build:
	docker build -t claude-code-proxy .

docker-run: docker-build
	docker run -p 42069:42069 \
		-v ~/.claude:/root/.claude \
		-v ~/.claude-code-proxy:/root/.claude-code-proxy \
		claude-code-proxy
```

**Step 3: Create docker-compose.yml**

Create `go/docker-compose.yml`:

```yaml
services:
  claude-proxy:
    build: .
    ports:
      - "42069:42069"
    volumes:
      - ~/.claude:/root/.claude
      - ~/.claude-code-proxy:/root/.claude-code-proxy
    environment:
      - CCP_LOG_LEVEL=info
```

**Step 4: Verify Docker builds**

Run: `cd go && docker build -t claude-code-proxy-test .`
Expected: builds successfully (multi-stage, small image)

**Step 5: Commit**

```bash
git add go/Dockerfile go/Makefile go/docker-compose.yml
git commit -m "feat(go): add Dockerfile, Makefile, and docker-compose"
```

---

### Task 11: Run All Tests & Final Verification

**Step 1: Run all tests**

Run: `cd go && go test ./... -v`
Expected: all PASS

**Step 2: Build and verify binary**

Run: `cd go && go build -o claude-code-proxy ./cmd/claude-code-proxy && ls -lh claude-code-proxy`
Expected: single binary, ~10-15MB

**Step 3: Smoke test (start server, hit health endpoint)**

```bash
cd go && ./claude-code-proxy --port 42070 &
sleep 1
curl -s http://localhost:42070/health | python3 -m json.tool
kill %1
```

Expected: `{"status": "ok", "server": "claude-code-proxy", ...}`

**Step 4: Commit any fixes**

```bash
git add -A go/
git commit -m "chore(go): final test pass and verification"
```

---

### Task 12: README

**Files:**
- Create: `go/README.md`

**Step 1: Write README**

Create `go/README.md` with:
- Quick start (binary, Docker)
- Configuration reference (YAML, env vars, CLI flags)
- Endpoints table
- Comparison with Node.js version
- Building from source

**Step 2: Commit**

```bash
git add go/README.md
git commit -m "docs(go): add README with usage and configuration reference"
```

---

## Dependency Graph

```
Task 1 (scaffold) ─┬─► Task 2 (logger)
                    │
                    ├─► Task 3 (config)
                    │
                    └─► Task 7 (preset) ──────────────┐
                                                       │
Task 2 + 3 ──► Task 4 (oauth) ──► Task 5 (auth) ──┐  │
                                                    │  │
Task 6 (transform) ────────────────────────────────┤  │
                                                    │  │
                                                    ▼  ▼
                                              Task 8 (proxy)
                                                    │
                                                    ▼
                                              Task 9 (server wiring)
                                                    │
                                                    ▼
                                              Task 10 (Docker)
                                                    │
                                                    ▼
                                              Task 11 (verification)
                                                    │
                                                    ▼
                                              Task 12 (README)
```

## Parallelizable Tasks

After Task 1 completes, the following can run in parallel:
- Task 2 (logger), Task 3 (config), Task 6 (transform), Task 7 (preset)

After Tasks 2+3 complete:
- Task 4 (oauth)

After Task 4:
- Task 5 (auth)

Tasks 8-12 are sequential.
