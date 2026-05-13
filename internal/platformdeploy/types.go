// Package platformdeploy contains the StackKit boundary for platform-managed
// application rollouts. OpenTofu owns platform installation; this package owns
// application registration and deploy operations against that platform.
package platformdeploy

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// BundleManifest is the generated handoff from StackKit generation to the
// platform deployment adapter.
type BundleManifest struct {
	Version    string              `json:"version"`
	Platform   string              `json:"platform"`
	SystemApps []SystemAppManifest `json:"systemApps,omitempty"`
	Apps       []AppManifest       `json:"apps"`
}

// SystemAppManifest describes a StackKit-owned control-plane application that
// is deployed through the same platform adapter as user applications.
type SystemAppManifest struct {
	AppManifest
	Role string `json:"role,omitempty"`
}

// AppManifest describes one generated compose bundle that must be registered
// with a platform adapter instead of run directly with Docker.
type AppManifest struct {
	Name        string              `json:"name"`
	Kind        string              `json:"kind,omitempty"`
	Platform    string              `json:"platform"`
	ManagedBy   string              `json:"managedBy"`
	Image       string              `json:"image,omitempty"`
	Port        int                 `json:"port,omitempty"`
	Host        string              `json:"host,omitempty"`
	Auth        string              `json:"auth,omitempty"`
	HealthPath  string              `json:"healthPath,omitempty"`
	ComposePath string              `json:"composePath"`
	ComposeYAML string              `json:"composeYAML,omitempty"`
	Env         map[string]string   `json:"env,omitempty"`
	Secrets     map[string]string   `json:"secrets,omitempty"`
	SetupPolicy string              `json:"setupPolicy,omitempty"`
	SetupDrops  []SetupDropManifest `json:"setupDrops,omitempty"`
}

const (
	// SetupPolicyManual keeps first-run setup in the application UI.
	SetupPolicyManual = "manual"
	// SetupPolicyOnDemand allows StackKit to run setup drops only after an explicit request.
	SetupPolicyOnDemand = "on_demand"
	// SetupPolicyAutomatic allows StackKit to run setup drops during rollout.
	SetupPolicyAutomatic = "automatic"
)

// SetupDropManifest describes an initial-configuration unit that can be run
// separately from the application deployment.
type SetupDropManifest struct {
	Name        string            `json:"name"`
	Version     string            `json:"version,omitempty"`
	Runner      string            `json:"runner,omitempty"`
	Description string            `json:"description,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Secrets     map[string]string `json:"secrets,omitempty"`
}

// DeploymentRef records the external platform identity for a StackKit app.
type DeploymentRef struct {
	Platform     string    `json:"platform" yaml:"platform"`
	AppName      string    `json:"appName" yaml:"appName"`
	ExternalID   string    `json:"externalId" yaml:"externalId"`
	DeploymentID string    `json:"deploymentId,omitempty" yaml:"deploymentId,omitempty"`
	LastDeployed time.Time `json:"lastDeployed,omitempty" yaml:"lastDeployed,omitempty"`
}

// Adapter is implemented by concrete platform API clients.
type Adapter interface {
	ApplyCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error)
}

// HTTPConfig configures HTTP-backed platform adapters.
type HTTPConfig struct {
	BaseURL string
	Token   string
	Client  *http.Client

	// Optional platform-specific placement values. Dokploy needs EnvironmentID
	// to create compose apps; Coolify usually needs project/server/environment or
	// destination identifiers. Tests and dry runs can omit these.
	EnvironmentID   string
	ServerID        string
	ProjectUUID     string
	EnvironmentUUID string
	DestinationUUID string
}

func (cfg HTTPConfig) httpClient() *http.Client {
	if cfg.Client != nil {
		return cfg.Client
	}
	return http.DefaultClient
}

func (cfg HTTPConfig) endpoint(path string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("platform API base URL is required")
	}
	return base + path, nil
}
