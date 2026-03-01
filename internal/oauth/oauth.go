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
	DefaultClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	DefaultAuthorizeURL = "https://claude.ai/oauth/authorize"
	DefaultTokenURL     = "https://console.anthropic.com/v1/oauth/token"
	DefaultRedirectURI  = "https://console.anthropic.com/oauth/code/callback"
	DefaultScope        = "org:create_api_key user:profile user:inference"
)

type PKCE struct {
	CodeVerifier  string
	CodeChallenge string
	State         string
}

type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type Manager struct {
	TokenPath string
	TokenURL  string // overridable for testing

	mu             sync.Mutex
	refreshPromise chan struct{}
	cachedToken    string
}

func NewManager() *Manager {
	home, _ := os.UserHomeDir()
	return &Manager{
		TokenPath: filepath.Join(home, ".claude-code-proxy", "tokens.json"),
		TokenURL:  DefaultTokenURL,
	}
}

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

func (m *Manager) RefreshAccessToken() (*TokenResponse, error) {
	m.mu.Lock()

	if m.refreshPromise != nil {
		ch := m.refreshPromise
		m.mu.Unlock()
		<-ch
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

func (m *Manager) GetValidAccessToken() (string, error) {
	tokens, err := m.LoadTokens()
	if err != nil || tokens == nil {
		return "", fmt.Errorf("no authentication tokens found")
	}

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

	if runtime.GOOS != "windows" {
		os.Chmod(m.TokenPath, 0o600)
	}

	slog.Info("Tokens saved successfully")
	return nil
}

func (m *Manager) IsAuthenticated() bool {
	tokens, err := m.LoadTokens()
	if err != nil || tokens == nil {
		return false
	}
	return tokens.AccessToken != "" && tokens.RefreshToken != ""
}

func (m *Manager) GetTokenExpiration() *time.Time {
	tokens, err := m.LoadTokens()
	if err != nil || tokens == nil || tokens.ExpiresAt == 0 {
		return nil
	}
	t := time.UnixMilli(tokens.ExpiresAt)
	return &t
}

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
