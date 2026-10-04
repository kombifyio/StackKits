package contentbridge

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Environment of the bridge container. The credential arrives as a file,
// never as an environment value that container inspection would expose.
const (
	EnvListen        = "CONTENT_BRIDGE_LISTEN"
	EnvHomelabID     = "CONTENT_BRIDGE_HOMELAB_ID"
	EnvImmichURL     = "CONTENT_BRIDGE_IMMICH_URL"
	EnvImmichKeyFile = "CONTENT_BRIDGE_IMMICH_API_KEY_FILE"
	EnvCacheTTL      = "CONTENT_BRIDGE_CACHE_TTL"

	DefaultListen        = ":8083"
	DefaultImmichURL     = "http://immich-server:2283"
	DefaultImmichKeyFile = "/run/secrets/immich-api-key"
)

// maxKeyFileBytes bounds the credential file. Immich keys are far shorter.
const maxKeyFileBytes = 4096

// Config is the validated environment of the bridge.
type Config struct {
	Listen        string
	HomelabID     string
	ImmichURL     string
	ImmichKeyFile string
	CacheTTL      time.Duration
}

// ConfigFromEnv reads the bridge configuration through getenv.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	value := func(name, fallback string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return fallback
	}
	cfg := Config{
		Listen:        value(EnvListen, DefaultListen),
		HomelabID:     value(EnvHomelabID, ""),
		ImmichURL:     value(EnvImmichURL, DefaultImmichURL),
		ImmichKeyFile: value(EnvImmichKeyFile, DefaultImmichKeyFile),
		CacheTTL:      MaxCacheTTL,
	}
	if !homelabIDPattern.MatchString(cfg.HomelabID) {
		return Config{}, fmt.Errorf("%s must be set to the installation identifier", EnvHomelabID)
	}
	if raw := strings.TrimSpace(getenv(EnvCacheTTL)); raw != "" {
		ttl, err := time.ParseDuration(raw)
		if err != nil || ttl <= 0 || ttl > MaxCacheTTL {
			return Config{}, fmt.Errorf("%s must be a duration above zero and at most %s", EnvCacheTTL, MaxCacheTTL)
		}
		cfg.CacheTTL = ttl
	}
	return cfg, nil
}

// FileKey returns a KeyFunc that reads the credential file on each call. A
// missing or empty file means no credential was issued yet.
func FileKey(path string) KeyFunc {
	return func() (string, error) {
		file, err := os.Open(path) //nolint:gosec // fixed custody path from the container configuration
		if err != nil {
			return "", ErrNotConfigured
		}
		defer func() { _ = file.Close() }()
		raw, err := io.ReadAll(io.LimitReader(file, maxKeyFileBytes+1))
		key := strings.TrimSpace(string(raw))
		if err != nil || key == "" || len(raw) > maxKeyFileBytes {
			return "", ErrNotConfigured
		}
		return key, nil
	}
}

// NewServiceFromConfig builds the service with the Immich adapter.
func NewServiceFromConfig(cfg Config, logger *slog.Logger) (*Service, error) {
	client, err := NewReadOnlyClient(cfg.ImmichURL, ImmichEndpoints(), FileKey(cfg.ImmichKeyFile), nil)
	if err != nil {
		return nil, err
	}
	return NewService(ServiceConfig{
		HomelabID: cfg.HomelabID,
		Adapters:  []Adapter{NewImmichAdapter(client, nil)},
		CacheTTL:  cfg.CacheTTL,
		Logger:    logger,
	}), nil
}
