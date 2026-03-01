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

type Resolver struct {
	OAuthMgr             *oauth.Manager
	FallbackToClaudeCode bool
	ClaudeCredPath       string // override for testing; empty = default

	mu          sync.Mutex
	cachedToken string
}

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
