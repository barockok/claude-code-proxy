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
