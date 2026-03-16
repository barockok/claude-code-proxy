package auth

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

// ClaudeCodeResolver reads accessToken from ~/.claude-code-proxy/.credentials.json
// Token refresh is managed externally (e.g. by Claude Code CLI).
type ClaudeCodeResolver struct{}

type claudeCodeCredentials struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

func NewClaudeCodeResolver() *ClaudeCodeResolver {
	return &ClaudeCodeResolver{}
}

func (r *ClaudeCodeResolver) Resolve() (string, string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", "", fmt.Errorf("cannot determine home directory: %w", err)
	}

	path := filepath.Join(home, ".claude-code-proxy", ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", fmt.Errorf("cannot read credentials file %s: %w", path, err)
	}

	var creds claudeCodeCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", "", "", fmt.Errorf("cannot parse credentials file: %w", err)
	}

	token := creds.ClaudeAiOauth.AccessToken
	if token == "" {
		return "", "", "", fmt.Errorf("no accessToken in credentials file %s", path)
	}

	return token, "Authorization", "Bearer ", nil
}

func (r *ClaudeCodeResolver) ClearCache() {}
