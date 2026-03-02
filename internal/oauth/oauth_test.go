package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

	pkce2 := GeneratePKCE()
	if pkce.State == pkce2.State {
		t.Error("two PKCE generations should produce different states")
	}
}

func TestBuildAuthorizationURL(t *testing.T) {
	mgr := NewManager(OAuthConfig{
		Name:         "anthropic",
		ClientID:     DefaultClientID,
		AuthorizeURL: DefaultAuthorizeURL,
		TokenURL:     DefaultTokenURL,
		RedirectURI:  DefaultRedirectURI,
		Scopes:       DefaultScope,
	})

	pkce := PKCE{
		CodeChallenge: "test_challenge",
		State:         "test_state",
	}

	url := mgr.BuildAuthorizationURL(pkce)

	if url == "" {
		t.Fatal("URL should not be empty")
	}

	for _, param := range []string{"client_id=", "response_type=code", "code_challenge=test_challenge", "state=test_state"} {
		if !stringContains(url, param) {
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

	mgr.SaveTokens(&Tokens{
		AccessToken:  "old_access",
		RefreshToken: "old_refresh",
		ExpiresAt:    1,
	})

	resp, err := mgr.RefreshAccessToken()
	if err != nil {
		t.Fatal(err)
	}

	if resp.AccessToken != "refreshed_access" {
		t.Errorf("access_token = %q, want refreshed_access", resp.AccessToken)
	}

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

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
