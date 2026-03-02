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
	if cfg.Providers == nil {
		t.Fatal("default providers map should not be nil")
	}
	if len(cfg.Providers) != 0 {
		t.Errorf("default providers should be empty, got %d", len(cfg.Providers))
	}
}

func TestLoadProviders(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(yamlPath, []byte(`
server:
  port: 9090
  host: "0.0.0.0"
logging:
  level: debug
providers:
  anthropic:
    models: ["claude-*"]
    upstream: "https://api.anthropic.com"
    auth:
      type: oauth
      client_id: "my-client-id"
      authorize_url: "https://claude.ai/oauth/authorize"
      token_url: "https://console.anthropic.com/v1/oauth/token"
      scopes: "org:create_api_key user:profile"
    headers:
      anthropic-version: "2023-06-01"
  openai:
    models: ["gpt-4o", "o3-*"]
    upstream: "https://api.openai.com"
    auth:
      type: api_key
      api_key: "sk-test-key"
      header_name: "Authorization"
      header_prefix: "Bearer "
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("port = %d, want 9090", cfg.Server.Port)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("host = %q, want 0.0.0.0", cfg.Server.Host)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("log level = %q, want debug", cfg.Logging.Level)
	}

	if len(cfg.Providers) != 2 {
		t.Fatalf("providers count = %d, want 2", len(cfg.Providers))
	}

	// Check anthropic provider
	anth, ok := cfg.Providers["anthropic"]
	if !ok {
		t.Fatal("missing anthropic provider")
	}
	if len(anth.Models) != 1 || anth.Models[0] != "claude-*" {
		t.Errorf("anthropic models = %v, want [claude-*]", anth.Models)
	}
	if anth.Upstream != "https://api.anthropic.com" {
		t.Errorf("anthropic upstream = %q", anth.Upstream)
	}
	if anth.Auth.Type != "oauth" {
		t.Errorf("anthropic auth type = %q, want oauth", anth.Auth.Type)
	}
	if anth.Auth.ClientID != "my-client-id" {
		t.Errorf("anthropic client_id = %q", anth.Auth.ClientID)
	}
	if anth.Auth.AuthorizeURL != "https://claude.ai/oauth/authorize" {
		t.Errorf("anthropic authorize_url = %q", anth.Auth.AuthorizeURL)
	}
	if anth.Auth.TokenURL != "https://console.anthropic.com/v1/oauth/token" {
		t.Errorf("anthropic token_url = %q", anth.Auth.TokenURL)
	}
	if anth.Auth.Scopes != "org:create_api_key user:profile" {
		t.Errorf("anthropic scopes = %q", anth.Auth.Scopes)
	}
	if anth.Headers["anthropic-version"] != "2023-06-01" {
		t.Errorf("anthropic anthropic-version header = %q", anth.Headers["anthropic-version"])
	}

	// Check openai provider
	oai, ok := cfg.Providers["openai"]
	if !ok {
		t.Fatal("missing openai provider")
	}
	if len(oai.Models) != 2 || oai.Models[0] != "gpt-4o" || oai.Models[1] != "o3-*" {
		t.Errorf("openai models = %v, want [gpt-4o o3-*]", oai.Models)
	}
	if oai.Upstream != "https://api.openai.com" {
		t.Errorf("openai upstream = %q", oai.Upstream)
	}
	if oai.Auth.Type != "api_key" {
		t.Errorf("openai auth type = %q, want api_key", oai.Auth.Type)
	}
	if oai.Auth.APIKey != "sk-test-key" {
		t.Errorf("openai api_key = %q", oai.Auth.APIKey)
	}
	if oai.Auth.HeaderName != "Authorization" {
		t.Errorf("openai header_name = %q", oai.Auth.HeaderName)
	}
	if oai.Auth.HeaderPrefix != "Bearer " {
		t.Errorf("openai header_prefix = %q", oai.Auth.HeaderPrefix)
	}
}

func TestExpandEnvVars(t *testing.T) {
	t.Setenv("MY_KEY", "secret123")

	// Simple expansion
	got := ExpandEnvVars("${MY_KEY}")
	if got != "secret123" {
		t.Errorf("ExpandEnvVars(${MY_KEY}) = %q, want secret123", got)
	}

	// Prefix and suffix around the var
	got = ExpandEnvVars("Bearer ${MY_KEY}!")
	if got != "Bearer secret123!" {
		t.Errorf("ExpandEnvVars with prefix+suffix = %q, want 'Bearer secret123!'", got)
	}

	// No vars passthrough
	got = ExpandEnvVars("no-vars-here")
	if got != "no-vars-here" {
		t.Errorf("ExpandEnvVars passthrough = %q, want no-vars-here", got)
	}

	// Unset var expands to empty string
	got = ExpandEnvVars("${UNSET_VAR_12345}")
	if got != "" {
		t.Errorf("ExpandEnvVars unset = %q, want empty", got)
	}
}

func TestProviderOrder(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(yamlPath, []byte(`
providers:
  zebra:
    models: ["z-*"]
    upstream: "https://zebra.example.com"
    auth:
      type: api_key
  alpha:
    models: ["a-*"]
    upstream: "https://alpha.example.com"
    auth:
      type: api_key
  middle:
    models: ["m-*"]
    upstream: "https://middle.example.com"
    auth:
      type: api_key
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"zebra", "alpha", "middle"}
	if len(cfg.ProviderOrder) != len(expected) {
		t.Fatalf("ProviderOrder length = %d, want %d", len(cfg.ProviderOrder), len(expected))
	}
	for i, name := range expected {
		if cfg.ProviderOrder[i] != name {
			t.Errorf("ProviderOrder[%d] = %q, want %q", i, cfg.ProviderOrder[i], name)
		}
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
	t.Setenv("CCP_SERVER_HOST", "localhost")
	t.Setenv("CCP_LOG_LEVEL", "ERROR")

	cfg := Defaults()
	ApplyEnv(&cfg)

	if cfg.Server.Port != 9999 {
		t.Errorf("env port = %d, want 9999", cfg.Server.Port)
	}
	if cfg.Server.Host != "localhost" {
		t.Errorf("env host = %q, want localhost", cfg.Server.Host)
	}
	if cfg.Logging.Level != "error" {
		t.Errorf("env log level = %q, want error", cfg.Logging.Level)
	}
}
