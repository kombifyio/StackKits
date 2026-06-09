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
	Bootstrap  BootstrapManifest   `json:"bootstrap,omitempty"`
	SystemApps []SystemAppManifest `json:"systemApps,omitempty"`
	Apps       []AppManifest       `json:"apps"`
}

// FallbackManifest records whether the generated bundle is intentionally
// targeting the standalone Compose fallback instead of a PaaS API.
type FallbackManifest struct {
	Enabled bool   `json:"enabled,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

// BootstrapManifest records the generated first-run setup contract for the
// selected platform and StackKit-owned default apps.
type BootstrapManifest struct {
	Mode          string                `json:"mode,omitempty"`
	DemoData      DemoDataManifest      `json:"demoData,omitempty"`
	SetupPolicies SetupPoliciesManifest `json:"setupPolicies,omitempty"`
}

type DemoDataManifest struct {
	Enabled bool `json:"enabled,omitempty"`
}

type SetupPoliciesManifest struct {
	Platform           string `json:"platform,omitempty"`
	ApplicationDefault string `json:"applicationDefault,omitempty"`
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
	ServiceKey  string              `json:"serviceKey,omitempty"`
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
	Name          string            `json:"name"`
	Version       string            `json:"version,omitempty"`
	Runner        string            `json:"runner,omitempty"`
	Description   string            `json:"description,omitempty"`
	RollbackNotes []string          `json:"rollbackNotes,omitempty"`
	Command       []string          `json:"command,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Secrets       map[string]string `json:"secrets,omitempty"`
}

// DeploymentRef records the external platform identity for a StackKit app.
type DeploymentRef struct {
	Platform       string    `json:"platform" yaml:"platform"`
	AppName        string    `json:"appName" yaml:"appName"`
	ExternalID     string    `json:"externalId" yaml:"externalId"`
	DeploymentID   string    `json:"deploymentId,omitempty" yaml:"deploymentId,omitempty"`
	ObservedStatus string    `json:"observedStatus,omitempty" yaml:"observedStatus,omitempty"`
	ObservedAt     time.Time `json:"observedAt,omitempty" yaml:"observedAt,omitempty"`
	LastDeployed   time.Time `json:"lastDeployed,omitempty" yaml:"lastDeployed,omitempty"`
	ServiceNames   []string  `json:"-" yaml:"-"`
}

// Adapter is implemented by concrete platform API clients.
type Adapter interface {
	ApplyCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error)
}

// DeploymentObserver is implemented by adapters that can verify platform-side
// start state after the deploy API accepted a compose bundle.
type DeploymentObserver interface {
	ObserveDeployment(ctx context.Context, ref DeploymentRef) (DeploymentRef, error)
}

// DeploymentBatchObserver is implemented by adapters that can observe several
// started deployments under one shared time budget.
type DeploymentBatchObserver interface {
	ObserveDeployments(ctx context.Context, refs []DeploymentRef) ([]DeploymentRef, error)
}

type BootstrapCapability string

const (
	BootstrapCapabilityProxyRouting   BootstrapCapability = "proxy-routing"
	BootstrapCapabilityAPIAccess      BootstrapCapability = "api-access"
	BootstrapCapabilityTeamManagement BootstrapCapability = "team-management"
	BootstrapCapabilityBackups        BootstrapCapability = "backups"
	BootstrapCapabilitySecrets        BootstrapCapability = "secrets"
	BootstrapCapabilityHealthchecks   BootstrapCapability = "healthchecks"
	BootstrapCapabilityServiceHandoff BootstrapCapability = "service-handoff"
)

// BootstrapCapabilityProvider declares the platform-specific first-run areas
// the adapter must harden before it can be treated as beta-supported.
type BootstrapCapabilityProvider interface {
	BootstrapProviderName() string
	BootstrapCapabilities() []BootstrapCapability
}

// BootstrapEvidence is written by generated platform bootstraps to make the
// selected PaaS setup auditable by release gates and scenario evidence.
type BootstrapEvidence struct {
	Provider     string                        `json:"provider,omitempty"`
	Mode         string                        `json:"mode,omitempty"`
	Capabilities []BootstrapCapabilityEvidence `json:"capabilities,omitempty"`
}

type BootstrapCapabilityEvidence struct {
	Capability BootstrapCapability `json:"capability"`
	Status     string              `json:"status"`
	Evidence   []string            `json:"evidence,omitempty"`
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
	EnvironmentID               string
	ServerID                    string
	ProjectUUID                 string
	EnvironmentUUID             string
	DestinationUUID             string
	LegacyDockerComposeAPI      bool
	DisableDockerRuntimeObserve bool
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
