package deploymentcontract

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// BundleManifest is the generated handoff to deployment and setup consumers.
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

func (m *SystemAppManifest) UnmarshalJSON(data []byte) error {
	var role struct {
		Role string `json:"role,omitempty"`
	}
	if err := json.Unmarshal(data, &role); err != nil {
		return err
	}
	var app AppManifest
	if err := json.Unmarshal(data, &app); err != nil {
		return err
	}
	m.AppManifest = app
	m.Role = role.Role
	return nil
}

// AppManifest describes one generated compose bundle for PaaS delivery or
// customer-app handoff.
type AppManifest struct {
	ServiceKey  string `json:"serviceKey,omitempty"`
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty"`
	Ownership   string `json:"ownership,omitempty"`
	Platform    string `json:"platform"`
	ManagedBy   string `json:"managedBy"`
	Image       string `json:"image,omitempty"`
	Port        int    `json:"port,omitempty"`
	Host        string `json:"host,omitempty"`
	URL         string `json:"url,omitempty"`
	RouteURL    string `json:"routeUrl,omitempty"`
	Auth        string `json:"auth,omitempty"`
	HealthPath  string `json:"healthPath,omitempty"`
	ComposePath string `json:"composePath"`
	ComposeYAML string `json:"composeYAML,omitempty"`
	// HostDirectory selects Komodo's files-on-host mode for an already
	// prepared, owner-private project. ComposePath is relative to this directory;
	// the API mutation contains no environment or secret-file contents.
	HostDirectory   string              `json:"hostDirectory,omitempty"`
	IgnoredServices []string            `json:"ignoredServices,omitempty"`
	Env             map[string]string   `json:"env,omitempty"`
	Secrets         map[string]string   `json:"secrets,omitempty"`
	SetupPolicy     SetupPolicy         `json:"setupPolicy,omitempty"`
	SetupDrops      []SetupDropManifest `json:"setupDrops,omitempty"`
}

// UnmarshalJSON accepts Terraform jsonencode output where object type coercion
// can stringify numeric ports inside mixed concat(...) app manifest lists.
func (m *AppManifest) UnmarshalJSON(data []byte) error {
	type appManifestAlias AppManifest

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	portRaw := fields["port"]
	delete(fields, "port")

	withoutPort, err := json.Marshal(fields)
	if err != nil {
		return err
	}

	var alias appManifestAlias
	if err := json.Unmarshal(withoutPort, &alias); err != nil {
		return err
	}

	*m = AppManifest(alias)
	if len(portRaw) == 0 || string(portRaw) == "null" {
		return nil
	}
	port, err := decodeManifestPort(portRaw)
	if err != nil {
		return err
	}
	m.Port = port
	return nil
}

func decodeManifestPort(raw json.RawMessage) (int, error) {
	var port int
	if err := json.Unmarshal(raw, &port); err == nil {
		return port, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, fmt.Errorf("decode app manifest port: %w", err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("decode app manifest port %q: %w", text, err)
	}
	return port, nil
}

type SetupPolicy string

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
	SetupPolicyManual SetupPolicy = "manual"
	// SetupPolicyOnDemand allows StackKit to run setup drops only after an explicit request.
	SetupPolicyOnDemand SetupPolicy = "on_demand"
	// SetupPolicyAutomatic allows StackKit to run setup drops during rollout.
	SetupPolicyAutomatic SetupPolicy = "automatic"
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
	ComposeYAML    string    `json:"-" yaml:"-"`
	ComposePath    string    `json:"-" yaml:"-"`
}
