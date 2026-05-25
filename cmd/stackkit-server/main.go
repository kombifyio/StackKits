// Package main provides the HTTP API server for kombify StackKits.
//
// This is a lightweight REST API that wraps the existing CLI logic,
// making StackKits functionality available to:
//   - kombify Stack (via internal calls or Cloudflare Edge)
//   - kombify Gateway (Cloudflare Edge) for external consumers
//   - AI agents and native client apps
//
// Usage:
//
//	stackkit-server                          # default :8082
//	stackkit-server --port 9090             # custom port
//	stackkit-server --base-dir /stackkits   # custom StackKit directory
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kombifyio/stackkits/internal/api"
	"github.com/kombifyio/stackkits/internal/kombifyme"
)

// Version is set at build time via ldflags.
var Version = "dev"

func main() {
	port := flag.Int("port", 8082, "HTTP server port")
	baseDir := flag.String("base-dir", "", "Base directory for StackKit definitions (default: executable directory)")
	apiKey := flag.String("api-key", "", "API key for authentication (or set STACKKITS_API_KEY env var)")
	allowUnauthenticated := flag.Bool("allow-unauthenticated", false, "Allow unauthenticated API access for local development only (or set STACKKITS_ALLOW_UNAUTHENTICATED=true)")
	corsOrigins := flag.String("cors-origins", "", "Comma-separated allowed CORS origins (or set STACKKITS_CORS_ORIGINS; empty disables browser CORS)")
	allowWildcardCORS := flag.Bool("allow-wildcard-cors", false, "Allow wildcard CORS for local development only (or set STACKKITS_ALLOW_WILDCARD_CORS=true)")
	rateLimit := flag.Int("rate-limit", 60, "Max requests per IP per minute; 0 = no limit (or set STACKKITS_RATE_LIMIT)")
	trustedProxies := flag.String("trusted-proxies", "", "Comma-separated trusted proxy IPs/CIDRs for X-Forwarded-For rate limiting (or set STACKKITS_TRUSTED_PROXIES)")
	logDir := flag.String("log-dir", "", "Directory containing deploy logs (or set STACKKITS_LOG_DIR)")
	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")
	instanceID := flag.String("instance-id", "", "Instance ID for kombify registry heartbeat (or set STACKKITS_INSTANCE_ID)")
	flag.Parse()

	setupLogging(*logLevel)

	cfg, cfgErr := resolveConfig(*port, *baseDir, *apiKey, *corsOrigins, *rateLimit, *logDir, *trustedProxies, *allowUnauthenticated, *allowWildcardCORS)
	if cfgErr != nil {
		slog.Error(cfgErr.Error())
		os.Exit(1)
	}

	slog.Info("starting kombify StackKits API server",
		"version", Version,
		"port", cfg.Port,
		"base_dir", cfg.BaseDir,
	)

	srv := api.NewServer(cfg)

	httpServer := newHTTPServer(cfg, srv.Handler())

	// Start heartbeat if instance is registered with kombify
	heartbeatCtx, heartbeatCancel := context.WithCancel(context.Background())
	defer heartbeatCancel()
	startHeartbeat(heartbeatCtx, *instanceID)

	runServer(httpServer)
}

