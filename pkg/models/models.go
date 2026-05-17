// Package models defines the core data structures for StackKits.
package models

import (
	"strings"
	"time"
)

// StackKitMetadata represents the metadata section of a stackkit.yaml
type StackKitMetadata struct {
	APIVersion  string   `yaml:"apiVersion" json:"apiVersion"`
	Kind        string   `yaml:"kind" json:"kind"`
	Name        string   `yaml:"name" json:"name"`
	Version     string   `yaml:"version" json:"version"`
	DisplayName string   `yaml:"displayName" json:"displayName"`
	Description string   `yaml:"description" json:"description"`
	Author      string   `yaml:"author,omitempty" json:"author,omitempty"`
	License     string   `yaml:"license" json:"license"`
	Homepage    string   `yaml:"homepage,omitempty" json:"homepage,omitempty"`
	Repository  string   `yaml:"repository,omitempty" json:"repository,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// StackKit represents a complete stackkit.yaml file
type StackKit struct {
	Metadata     StackKitMetadata          `yaml:"metadata" json:"metadata"`
	SupportedOS  []string                  `yaml:"supportedOS" json:"supportedOS"`
	Requirements Requirements              `yaml:"requirements" json:"requirements"`
	Modes        Modes                     `yaml:"modes" json:"modes"`
	Application  map[string]ApplicationDef `yaml:"application,omitempty" json:"application,omitempty"`
	Platform     map[string]PlatformDef    `yaml:"platform,omitempty" json:"platform,omitempty"`
	Features     Features                  `yaml:"features,omitempty" json:"features,omitempty"`
}

// Requirements defines system requirements
type Requirements struct {
	Minimum     ResourceSpec `yaml:"minimum" json:"minimum"`
	Recommended ResourceSpec `yaml:"recommended" json:"recommended"`
}

// ResourceSpec defines resource specifications
type ResourceSpec struct {
	CPU  int `yaml:"cpu" json:"cpu"`
	RAM  int `yaml:"memory" json:"ram"` // in GB (yaml: "memory" to match stackkit.yaml)
	Disk int `yaml:"disk" json:"disk"`  // in GB
}

// Modes defines deployment modes
type Modes struct {
	Simple   ModeSpec `yaml:"simple" json:"simple"`
	Advanced ModeSpec `yaml:"advanced,omitempty" json:"advanced,omitempty"`
}

// ModeSpec defines a single deployment mode
type ModeSpec struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Engine      string `yaml:"engine" json:"engine"` // "opentofu" or "terramate"
	Default     bool   `yaml:"default,omitempty" json:"default,omitempty"`
}

const (
	ComputeTierLow      = "low"
	ComputeTierStandard = "standard"
	ComputeTierHigh     = "high"

	RuntimeNative = "native"

	StorageOverlay2    = "overlay2"
	StorageVFS         = "vfs"
	StorageFuseOverlay = "fuse-overlayfs"

	VirtNone   = "none"
	VirtKVM    = "kvm"
	VirtLXC    = "lxc"
	VirtOpenVZ = "openvz"

	DNSFixNone = "none"

	// PAAS platform types
	PAASDokploy = "dokploy"
	PAASCoolify = "coolify"
	PAASDockge  = "dockge"
	PAASNone    = "none"

	// Reverse proxy backend — determines which Traefik instance routes platform services.
	// User app rollouts are owned by the selected PaaS; StackKit only records
	// route and manifest handoff metadata for those apps.
	ReverseProxyStandalone = "standalone"
	ReverseProxyDokploy    = "dokploy"
	ReverseProxyCoolify    = "coolify"

	// Domain constants
	DomainKombifyMe = "kombify.me"
	DomainHomelab   = "homelab"
	// DomainHomeLab keeps the historical constant name for compatibility. The
	// default local deployment domain uses the browser-reserved .localhost
	// namespace, so generated links resolve without hosts-file edits, LAN DNS,
	// random host ports, or manual workstation setup.
	DomainHomeLab           = "home.localhost"
	DomainHomeLocalhost     = "home.localhost"
	DomainStackHome         = "stack.home"
	DomainHomeKombifyLegacy = "home.kombify"
	DomainHomeLabLegacy     = "home.lab"
	DomainHomeDNS           = "home"

	OwnerBootstrapModeAuto   = "auto"
	OwnerBootstrapModeCustom = "custom"
	OwnerBootstrapModeNone   = "none"

	OwnerSourceLocal = "local"
	OwnerSourceCloud = "cloud"
)

// IsKombifyMeDomain returns true for the managed kombify.me shared domain.
func IsKombifyMeDomain(domain string) bool {
	return strings.EqualFold(domain, DomainKombifyMe)
}

// NeedsSyntheticAdminEmail reports whether the configured admin identity is only
// a placeholder and must be converted into a usable email-shaped login.
func NeedsSyntheticAdminEmail(email string) bool {
	email = strings.TrimSpace(email)
	return email == "" || strings.EqualFold(email, "admin")
}

// SyntheticAdminEmail returns the generated admin email for a deployment.
func SyntheticAdminEmail(domain, subdomainPrefix string) string {
	return "admin@" + adminEmailHost(domain, subdomainPrefix)
}

// NormalizeAdminEmail converts empty or bare admin identities into a usable
// email-shaped login. Bare usernames are scoped to the deployment host.
func NormalizeAdminEmail(email, domain, subdomainPrefix string) string {
	email = strings.TrimSpace(email)
	if NeedsSyntheticAdminEmail(email) {
		return SyntheticAdminEmail(domain, subdomainPrefix)
	}
	if strings.Contains(email, "@") {
		return email
	}
	return email + "@" + adminEmailHost(domain, subdomainPrefix)
}

// DefaultAppHost returns the route host for a StackSpec app when route.host is
// omitted. kombify.me app routes use the flat registry service naming shape.
func DefaultAppHost(domain, subdomainPrefix, appName string) string {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain = DomainHomeLab
	}
	prefix := strings.Trim(strings.TrimSpace(subdomainPrefix), ".")
	if IsKombifyMeDomain(domain) && prefix != "" {
		return prefix + "-" + appName + "." + domain
	}
	return appName + "." + domain
}

func adminEmailHost(domain, subdomainPrefix string) string {
	domain = strings.ToLower(strings.Trim(strings.TrimSpace(domain), "."))
	subdomainPrefix = strings.ToLower(strings.Trim(strings.TrimSpace(subdomainPrefix), "."))

	switch domain {
	case "", DomainHomelab, DomainHomeDNS, DomainHomeKombifyLegacy, DomainHomeLabLegacy, "localhost", "stack.local":
		domain = DomainHomeLab
	}

	if IsKombifyMeDomain(domain) && subdomainPrefix != "" {
		return subdomainPrefix + "." + DomainKombifyMe
	}
	return domain
}

// IsLocalDomain returns true for local-only or non-routable domains.
func IsLocalDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" ||
		domain == DomainHomelab ||
		domain == DomainHomeLab ||
		domain == DomainStackHome ||
		domain == DomainHomeKombifyLegacy ||
		domain == DomainHomeLabLegacy ||
		domain == DomainHomeLocalhost ||
		domain == DomainHomeDNS ||
		domain == "stack.local" ||
		domain == "localhost" {
		return true
	}

	localSuffixes := []string{".kombify", ".internal", ".local", ".lab", ".lan", ".home", ".homebase", ".localhost"}
	for _, suffix := range localSuffixes {
		if strings.HasSuffix(domain, suffix) {
			return true
		}
	}

	return false
}

// IsLocalhostDomain returns true for the browser-reserved localhost namespace.
func IsLocalhostDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	return domain == "localhost" || strings.HasSuffix(domain, ".localhost")
}

// RequiresKombifyPoint returns true when local service names need our LAN DNS resolver.
func RequiresKombifyPoint(domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" || IsKombifyMeDomain(domain) || IsLocalhostDomain(domain) {
		return false
	}
	return IsLocalDomain(domain)
}

// LocalDNSDomain returns the domain suffix for Kombify Point local DNS mode.
func LocalDNSDomain(localName string) string {
	localName = strings.Trim(strings.ToLower(strings.TrimSpace(localName)), ".")
	if localName == "" {
		return DomainStackHome
	}
	if strings.HasSuffix(localName, "."+DomainHomeDNS) {
		return localName
	}
	return localName + "." + DomainHomeDNS
}

// HasCustomPublicDomain returns true when the stack uses a routable, non-kombify domain.
func (s *StackSpec) HasCustomPublicDomain() bool {
	return s.Domain != "" && !IsLocalDomain(s.Domain) && !IsKombifyMeDomain(s.Domain)
}

// ToolRole represents the role of a tool within a StackKit (v5).
type ToolRole string

const (
	RoleDefault     ToolRole = "default"
	RoleAlternative ToolRole = "alternative"
	RoleOptional    ToolRole = "optional"
	RoleAddon       ToolRole = "addon"
)

// ApplicationDef defines an application-layer (L3) service slot in stackkit.yaml.
// Pre-2026-04 named UseCaseDef under `useCases:` — renamed in migration 000084
// to align with the canonical Foundation/Platform/Application layer standard
// (ADR-0012, ARCHITECTURE_V6 §4).
type ApplicationDef struct {
	Role         ToolRole `yaml:"role" json:"role"`
	DefaultTool  string   `yaml:"defaultTool,omitempty" json:"defaultTool,omitempty"`
	Alternatives []string `yaml:"alternatives,omitempty" json:"alternatives,omitempty"`
	Description  string   `yaml:"description,omitempty" json:"description,omitempty"`
}

// PlatformDef defines a platform service in stackkit.yaml (v5).
type PlatformDef struct {
	Role         ToolRole `yaml:"role" json:"role"`
	DefaultTool  string   `yaml:"defaultTool,omitempty" json:"defaultTool,omitempty"`
	Alternatives []string `yaml:"alternatives,omitempty" json:"alternatives,omitempty"`
}

// Features defines optional features
type Features struct {
	MultiNode    bool `yaml:"multiNode,omitempty" json:"multiNode,omitempty"`
	VPNOverlay   bool `yaml:"vpnOverlay,omitempty" json:"vpnOverlay,omitempty"`
	PublicAccess bool `yaml:"publicAccess,omitempty" json:"publicAccess,omitempty"`
}

// StackSpec represents the user's deployment specification (stack-spec.yaml)
type StackSpec struct {
	Name            string             `yaml:"name" json:"name"`
	StackKit        string             `yaml:"stackkit" json:"stackkit"`
	Mode            string             `yaml:"mode,omitempty" json:"mode,omitempty"`
	Runtime         string             `yaml:"runtime,omitempty" json:"runtime,omitempty"` // "docker" or "native"
	Context         string             `yaml:"context,omitempty" json:"context,omitempty"`
	Domain          string             `yaml:"domain,omitempty" json:"domain,omitempty"`
	SubdomainPrefix string             `yaml:"subdomainPrefix,omitempty" json:"subdomainPrefix,omitempty"`
	Email           string             `yaml:"email,omitempty" json:"email,omitempty"`
	AdminEmail      string             `yaml:"adminEmail,omitempty" json:"adminEmail,omitempty"`
	Network         NetworkSpec        `yaml:"network,omitempty" json:"network,omitempty"`
	Compute         ComputeSpec        `yaml:"compute,omitempty" json:"compute,omitempty"`
	Storage         StorageSpec        `yaml:"storage,omitempty" json:"storage,omitempty"`
	SSH             SSHSpec            `yaml:"ssh,omitempty" json:"ssh,omitempty"`
	Nodes           []NodeSpec         `yaml:"nodes,omitempty" json:"nodes,omitempty"`
	TLS             TLSSpec            `yaml:"tls,omitempty" json:"tls,omitempty"`
	PAAS            string             `yaml:"paas,omitempty" json:"paas,omitempty"` // "dokploy" or "coolify" for normal StackKits (auto-detected if empty)
	Addons          []string           `yaml:"addons,omitempty" json:"addons,omitempty"`
	Services        map[string]any     `yaml:"services,omitempty" json:"services,omitempty"`
	Apps            map[string]AppSpec `yaml:"apps,omitempty" json:"apps,omitempty"`
	Environment     map[string]string  `yaml:"environment,omitempty" json:"environment,omitempty"`
	Identity        *IdentitySpec      `yaml:"identity,omitempty" json:"identity,omitempty"`

	// Owner is set by `stackkit init` or orchestration handoff when owner
	// bootstrap is requested. Empty means owner provisioning is disabled. The
	// explicit bootstrapMode separates SaaS auto-owner, self-hosted custom
	// owner, and OSS/BYOS no-owner specs without inventing a fake human owner.
	Owner OwnerConfig `yaml:"owner,omitempty" json:"owner,omitempty"`

	// Extended spec sections — derived from base-kit CUE schemas.
	// These capture the full Zielbild that StackKits need to generate configs.
	System          *SystemSpec           `yaml:"system,omitempty" json:"system,omitempty"`
	DNS             *DNSSpec              `yaml:"dns,omitempty" json:"dns,omitempty"`
	VPN             *VPNSpec              `yaml:"vpn,omitempty" json:"vpn,omitempty"`
	Firewall        *FirewallSpec         `yaml:"firewall,omitempty" json:"firewall,omitempty"`
	Backup          *BackupSpec           `yaml:"backup,omitempty" json:"backup,omitempty"`
	Observability   *ObservabilitySpec    `yaml:"observability,omitempty" json:"observability,omitempty"`
	ContainerConfig *ContainerRuntimeSpec `yaml:"container,omitempty" json:"container,omitempty"`
	Branding        *BrandingSpec         `yaml:"branding,omitempty" json:"branding,omitempty"`
	Tunnel          *TunnelSpec           `yaml:"tunnel,omitempty" json:"tunnel,omitempty"`
	DriftDetection  *DriftDetectionSpec   `yaml:"driftDetection,omitempty" json:"driftDetection,omitempty"`
}

// AppSpec describes a user application deployed behind the StackKits platform.
type AppSpec struct {
	Kind    string            `yaml:"kind,omitempty" json:"kind,omitempty"`
	Image   string            `yaml:"image,omitempty" json:"image,omitempty"`
	Port    int               `yaml:"port,omitempty" json:"port,omitempty"`
	Route   AppRouteSpec      `yaml:"route,omitempty" json:"route,omitempty"`
	Health  AppHealthSpec     `yaml:"health,omitempty" json:"health,omitempty"`
	Setup   AppSetupSpec      `yaml:"setup,omitempty" json:"setup,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Secrets map[string]string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

// AppSetupSpec describes first-run setup behavior for a platform-managed app.
type AppSetupSpec struct {
	Policy string          `yaml:"policy,omitempty" json:"policy,omitempty"`
	Drops  []SetupDropSpec `yaml:"drops,omitempty" json:"drops,omitempty"`
}

// SetupDropSpec describes a setup unit that can run separately from deploy.
type SetupDropSpec struct {
	Name        string            `yaml:"name" json:"name"`
	Version     string            `yaml:"version,omitempty" json:"version,omitempty"`
	Runner      string            `yaml:"runner,omitempty" json:"runner,omitempty"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Command     []string          `yaml:"command,omitempty" json:"command,omitempty"`
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Secrets     map[string]string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

// AppRouteSpec describes how an app is exposed through Traefik.
type AppRouteSpec struct {
	Host string `yaml:"host,omitempty" json:"host,omitempty"`
	Auth string `yaml:"auth,omitempty" json:"auth,omitempty"` // login-gateway (default) or public
}

// AppHealthSpec describes the app health endpoint contract.
type AppHealthSpec struct {
	Path string `yaml:"path,omitempty" json:"path,omitempty"`
}

// OwnerConfig holds the owner-provisioning fields that `stackkit init` captures
// and TechStack may pass through stack-spec handoff. `bootstrapMode` is the
// current lane selector:
//   - auto: owner identity is resolved by kombify Cloud/TechStack. StackKits
//     must not require or invent owner.email/owner.username in the public spec.
//   - custom: explicit self-hosted/local Owner. Email and username are required.
//   - none: no Owner bootstrap for OSS/BYOS or legacy/manual setups.
//
// Persisted to stack-spec.yaml under the `owner:` key with omitempty so older
// specs round-trip without picking up an empty owner block.
type OwnerConfig struct {
	// BootstrapMode selects how owner identity is prepared: "auto", "custom",
	// or "none". Empty preserves legacy specs; a non-empty Source with no
	// BootstrapMode resolves to custom(local) or auto(cloud).
	BootstrapMode string `yaml:"bootstrapMode,omitempty" json:"bootstrapMode,omitempty"`

	// Source selects the provisioning path: "local" for custom self-hosted
	// bootstrap or "cloud" for orchestrator-managed auto bootstrap.
	Source string `yaml:"source,omitempty" json:"source,omitempty"`

	// Email is the owner's address. Required only for custom bootstrap.
	Email string `yaml:"email,omitempty" json:"email,omitempty"`

	// Username is the PocketID login handle. Required only for custom bootstrap.
	Username string `yaml:"username,omitempty" json:"username,omitempty"`

	// DisplayName is rendered in PocketID's UI; defaults to Username when empty.
	DisplayName string `yaml:"displayName,omitempty" json:"displayName,omitempty"`

	// RecoveryPassphraseHash is the argon2id-PHC hash of the recovery
	// passphrase used to encrypt the break-glass bundle. Required when Source
	// is non-empty; the plaintext is re-prompted at apply-time.
	RecoveryPassphraseHash string `yaml:"recoveryPassphraseHash,omitempty" json:"recoveryPassphraseHash,omitempty"`

	// RecoveryMaterialRef points at orchestrator-owned recovery material. It is
	// a reference only; plaintext recovery passphrases do not belong in specs.
	RecoveryMaterialRef string `yaml:"recoveryMaterialRef,omitempty" json:"recoveryMaterialRef,omitempty"`

	// CloudOIDCIssuer is the URL of the external OIDC issuer for auto/cloud
	// owner bootstrap.
	CloudOIDCIssuer string `yaml:"cloudOidcIssuer,omitempty" json:"cloudOidcIssuer,omitempty"`

	// CloudOIDCClientID is the registered client ID at the cloud OIDC issuer.
	CloudOIDCClientID string `yaml:"cloudOidcClientId,omitempty" json:"cloudOidcClientId,omitempty"`

	// CloudOIDCClientSecretRef is a secret-store reference (e.g. doppler:// or
	// secret://) pointing at the OIDC client secret. The literal secret is
	// never persisted in the spec.
	CloudOIDCClientSecretRef string `yaml:"cloudOidcClientSecretRef,omitempty" json:"cloudOidcClientSecretRef,omitempty"`

	// CloudOIDCForeignSubject is the cloud user's stable subject ID at the
	// external IdP. PocketID uses it to federate the local owner record.
	CloudOIDCForeignSubject string `yaml:"cloudOidcForeignSubject,omitempty" json:"cloudOidcForeignSubject,omitempty"`
}

// IsZero lets YAML encoders omit an empty owner block while still preserving
// an explicit bootstrapMode: none block.
func (o OwnerConfig) IsZero() bool {
	return o.BootstrapMode == "" &&
		o.Source == "" &&
		o.Email == "" &&
		o.Username == "" &&
		o.DisplayName == "" &&
		o.RecoveryPassphraseHash == "" &&
		o.RecoveryMaterialRef == "" &&
		o.CloudOIDCIssuer == "" &&
		o.CloudOIDCClientID == "" &&
		o.CloudOIDCClientSecretRef == "" &&
		o.CloudOIDCForeignSubject == ""
}

// EffectiveBootstrapMode normalizes legacy owner specs to the new lane model.
func (o OwnerConfig) EffectiveBootstrapMode() string {
	mode := strings.ToLower(strings.TrimSpace(o.BootstrapMode))
	if mode != "" {
		return mode
	}
	switch strings.ToLower(strings.TrimSpace(o.Source)) {
	case OwnerSourceLocal:
		return OwnerBootstrapModeCustom
	case OwnerSourceCloud:
		return OwnerBootstrapModeAuto
	default:
		return ""
	}
}

func IsKnownOwnerBootstrapMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", OwnerBootstrapModeAuto, OwnerBootstrapModeCustom, OwnerBootstrapModeNone:
		return true
	default:
		return false
	}
}

func IsKnownOwnerSource(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "", OwnerSourceLocal, OwnerSourceCloud:
		return true
	default:
		return false
	}
}

// IdentitySpec configures the identity/auth stack behavior.
type IdentitySpec struct {
	// AuthMode overrides the default TinyAuth auth mode (e.g. "passkeys_plus_legacy").
	AuthMode string `yaml:"authMode,omitempty" json:"authMode,omitempty"`
	// SecondUserEmail is an optional second admin user email.
	SecondUserEmail string `yaml:"secondUserEmail,omitempty" json:"secondUserEmail,omitempty"`
	// OIDCProvider selects the OIDC provider ("pocketid" default, or external).
	OIDCProvider string `yaml:"oidcProvider,omitempty" json:"oidcProvider,omitempty"`
	// LDAPEnabled enables LLDAP for directory services.
	LDAPEnabled *bool `yaml:"ldapEnabled,omitempty" json:"ldapEnabled,omitempty"`
	// LDAPOrganization sets the LDAP organization name.
	LDAPOrganization string `yaml:"ldapOrganization,omitempty" json:"ldapOrganization,omitempty"`
}

// TLSSpec defines TLS/HTTPS certificate configuration
type TLSSpec struct {
	Provider  string `yaml:"provider,omitempty" json:"provider,omitempty"`   // DNS provider for DNS-01 challenge (e.g. "cloudflare")
	Challenge string `yaml:"challenge,omitempty" json:"challenge,omitempty"` // "tls" (default) or "dns"
}

// StorageSpec defines external storage configuration
type StorageSpec struct {
	ExternalDevice string `yaml:"externalDevice,omitempty" json:"externalDevice,omitempty"`
	MountPoint     string `yaml:"mountPoint,omitempty" json:"mountPoint,omitempty"`
	// DataDir is the root directory for service data (default "/opt/data").
	DataDir string `yaml:"dataDir,omitempty" json:"dataDir,omitempty"`
	// BackupDir is the directory for local backups (default "/opt/backups").
	BackupDir string `yaml:"backupDir,omitempty" json:"backupDir,omitempty"`
	// MediaPath is the path for media storage (Jellyfin, Immich, etc.).
	MediaPath string `yaml:"mediaPath,omitempty" json:"mediaPath,omitempty"`
}

// NetworkSpec defines network configuration
type NetworkSpec struct {
	Mode    string `yaml:"mode" json:"mode"` // "local", "public", "hybrid"
	Subnet  string `yaml:"subnet,omitempty" json:"subnet,omitempty"`
	Gateway string `yaml:"gateway,omitempty" json:"gateway,omitempty"`
	// MTU for the Docker network (default 1500).
	MTU int `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	// IPv6 enables IPv6 on the Docker network.
	IPv6 bool `yaml:"ipv6,omitempty" json:"ipv6,omitempty"`
}

// ComputeSpec defines compute tier configuration
type ComputeSpec struct {
	Tier string `yaml:"tier" json:"tier"` // "low", "standard", "high"
}

// SSHSpec defines SSH configuration
type SSHSpec struct {
	KeyPath string `yaml:"keyPath,omitempty" json:"keyPath,omitempty"`
	User    string `yaml:"user,omitempty" json:"user,omitempty"`
	Port    int    `yaml:"port,omitempty" json:"port,omitempty"`
	// PermitRootLogin controls sshd PermitRootLogin ("no", "prohibit-password", etc.).
	PermitRootLogin string `yaml:"permitRootLogin,omitempty" json:"permitRootLogin,omitempty"`
	// PasswordAuth enables/disables password authentication in sshd.
	PasswordAuth *bool `yaml:"passwordAuth,omitempty" json:"passwordAuth,omitempty"`
	// MaxAuthTries limits authentication attempts per connection.
	MaxAuthTries int `yaml:"maxAuthTries,omitempty" json:"maxAuthTries,omitempty"`
}

const (
	// NodeRoleMain is the canonical role for the homelab control node. The
	// main node owns identity, StackKits control state, and the PaaS UI.
	NodeRoleMain = "main"
	// NodeRoleControlPlane is a compatibility alias for NodeRoleMain.
	NodeRoleControlPlane = "control-plane"
	// NodeRoleStandalone is a compatibility alias for a single-node main.
	NodeRoleStandalone = "standalone"
	NodeRoleWorker     = "worker"
	NodeRoleStorage    = "storage"
)

// CanonicalNodeRole maps compatible legacy role names to the BaseKit v1 role
// vocabulary used for validation and placement decisions.
func CanonicalNodeRole(role string) string {
	switch role {
	case "", NodeRoleMain, NodeRoleControlPlane, NodeRoleStandalone:
		return NodeRoleMain
	case NodeRoleWorker:
		return NodeRoleWorker
	case NodeRoleStorage:
		return NodeRoleStorage
	default:
		return role
	}
}

// IsMainNodeRole reports whether role identifies the homelab main node.
func IsMainNodeRole(role string) bool {
	return CanonicalNodeRole(role) == NodeRoleMain
}

// IsKnownNodeRole reports whether role is part of the supported BaseKit node
// vocabulary or one of its compatibility aliases.
func IsKnownNodeRole(role string) bool {
	switch role {
	case "", NodeRoleMain, NodeRoleControlPlane, NodeRoleStandalone, NodeRoleWorker, NodeRoleStorage:
		return true
	default:
		return false
	}
}

// IsMultiNode reports whether the spec describes one homelab spread across
// multiple registered nodes.
func (s *StackSpec) IsMultiNode() bool {
	return s != nil && len(s.Nodes) > 1
}

// NodeSpec defines a deployment node
type NodeSpec struct {
	Name     string   `yaml:"name" json:"name"`
	Role     string   `yaml:"role" json:"role"` // "main", "worker", "storage"; aliases: "control-plane", "standalone"
	IP       string   `yaml:"ip" json:"ip"`
	Host     string   `yaml:"host,omitempty" json:"host,omitempty"` // hostname or FQDN
	Services []string `yaml:"services,omitempty" json:"services,omitempty"`
	// Per-node compute resources (from CUE #ComputeResources).
	CPUCores  int `yaml:"cpuCores,omitempty" json:"cpuCores,omitempty"`
	RAMGB     int `yaml:"ramGB,omitempty" json:"ramGB,omitempty"`
	StorageGB int `yaml:"storageGB,omitempty" json:"storageGB,omitempty"`
	// OS configuration for the node.
	OS *NodeOSSpec `yaml:"os,omitempty" json:"os,omitempty"`
}

// DeploymentState represents the current deployment state
type DeploymentState struct {
	StackKit           string             `yaml:"stackkit" json:"stackkit"`
	Mode               string             `yaml:"mode" json:"mode"`
	Status             DeploymentStatus   `yaml:"status" json:"status"`
	LastApplied        time.Time          `yaml:"lastApplied" json:"lastApplied"`
	TofuState          string             `yaml:"tofuState,omitempty" json:"tofuState,omitempty"`
	Services           []ServiceState     `yaml:"services" json:"services"`
	PlatformSystemApps []PlatformAppState `yaml:"platformSystemApps,omitempty" json:"platformSystemApps,omitempty"`
	PlatformApps       []PlatformAppState `yaml:"platformApps,omitempty" json:"platformApps,omitempty"`
	SetupRuns          []SetupRunState    `yaml:"setupRuns,omitempty" json:"setupRuns,omitempty"`

	// KitVersionID, KitSemver, KitChannel are populated by `stackkit apply`
	// and `stackkit kit upgrade` (kit-update-phase-1, ADR-0018). State files
	// written by older CLI versions leave these empty; the upgrade command
	// fails with a clear "re-apply to populate version metadata" message
	// rather than guessing.
	KitVersionID string `yaml:"kitVersionId,omitempty" json:"kitVersionId,omitempty"`
	KitSemver    string `yaml:"kitSemver,omitempty" json:"kitSemver,omitempty"`
	KitChannel   string `yaml:"kitChannel,omitempty" json:"kitChannel,omitempty"`

	// LastSnapshotDir points at .stackkit/snapshots/<ts>-<old-kit>/ — used
	// by `stackkit kit upgrade rollback` as the default --to-snapshot when
	// the operator does not pass one explicitly.
	LastSnapshotDir string `yaml:"lastSnapshotDir,omitempty" json:"lastSnapshotDir,omitempty"`
}

// DeploymentStatus represents deployment status
type DeploymentStatus string

const (
	StatusPending  DeploymentStatus = "pending"
	StatusPlanning DeploymentStatus = "planning"
	StatusApplying DeploymentStatus = "applying"
	StatusRunning  DeploymentStatus = "running"
	StatusDegraded DeploymentStatus = "degraded"
	StatusError    DeploymentStatus = "error"
	StatusRemoved  DeploymentStatus = "removed"
)

// ServiceState represents the state of a service
type ServiceState struct {
	Name      string        `yaml:"name" json:"name"`
	Status    ServiceStatus `yaml:"status" json:"status"`
	Container string        `yaml:"container,omitempty" json:"container,omitempty"`
	URL       string        `yaml:"url,omitempty" json:"url,omitempty"`
	Health    HealthStatus  `yaml:"health" json:"health"`
}

// PlatformAppState records the external platform identity for a StackKit L3 app.
type PlatformAppState struct {
	Name         string          `yaml:"name" json:"name"`
	Role         string          `yaml:"role,omitempty" json:"role,omitempty"`
	Platform     string          `yaml:"platform" json:"platform"`
	ExternalID   string          `yaml:"externalId" json:"externalId"`
	DeploymentID string          `yaml:"deploymentId,omitempty" json:"deploymentId,omitempty"`
	ComposePath  string          `yaml:"composePath,omitempty" json:"composePath,omitempty"`
	SetupPolicy  string          `yaml:"setupPolicy,omitempty" json:"setupPolicy,omitempty"`
	SetupDrops   []SetupDropSpec `yaml:"setupDrops,omitempty" json:"setupDrops,omitempty"`
	LastDeployed time.Time       `yaml:"lastDeployed,omitempty" json:"lastDeployed,omitempty"`
}

// SetupRunState records an explicit setup-drop execution. Manual setup drops
// remain absent from this list until a user requests a run.
type SetupRunState struct {
	AppName       string    `yaml:"appName" json:"appName"`
	DropName      string    `yaml:"dropName" json:"dropName"`
	Policy        string    `yaml:"policy,omitempty" json:"policy,omitempty"`
	Status        string    `yaml:"status" json:"status"`
	LastRequested time.Time `yaml:"lastRequested,omitempty" json:"lastRequested,omitempty"`
	LastStarted   time.Time `yaml:"lastStarted,omitempty" json:"lastStarted,omitempty"`
	LastFinished  time.Time `yaml:"lastFinished,omitempty" json:"lastFinished,omitempty"`
}

// ServiceStatus represents service status
type ServiceStatus string

const (
	ServiceStatusRunning  ServiceStatus = "running"
	ServiceStatusStopped  ServiceStatus = "stopped"
	ServiceStatusStarting ServiceStatus = "starting"
	ServiceStatusError    ServiceStatus = "error"
	ServiceStatusUnknown  ServiceStatus = "unknown"
)

// HealthStatus represents health check status
type HealthStatus string

const (
	HealthStatusHealthy   HealthStatus = "healthy"
	HealthStatusUnhealthy HealthStatus = "unhealthy"
	HealthStatusStarting  HealthStatus = "starting"
	HealthStatusNone      HealthStatus = "none"
	HealthStatusUnknown   HealthStatus = "unknown"
)

// ValidationResult represents the result of a validation
type ValidationResult struct {
	Valid    bool              `json:"valid"`
	Errors   []ValidationError `json:"errors,omitempty"`
	Warnings []ValidationError `json:"warnings,omitempty"`
}

// ValidationError represents a validation error or warning
type ValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// CompatibilityTier classifies a VPS by how well it supports Docker/StackKits.
type CompatibilityTier string

const (
	// TierFull means Docker works perfectly with all features.
	TierFull CompatibilityTier = "full"
	// TierDegraded means Docker works with auto-workarounds (vfs, host network, DNS fix).
	TierDegraded CompatibilityTier = "degraded"
	// TierIncompatible means the kernel blocks unshare — Docker cannot run at all.
	TierIncompatible CompatibilityTier = "incompatible"
)

// NodeContext classifies the deployment environment.
// Matches CUE #NodeContext: "local" | "cloud" | "pi".
// Auto-detected from network environment + hardware, overridable via --context flag.
type NodeContext string

const (
	// ContextLocal means a home/office server (behind NAT, no public IP).
	ContextLocal NodeContext = "local"
	// ContextCloud means a VPS, dedicated server, or kombify Cloud instance (public IP).
	ContextCloud NodeContext = "cloud"
	// ContextPi means a low-resource ARM64 device (Raspberry Pi, etc.).
	ContextPi NodeContext = "pi"
)

// NetworkEnvironment classifies where the server is running.
// This is a lower-level detection detail used internally by context resolution.
type NetworkEnvironment string

const (
	// NetEnvHome means the server is on a home/office LAN behind NAT.
	NetEnvHome NetworkEnvironment = "home"
	// NetEnvVPS means the server is a VPS/dedicated server with a public IP.
	NetEnvVPS NetworkEnvironment = "vps"
	// NetEnvCloud means the server was provisioned via kombify Cloud (SaaS).
	NetEnvCloud NetworkEnvironment = "cloud"
	// NetEnvUnknown means the environment could not be determined.
	NetEnvUnknown NetworkEnvironment = "unknown"
)

// DockerCapabilities represents detected Docker runtime capabilities.
// Written by `stackkit prepare` and read by `stackkit generate`.
type DockerCapabilities struct {
	BridgeNetworking bool   `json:"bridgeNetworking"`
	Iptables         bool   `json:"iptables"`
	StorageDriver    string `json:"storageDriver"`

	// Docker runtime functionality — false when the kernel blocks unshare/namespaces
	// (e.g. OpenVZ containers), making Docker unable to run any containers.
	DockerFunctional bool   `json:"dockerFunctional"`
	RuntimeError     string `json:"runtimeError,omitempty"`

	// VPS environment detection
	VirtualizationType string            `json:"virtualizationType,omitempty"` // "kvm", "openvz", "lxc", "none"
	CompatibilityTier  CompatibilityTier `json:"compatibilityTier,omitempty"`  // "full", "degraded", "incompatible"
	UnshareAvailable   bool              `json:"unshareAvailable"`
	CgroupVersion      string            `json:"cgroupVersion,omitempty"` // "v1", "v2"
	MemoryLimits       bool              `json:"memoryLimits"`            // false when nested Docker cannot apply cgroup memory limits

	// DNS and image pre-pull status (troubleshooting engine)
	DNSWorking      bool     `json:"dnsWorking"`
	DNSFix          string   `json:"dnsFix,omitempty"` // "none", "daemon-json", "host-prepull"
	PrePulledImages []string `json:"prePulledImages,omitempty"`
	PrePullFailed   []string `json:"prePullFailed,omitempty"`

	// Disk space (detected during prepare)
	DiskTotalGB float64 `json:"diskTotalGB,omitempty"`
	DiskAvailGB float64 `json:"diskAvailGB,omitempty"`
	DiskMount   string  `json:"diskMount,omitempty"`   // mount point checked (e.g. "/" or "/var/lib/docker")
	LVMDetected bool    `json:"lvmDetected,omitempty"` // root is on LVM
	LVMExtended bool    `json:"lvmExtended,omitempty"` // auto-extended during prepare

	// Hardware profile (detected during prepare)
	CPUCores int     `json:"cpuCores,omitempty"`
	MemoryGB float64 `json:"memoryGB,omitempty"`

	// Resolved NodeContext (auto-detected or overridden via --context flag)
	ResolvedContext NodeContext `json:"resolvedContext,omitempty"` // "local", "cloud", "pi"

	// Network environment detection (lower-level detail feeding into context resolution)
	NetworkEnv         NetworkEnvironment `json:"networkEnv,omitempty"`         // "home", "vps", "cloud", "unknown"
	PublicIP           string             `json:"publicIP,omitempty"`           // External IP (empty if detection failed)
	PrivateIP          string             `json:"privateIP,omitempty"`          // LAN/internal IP
	IsNAT              bool               `json:"isNAT,omitempty"`              // true if behind NAT (home network)
	HasPublicInterface bool               `json:"hasPublicInterface,omitempty"` // true if a network interface has a public IP directly

	// Block devices and storage resolution
	BlockDevices      []BlockDevice      `json:"blockDevices,omitempty"`
	StorageResolution *StorageResolution `json:"storageResolution,omitempty"`
}

// BlockDevice represents a detected block device on the host.
type BlockDevice struct {
	Name       string  `json:"name"`
	Path       string  `json:"path"`
	SizeGB     float64 `json:"sizeGB"`
	Type       string  `json:"type"` // "disk", "part"
	Mountpoint string  `json:"mountpoint"`
	FSType     string  `json:"fstype"`
	Model      string  `json:"model"`
	Removable  bool    `json:"removable"`
}

// StorageResolution records the strategy chosen to resolve insufficient storage.
type StorageResolution struct {
	Strategy string `json:"strategy"` // "none", "external-device", "tier-downgrade", "force"
	Device   string `json:"device,omitempty"`
	Mount    string `json:"mount,omitempty"`
}

// InstanceRegistration is the payload sent to kombify when a stackkit-server
// registers itself for Direct Connect (Cloudflare Edge proxies directly to it).
type InstanceRegistration struct {
	InstanceID  string        `json:"instance_id"`        // Unique instance identifier (device fingerprint + stackkit name)
	EndpointURL string        `json:"endpoint_url"`       // Public URL where stackkit-server is reachable (e.g. https://api.mylab.kombify.me)
	StackKit    string        `json:"stackkit"`           // StackKit name (e.g. "base-kit")
	Version     string        `json:"version,omitempty"`  // StackKit version
	Services    []ServiceInfo `json:"services"`           // Running services
	Status      string        `json:"status"`             // "running", "degraded", "stopped"
	APIPort     int           `json:"api_port,omitempty"` // Port stackkit-server listens on
	LastSeen    time.Time     `json:"last_seen"`          // Last heartbeat timestamp
}

// ServiceInfo is a lightweight service descriptor for registry registration.
type ServiceInfo struct {
	Name   string `json:"name"`
	URL    string `json:"url,omitempty"`
	Status string `json:"status"` // "running", "stopped", "error"
}

// ResolveReverseProxy determines which Traefik instance routes platform services
// based on the PaaS selection. Day-1 installs use StackKit's own Traefik so a
// fresh node never depends on a PaaS-created Docker network that does not exist
// yet.
func (s *StackSpec) ResolveReverseProxy() string {
	return ResolveReverseProxyForPAAS(s.PAAS)
}

// ResolveReverseProxyForPAAS determines which Traefik instance routes platform
// services for the given PAAS selection.
func ResolveReverseProxyForPAAS(paas string) string {
	switch paas {
	case PAASDokploy:
		return ReverseProxyDokploy
	case PAASCoolify:
		return ReverseProxyCoolify
	default:
		return ReverseProxyStandalone
	}
}

// ResolvePAAS determines the PAAS platform from explicit setting or compute tier.
func (s *StackSpec) ResolvePAAS() string {
	if s.PAAS != "" {
		return s.PAAS
	}
	return s.resolvePAASByTier()
}

// ResolvePAASForContext is the canonical auto-selection policy for normal
// StackKit platform adapters. Explicit user config wins; otherwise fresh
// StackKit rollouts default to Coolify. Dokploy remains a supported explicit
// adapter while its first-owner and SSO story is hardened separately.
func (s *StackSpec) ResolvePAASForContext(ctx NodeContext) string {
	if s.PAAS != "" {
		return s.PAAS
	}

	return PAASCoolify
}

func (s *StackSpec) resolvePAASByTier() string {
	return PAASCoolify
}

// IsStandardPAAS reports whether the platform is allowed for normal StackKit
// rollouts. Normal Base/Modern/HA kits must resolve to one of these.
func IsStandardPAAS(paas string) bool {
	return paas == PAASDokploy || paas == PAASCoolify
}

// IsExperimentalPAAS reports whether the platform exists only as a constrained
// nonstandard mode rather than a normal StackKit default.
func IsExperimentalPAAS(paas string) bool {
	return paas == PAASDockge
}

// SystemInfo represents system information from a node
type SystemInfo struct {
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	OSVersion     string `json:"osVersion"`
	Arch          string `json:"arch"`
	CPUCores      int    `json:"cpuCores"`
	MemoryMB      int    `json:"memoryMB"`
	DiskGB        int    `json:"diskGB"`
	DockerVersion string `json:"dockerVersion,omitempty"`
	TofuVersion   string `json:"tofuVersion,omitempty"`
}

// ---------------------------------------------------------------------------
// Extended spec types — derived from base-kit CUE schemas.
// These capture the full Zielbild that StackKits need to generate all configs.
// ---------------------------------------------------------------------------

// SystemSpec maps to CUE #SystemConfig — host-level system settings.
type SystemSpec struct {
	// Timezone in IANA format (e.g. "Europe/Berlin"). Default: "UTC".
	Timezone string `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	// Locale for the system (e.g. "en_US.UTF-8").
	Locale string `yaml:"locale,omitempty" json:"locale,omitempty"`
	// Swap policy: "disabled", "auto", "manual".
	Swap string `yaml:"swap,omitempty" json:"swap,omitempty"`
	// SwapSizeMB when Swap is "manual".
	SwapSizeMB int `yaml:"swapSizeMB,omitempty" json:"swapSizeMB,omitempty"`
	// UnattendedUpgrades: "disabled", "security" (default), "all".
	UnattendedUpgrades string `yaml:"unattendedUpgrades,omitempty" json:"unattendedUpgrades,omitempty"`
}

// DNSSpec maps to CUE #DNSConfig — DNS resolver settings.
type DNSSpec struct {
	// Servers lists upstream DNS resolvers (default: ["1.1.1.1", "8.8.8.8"]).
	Servers []string `yaml:"servers,omitempty" json:"servers,omitempty"`
	// LocalResolver enables a local DNS resolver (AdGuard Home, Unbound).
	LocalResolver bool `yaml:"localResolver,omitempty" json:"localResolver,omitempty"`
	// LocalResolverTool selects the local resolver tool ("adguard-home", "unbound").
	LocalResolverTool string `yaml:"localResolverTool,omitempty" json:"localResolverTool,omitempty"`
	// DoH enables DNS-over-HTTPS.
	DoH bool `yaml:"doh,omitempty" json:"doh,omitempty"`
	// DoHUpstream is the DoH upstream URL.
	DoHUpstream string `yaml:"dohUpstream,omitempty" json:"dohUpstream,omitempty"`
}

// VPNSpec maps to CUE #VPNConfig — VPN/overlay network settings.
type VPNSpec struct {
	// Enabled activates the VPN overlay.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Type selects the VPN provider: "headscale", "tailscale", "wireguard", "netbird", "none".
	Type string `yaml:"type,omitempty" json:"type,omitempty"`
	// Subnet for the VPN network.
	Subnet string `yaml:"subnet,omitempty" json:"subnet,omitempty"`
	// Port for the VPN service.
	Port int `yaml:"port,omitempty" json:"port,omitempty"`
	// Headscale-specific configuration.
	Headscale *HeadscaleConfig `yaml:"headscale,omitempty" json:"headscale,omitempty"`
}

// HeadscaleConfig holds Headscale-specific VPN settings.
type HeadscaleConfig struct {
	ServerURL       string   `yaml:"serverUrl,omitempty" json:"serverUrl,omitempty"`
	Namespace       string   `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	ExitNode        bool     `yaml:"exitNode,omitempty" json:"exitNode,omitempty"`
	AdvertiseRoutes []string `yaml:"advertiseRoutes,omitempty" json:"advertiseRoutes,omitempty"`
}

// FirewallSpec maps to CUE #FirewallPolicy — host firewall settings.
type FirewallSpec struct {
	// Enabled activates the firewall (default true).
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Backend: "ufw", "firewalld", "nftables".
	Backend string `yaml:"backend,omitempty" json:"backend,omitempty"`
	// DefaultInbound policy: "allow" or "deny" (default "deny").
	DefaultInbound string `yaml:"defaultInbound,omitempty" json:"defaultInbound,omitempty"`
	// Rules defines additional firewall rules.
	Rules []FirewallRuleSpec `yaml:"rules,omitempty" json:"rules,omitempty"`
	// RateLimit configures brute-force protection (fail2ban-style).
	RateLimit *RateLimitSpec `yaml:"rateLimit,omitempty" json:"rateLimit,omitempty"`
}

// FirewallRuleSpec defines a single firewall rule.
type FirewallRuleSpec struct {
	Port     int    `yaml:"port" json:"port"`
	Protocol string `yaml:"protocol,omitempty" json:"protocol,omitempty"` // "tcp", "udp", "both"
	Source   string `yaml:"source,omitempty" json:"source,omitempty"`     // IP/CIDR or "any"
	Action   string `yaml:"action,omitempty" json:"action,omitempty"`     // "allow", "deny"
	Comment  string `yaml:"comment,omitempty" json:"comment,omitempty"`
}

// RateLimitSpec configures brute-force / rate limiting on the firewall.
type RateLimitSpec struct {
	Enabled    bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	MaxRetries int  `yaml:"maxRetries,omitempty" json:"maxRetries,omitempty"`
	FindTime   int  `yaml:"findTime,omitempty" json:"findTime,omitempty"` // seconds
	BanTime    int  `yaml:"banTime,omitempty" json:"banTime,omitempty"`   // seconds
}

// BackupSpec maps to CUE #BackupConfig — backup configuration.
type BackupSpec struct {
	// Enabled activates backups (default true via CUE).
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Backend: "restic", "borgbackup", "rclone", "rsync".
	Backend string `yaml:"backend,omitempty" json:"backend,omitempty"`
	// Schedule in cron format (default derived from compute tier).
	Schedule string `yaml:"schedule,omitempty" json:"schedule,omitempty"`
	// Retention defines how many backups to keep.
	Retention *BackupRetentionSpec `yaml:"retention,omitempty" json:"retention,omitempty"`
	// Destinations lists backup targets.
	Destinations []BackupDestinationSpec `yaml:"destinations,omitempty" json:"destinations,omitempty"`
	// Paths to include in backups.
	Paths []string `yaml:"paths,omitempty" json:"paths,omitempty"`
	// Excludes lists patterns to exclude from backups.
	Excludes []string `yaml:"excludes,omitempty" json:"excludes,omitempty"`
}

// BackupRetentionSpec defines backup retention policy.
type BackupRetentionSpec struct {
	Daily   int `yaml:"daily,omitempty" json:"daily,omitempty"`
	Weekly  int `yaml:"weekly,omitempty" json:"weekly,omitempty"`
	Monthly int `yaml:"monthly,omitempty" json:"monthly,omitempty"`
	Yearly  int `yaml:"yearly,omitempty" json:"yearly,omitempty"`
}

// BackupDestinationSpec defines a backup target.
type BackupDestinationSpec struct {
	Name string `yaml:"name" json:"name"`
	Type string `yaml:"type" json:"type"` // "local", "s3", "b2", "sftp"
	Path string `yaml:"path,omitempty" json:"path,omitempty"`
	// S3-specific fields.
	S3Bucket   string `yaml:"s3Bucket,omitempty" json:"s3Bucket,omitempty"`
	S3Endpoint string `yaml:"s3Endpoint,omitempty" json:"s3Endpoint,omitempty"`
	S3Region   string `yaml:"s3Region,omitempty" json:"s3Region,omitempty"`
	// SFTP-specific fields.
	SFTPHost string `yaml:"sftpHost,omitempty" json:"sftpHost,omitempty"`
	SFTPUser string `yaml:"sftpUser,omitempty" json:"sftpUser,omitempty"`
	SFTPPath string `yaml:"sftpPath,omitempty" json:"sftpPath,omitempty"`
}

// ObservabilitySpec maps to CUE #LoggingConfig + #MetricsConfig + #AlertingConfig.
type ObservabilitySpec struct {
	// Logging configuration.
	Logging *LoggingSpec `yaml:"logging,omitempty" json:"logging,omitempty"`
	// Metrics configuration.
	Metrics *MetricsSpec `yaml:"metrics,omitempty" json:"metrics,omitempty"`
	// Alerting configuration.
	Alerting *AlertingSpec `yaml:"alerting,omitempty" json:"alerting,omitempty"`
}

// LoggingSpec configures container/host logging.
type LoggingSpec struct {
	// Driver: "json-file", "journald", "loki", "none".
	Driver string `yaml:"driver,omitempty" json:"driver,omitempty"`
	// Level: "debug", "info", "warn", "error".
	Level string `yaml:"level,omitempty" json:"level,omitempty"`
	// MaxSize per log file (e.g. "50m").
	MaxSize string `yaml:"maxSize,omitempty" json:"maxSize,omitempty"`
	// MaxFile count of rotated log files.
	MaxFile int `yaml:"maxFile,omitempty" json:"maxFile,omitempty"`
}

// MetricsSpec configures metrics collection.
type MetricsSpec struct {
	// Enabled activates metrics collection.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Backend: "prometheus", "victoriametrics", "none".
	Backend string `yaml:"backend,omitempty" json:"backend,omitempty"`
	// ScrapeInterval (e.g. "15s").
	ScrapeInterval string `yaml:"scrapeInterval,omitempty" json:"scrapeInterval,omitempty"`
	// Retention period (e.g. "15d", "30d").
	Retention string `yaml:"retention,omitempty" json:"retention,omitempty"`
}

// AlertingSpec configures alerting.
type AlertingSpec struct {
	// Enabled activates alerting.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Receivers lists alert destinations.
	Receivers []AlertReceiverSpec `yaml:"receivers,omitempty" json:"receivers,omitempty"`
}

// AlertReceiverSpec defines an alert destination.
type AlertReceiverSpec struct {
	Name string `yaml:"name" json:"name"`
	Type string `yaml:"type" json:"type"` // "email", "slack", "discord", "telegram", "webhook"
}

// ContainerRuntimeSpec maps to CUE #ContainerRuntime.
type ContainerRuntimeSpec struct {
	// Engine: "docker" (default) or "podman".
	Engine string `yaml:"engine,omitempty" json:"engine,omitempty"`
	// Rootless enables rootless container mode.
	Rootless bool `yaml:"rootless,omitempty" json:"rootless,omitempty"`
	// StorageDriver: "overlay2", "btrfs", "zfs", "vfs".
	StorageDriver string `yaml:"storageDriver,omitempty" json:"storageDriver,omitempty"`
	// DataRoot directory for container data (default "/var/lib/docker").
	DataRoot string `yaml:"dataRoot,omitempty" json:"dataRoot,omitempty"`
	// RegistryMirrors lists Docker registry mirrors.
	RegistryMirrors []string `yaml:"registryMirrors,omitempty" json:"registryMirrors,omitempty"`
	// LogDriver default for containers: "json-file", "journald", etc.
	LogDriver string `yaml:"logDriver,omitempty" json:"logDriver,omitempty"`
}

// BrandingSpec configures dashboard branding.
type BrandingSpec struct {
	// Color is the primary brand color (hex, e.g. "#4F46E5").
	Color string `yaml:"color,omitempty" json:"color,omitempty"`
	// DashboardTitle is the title shown on the dashboard.
	DashboardTitle string `yaml:"dashboardTitle,omitempty" json:"dashboardTitle,omitempty"`
}

// TunnelSpec maps to CUE tunnel addon — CGNAT/DS-Lite bypass configuration.
type TunnelSpec struct {
	// Enabled activates the tunnel.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Provider: "cloudflare" or "pangolin".
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"`
	// Cloudflare-specific configuration.
	Cloudflare *CloudflareTunnelConfig `yaml:"cloudflare,omitempty" json:"cloudflare,omitempty"`
	// Pangolin-specific configuration.
	Pangolin *PangolinTunnelConfig `yaml:"pangolin,omitempty" json:"pangolin,omitempty"`
}

// CloudflareTunnelConfig holds Cloudflare Tunnel settings.
type CloudflareTunnelConfig struct {
	// TunnelName is the Cloudflare tunnel name.
	TunnelName string `yaml:"tunnelName,omitempty" json:"tunnelName,omitempty"`
	// ZeroTrust enables Cloudflare Zero Trust access policies.
	ZeroTrust bool `yaml:"zeroTrust,omitempty" json:"zeroTrust,omitempty"`
}

// PangolinTunnelConfig holds Pangolin settings.
type PangolinTunnelConfig struct {
	// ServerDomain is the Pangolin server domain.
	ServerDomain string `yaml:"serverDomain,omitempty" json:"serverDomain,omitempty"`
}

// DriftDetectionSpec configures Day-2 drift detection (advanced mode).
type DriftDetectionSpec struct {
	// Enabled activates drift detection.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Schedule in cron format (default "0 */6 * * *").
	Schedule string `yaml:"schedule,omitempty" json:"schedule,omitempty"`
}

// NodeOSSpec defines the OS configuration for a node.
type NodeOSSpec struct {
	// Family: "debian" or "rhel".
	Family string `yaml:"family,omitempty" json:"family,omitempty"`
	// Distro: "ubuntu", "debian", "rocky", "alma".
	Distro string `yaml:"distro,omitempty" json:"distro,omitempty"`
	// Version of the distro (e.g. "24.04").
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
}
