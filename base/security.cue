// Package base - Security configuration schemas
package base

// #SSHHardening defines SSH security settings
#SSHHardening: {
	// SSH port
	port: uint16 & >0 & <=65535 | *22

	// Permit root login
	permitRootLogin: "yes" | "no" | "prohibit-password" | "forced-commands-only" | *"no"

	// Password authentication
	passwordAuth: bool | *false

	// Public key authentication
	pubkeyAuth: bool | *true

	// Max authentication attempts
	maxAuthTries: int & >=1 & <=10 | *3

	// Login grace time in seconds
	loginGraceTime: int & >=10 & <=300 | *60

	// Allow TCP forwarding
	allowTcpForwarding: bool | *false

	// Allow agent forwarding
	allowAgentForwarding: bool | *false

	// X11 forwarding
	x11Forwarding: bool | *false

	// Allowed users (empty = all)
	allowUsers: [...string] | *[]

	// Allowed groups
	allowGroups: [...string] | *[]

	// Client alive interval
	clientAliveInterval: int | *300

	// Client alive count max
	clientAliveCountMax: int | *3

	// Banner message
	banner?: string

	// Additional sshd_config options
	extraConfig?: [string]: string
}

// #FirewallPolicy defines firewall rules
#FirewallPolicy: {
	// Enable firewall
	enabled: bool | *true

	// Firewall backend
	backend: "ufw" | "firewalld" | "iptables" | "nftables" | *"ufw"

	// Default inbound policy
	defaultInbound: "allow" | "deny" | *"deny"

	// Default outbound policy
	defaultOutbound: "allow" | "deny" | *"allow"

	// Default forward policy
	defaultForward: "allow" | "deny" | *"deny"

	// Firewall rules
	rules: [...#FirewallRule] | *[]

	// Rate limiting
	rateLimit?: {
		enabled:    bool | *true
		maxRetries: int | *6
		findTime:   int | *600
		banTime:    int | *600
	}
}

// #FirewallRule defines a single firewall rule
#FirewallRule: {
	// Port number
	port: uint16 & >0 & <=65535

	// Protocol
	protocol: "tcp" | "udp" | "both" | *"tcp"

	// Source (IP, CIDR, or "any")
	source: string | *"any"

	// Destination (IP, CIDR, or "any")
	destination: string | *"any"

	// Action
	action: "allow" | "deny" | "reject" | *"allow"

	// Direction
	direction: "in" | "out" | "both" | *"in"

	// Comment/description
	comment?: string

	// Rule enabled
	enabled: bool | *true
}

// #ContainerSecurityContext defines container security settings
// Based on Docker hardening best practices
#ContainerSecurityContext: {
	// Run as non-root
	runAsNonRoot: bool | *true

	// User ID (e.g., 99:100 for nobody:users on Unraid)
	runAsUser?: int

	// Group ID
	runAsGroup?: int

	// Read-only root filesystem (recommended for security)
	readOnlyRootFilesystem: bool | *false

	// Privileged mode (never enable unless absolutely required)
	privileged: bool | *false

	// Capabilities to add (only if strictly needed after dropping ALL)
	capabilitiesAdd: [...string] | *[]

	// Capabilities to drop (drop ALL by default, add back only what's needed)
	capabilitiesDrop: [...string] | *["ALL"]

	// Seccomp profile
	seccompProfile: "RuntimeDefault" | "Unconfined" | "Localhost" | *"RuntimeDefault"

	// AppArmor profile
	appArmorProfile?: string

	// No new privileges (prevent privilege escalation)
	noNewPrivileges: bool | *true

	// Disable TTY and stdin (reduces attack surface)
	tty: bool | *false
	stdinOpen: bool | *false
}

// #ContainerResourceLimits defines resource constraints
// Prevents containers from consuming all host resources
#ContainerResourceLimits: {
	// Memory limit (e.g., "512m", "2g")
	memLimit?: string

	// Memory reservation (soft limit)
	memReservation?: string

	// CPU limit (number of CPUs, e.g., 2.5)
	cpus?: number

	// CPU shares (relative weight)
	cpuShares?: int

	// PID limit (prevent fork bombs)
	pidsLimit: int | *512
}

