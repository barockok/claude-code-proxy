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
