// Package api provides the HTTP API server for kombify StackKits.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
)

// ServerConfig holds configuration for the API server.
type ServerConfig struct {
	Port                          int
	BaseDir                       string
	Version                       string
	APIKey                        string   // If set, all non-health endpoints require X-API-Key header
	CORSOrigins                   []string // Allowed CORS origins; empty disables browser CORS, "*" allows wildcard
	RateLimit                     int      // Max requests per IP per minute; 0 = no limit
	LogDir                        string   // Directory containing deploy log files (.stackkit/logs/)
	ServiceAuthSecret             string
	ServiceAuthSecretNext         string
	RuntimeActionMode             string
	RuntimeRestoreVerifierCommand string
	// TrustedProxies may provide X-Forwarded-For for rate-limit identity.
	// Empty means X-Forwarded-For is ignored.
	TrustedProxies []string
}

// Server is the StackKits HTTP API server.
type Server struct {
	config ServerConfig
	mux    *http.ServeMux
	ctx    context.Context
	cancel context.CancelFunc
}

// NewServer creates a new API server.
func NewServer(cfg ServerConfig) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		config: cfg,
		mux:    http.NewServeMux(),
		ctx:    ctx,
		cancel: cancel,
	}
	s.routes()
	return s
}

// Close stops background goroutines (e.g., rate limiter cleanup).
// Call this when the server is no longer needed to prevent goroutine leaks.
func (s *Server) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

// Handler returns the HTTP handler with middleware applied.
func (s *Server) Handler() http.Handler {
	var handler http.Handler = s.mux
	handler = requestIDMiddleware(handler)
	if s.config.APIKey != "" {
		handler = apiKeyMiddleware(s.config.APIKey)(handler)
	}
	if s.config.RateLimit > 0 {
		handler = rateLimitMiddleware(s.ctx, s.config.RateLimit, s.config.TrustedProxies)(handler)
	}
	handler = corsMiddleware(s.config.CORSOrigins)(handler)
	handler = loggingMiddleware(handler)
	handler = recoveryMiddleware(handler)
	return handler
}

func (s *Server) routes() {
	// Health & discovery
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/capabilities", s.handleCapabilities)
	s.mux.HandleFunc("GET /api/v1/openapi.yaml", s.handleOpenAPISpec)

	s.registerRuntimeActionRoutes()

	// StackKit catalog
	s.mux.HandleFunc("GET /api/v1/stackkits", s.handleListStackKits)
	s.mux.HandleFunc("GET /api/v1/stackkits/{name}", s.handleGetStackKit)
	s.mux.HandleFunc("GET /api/v1/stackkits/{name}/schema", s.handleGetStackKitSchema)
	s.mux.HandleFunc("GET /api/v1/stackkits/{name}/defaults", s.handleGetStackKitDefaults)

	// Validation
	s.mux.HandleFunc("POST /api/v1/validate", s.handleValidateSpec)
	s.mux.HandleFunc("POST /api/v1/validate/partial", s.handleValidatePartial)

	// Generation
	s.mux.HandleFunc("POST /api/v1/generate/tfvars", s.handleGenerateTFVars)
	s.mux.HandleFunc("POST /api/v1/generate/preview", s.handleGeneratePreview)

	// Logs
	s.mux.HandleFunc("GET /api/v1/logs", s.handleListLogs)
	s.mux.HandleFunc("GET /api/v1/logs/latest", s.handleGetLatestLog)
	s.mux.HandleFunc("GET /api/v1/logs/{runID}", s.handleGetLog)
	s.mux.HandleFunc("GET /api/v1/logs/{runID}/stream", s.handleStreamLog)
}

// ── Response helpers ──────────────────────────────────────────────

