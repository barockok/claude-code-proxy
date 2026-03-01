package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/anthropics/claude-code-proxy/internal/config"
)

const (
	DefaultUpstreamURL = "https://api.anthropic.com/v1/messages"
	Version            = "2023-06-01"
	BetaHeader         = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14"
	UserAgent          = "claude-code-proxy/1.0.0"
)

type AuthResolver interface {
	Resolve(apiKeyHeader string) (string, error)
	ClearCache()
}

type Handler struct {
	cfg         *config.Config
	auth        AuthResolver
	client      *http.Client
	UpstreamURL string
}

func NewHandler(cfg *config.Config, auth AuthResolver) *Handler {
	return &Handler{
		cfg:  cfg,
		auth: auth,
		client: &http.Client{
			Timeout: 0, // no timeout — let upstream take as long as it needs
		},
		UpstreamURL: DefaultUpstreamURL,
	}
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

	slog.Debug("Incoming request", "path", r.URL.Path, "bytes", len(rawBody))

	token, err := h.auth.Resolve("")
	if err != nil {
		slog.Error("Authentication failed", "error", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	resp, err := h.doUpstreamRequest(context.Background(), rawBody, token)
	if err != nil {
		slog.Error("Upstream request failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		resp.Body.Close()
		slog.Info("Got 401, refreshing token and retrying")
		h.auth.ClearCache()

		token, err = h.auth.Resolve("")
		if err != nil {
			slog.Warn("Token refresh failed", "error", err)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication failed after retry"})
			return
		}

		resp, err = h.doUpstreamRequest(context.Background(), rawBody, token)
		if err != nil {
			slog.Error("Retry upstream request failed", "error", err)
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
		slog.Debug("Streaming response sent to client")
	} else {
		w.Header().Del("Content-Encoding")
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.Error("Failed to read upstream response", "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream read error"})
			return
		}

		slog.Debug("Non-streaming response", "status", resp.StatusCode, "bytes", len(respBody))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		slog.Debug("Buffered response sent to client")
	}
}

func (h *Handler) doUpstreamRequest(ctx context.Context, body []byte, token string) (*http.Response, error) {
	slog.Debug("Outgoing request to upstream", "bytes", len(body))

	req, err := http.NewRequestWithContext(ctx, "POST", h.UpstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	req.Header.Set("anthropic-version", Version)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("anthropic-beta", BetaHeader)

	return h.client.Do(req)
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