// #ContainerTmpfs defines tmpfs mount configuration
// Prevents payload execution from /tmp
#ContainerTmpfs: {
	// Mount path
	path: string | *"/tmp"

	// Options: noexec prevents execution, nosuid ignores SUID, nodev ignores devices
	options: string | *"rw,noexec,nosuid,nodev"

	// Size limit
	size: string | *"512m"
}

// #ContainerLogging defines container log settings
// Prevents logging bombs that could fill disk
#ContainerLogging: {
	// Log driver
	driver: "json-file" | "syslog" | "journald" | "none" | *"json-file"

	// Options for json-file driver
	options: {
		// Max size per log file
		maxSize: string | *"50m"
		// Number of log files to keep
		maxFile: string | *"5"
	}
}

// #ContainerNetworkSecurity defines network isolation settings
#ContainerNetworkSecurity: {
	// Network mode
	networkMode: "bridge" | "host" | "none" | "custom" | *"bridge"

	// Custom network name (if networkMode = custom)
	networkName?: string

	// Isolate in DMZ network for public-facing containers
	dmzIsolation: bool | *false

	// Disable inter-container communication (ICC)
	disableICC: bool | *false

	// DNS servers (override container DNS)
	dnsServers: [...string] | *[]
}

// #DockerHardeningProfile combines all container security settings
#DockerHardeningProfile: {
	// Profile name
	name: "minimal" | "standard" | "hardened" | "paranoid" | *"standard"

	// Security context
	securityContext: #ContainerSecurityContext

	// Resource limits
	resourceLimits: #ContainerResourceLimits

	// Tmpfs configuration for /tmp
	tmpfs?: #ContainerTmpfs

	// Logging configuration
	logging: #ContainerLogging

	// Network security
	networkSecurity: #ContainerNetworkSecurity

	// Volume mount policy
	volumePolicy: {
		// Prefer read-only mounts for data
		preferReadOnly: bool | *true
		// Allowed bind mount paths (empty = no restrictions)
		allowedBindPaths: [...string] | *[]
	}
}

// #SecretsPolicy defines how secrets are managed.
// Default for homelab: SOPS + age (encrypted in Git, no server component).
// Doppler is dev-only, NOT a self-hosting solution.
#SecretsPolicy: {
	// Secrets backend
	backend: "file" | "env" | "vault" | "sops-age" | *"sops-age"

	// Secrets directory inside containers
	secretsDir: string | *"/run/secrets"

	// File permissions for secrets
	fileMode: string | *"0400"

	// Vault configuration (if backend = vault)
	vault?: {
		address: string
		role:    string
		path:    string | *"secret"
	}

	// SOPS + age configuration (default backend for homelab)
	sopsAge: #SOPSAgeConfig | *{
		keyFile:      "~/.config/sops/age/keys.txt"
		encryptedDir: "secrets/"
		creationRules: [{
			pathRegex: "secrets/.*\\.enc\\.yaml$"
			age:       "" // Set at deploy time from keyFile
		}]
	}
}

// #SOPSAgeConfig defines SOPS + age encryption settings.
// Secrets are encrypted at rest in Git using age public key.
// `stackkit generate` decrypts them using the age private key
// and injects values into deployment artifacts.
//
// Workflow:
//   1. Generate age keypair: `age-keygen -o keys.txt`
//   2. Encrypt: `sops --encrypt --age <public-key> secrets.yaml > secrets.enc.yaml`
//   3. Commit encrypted file to Git (safe — encrypted)
//   4. `stackkit generate` reads keys.txt, decrypts, produces tfvars
//   5. `stackkit apply` deploys with decrypted values
//
// age is chosen over PGP because: simpler, no key server, smaller keys,
// no web of trust complexity. Over KMS because: no cloud dependency.
#SOPSAgeConfig: {
	// Path to age private key file (never committed to Git)
	keyFile: string | *"~/.config/sops/age/keys.txt"

	// Directory containing encrypted secret files
	encryptedDir: string | *"secrets/"

	// SOPS creation rules (maps file patterns to age recipients)
	creationRules: [...#SOPSCreationRule] | *[]

	// Encrypted file suffix (helps identify encrypted vs plain files)
	encryptedSuffix: string | *".enc.yaml"
}

// #SOPSCreationRule maps file patterns to encryption recipients.
#SOPSCreationRule: {
	// Regex for file paths this rule applies to
	pathRegex: string

	// age public key (recipient) — the encrypted-to key
	age: string
}

