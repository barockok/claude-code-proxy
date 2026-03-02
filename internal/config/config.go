package config

import (
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server        ServerConfig              `yaml:"server"`
	Logging       LoggingConfig             `yaml:"logging"`
	Providers     map[string]ProviderConfig `yaml:"-"`
	ProviderOrder []string                  `yaml:"-"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
}

type ProviderConfig struct {
	Models   []string           `yaml:"models"`
	Upstream string             `yaml:"upstream"`
	Auth     ProviderAuthConfig `yaml:"auth"`
	Headers  map[string]string  `yaml:"headers"`
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

// ExpandEnvVars replaces ${VAR} references in s with their environment variable values.
func ExpandEnvVars(s string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		varName := envVarRe.FindStringSubmatch(match)[1]
		return os.Getenv(varName)
	})
}

// Defaults returns a Config with sensible default values.
func Defaults() Config {
	return Config{
		Server:    ServerConfig{Port: 42069, Host: ""},
		Logging:   LoggingConfig{Level: "info"},
		Providers: make(map[string]ProviderConfig),
	}
}

// Load reads config from a YAML file at path, falling back to defaults if the file doesn't exist.
// It uses yaml.Node to preserve the insertion order of provider keys.
func Load(path string) (Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	// Unmarshal server and logging sections normally.
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}

	// Parse the full document as a yaml.Node tree to preserve provider key order.
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return cfg, err
	}

	// doc is a Document node; its first Content child is the top-level mapping.
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return cfg, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return cfg, nil
	}

	// Find the "providers" key in the top-level mapping.
	for i := 0; i < len(root.Content)-1; i += 2 {
		keyNode := root.Content[i]
		valNode := root.Content[i+1]
		if keyNode.Value != "providers" {
			continue
		}
		if valNode.Kind != yaml.MappingNode {
			break
		}

		// Iterate provider entries preserving YAML order.
		for j := 0; j < len(valNode.Content)-1; j += 2 {
			providerName := valNode.Content[j].Value
			providerNode := valNode.Content[j+1]

			var pc ProviderConfig
			if err := providerNode.Decode(&pc); err != nil {
				return cfg, err
			}

			cfg.Providers[providerName] = pc
			cfg.ProviderOrder = append(cfg.ProviderOrder, providerName)
		}
		break
	}

	return cfg, nil
}

// ApplyEnv overrides config fields with environment variable values when set.
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
