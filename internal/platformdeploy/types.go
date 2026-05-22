// Package platformdeploy contains the StackKit boundary for PaaS delivery.
// OpenTofu owns platform installation; StackKit-owned/default L3 applications
// are PaaS-intended, while customer-installed applications outside the manifest
// are state-unmanaged by StackKit.
package platformdeploy

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// BundleManifest is the generated handoff from StackKit generation to the
// selected PaaS.
type BundleManifest struct {
	Version    string              `json:"version"`
	Platform   string              `json:"platform"`
	Fallback   FallbackManifest    `json:"fallback,omitempty"`
	SystemApps []SystemAppManifest `json:"systemApps,omitempty"`
	Apps       []AppManifest       `json:"apps"`
}

// FallbackManifest records whether the generated bundle is intentionally
// targeting the standalone Compose fallback instead of a PaaS API.
type FallbackManifest struct {
	Enabled bool   `json:"enabled,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

// SystemAppManifest describes a StackKit-owned control-plane application that
// StackKit may deploy through the selected platform adapter.
type SystemAppManifest struct {
	AppManifest
	Role string `json:"role,omitempty"`
}

// AppManifest describes one generated compose bundle for PaaS delivery or
// customer-app handoff.
type AppManifest struct {
	Name        string              `json:"name"`
	Kind        string              `json:"kind,omitempty"`
	Ownership   string              `json:"ownership,omitempty"`
	Platform    string              `json:"platform"`
	ManagedBy   string              `json:"managedBy"`
	Image       string              `json:"image,omitempty"`
	Port        int                 `json:"port,omitempty"`
	Host        string              `json:"host,omitempty"`
	URL         string              `json:"url,omitempty"`
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
	// AppManagementManaged means StackKit observed a PaaS deployment identity.
	AppManagementManaged = "managed"
	// AppManagementHandoff means StackKit recorded customer app handoff metadata only.
	AppManagementHandoff = "handoff"
	// AppManagementUnmanaged means StackKit has manifest metadata but no PaaS identity.
	AppManagementUnmanaged = "unmanaged"
	// AppManagementFallback means StackKit deployed the app through the explicit
	// standalone Compose fallback instead of a PaaS adapter.
	AppManagementFallback = "fallback"

	// AppOwnershipStackKit means StackKit owns the app lifecycle and must
	// prefer delivery through the selected PaaS adapter when configured.
	AppOwnershipStackKit = "stackkit"
	// AppOwnershipCustomer means StackKit records handoff metadata only; the
	// selected PaaS/Admin product owns deployment and lifecycle.
	AppOwnershipCustomer = "customer"

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
	APIKey  string
	Secret  string
	Client  *http.Client

	// Optional platform-specific placement values. Dokploy needs EnvironmentID
	// to create compose apps; Coolify usually needs project/server/environment or
	// destination identifiers. Komodo uses ServerID to attach compose stacks.
	EnvironmentID          string
	ServerID               string
	ProjectUUID            string
	EnvironmentUUID        string
	DestinationUUID        string
	LegacyDockerComposeAPI bool
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