// ResponseMeta contains request tracking metadata.
type ResponseMeta struct {
	RequestID string `json:"request_id,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

type successResponse struct {
	Data interface{}   `json:"data"`
	Meta *ResponseMeta `json:"meta,omitempty"`
}

type errorDetail struct {
	Code        int                    `json:"code"`
	Message     string                 `json:"message"`
	Category    string                 `json:"category,omitempty"`
	ErrorCode   string                 `json:"error_code,omitempty"`
	Details     map[string]interface{} `json:"details,omitempty"`
	Suggestions []string               `json:"suggestions,omitempty"`
}

type errorResponse struct {
	Error errorDetail   `json:"error"`
	Meta  *ResponseMeta `json:"meta,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

func writeSuccess(w http.ResponseWriter, r *http.Request, status int, data interface{}) {
	writeJSON(w, status, successResponse{
		Data: data,
		Meta: metaFromRequest(r),
	})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if status >= http.StatusInternalServerError {
		message = "internal error"
	}
	writeJSON(w, status, errorResponse{
		Error: errorDetail{Code: status, Message: message},
		Meta:  metaFromRequest(r),
	})
}

// writeStructuredError writes a StackKitError as a JSON response with category, code, and suggestions.
func writeStructuredError(w http.ResponseWriter, r *http.Request, status int, err *skerrors.StackKitError) {
	writeJSON(w, status, errorResponse{
		Error: errorDetail{
			Code:        status,
			Message:     err.Message,
			Category:    string(err.Category),
			ErrorCode:   err.Code,
			Details:     err.Fields,
			Suggestions: err.Suggestions,
		},
		Meta: metaFromRequest(r),
	})
}

func metaFromRequest(r *http.Request) *ResponseMeta {
	rid := ""
	if r != nil {
		rid = r.Header.Get("X-Request-ID")
	}
	return &ResponseMeta{
		RequestID: rid,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

// ── Middleware ─────────────────────────────────────────────────────

type contextKey string

const requestIDKey contextKey = "request_id"

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if rid == "" {
			rid = uuid.NewString()
		} else if len(rid) > 128 {
			rid = rid[:128]
		}
		r.Header.Set("X-Request-ID", rid)
		w.Header().Set("X-Request-ID", rid)
		ctx := context.WithValue(r.Context(), requestIDKey, rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func corsMiddleware(origins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(origins) > 0 {
				reqOrigin := r.Header.Get("Origin")
				matched := false
				for _, o := range origins {
					if o == "*" {
						w.Header().Set("Access-Control-Allow-Origin", "*")
						matched = true
						break
					}
					if o == reqOrigin {
						w.Header().Set("Access-Control-Allow-Origin", reqOrigin)
						w.Header().Set("Vary", "Origin")
						matched = true
						break
					}
				}
				if !matched && reqOrigin != "" {
					slog.Debug("CORS origin denied", "origin", reqOrigin)
				}
				// Non-matching origins get NO Access-Control-Allow-Origin header (CORS denied)
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, X-API-Key, X-Kombify-Service-Auth, X-User-ID, X-Org-ID")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ── Rate Limiting ─────────────────────────────────────────────────

// rateLimitEntry tracks request counts for a client IP.
type rateLimitEntry struct {
	count  int
	window time.Time
}

// rateLimitMiddleware applies a simple per-IP sliding-window rate limit.
// maxPerMinute is the maximum number of requests allowed per IP per minute.
// Health endpoints are exempt. The ctx parameter allows graceful shutdown of
// the cleanup goroutine to prevent leaks.
func rateLimitMiddleware(ctx context.Context, maxPerMinute int, trustedProxies []string) func(http.Handler) http.Handler {
	var mu sync.Mutex
	clients := make(map[string]*rateLimitEntry)

	// Cleanup old entries every 5 minutes, stops when ctx is canceled
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				now := time.Now()
				for ip, entry := range clients {
					if now.Sub(entry.window) > time.Minute {
						delete(clients, ip)
					}
				}
				mu.Unlock()
			}
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Exempt health endpoints from rate limiting
			if r.URL.Path == "/health" || r.URL.Path == "/api/v1/health" {
				next.ServeHTTP(w, r)
				return
			}

			ip := clientIPForRateLimit(r, trustedProxies)

			mu.Lock()
			now := time.Now()
			entry, exists := clients[ip]
			if !exists || now.Sub(entry.window) > time.Minute {
				clients[ip] = &rateLimitEntry{count: 1, window: now}
				mu.Unlock()
				next.ServeHTTP(w, r)
				return
			}

			entry.count++
			if entry.count > maxPerMinute {
				mu.Unlock()
				w.Header().Set("Retry-After", "60")
				writeStructuredError(w, r, http.StatusTooManyRequests, skerrors.NewResourceError(
					"rate_limit_exceeded", "rate limit exceeded",
					skerrors.WithField("limit", maxPerMinute),
					skerrors.WithField("window", "1m"),
					skerrors.WithSuggestion("Wait 60 seconds before retrying"),
					skerrors.WithSuggestion("Reduce request frequency or contact administrator to increase limits"),
				))
				return
			}
			mu.Unlock()

			next.ServeHTTP(w, r)
		})
	}
}

func clientIPForRateLimit(r *http.Request, trustedProxies []string) string {
	remoteIP := remoteHost(r.RemoteAddr)
	if remoteIP == "" {
		remoteIP = r.RemoteAddr
	}

	if isTrustedProxy(remoteIP, trustedProxies) {
		if forwardedIP := firstForwardedFor(r.Header.Get("X-Forwarded-For")); forwardedIP != "" {
			return forwardedIP
		}
	}

	return remoteIP
}

func remoteHost(remoteAddr string) string {
	addr := strings.TrimSpace(remoteAddr)
	if addr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	if ip := net.ParseIP(addr); ip != nil {
		return ip.String()
	}
	return addr
}

func firstForwardedFor(header string) string {
	for _, part := range strings.Split(header, ",") {
		candidate := strings.TrimSpace(part)
		if candidate == "" {
			continue
		}
		if ip := net.ParseIP(candidate); ip != nil {
			return ip.String()
		}
		return ""
	}
	return ""
}

func isTrustedProxy(remoteIP string, trustedProxies []string) bool {
	ip := net.ParseIP(remoteIP)
	for _, proxy := range trustedProxies {
		proxy = strings.TrimSpace(proxy)
		if proxy == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(proxy); err == nil {
			if ip != nil && network.Contains(ip) {
				return true
			}
			continue
		}
		if proxyIP := net.ParseIP(proxy); proxyIP != nil {
			if ip != nil && ip.Equal(proxyIP) {
				return true
			}
			continue
		}
		if proxy == remoteIP {
			return true
		}
	}
	return false
}

// statusResponseWriter wraps http.ResponseWriter to capture the status code.
type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *statusResponseWriter) WriteHeader(code int) {
	if !w.written {
		w.statusCode = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusResponseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.statusCode = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", r.Header.Get("X-Request-ID"),
		)
	})
}

