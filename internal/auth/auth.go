package auth

import (
	"fmt"
	"log/slog"
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