// #GitleaksConfig defines pre-commit secret scanning settings.
// Prevents accidental commits of unencrypted secrets.
#GitleaksConfig: {
	// Enable gitleaks pre-commit hook
	enabled: bool | *true

	// Allow-listed paths (e.g., encrypted files are OK to commit)
	allowPaths: [...string] | *["secrets/.*\\.enc\\.yaml$"]

	// Additional rules
	additionalRules: [...string] | *[]
}

// #TLSPolicy defines TLS/SSL settings
#TLSPolicy: {
	// Minimum TLS version
	minVersion: "1.2" | "1.3" | *"1.2"

	// Require TLS for all services
	requireTLS: bool | *true

	// Certificate source
	certSource: "acme" | "self-signed" | "manual" | *"acme"

	// ACME provider
	acmeProvider: "letsencrypt" | "letsencrypt-staging" | "zerossl" | "buypass" | *"letsencrypt"

	// ACME email
	acmeEmail?: string

	// ACME challenge type
	acmeChallenge: "http" | "dns" | "tls-alpn" | *"http"

	// DNS provider for ACME DNS challenge
	acmeDnsProvider?: string

	// Certificate renewal threshold (days)
	renewalThreshold: int & >=7 & <=60 | *30

	// HSTS enabled
	hsts: bool | *true

	// HSTS max age (seconds)
	hstsMaxAge: int | *31536000

	// HSTS include subdomains
	hstsIncludeSubdomains: bool | *true

	// HSTS preload
	hstsPreload: bool | *false
}

// #AuditConfig defines audit logging settings
#AuditConfig: {
	// Enable audit logging
	enabled: bool | *false

	// Audit log path
	logPath: string | *"/var/log/audit/audit.log"

	// Max log file size (MB)
	maxLogSize: int | *50

	// Number of log files to keep
	numLogs: int | *5

	// Events to audit
	events: [...string] | *["auth", "sudo", "file-access"]

	// Syscalls to audit
	syscalls: [...string] | *[]
}

// =============================================================================
// ZERO-TRUST IDENTITY ARCHITECTURE (kombify Identity Plan)
// =============================================================================

// #IdentityProvider defines the identity provider configuration
#IdentityProvider: {
	// Provider type (pocketid for local passkeys, lldap for directory, tinyauth for lightweight proxy, external for any OIDC)
	type: "pocketid" | "lldap" | "tinyauth" | "external" | *"pocketid"

	// Provider name
	name: string

	// OIDC endpoint (for pocketid, external OIDC providers)
	oidcEndpoint?: string

	// LDAP endpoint (for lldap)
	ldapEndpoint?: string

	// Client ID for OIDC
	clientId?: string

	// Whether this is the primary provider
	primary: bool | *false

	// Supported auth methods
	authMethods: [...#AuthMethod] | *["passkey"]

	// Scopes to request
	scopes: [...string] | *["openid", "profile", "email", "groups"]
}

// #AuthMethod defines supported authentication methods
#AuthMethod: "passkey" | "password" | "mfa" | "certificate" | "oauth"

// #ZeroTrustPolicy defines the zero-trust security policy
// All settings are defaults - users can adjust any setting to match their needs
#ZeroTrustPolicy: {
	// Enable zero-trust mode (recommended default, can be disabled)
	enabled: bool | *true

	// Device trust via mTLS
	deviceTrust: {
		enabled:       bool | *true
		requireCert:   bool | *true
		certAuthority: string | *"step-ca"
		// SCEP enrollment enabled
		scepEnrollment: bool | *true
	}

	// Identity trust via OIDC
	identityTrust: {
		enabled: bool | *true
		// Passkey recommended by default, but password auth is fully supported
		requirePasskey: bool | *true
		// Password fallback - set to true if you prefer username+password
		allowPasswordFallback: bool | *false
		// MFA for admin operations (optional)
		requireMfaForAdmin: bool | *true
	}

	// Network segmentation
	networkSegmentation: {
		enabled: bool | *true
		zones: [...#NetworkZone] | *[
				{name: "mgmt", access: "admin-only"},
				{name: "apps", access: "authenticated"},
				{name: "dmz", access: "public"},
		]
	}
}

// #NetworkZone defines a network zone for segmentation
#NetworkZone: {
	name:   string
	access: "public" | "authenticated" | "admin-only" | "internal-only"
	// CIDR range (optional)
	cidr?: string
	// VLAN ID (optional)
	vlanId?: int
}

