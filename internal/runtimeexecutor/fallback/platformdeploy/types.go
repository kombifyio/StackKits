package platformdeploy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/kombifyio/stackkits/internal/deploymentcontract"
)

type BundleManifest = deploymentcontract.BundleManifest
type FallbackManifest = deploymentcontract.FallbackManifest
type BootstrapManifest = deploymentcontract.BootstrapManifest
type DemoDataManifest = deploymentcontract.DemoDataManifest
type SetupPoliciesManifest = deploymentcontract.SetupPoliciesManifest
type SystemAppManifest = deploymentcontract.SystemAppManifest
type AppManifest = deploymentcontract.AppManifest
type SetupPolicy = deploymentcontract.SetupPolicy
type SetupDropManifest = deploymentcontract.SetupDropManifest
type DeploymentRef = deploymentcontract.DeploymentRef

const (
	AppManagementManaged   = deploymentcontract.AppManagementManaged
	AppManagementHandoff   = deploymentcontract.AppManagementHandoff
	AppManagementUnmanaged = deploymentcontract.AppManagementUnmanaged
	AppManagementFallback  = deploymentcontract.AppManagementFallback
	AppOwnershipStackKit   = deploymentcontract.AppOwnershipStackKit
	AppOwnershipCustomer   = deploymentcontract.AppOwnershipCustomer
	SetupPolicyManual      = deploymentcontract.SetupPolicyManual
	SetupPolicyOnDemand    = deploymentcontract.SetupPolicyOnDemand
	SetupPolicyAutomatic   = deploymentcontract.SetupPolicyAutomatic
)

// ComponentFailure records one application-scoped rollout failure. A failure
// with IdentityCommitted=false may be retried through another registered
// adapter without risking two platform-owned deployment identities.
type ComponentFailure struct {
	AppName           string `json:"appName" yaml:"appName"`
	Platform          string `json:"platform" yaml:"platform"`
	Stage             string `json:"stage" yaml:"stage"`
	Message           string `json:"message" yaml:"message"`
	IdentityCommitted bool   `json:"identityCommitted" yaml:"identityCommitted"`
	Retryable         bool   `json:"retryable" yaml:"retryable"`
}

// DeploymentIdentityUncertainError means a platform mutation may already own
// the workload. Reconcile it before retrying or selecting another manager.
type DeploymentIdentityUncertainError struct {
	Err error
}

func (e *DeploymentIdentityUncertainError) Error() string { return e.Err.Error() }
func (e *DeploymentIdentityUncertainError) Unwrap() error { return e.Err }

// BundleResult preserves every successful deployment reference alongside
// application-scoped failures. Callers can finish the independent graph,
// persist a degraded state, and retry only components that still need work.
type BundleResult struct {
	Refs     []DeploymentRef    `json:"refs,omitempty" yaml:"refs,omitempty"`
	Failures []ComponentFailure `json:"failures,omitempty" yaml:"failures,omitempty"`
}

func (r BundleResult) Degraded() bool { return len(r.Failures) > 0 }

// DegradedError is a terminal, usable rollout result rather than a lost
// deployment. It deliberately carries component failures without discarding
// successful references.
type DegradedError struct {
	Failures []ComponentFailure
}

func (e *DegradedError) Error() string {
	if e == nil || len(e.Failures) == 0 {
		return "rollout completed with degraded components"
	}
	return fmt.Sprintf("rollout completed with %d degraded component(s); first: %s: %s", len(e.Failures), e.Failures[0].AppName, e.Failures[0].Message)
}

func IsDegraded(err error) bool {
	var degraded *DegradedError
	return errors.As(err, &degraded)
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
	ReasonCode string              `json:"reasonCode,omitempty"`
	Guidance   string              `json:"guidance,omitempty"`
}

func RequiresBootstrapEvidence(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "coolify", "komodo":
		return true
	default:
		return false
	}
}

