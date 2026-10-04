// Command stackkit-content-bridge is the node side of the kombify Content
// Bridge. It answers consented counts from the apps of one server as
// `homelab-content/v1`, strictly read-only, from a container that has no
// internet egress (HOMELAB-CONTENT-BRIDGE-STANDARD, docs/ADR/ADR-0047).
//
// It is a separate binary on purpose: it links only the standard library and
// internal/contentbridge, so a read-only, capability-free container carries
// none of the CLI, API server or MCP code.
//
// Configuration is environment only:
//
//	CONTENT_BRIDGE_HOMELAB_ID           installation identifier (required)
//	CONTENT_BRIDGE_LISTEN               default :8083
//	CONTENT_BRIDGE_IMMICH_URL           default http://immich-server:2283
//	CONTENT_BRIDGE_IMMICH_API_KEY_FILE  default /run/secrets/immich-api-key
//	CONTENT_BRIDGE_CACHE_TTL            default and maximum 60s
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kombifyio/stackkits/internal/contentbridge"
)

// Build identity is set at build time via ldflags.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func main() {
	showVersion := flag.Bool("version", false, "print the build identity and exit")
	showPermissions := flag.Bool("print-immich-permissions", false, "print the Immich API key permissions the bridge needs, one per line, and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("stackkit-content-bridge %s (%s, %s)\n", Version, GitCommit, BuildDate)
		return
	}
	if *showPermissions {
		for _, permission := range contentbridge.ImmichPermissions() {
			fmt.Println(permission)
		}
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("content bridge stopped", "error", err.Error())
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := contentbridge.ConfigFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	service, err := contentbridge.NewServiceFromConfig(cfg, logger)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: cfg.Listen,
		// A tier is granted only to a caller on the bridge's own loopback until
		// the Gateway's signed envelope authority replaces this one.
		Handler:           contentbridge.NewHandler(service, contentbridge.LoopbackQueryTier{}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	logger.Info("content bridge starting", "version", Version, "listen", cfg.Listen)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	failed := make(chan error, 1)
	go func() { failed <- server.ListenAndServe() }()
	select {
	case err := <-failed:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