// #PKIConfig defines PKI and certificate authority settings
#PKIConfig: {
	// PKI backend
	backend: "step-ca" | "vault-pki" | "cfssl" | "manual" | *"step-ca"

	// CA endpoint
	caEndpoint?: string

	// ACME endpoint for automated certificate issuance
	acmeEndpoint?: string

	// SCEP endpoint for device enrollment
	scepEndpoint?: string

	// Default certificate validity (hours)
	certValidityHours: int | *720 // 30 days

	// Intermediate CA configuration
	intermediate?: {
		commonName: string
		validityDays: int | *365
	}

	// Enable mTLS for internal services
	internalMTLS: bool | *true

	// SPIFFE configuration for workload identity
	spiffe?: {
		enabled:     bool | *true
		trustDomain: string
	}
}

// #ServiceIdentity defines identity for workloads/agents
#ServiceIdentity: {
	// Identity type
	type: "spiffe" | "mtls" | "oauth-client" | *"mtls"

	// SPIFFE ID (if type = spiffe)
	spiffeId?: string

	// OAuth2 client credentials (if type = oauth-client)
	oauth?: {
		clientId: string
		scopes: [...string]
	}

	// Certificate common name (if type = mtls)
	certCN?: string

	// Short-lived credential rotation
	rotationEnabled: bool | *true
	rotationInterval: string | *"24h"
}

// #RBACPolicy defines role-based access control
#RBACPolicy: {
	// Enable RBAC
	enabled: bool | *true

	// Role source (lldap groups, IdP claims, local)
	roleSource: "lldap" | "idp-claims" | "local" | *"lldap"

	// Standard roles (from kombify identity plan)
	roles: [...#Role] | *[
			{name: "owner", permissions: ["*"]},
			{name: "operator", permissions: ["deploy", "update", "monitor", "backup"]},
			{name: "developer", permissions: ["deploy", "logs", "exec"]},
			{name: "viewer", permissions: ["read", "logs"]},
	]

	// Map external groups to internal roles
	groupMappings: [...#GroupMapping] | *[]
}

// #Role defines a role with permissions
#Role: {
	name: string
	permissions: [...string]
	// Optional: restrict to specific resources
	resources?: [...string]
}

// #GroupMapping maps IdP groups to internal roles
#GroupMapping: {
	externalGroup: string
	internalRole:  string
}

// #AccessProfile defines access configuration presets
#AccessProfile: "local-only" | "tunnel-only" | "vpn-only" | "vpn-plus-tunnel" | "full-zero-trust"

// #ExternalAccess defines how the homelab is accessed from outside
#ExternalAccess: {
	// Access profile preset
	profile: #AccessProfile | *"local-only"

	// Cloudflare Tunnel configuration
	tunnel?: {
		enabled:     bool | *false
		tunnelToken: string
		// Public hostnames exposed via tunnel
		hostnames: [...string]
	}

	// VPN configuration
	vpn?: {
		enabled:  bool | *false
		type:     "wireguard" | "openvpn" | "tailscale" | *"wireguard"
		endpoint: string
		// Identity-aware VPN (connects to same IdP)
		identityAware: bool | *false
	}

	// Require mTLS even for tunneled traffic
	requireMTLSForTunnel: bool | *true

	// Require OIDC login after VPN/tunnel
	requireOIDCAfterAccess: bool | *true
}

// #EmergencyAccess defines disaster recovery access paths
#EmergencyAccess: {
	// Enable emergency admin account
	enabled: bool | *true

	// Username for emergency access
	username: string | *"emergency-admin"

	// Restrict to management VLAN only
	restrictToMgmtVLAN: bool | *true

	// Allowed source IPs (empty = mgmt VLAN only)
	allowedSources: [...string] | *[]

	// Offline mode fallback (use local lldap when IdP unreachable)
	offlineFallback: bool | *true
}

// #IdentityLifecycle defines provisioning and offboarding
#IdentityLifecycle: {
	// Automated provisioning from SaaS layer
	autoProvision: bool | *true

	// Provision source
	provisionSource: "kombifysphere" | "lldap" | "manual" | *"lldap"

	// Offboarding policy
	offboarding: {
		// Immediate token invalidation
		immediateTokenRevoke: bool | *true
		// Remove from all groups
		removeGroupMemberships: bool | *true
		// Archive activity logs
		archiveActivity: bool | *true
	}
}
