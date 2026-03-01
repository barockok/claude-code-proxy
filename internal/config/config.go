package config

import (
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

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

func Defaults() Config {
	return Config{
		Server:  ServerConfig{Port: 42069, Host: ""},
		Logging: LoggingConfig{Level: "info"},
		Proxy:   ProxyConfig{FilterSamplingParams: false, StripTTL: true},
		Auth:    AuthConfig{AutoOpenBrowser: true, FallbackToClaudeCode: true},
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
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
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
