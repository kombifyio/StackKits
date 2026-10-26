package platformdeploy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// DokployBootstrapConfig is the existing qpb.6 bootstrap's private output.
// It carries no invented server identifier: empty ServerID means the local
// Dokploy host, as in the upstream API and provider.
type DokployBootstrapConfig struct {
	Platform      string `json:"platform"`
	Endpoint      string `json:"endpoint,omitempty"`
	BaseURL       string `json:"baseUrl,omitempty"`
	Token         string `json:"token,omitempty"`
	APIKey        string `json:"apiKey,omitempty"`
	ProjectID     string `json:"projectId"`
	EnvironmentID string `json:"environmentId"`
	ServerID      string `json:"serverId,omitempty"`
}

func (c DokployBootstrapConfig) HTTPConfig() (HTTPConfig, error) {
	if c.Platform != "dokploy" || !dokployIDPattern.MatchString(c.ProjectID) || !dokployIDPattern.MatchString(c.EnvironmentID) ||
		(c.ServerID != "" && !dokployIDPattern.MatchString(c.ServerID)) {
		return HTTPConfig{}, errors.New("Dokploy bootstrap requires exact platform, project, environment and optional server identities")
	}
	if c.Token != "" && c.APIKey != "" && c.Token != c.APIKey {
		return HTTPConfig{}, errors.New("Dokploy bootstrap token and apiKey disagree; preserve custody and repair the source")
	}
	token := firstNonEmpty(c.Token, c.APIKey)
	if token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n") {
		return HTTPConfig{}, errors.New("Dokploy bootstrap requires its retained API key")
	}
	if c.Endpoint != "" && c.BaseURL != "" && c.Endpoint != c.BaseURL {
		return HTTPConfig{}, errors.New("Dokploy bootstrap endpoint and baseUrl disagree")
	}
	endpoint := firstNonEmpty(c.Endpoint, c.BaseURL)
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || u.Opaque != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return HTTPConfig{}, errors.New("Dokploy bootstrap endpoint requires HTTPS or a literal loopback HTTP address, without credentials or a path")
	}
	return HTTPConfig{BaseURL: strings.TrimSuffix(endpoint, "/"), Token: token, EnvironmentID: c.EnvironmentID, ServerID: c.ServerID,
		Client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// LoadOwnerDokployBootstrapConfig reads only the legacy bootstrap authority,
// never environment variables, a supplied key, a generated file or another
// platform's configuration. Reading it does not declare bootstrap readiness.
func LoadOwnerDokployBootstrapConfig(workspace string) (DokployBootstrapConfig, error) {
	var cfg DokployBootstrapConfig
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return cfg, err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return cfg, err
	}
	file, err := view.Open(ownerPlatformConfigPath)
	if err != nil {
		return cfg, errors.New("Dokploy requires the existing owner bootstrap at .stackkit/platform.json; install/bootstrap the selected platform first")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return cfg, errors.New("Dokploy bootstrap configuration must be an owner-private regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return cfg, errors.New("Dokploy bootstrap configuration exceeds its bounded schema")
	}
	defer clear(raw)
	if json.Unmarshal(raw, &cfg) != nil {
		return cfg, errors.New("Dokploy bootstrap configuration is invalid")
	}
	_, err = cfg.HTTPConfig()
	return cfg, err
}
