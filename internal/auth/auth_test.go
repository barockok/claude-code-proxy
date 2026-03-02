package auth

import (
	"testing"
)

func TestStaticKeyResolver(t *testing.T) {
	r := NewStaticKeyResolver("sk-test-123", "Authorization", "Bearer ")
	token, header, prefix, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if token != "sk-test-123" {
		t.Errorf("token = %q, want sk-test-123", token)
	}
	if header != "Authorization" {
		t.Errorf("header = %q, want Authorization", header)
	}
	if prefix != "Bearer " {
		t.Errorf("prefix = %q, want 'Bearer '", prefix)
	}
}

func TestStaticKeyResolver_CustomHeader(t *testing.T) {
	r := NewStaticKeyResolver("AIza-test", "x-goog-api-key", "")
	token, header, prefix, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if token != "AIza-test" {
		t.Errorf("token = %q, want AIza-test", token)
	}
	if header != "x-goog-api-key" {
		t.Errorf("header = %q, want x-goog-api-key", header)
	}
	if prefix != "" {
		t.Errorf("prefix = %q, want empty", prefix)
	}
}

type mockOAuthMgr struct {
	authenticated bool
	token         string
	err           error
}

func (m *mockOAuthMgr) IsAuthenticated() bool                { return m.authenticated }
func (m *mockOAuthMgr) GetValidAccessToken() (string, error) { return m.token, m.err }

func TestOAuthResolver(t *testing.T) {
	mock := &mockOAuthMgr{authenticated: true, token: "access-tok"}
	r := NewOAuthResolver(mock)
	token, header, prefix, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if token != "access-tok" {
		t.Errorf("token = %q, want access-tok", token)
	}
	if header != "Authorization" {
		t.Errorf("header = %q, want Authorization", header)
	}
	if prefix != "Bearer " {
		t.Errorf("prefix = %q, want 'Bearer '", prefix)
	}
}

func TestOAuthResolver_NotAuthenticated(t *testing.T) {
	mock := &mockOAuthMgr{authenticated: false}
	r := NewOAuthResolver(mock)
	_, _, _, err := r.Resolve()
	if err == nil {
		t.Fatal("expected error when not authenticated")
	}
}