func newHTTPServer(cfg api.ServerConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 14*time.Minute + 30*time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

func setupLogging(logLevel string) {
	var level slog.Level
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
}

func resolveConfig(port int, baseDir, apiKey, corsOrigins string, rateLimit int, logDir, trustedProxies string, allowUnauthenticated, allowWildcardCORS bool) (api.ServerConfig, error) {
	dir := resolveBaseDir(baseDir)
	runtimeProfile := resolveRuntimeProfile()
	productionGuards := runtimeProfileRequiresProductionGuards(runtimeProfile)
	key, err := resolveAPIKey(apiKey, allowUnauthenticated, productionGuards)
	if err != nil {
		return api.ServerConfig{}, err
	}
	origins, err := resolveCORSOrigins(corsOrigins, allowWildcardCORS, productionGuards)
	if err != nil {
		return api.ServerConfig{}, err
	}
	rl := resolveRateLimit(rateLimit)
	ld := resolveLogDir(logDir, dir)
	proxies := resolveTrustedProxies(trustedProxies)

	return api.ServerConfig{
		Port:                          port,
		BaseDir:                       dir,
		Version:                       Version,
		APIKey:                        key,
		CORSOrigins:                   origins,
		RateLimit:                     rl,
		LogDir:                        ld,
		TrustedProxies:                proxies,
		ServiceAuthSecret:             strings.TrimSpace(os.Getenv("SERVICE_AUTH_SECRET")),
		ServiceAuthSecretNext:         strings.TrimSpace(os.Getenv("SERVICE_AUTH_SECRET_NEXT")),
		RuntimeActionMode:             resolveRuntimeActionMode(),
		RuntimeRestoreVerifierCommand: strings.TrimSpace(os.Getenv("STACKKITS_RESTORE_DRILL_COMMAND")),
		SetupActionMode:               resolveSetupActionMode(),
		SetupAdminEmail:               strings.TrimSpace(os.Getenv("STACKKIT_ADMIN_EMAIL")),
		SetupAdminPassword:            strings.TrimSpace(os.Getenv("STACKKIT_ADMIN_PASSWORD")),
		SetupImmichURL:                strings.TrimSpace(os.Getenv("STACKKIT_SETUP_IMMICH_URL")),
	}, nil
}

func resolveRuntimeProfile() string {
	for _, key := range []string{"STACKKITS_RUNTIME_PROFILE", "KOMBIFY_ENV", "APP_ENV", "ENVIRONMENT"} {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
		switch value {
		case "prod", "production", "public", "managed", "enterprise":
			return "production"
		case "dev", "development", "local", "test":
			return "local"
		}
	}
	if envBool("STACKKITS_PRODUCTION") {
		return "production"
	}
	return "local"
}

func runtimeProfileRequiresProductionGuards(profile string) bool {
	return profile == "production"
}

func resolveRuntimeActionMode() string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("STACKKITS_RUNTIME_ACTION_MODE")))
	switch mode {
	case "apply":
		slog.Warn("StackKits runtime actions will execute OpenTofu apply/state commands")
		return "apply"
	case "", "dry-run":
		return "dry-run"
	default:
		slog.Warn("unknown STACKKITS_RUNTIME_ACTION_MODE; falling back to dry-run", "mode", mode)
		return "dry-run"
	}
}

func resolveSetupActionMode() string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("STACKKITS_SETUP_ACTION_MODE")))
	switch mode {
	case "apply":
		slog.Warn("StackKits setup actions will execute node-local setup drops")
		return "apply"
	case "", "dry-run":
		return "dry-run"
	default:
		slog.Warn("unknown STACKKITS_SETUP_ACTION_MODE; falling back to dry-run", "mode", mode)
		return "dry-run"
	}
}

func resolveLogDir(flagVal, baseDir string) string {
	dir := flagVal
	if dir == "" {
		dir = os.Getenv("STACKKITS_LOG_DIR")
	}
	if dir == "" {
		// Default: .stackkit/logs/ relative to base dir
		dir = filepath.Join(baseDir, ".stackkit", "logs")
	}
	if dir != "" {
		slog.Info("deploy logs directory", "log_dir", dir)
	}
	return dir
}

func resolveBaseDir(flagVal string) string {
	dir := flagVal
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			slog.Error("failed to get executable path", "error", err)
			os.Exit(1)
		}
		dir = filepath.Dir(exe)
	}
	if envDir := os.Getenv("STACKKITS_BASE_DIR"); envDir != "" {
		dir = envDir
	}
	return dir
}

func resolveAPIKey(flagVal string, allowUnauthenticated bool, productionGuards bool) (string, error) {
	key := flagVal
	if key == "" {
		key = os.Getenv("STACKKITS_API_KEY")
	}
	bypassRequested := allowUnauthenticated || envBool("STACKKITS_ALLOW_UNAUTHENTICATED")
	if productionGuards && bypassRequested {
		return "", fmt.Errorf("unauthenticated API access is forbidden when STACKKITS_RUNTIME_PROFILE is production/public/managed")
	}
	if key != "" {
		slog.Info("API key authentication enabled")
	} else {
		if bypassRequested {
			slog.Warn("unauthenticated API access enabled for local development")
			return "", nil
		}
		return "", fmt.Errorf("API key is required; set STACKKITS_API_KEY or pass --allow-unauthenticated for local development")
	}
	return key, nil
}

