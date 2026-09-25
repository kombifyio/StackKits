package platformdeploy

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const ownerPlatformConfigPath = ".stackkit/platform.json"

// ownerKomodoConfig is the minimal persisted shape needed to select the
// existing Komodo API authority. Credential values are never included in errors.
type ownerKomodoConfig struct {
	Platform          string            `json:"platform,omitempty"`
	Endpoint          string            `json:"endpoint,omitempty"`
	BaseURL           string            `json:"baseUrl,omitempty"`
	APIKey            string            `json:"apiKey,omitempty"`
	APISecret         string            `json:"apiSecret,omitempty"`
	BootstrapEvidence BootstrapEvidence `json:"bootstrapEvidence,omitempty"`
}

// LoadOwnerKomodoConfig reads the exact owner-custodied platform configuration
// through a held workspace root. It is intended for mutation boundaries that
// must not fall back to environment or generated configuration.
func LoadOwnerKomodoConfig(workspace string) (HTTPConfig, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return HTTPConfig{}, fmt.Errorf("open owner workspace: %w", err)
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return HTTPConfig{}, fmt.Errorf("open owner workspace view: %w", err)
	}
	file, err := view.Open(ownerPlatformConfigPath)
	if err != nil {
		return HTTPConfig{}, fmt.Errorf("open owner platform config: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return HTTPConfig{}, fmt.Errorf("owner platform config is not private")
	}
	payload, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return HTTPConfig{}, fmt.Errorf("read owner platform config: %w", err)
	}
	if len(payload) > 1<<20 {
		return HTTPConfig{}, fmt.Errorf("owner platform config exceeds size limit")
	}
	var cfg ownerKomodoConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return HTTPConfig{}, fmt.Errorf("decode owner platform config: %w", err)
	}
	cfg.Platform = strings.ToLower(strings.TrimSpace(cfg.Platform))
	if cfg.Platform != "komodo" {
		return HTTPConfig{}, fmt.Errorf("owner platform config selects %q, want %q", cfg.Platform, "komodo")
	}
	if err := ValidateBootstrapEvidence("komodo", cfg.BootstrapEvidence); err != nil {
		return HTTPConfig{}, err
	}
	return HTTPConfig{BaseURL: firstNonEmpty(cfg.Endpoint, cfg.BaseURL), APIKey: cfg.APIKey, Secret: cfg.APISecret}, nil
}
