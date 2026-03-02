package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/anthropics/claude-code-proxy/internal/auth"
	"github.com/anthropics/claude-code-proxy/internal/provider"
)

type ProviderEntry struct {
	Upstream string
	Auth     auth.Resolver
	Headers  map[string]string
	Patterns []string
}

type Handler struct {
	router    *provider.Router
	providers map[string]*ProviderEntry
	client    *http.Client
}

func NewHandler() *Handler {
	return &Handler{
		router:    provider.NewRouter(),
		providers: make(map[string]*ProviderEntry),
		client:    &http.Client{Timeout: 0},
	}
}

func (h *Handler) AddProvider(name string, entry *ProviderEntry) {
	h.providers[name] = entry
	h.router.Add(name, entry.Patterns)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Failed to read request body", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}

	model := provider.ExtractModel(rawBody)
	if model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or empty 'model' field in request body"})
		return
	}

	providerName, ok := h.router.Match(model)
	if !ok {
		slog.Warn("No provider matched", "model", model)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "no provider configured for model: " + model,
		})
		return
	}

	entry := h.providers[providerName]
	slog.Debug("Routing request", "model", model, "provider", providerName, "upstream", entry.Upstream)

	token, headerName, headerPrefix, err := entry.Auth.Resolve()
	if err != nil {
		slog.Error("Authentication failed", "provider", providerName, "error", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	upstreamURL := entry.Upstream + r.URL.Path

	resp, err := h.doUpstreamRequest(context.Background(), upstreamURL, rawBody, token, headerName, headerPrefix, entry.Headers)
	if err != nil {
		slog.Error("Upstream request failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		resp.Body.Close()
		slog.Info("Got 401, refreshing token and retrying", "provider", providerName)
		entry.Auth.ClearCache()

		token, headerName, headerPrefix, err = entry.Auth.Resolve()
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication failed after retry"})
			return
		}

		resp, err = h.doUpstreamRequest(context.Background(), upstreamURL, rawBody, token, headerName, headerPrefix, entry.Headers)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		defer resp.Body.Close()
	}

	for key, vals := range resp.Header {
		for _, val := range vals {
			w.Header().Add(key, val)
		}
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		w.WriteHeader(resp.StatusCode)
		flusher, canFlush := w.(http.Flusher)
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				if canFlush {
					flusher.Flush()
				}
			}
			if err != nil {
				if err != io.EOF {
					slog.Error("Stream read error", "error", err)
				}
				break
			}
		}
	} else {
		w.Header().Del("Content-Encoding")
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream read error"})
			return
		}
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
	}
}

func (h *Handler) doUpstreamRequest(ctx context.Context, url string, body []byte, token, headerName, headerPrefix string, extraHeaders map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerName, headerPrefix+token)

	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	return h.client.Do(req)
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