func ValidateBootstrapEvidence(platform string, evidence BootstrapEvidence) error {
	provider := strings.ToLower(strings.TrimSpace(evidence.Provider))
	wantProvider := strings.ToLower(strings.TrimSpace(platform))
	if provider == "" {
		return fmt.Errorf("%s platform bootstrap evidence is required in .stackkit/platform.json", wantProvider)
	}
	if provider != wantProvider {
		return fmt.Errorf("%s platform bootstrap evidence provider = %q, want %q", wantProvider, evidence.Provider, wantProvider)
	}
	if strings.TrimSpace(evidence.Mode) == "" {
		return fmt.Errorf("%s platform bootstrap evidence mode is required", wantProvider)
	}
	byCapability := map[BootstrapCapability]BootstrapCapabilityEvidence{}
	for _, capability := range evidence.Capabilities {
		byCapability[capability.Capability] = capability
	}
	for _, want := range []BootstrapCapability{
		BootstrapCapabilityAPIAccess,
		BootstrapCapabilityTeamManagement,
		BootstrapCapabilityProxyRouting,
		BootstrapCapabilitySecrets,
		BootstrapCapabilityBackups,
		BootstrapCapabilityHealthchecks,
		BootstrapCapabilityServiceHandoff,
	} {
		capability, ok := byCapability[want]
		if !ok {
			return fmt.Errorf("%s platform bootstrap evidence missing capability %q", wantProvider, want)
		}
		// Komodo deployment is admitted with an explicit unsupported data
		// operation. The workload lifecycle capability still refuses backup and
		// restore before execution; a mounted volume is never backup evidence.
		if wantProvider == "komodo" && want == BootstrapCapabilityBackups &&
			capability.Status == "unavailable" && capability.ReasonCode == "komodo_backup_restore_unavailable" &&
			strings.TrimSpace(capability.Guidance) != "" {
			continue
		}
		if strings.ToLower(strings.TrimSpace(capability.Status)) != "configured" {
			return fmt.Errorf("%s platform bootstrap evidence capability %q status = %q, want configured", wantProvider, want, capability.Status)
		}
		joinedEvidence := strings.Join(capability.Evidence, "\n")
		for _, marker := range requiredBootstrapEvidenceMarkers(wantProvider, want) {
			if !strings.Contains(joinedEvidence, marker) {
				return fmt.Errorf("%s platform bootstrap evidence capability %q evidence missing %q", wantProvider, want, marker)
			}
		}
	}
	return nil
}

func requiredBootstrapEvidenceMarkers(provider string, capability BootstrapCapability) []string {
	switch provider {
	case "coolify":
		switch capability {
		case BootstrapCapabilityAPIAccess:
			return []string{"instance_settings.is_api_enabled=true", "token.scope=root"}
		case BootstrapCapabilityTeamManagement:
			return []string{"root.user.email=", "team.show_boarding=false"}
		case BootstrapCapabilityProxyRouting:
			return []string{"coolify-proxy"}
		case BootstrapCapabilitySecrets:
			return []string{"platformConfig.mode=0600"}
		case BootstrapCapabilityBackups:
			return []string{"stackkit.backup=required", "restore-drill endpoint="}
		case BootstrapCapabilityHealthchecks:
			return []string{"server_settings.is_usable=true"}
		case BootstrapCapabilityServiceHandoff:
			return []string{"project=StackKit"}
		}
	case "komodo":
		switch capability {
		case BootstrapCapabilityAPIAccess:
			return []string{"api key created"}
		case BootstrapCapabilityTeamManagement:
			return []string{"registration disabled", "non-admin resource creation disabled"}
		case BootstrapCapabilityProxyRouting:
			return []string{"StackKit Traefik"}
		case BootstrapCapabilitySecrets:
			return []string{"platformConfig.mode=0600"}
		case BootstrapCapabilityBackups:
			return []string{"stackkit.backup=required", "restore-drill endpoint="}
		case BootstrapCapabilityHealthchecks:
			return []string{"Core healthcheck"}
		case BootstrapCapabilityServiceHandoff:
			return []string{"serverId=stackkit-local"}
		}
	}
	return nil
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
	DockerEnv                   []string
	WaitForReadiness            bool
}

// ReadinessChecker is implemented by adapters whose API can be installed but
// not yet ready immediately after platform bootstrap.
type ReadinessChecker interface {
	WaitReady(ctx context.Context) error
}

// SupplementalNodeTarget describes an additional node that should be attached
// to the selected platform before StackKit-owned apps are deployed.
type SupplementalNodeTarget struct {
	Name      string
	Role      string
	IP        string
	Host      string
	Services  []string
	Platform  NodePlatformTarget
	Bootstrap *NodeBootstrap
}

type NodePlatformTarget struct {
	ServerID        string
	DestinationUUID string
	EnvironmentID   string
	ProjectUUID     string
	EnvironmentUUID string
}

type NodeBootstrap struct {
	KomodoCoreAddress   string
	KomodoOnboardingKey string
	SSH                 *SSHBootstrap
}

type SSHBootstrap struct {
	Host             string
	User             string
	Port             int
	KeyPath          string
	KeyPEM           string
	PrivateKey       string
	ClientPrivateKey string
	ProxyJump        string
	// HostKey is the verified OpenSSH public key of Host. It is required: the
	// runner never connects to a node it cannot verify.
	HostKey string
}

type NodePrepareResult struct {
	NodeName        string
	Role            string
	Platform        string
	Status          string
	Detail          string
	Services        []string
	ServerID        string
	DestinationUUID string
	EnvironmentID   string
	ProjectUUID     string
	EnvironmentUUID string
	PrivateKeyUUID  string
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
