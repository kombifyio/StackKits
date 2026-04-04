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
	Metadata     StackKitMetadata       `yaml:"metadata" json:"metadata"`
	SupportedOS  []string               `yaml:"supportedOS" json:"supportedOS"`
	Requirements Requirements           `yaml:"requirements" json:"requirements"`
	Modes        Modes                  `yaml:"modes" json:"modes"`
	UseCases     map[string]UseCaseDef  `yaml:"useCases,omitempty" json:"useCases,omitempty"`
	Platform     map[string]PlatformDef `yaml:"platform,omitempty" json:"platform,omitempty"`
	Features     Features               `yaml:"features,omitempty" json:"features,omitempty"`
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

	// Reverse proxy backend — determines which Traefik instance routes platform services
	ReverseProxyStandalone = "standalone" // StackKit deploys its own Traefik
	ReverseProxyDokploy    = "dokploy"    // Platform services use Dokploy's Traefik
	ReverseProxyCoolify    = "coolify"    // Platform services use Coolify's Traefik

	// Domain constants
	DomainKombifyMe = "kombify.me"
	DomainHomelab   = "homelab"
	DomainHomeLab   = "home.lab"
)

// IsKombifyMeDomain returns true for the managed kombify.me shared domain.
func IsKombifyMeDomain(domain string) bool {
	return strings.EqualFold(domain, DomainKombifyMe)
}

// IsLocalDomain returns true for local-only or non-routable domains.
func IsLocalDomain(domain string) bool {
	if domain == "" || domain == DomainHomelab || domain == DomainHomeLab || domain == "stack.local" {
		return true
	}

	localSuffixes := []string{".local", ".lab", ".lan", ".home", ".homebase"}
	for _, suffix := range localSuffixes {
		if strings.HasSuffix(domain, suffix) {
			return true
		}
	}

	return false
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

// UseCaseDef defines a use case in stackkit.yaml (v5).
type UseCaseDef struct {
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
	Name            string            `yaml:"name" json:"name"`
	StackKit        string            `yaml:"stackkit" json:"stackkit"`
	Mode            string            `yaml:"mode,omitempty" json:"mode,omitempty"`
	Runtime         string            `yaml:"runtime,omitempty" json:"runtime,omitempty"` // "docker" or "native"
	Context         string            `yaml:"context,omitempty" json:"context,omitempty"`
	Domain          string            `yaml:"domain,omitempty" json:"domain,omitempty"`
	SubdomainPrefix string            `yaml:"subdomainPrefix,omitempty" json:"subdomainPrefix,omitempty"`
	Email           string            `yaml:"email,omitempty" json:"email,omitempty"`
	AdminEmail      string            `yaml:"adminEmail,omitempty" json:"adminEmail,omitempty"`
	Network         NetworkSpec       `yaml:"network,omitempty" json:"network,omitempty"`
	Compute         ComputeSpec       `yaml:"compute,omitempty" json:"compute,omitempty"`
	Storage         StorageSpec       `yaml:"storage,omitempty" json:"storage,omitempty"`
	SSH             SSHSpec           `yaml:"ssh,omitempty" json:"ssh,omitempty"`
	Nodes           []NodeSpec        `yaml:"nodes,omitempty" json:"nodes,omitempty"`
	TLS             TLSSpec           `yaml:"tls,omitempty" json:"tls,omitempty"`
	PAAS            string            `yaml:"paas,omitempty" json:"paas,omitempty"` // "dokploy", "coolify", "dockge", "none" (auto-detected from tier if empty)
	Addons          []string          `yaml:"addons,omitempty" json:"addons,omitempty"`
	Services        map[string]any    `yaml:"services,omitempty" json:"services,omitempty"`
	Environment     map[string]string `yaml:"environment,omitempty" json:"environment,omitempty"`
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
}

// NetworkSpec defines network configuration
type NetworkSpec struct {
	Mode    string `yaml:"mode" json:"mode"` // "local", "public", "hybrid"
	Subnet  string `yaml:"subnet,omitempty" json:"subnet,omitempty"`
	Gateway string `yaml:"gateway,omitempty" json:"gateway,omitempty"`
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
}

// NodeSpec defines a deployment node
type NodeSpec struct {
	Name     string   `yaml:"name" json:"name"`
	Role     string   `yaml:"role" json:"role"` // "control-plane", "worker", "standalone"
	IP       string   `yaml:"ip" json:"ip"`
	Services []string `yaml:"services,omitempty" json:"services,omitempty"`
}

// DeploymentState represents the current deployment state
type DeploymentState struct {
	StackKit    string           `yaml:"stackkit" json:"stackkit"`
	Mode        string           `yaml:"mode" json:"mode"`
	Status      DeploymentStatus `yaml:"status" json:"status"`
	LastApplied time.Time        `yaml:"lastApplied" json:"lastApplied"`
	TofuState   string           `yaml:"tofuState,omitempty" json:"tofuState,omitempty"`
	Services    []ServiceState   `yaml:"services" json:"services"`
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
// registers itself for Direct Connect (Kong proxies directly to it).
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
// based on the PAAS selection. When PAAS manages its own Traefik (Dokploy, Coolify),
// platform services attach to that Traefik instead of deploying a separate one.
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

// ResolvePAASForContext determines the PAAS platform from explicit setting,
// runtime context, and finally compute tier.
func (s *StackSpec) ResolvePAASForContext(ctx NodeContext) string {
	if s.PAAS != "" {
		return s.PAAS
	}

	if ctx == ContextCloud {
		if s.HasCustomPublicDomain() {
			return PAASCoolify
		}
		return PAASDokploy
	}

	return s.resolvePAASByTier()
}

func (s *StackSpec) resolvePAASByTier() string {
	tier := s.Compute.Tier
	if tier == "" {
		tier = ComputeTierStandard
	}

	switch tier {
	case ComputeTierLow:
		return PAASDockge
	default:
		return PAASDokploy
	}
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