func resolveCORSOrigins(flagVal string, allowWildcard bool, productionGuards bool) ([]string, error) {
	corsStr := flagVal
	if corsStr == "" {
		corsStr = os.Getenv("STACKKITS_CORS_ORIGINS")
	}
	wildcardRequested := allowWildcard || envBool("STACKKITS_ALLOW_WILDCARD_CORS")
	if productionGuards && wildcardRequested {
		return nil, fmt.Errorf("wildcard CORS is forbidden when STACKKITS_RUNTIME_PROFILE is production/public/managed")
	}
	if corsStr == "" {
		if wildcardRequested {
			slog.Warn("wildcard CORS enabled for local development")
			return []string{"*"}, nil
		}
		slog.Info("browser CORS disabled; set STACKKITS_CORS_ORIGINS to allow browser clients")
		return nil, nil
	}
	var origins []string
	for _, o := range strings.Split(corsStr, ",") {
		if trimmed := strings.TrimSpace(o); trimmed != "" {
			if trimmed == "*" && productionGuards {
				return nil, fmt.Errorf("wildcard CORS is forbidden when STACKKITS_RUNTIME_PROFILE is production/public/managed")
			}
			origins = append(origins, trimmed)
		}
	}
	if len(origins) == 0 {
		slog.Info("browser CORS disabled; no valid origins configured")
	} else {
		slog.Info("CORS restricted", "origins", origins)
	}
	return origins, nil
}

func resolveTrustedProxies(flagVal string) []string {
	proxyStr := flagVal
	if proxyStr == "" {
		proxyStr = os.Getenv("STACKKITS_TRUSTED_PROXIES")
	}
	if proxyStr == "" {
		return nil
	}
	var proxies []string
	for _, proxy := range strings.Split(proxyStr, ",") {
		if trimmed := strings.TrimSpace(proxy); trimmed != "" {
			proxies = append(proxies, trimmed)
		}
	}
	if len(proxies) > 0 {
		slog.Info("trusted proxies configured", "proxies", proxies)
	}
	return proxies
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func resolveRateLimit(flagVal int) int {
	rl := flagVal
	if envRL := os.Getenv("STACKKITS_RATE_LIMIT"); envRL != "" {
		if v, err := strconv.Atoi(envRL); err == nil {
			rl = v
		}
	}
	if rl > 0 {
		slog.Info("rate limiting enabled", "max_per_minute", rl)
	}
	return rl
}

// startHeartbeat sends periodic heartbeats to kombify so Cloudflare Edge knows this instance is alive.
// Requires KOMBIFY_API_KEY env var and a valid instance ID.
func startHeartbeat(ctx context.Context, flagInstanceID string) {
	iid := flagInstanceID
	if iid == "" {
		iid = os.Getenv("STACKKITS_INSTANCE_ID")
	}
	if iid == "" {
		return
	}

	apiKey := os.Getenv("KOMBIFY_API_KEY")
	if apiKey == "" {
		slog.Warn("heartbeat skipped — no KOMBIFY_API_KEY set")
		return
	}

	client := kombifyme.NewClient(apiKey)
	slog.Info("heartbeat enabled", "instance_id", iid, "interval", "60s")

	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		// Send initial heartbeat immediately
		if err := client.Heartbeat(iid, "running"); err != nil {
			slog.Warn("heartbeat failed", "error", err)
		}

		for {
			select {
			case <-ctx.Done():
				// Deregister on shutdown
				if err := client.DeregisterInstance(iid); err != nil {
					slog.Warn("deregister failed", "error", err)
				} else {
					slog.Info("deregistered from kombify", "instance_id", iid)
				}
				return
			case <-ticker.C:
				if err := client.Heartbeat(iid, "running"); err != nil {
					slog.Warn("heartbeat failed", "error", err)
				}
			}
		}
	}()
}

func runServer(httpServer *http.Server) {
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("server listening", "addr", httpServer.Addr)

	<-done
	slog.Info("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}

	slog.Info("server stopped")
}
