// stackkit-backup-controller is the host binary for the kombify
// Backup-Controller HTTP API.
//
// In Phase 4 of the backup rollout this binary runs as a long-lived
// service (via systemd or a Docker container) and exposes the routes
// defined in internal/backup-controller/server.go: tenant onboarding,
// host enrollment, job creation, agent heartbeat, audit log.
//
// Today the binary uses the in-memory Store and JobQueue
// implementations from internal/backup-controller. That is intentional
// — it gives the kombify-TechStack team a real network endpoint to
// integrate against while the Postgres-backed Store and NATS
// JetStream Queue land in follow-up PRs (see the package README and
// docs/plans/2026-05-01-backup-rollout.md). The HTTP API surface is
// stable; only the backing store changes.
//
// Usage:
//
//	stackkit-backup-controller                    # default :8083
//	stackkit-backup-controller --port 9090
//	stackkit-backup-controller --api-key prod-key
//
// Environment variables (override flags when set):
//
//	BACKUP_CONTROLLER_PORT       — listen port
//	BACKUP_CONTROLLER_API_KEY    — operator X-API-Key
//	BACKUP_CONTROLLER_LOG_LEVEL  — debug | info | warn | error
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	backupcontroller "github.com/kombifyio/stackkits/internal/backup-controller"
)

// Version is set at build time via ldflags.
var Version = "dev"

func main() {
	port := flag.Int("port", 8083, "HTTP server port")
	apiKey := flag.String("api-key", "", "Operator X-API-Key (or BACKUP_CONTROLLER_API_KEY)")
	logLevel := flag.String("log-level", "info", "Log level: debug | info | warn | error")
	enableScheduler := flag.Bool("scheduler", true, "Run the in-process cron scheduler (default true; disable when running multiple replicas behind a leader-election lock)")
	flag.Parse()

	setupLogging(envOr("BACKUP_CONTROLLER_LOG_LEVEL", *logLevel))

	resolved := resolvePort(*port)
	key := envOr("BACKUP_CONTROLLER_API_KEY", *apiKey)
	if key == "" {
		slog.Error("operator API key is required; set --api-key or BACKUP_CONTROLLER_API_KEY")
		os.Exit(1)
	}

	store := backupcontroller.NewMemoryStore()
	queue := backupcontroller.NewMemoryQueue(0)
	defer func() { _ = queue.Close() }()

	audit := &backupcontroller.AuditLog{Store: store}

	srv := &backupcontroller.Server{
		Store:          store,
		Audit:          audit,
		OperatorAPIKey: key,
	}

	slog.Info("starting kombify Backup-Controller (scaffold)",
		"version", Version,
		"port", resolved,
		"store", "in-memory",
		"queue", "in-memory",
	)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", resolved),
		Handler:      srv.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()

	if *enableScheduler {
		sched := &backupcontroller.Scheduler{Store: store, Queue: queue}
		go func() {
			if err := sched.Run(rootCtx); err != nil && err != context.Canceled {
				slog.Error("scheduler exited unexpectedly", "err", err)
			}
		}()
		slog.Info("in-process scheduler enabled")
	}

	runServer(httpServer, cancelRoot)
}

func setupLogging(level string) {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
	slog.SetDefault(logger)
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func resolvePort(flagVal int) int {
	if v := os.Getenv("BACKUP_CONTROLLER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
		slog.Warn("ignoring invalid BACKUP_CONTROLLER_PORT", "value", v)
	}
	return flagVal
}

func runServer(httpServer *http.Server, cancelRoot context.CancelFunc) {
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()
	slog.Info("listening", "addr", httpServer.Addr)

	<-done
	slog.Info("shutting down…")

	cancelRoot() // stop scheduler
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("server stopped")
}
