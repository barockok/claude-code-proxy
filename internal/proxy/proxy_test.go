package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/claude-code-proxy/internal/config"
)

type mockAuthResolver struct{ token string }

func (m *mockAuthResolver) Resolve(apiKey string) (string, error) {
	return m.token, nil
}
func (m *mockAuthResolver) ClearCache() {}

func TestProxyHandler_BufferedResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q, want Bearer test-token", r.Header.Get("Authorization"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("missing anthropic-version header")
		}

		// Body should be passed through as-is
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		if body["model"] != "claude-3-5-sonnet-20241022" {
			t.Errorf("model not passed through")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"content": []map[string]string{{"type": "text", "text": "hello"}},
		})
	}))
	defer upstream.Close()

	cfg := config.Defaults()
	h := NewHandler(&cfg, &mockAuthResolver{token: "Bearer test-token"})
	h.UpstreamURL = upstream.URL

	reqBody := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["content"] == nil {
		t.Error("response should contain content")
	}
}

func TestProxyHandler_StreamingResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)

		flusher, _ := w.(http.Flusher)
		for _, event := range []string{
			`data: {"type":"content_block_start"}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
			`data: {"type":"message_stop"}`,
		} {
			w.Write([]byte(event + "\n\n"))
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	cfg := config.Defaults()
	h := NewHandler(&cfg, &mockAuthResolver{token: "Bearer test"})
	h.UpstreamURL = upstream.URL

	reqBody := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "text_delta") {
		t.Error("streaming response should contain text_delta events")
	}
}

func TestProxyHandler_401Retry(t *testing.T) {
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

	cfg := config.Defaults()
	h := NewHandler(&cfg, &mockAuthResolver{token: "Bearer test"})
	h.UpstreamURL = upstream.URL

	reqBody := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if attempts != 2 {
		t.Errorf("should retry on 401, got %d attempts", attempts)
	}
	if w.Code != 200 {
		t.Errorf("final status = %d, want 200 after retry", w.Code)
	}
}

func TestProxyHandler_BodyPassthrough(t *testing.T) {
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	cfg := config.Defaults()
	h := NewHandler(&cfg, &mockAuthResolver{token: "Bearer test"})
	h.UpstreamURL = upstream.URL

	// Send body with system prompt already set - proxy should NOT modify it
	reqBody := `{"model":"claude-3-5-sonnet-20241022","system":"my custom prompt","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	// Body should be passed through exactly as-is
	if receivedBody != reqBody {
		t.Errorf("body was modified.\ngot:  %s\nwant: %s", receivedBody, reqBody)
	}
}