// apiKeyMiddleware validates the X-API-Key header against the configured key.
// Health and OpenAPI spec endpoints are exempt from authentication.
func apiKeyMiddleware(validKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Allow health and OpenAPI spec without auth
			if r.URL.Path == "/health" || r.URL.Path == "/api/v1/health" || r.URL.Path == "/api/v1/openapi.yaml" || strings.HasPrefix(r.URL.Path, "/api/v1/internal/") {
				next.ServeHTTP(w, r)
				return
			}
			// Allow CORS preflight without auth
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			key := strings.TrimSpace(r.Header.Get("X-API-Key"))
			if key == "" {
				writeStructuredError(w, r, http.StatusUnauthorized, skerrors.NewAuthError(
					"missing_api_key", "missing X-API-Key header",
					skerrors.WithSuggestion("Include the X-API-Key header in your request"),
					skerrors.WithSuggestion("Health endpoint does not require authentication: GET /api/v1/health"),
				))
				return
			}
			// Use constant-time comparison to prevent timing attacks
			if subtle.ConstantTimeCompare([]byte(key), []byte(validKey)) != 1 {
				writeStructuredError(w, r, http.StatusForbidden, skerrors.NewAuthError(
					"invalid_api_key", "invalid API key",
					skerrors.WithSuggestion("Verify your API key is correct"),
					skerrors.WithSuggestion("API keys are set via the --api-key flag on the server"),
				))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic recovered", "error", err, "path", r.URL.Path)
				writeError(w, r, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
