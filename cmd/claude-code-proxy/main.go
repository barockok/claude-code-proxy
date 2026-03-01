package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anthropics/claude-code-proxy/internal/auth"
	"github.com/anthropics/claude-code-proxy/internal/config"
	"github.com/anthropics/claude-code-proxy/internal/logger"
	"github.com/anthropics/claude-code-proxy/internal/oauth"
	"github.com/anthropics/claude-code-proxy/internal/proxy"
)

//go:embed static/*
var staticFS embed.FS

type pkceState struct {
	CodeVerifier string
	CreatedAt    time.Time
}

var (
	pkceStates   = make(map[string]pkceState)
	pkceMu       sync.Mutex
	pkceExpiryMs = 10 * time.Minute
)

func cleanupExpiredPKCE() {
	pkceMu.Lock()
	defer pkceMu.Unlock()
	now := time.Now()
	for state, data := range pkceStates {
		if now.Sub(data.CreatedAt) > pkceExpiryMs {
			delete(pkceStates, state)
		}
	}
}

func isRunningInDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return false
	}
	s := string(data)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd")
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		slog.Debug("Failed to open browser", "error", err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, x-api-key")

		if r.Method == http.MethodOptions {
			w.WriteHeader(200)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	port := flag.Int("port", 0, "server port (overrides config)")
	host := flag.String("host", "", "server host (overrides config)")
	logLevel := flag.String("log-level", "", "log level: trace, debug, info, warn, error")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	config.ApplyEnv(&cfg)

	if *port != 0 {
		cfg.Server.Port = *port
	}
	if *host != "" {
		cfg.Server.Host = *host
	}
	if *logLevel != "" {
		cfg.Logging.Level = *logLevel
	}

	logger.Init(cfg.Logging.Level, nil)

	slog.Info("Config loaded", "port", cfg.Server.Port, "log_level", cfg.Logging.Level)

	serverHost := cfg.Server.Host
	if serverHost == "" {
		if isRunningInDocker() {
			serverHost = "0.0.0.0"
		} else {
			serverHost = "127.0.0.1"
		}
	}

	oauthMgr := oauth.NewManager()

	authResolver := &auth.Resolver{
		OAuthMgr:             oauthMgr,
		FallbackToClaudeCode: cfg.Auth.FallbackToClaudeCode,
	}

	proxyHandler := proxy.NewHandler(&cfg, authResolver)

	ticker := time.NewTicker(60 * time.Second)
	go func() {
		for range ticker.C {
			cleanupExpiredPKCE()
		}
	}()

	mux := http.NewServeMux()

	// OAuth routes
	mux.HandleFunc("GET /auth/login", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFS.ReadFile("static/login.html")
		if err != nil {
			http.Error(w, "Not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	})

	mux.HandleFunc("GET /auth/get-url", func(w http.ResponseWriter, r *http.Request) {
		pkce := oauth.GeneratePKCE()

		pkceMu.Lock()
		pkceStates[pkce.State] = pkceState{
			CodeVerifier: pkce.CodeVerifier,
			CreatedAt:    time.Now(),
		}
		pkceMu.Unlock()

		authURL := oauth.BuildAuthorizationURL(pkce)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"url":   authURL,
			"state": pkce.State,
		})
		slog.Info("Generated OAuth authorization URL")
	})

	mux.HandleFunc("GET /auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		code := q.Get("code")
		state := q.Get("state")

		if manualCode := q.Get("manual_code"); manualCode != "" {
			parts := strings.SplitN(manualCode, "#", 2)
			if len(parts) != 2 {
				http.Error(w, "Invalid code format. Expected: code#state", 400)
				return
			}
			code = parts[0]
			state = parts[1]
		}

		if code == "" || state == "" {
			http.Error(w, "Missing authorization code or state", 400)
			return
		}

		pkceMu.Lock()
		pkceData, ok := pkceStates[state]
		if ok {
			delete(pkceStates, state)
		}
		pkceMu.Unlock()

		if !ok {
			http.Error(w, "Invalid or expired state. Please start again.", 400)
			return
		}

		tokens, err := oauthMgr.ExchangeCodeForTokens(code, pkceData.CodeVerifier, state)
		if err != nil {
			slog.Error("OAuth callback error", "error", err)
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(500)
			fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>Authentication Failed</title></head><body><h1>Authentication Failed</h1><p>Error: %s</p><p><a href="/auth/login">Try again</a></p></body></html>`, err.Error())
			return
		}

		tokenData := &oauth.Tokens{
			AccessToken:  tokens.AccessToken,
			RefreshToken: tokens.RefreshToken,
			ExpiresAt:    time.Now().UnixMilli() + int64(tokens.ExpiresIn)*1000,
		}
		if err := oauthMgr.SaveTokens(tokenData); err != nil {
			slog.Error("Failed to save tokens", "error", err)
			http.Error(w, "Failed to save tokens", 500)
			return
		}

		data, _ := staticFS.ReadFile("static/callback.html")
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
		slog.Info("OAuth authentication successful")
	})

	mux.HandleFunc("GET /auth/status", func(w http.ResponseWriter, r *http.Request) {
		isAuth := oauthMgr.IsAuthenticated()
		exp := oauthMgr.GetTokenExpiration()

		var expiresAt *string
		if exp != nil {
			s := exp.Format(time.RFC3339)
			expiresAt = &s
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"authenticated": isAuth,
			"expires_at":    expiresAt,
		})
	})

	mux.HandleFunc("GET /auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if err := oauthMgr.Logout(); err != nil {
			slog.Error("Logout error", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to logout"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Logged out successfully",
		})
		slog.Info("User logged out")
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "ok",
			"server":    "claude-code-proxy",
			"timestamp": time.Now().UnixMilli(),
		})
	})

	// Proxy routes
	mux.HandleFunc("POST /v1/messages", proxyHandler.ServeHTTP)
	mux.HandleFunc("POST /v1/{preset}/messages", proxyHandler.ServeHTTP)

	addr := fmt.Sprintf("%s:%d", serverHost, cfg.Server.Port)
	server := &http.Server{
		Addr:              addr,
		Handler:           corsMiddleware(mux),
		ReadHeaderTimeout: 0,
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       0,
	}

	isAuth := oauthMgr.IsAuthenticated()
	exp := oauthMgr.GetTokenExpiration()

	slog.Info(fmt.Sprintf("claude-code-proxy listening on %s", addr))
	slog.Info("")
	slog.Info("Authentication Status:")
	if isAuth && exp != nil {
		slog.Info(fmt.Sprintf("  Authenticated until %s", exp.Local().Format(time.RFC1123)))
	} else {
		slog.Info("  Not authenticated")
		authURL := fmt.Sprintf("http://localhost:%d/auth/login", cfg.Server.Port)
		slog.Info(fmt.Sprintf("  Visit %s to authenticate", authURL))

		if cfg.Auth.AutoOpenBrowser && !isRunningInDocker() {
			slog.Info("  Opening browser for authentication...")
			go func() {
				time.Sleep(1 * time.Second)
				openBrowser(authURL)
			}()
		}
	}
	slog.Info("")

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		slog.Info("Shutting down...")
		ticker.Stop()
		server.Close()
	}()

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("Server failed", "error", err)
		os.Exit(1)
	}
}
