package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockResolver struct {
	token        string
	headerName   string
	headerPrefix string
}

func (m *mockResolver) Resolve() (string, string, string, error) {
	return m.token, m.headerName, m.headerPrefix, nil
}
func (m *mockResolver) ClearCache() {}

func TestHandler_RoutesToCorrectProvider(t *testing.T) {
	var gotAuth string
	var gotCustomHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCustomHeader = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	h := NewHandler()
	h.AddProvider("anthropic", &ProviderEntry{
		Upstream: upstream.URL,
		Auth:     &mockResolver{token: "oauth-tok", headerName: "Authorization", headerPrefix: "Bearer "},
		Headers:  map[string]string{"anthropic-version": "2023-06-01"},
		Patterns: []string{"claude-*"},
	})

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if gotAuth != "Bearer oauth-tok" {
		t.Errorf("Authorization = %q, want 'Bearer oauth-tok'", gotAuth)
	}
	if gotCustomHeader != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want 2023-06-01", gotCustomHeader)
	}
}

func TestHandler_UnknownModel(t *testing.T) {
	h := NewHandler()
	h.AddProvider("anthropic", &ProviderEntry{
		Upstream: "https://unused",
		Auth:     &mockResolver{token: "t", headerName: "Authorization", headerPrefix: "Bearer "},
		Patterns: []string{"claude-*"},
	})

	body := `{"model":"unknown-model","messages":[]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400 for unknown model", w.Code)
	}
}

func TestHandler_StreamingResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		w.Write([]byte("data: {\"type\":\"text_delta\"}\n\n"))
		flusher.Flush()
	}))
	defer upstream.Close()

	h := NewHandler()
	h.AddProvider("anthropic", &ProviderEntry{
		Upstream: upstream.URL,
		Auth:     &mockResolver{token: "t", headerName: "Authorization", headerPrefix: "Bearer "},
		Patterns: []string{"claude-*"},
	})

	body := `{"model":"claude-sonnet-4-20250514","stream":true,"messages":[]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", w.Header().Get("Content-Type"))
	}
}

func TestHandler_401Retry(t *testing.T) {
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	h := NewHandler()
	h.AddProvider("anthropic", &ProviderEntry{
		Upstream: upstream.URL,
		Auth:     &mockResolver{token: "t", headerName: "Authorization", headerPrefix: "Bearer "},
		Patterns: []string{"claude-*"},
	})

	body := `{"model":"claude-sonnet-4-20250514","messages":[]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestHandler_BodyPassthrough(t *testing.T) {
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	h := NewHandler()
	h.AddProvider("openai", &ProviderEntry{
		Upstream: upstream.URL,
		Auth:     &mockResolver{token: "sk-test", headerName: "Authorization", headerPrefix: "Bearer "},
		Patterns: []string{"gpt-*"},
	})

	reqBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(reqBody))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if receivedBody != reqBody {
		t.Errorf("body modified.\ngot:  %s\nwant: %s", receivedBody, reqBody)
	}
}
