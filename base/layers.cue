// Package base - 3-Layer Architecture Validation Schemas
package base

// =============================================================================
// INSTALLATION METHOD (shared)
// =============================================================================

// #InstallMethod defines how platform services are installed
#InstallMethod: "container" | "bare_metal" | "vm"

// =============================================================================
// BARE-METAL INSTALLATION SUPPORT (Layer 2)
// =============================================================================

// #BareMetalInstall defines bare-metal installation configuration
#BareMetalInstall: {
	// Enable bare-metal installation method
	enabled: bool | *false

	// Installation directory
	installDir: string | *"/opt/stackkits"

	// Systemd service configuration
	systemd: {
		// Create systemd service
		enabled: bool | *true

		// Service user
		user: string | *"stackkits"

		// Service group
		group: string | *"stackkits"
	}

	// Binary download configuration
	binary: {
		// Download URL template
		urlTemplate: string

		// Version to install
		version: string | *"latest"

		// Checksum verification
		verifyChecksum: bool | *true
	}

	// Reverse proxy configuration (when not using Traefik in container)
	reverseProxy?: {
		// Type of reverse proxy
		type: "nginx" | "caddy" | "traefik-binary"

		// Configuration template
		configTemplate?: string

		// ACME email for TLS
		acmeEmail?: string
	}
}

// =============================================================================
// PAAS CONFIGURATION (Layer 2)
// =============================================================================

// #PAASServiceType defines the standard PAAS platforms for normal StackKits.
// Dockge and Portainer are compose/container managers, not required PaaS
// adapters for L3 rollouts.
#PAASServiceType: "dokploy" | "coolify"

// #PAASConfig is the main PAAS configuration block for Layer 2
#PAASConfig: {
	// PAAS platform selection
	type: #PAASServiceType

	// Installation method
	installMethod: #InstallMethod | *"container"

	// Dokploy configuration (when type == "dokploy")
	dokploy?: #DokployConfig

	// Coolify configuration (when type == "coolify")
	coolify?: #CoolifyConfig

	// Portainer configuration (when type == "portainer")
	portainer?: #PortainerConfig

	// Dockge configuration (when type == "dockge")
	dockge?: #DockgeConfig
}

// #DokployConfig defines Dokploy PAAS settings
#DokployConfig: {
	enabled: bool | *true
	version: string | *"latest"
	image:   string | *"dokploy/dokploy"
	port:    uint16 & >0 & <=65535 | *3000
	database?: {
		external:          bool | *false
		postgresVersion:   string | *"16-alpine"
		connectionString?: string
	}
	traefik: {
		enabled:      bool | *true
		host?:        string
		tls:          bool | *true
		middlewares?: [...string]
	}
	storage: {
		dataVolume: string | *"dokploy-data"
		backup:     bool | *true
	}
	resources?:   #ResourceLimits
	environment?: [string]: string
}

// #CoolifyConfig defines Coolify PAAS settings
#CoolifyConfig: {
	enabled: bool | *false
	version: string | *"latest"
	image:   string | *"ghcr.io/coollabsio/coolify"
	port:    uint16 & >0 & <=65535 | *8000
	storage: {
		dataPath: string | *"/data/coolify"
		backup:   bool | *true
	}
	traefik: {
		enabled: bool | *true
		host?:   string
		tls:     bool | *true
	}
	resources?: #ResourceLimits
}

// #PortainerConfig defines Portainer container management settings
#PortainerConfig: {
	enabled:   bool | *false
	version:   string | *"latest"
	image:     string | *"portainer/portainer-ce"
	httpPort:  uint16 & >0 & <=65535 | *9000
	httpsPort: uint16 & >0 & <=65535 | *9443
	storage: {
		dataVolume: string | *"portainer-data"
		backup:     bool | *true
	}
	traefik: {
		enabled: bool | *true
		host?:   string
		tls:     bool | *true
	}
}

// #DockgeConfig defines Dockge compose management settings
#DockgeConfig: {
	enabled:   bool | *false
	version:   string | *"1"
	image:     string | *"louislam/dockge"
	port:      uint16 & >0 & <=65535 | *5001
	stacksDir: string | *"/opt/stacks"
	storage: {
		dataVolume: string | *"dockge-data"
		backup:     bool | *true
	}
	traefik: {
		enabled: bool | *true
		host?:   string
		tls:     bool | *true
	}
}

// =============================================================================
// PLATFORM IDENTITY CONFIGURATION (Layer 2)
// =============================================================================

// #PlatformIdentityConfig is the main identity configuration block for Layer 2
#PlatformIdentityConfig: {
	// TinyAuth - Lightweight identity proxy
	tinyauth?: #TinyAuthConfig

	// PocketID - OIDC provider
	pocketid?: #PocketIDConfig

	// Authelia - Full-featured authentication server
	authelia?: #AutheliaConfig

	// Authentik - Advanced identity provider
	authentik?: #AuthentikConfig
}

// #TinyAuthConfig defines TinyAuth identity proxy settings
#TinyAuthConfig: {
	enabled:       bool | *true
	version:       string | *"v3"
	image:         string | *"ghcr.io/steveiliop56/tinyauth"
	port:          uint16 & >0 & <=65535 | *3000
	installMethod: #InstallMethod | *"container"
	appUrl?:       string
	logLevel:      "debug" | "info" | "warn" | "error" | *"info"
	sessionSecret: string | *"secret://tinyauth/session-secret"
	users: {
		usersFile?: string
		definitions?: [...{
			username:     string
			passwordHash: string
			email?:       string
		}]
	}
	oauth?: {
		github?: {
			enabled:      bool | *false
			clientId:     string
			clientSecret: string
		}
		google?: {
			enabled:      bool | *false
			clientId:     string
			clientSecret: string
		}
		oidc?: {
			enabled:      bool | *false
			issuer:       string
			clientId:     string
			clientSecret: string
			scopes: [...string] | *["openid", "profile", "email"]
		}
	}
	traefik: {
		enabled:             bool | *true
		middlewareName:      string | *"tinyauth"
		authUrl?:            string
		authResponseHeaders: [...string] | *["X-User", "X-Email"]
	}
	storage: {
		dataVolume: string | *"tinyauth-data"
		backup:     bool | *true
	}
	resources?: #ResourceLimits
}

// #PocketIDConfig defines PocketID OIDC provider settings
#PocketIDConfig: {
	enabled:       bool | *false
	version:       string | *"latest"
	image:         string | *"stonith404/pocket-id"
	port:          uint16 & >0 & <=65535 | *3000
	installMethod: #InstallMethod | *"container"
	publicAppUrl:  string
	database: {
		type: "sqlite" | "postgres" | *"sqlite"
		sqlite?: {
			path: string | *"/app/data/pocket-id.db"
		}
		postgres?: {
			connectionString: string
		}
	}
	smtp?: {
		enabled:  bool | *false
		host:     string
		port:     uint16 | *587
		user:     string
		password: string
		from:     string
		tls:      "starttls" | "tls" | "none" | *"starttls"
	}
	oidc: {
		issuerUrl?:           string
		defaultScopes: [...string] | *["openid", "profile", "email"]
		accessTokenLifetime:  int | *60
		refreshTokenLifetime: int | *30
	}
	ldap?: {
		enabled:      bool | *false
		url:          string
		bindDn:       string
		bindPassword: string
		baseDn:       string
		userFilter:   string | *"(objectClass=person)"
		syncInterval: int | *60
	}
	traefik: {
		enabled: bool | *true
		host?:   string
		tls:     bool | *true
	}
	storage: {
		dataVolume: string | *"pocketid-data"
		backup:     bool | *true
	}
	resources?: #ResourceLimits
}

// #AutheliaConfig defines Authelia authentication server settings
#AutheliaConfig: {
	enabled:       bool | *false
	version:       string | *"latest"
	image:         string | *"authelia/authelia"
	port:          uint16 & >0 & <=65535 | *9091
	installMethod: #InstallMethod | *"container"
	logLevel:      "debug" | "info" | "warn" | "error" | *"info"
	authentication: {
		backend: "file" | "ldap" | *"file"
		file?: {
			path: string | *"/config/users_database.yml"
		}
		ldap?: {
			url:    string
			baseDn: string
			user?:  string
		}
	}
	session: {
		secret:   string
		name:     string | *"authelia_session"
		lifetime: string | *"1h"
		domain:   string
	}
	storage: {
		type: "sqlite" | "postgres" | "mysql" | *"sqlite"
		sqlite?: {
			path: string | *"/config/db.sqlite3"
		}
	}
	accessControl: {
		defaultPolicy: "deny" | "one_factor" | "two_factor" | *"deny"
		rules?: [...{
			domain:  string
			policy:  "bypass" | "one_factor" | "two_factor"
			subject?: [...string]
		}]
	}
	traefik: {
		enabled:        bool | *true
		middlewareName: string | *"authelia"
	}
	resources?: #ResourceLimits
}

// #AuthentikConfig defines Authentik identity provider settings
#AuthentikConfig: {
	enabled:        bool | *false
	version:        string | *"latest"
	composeProject: string | *"authentik"
	port:           uint16 & >0 & <=65535 | *9443
	installMethod:  #InstallMethod | *"container"
	database: {
		bundled:              bool | *true
		externalConnection?:  string
	}
	redis: {
		bundled: bool | *true
	}
	secretKey:          string
	bootstrapPassword?: string
	traefik: {
		enabled: bool | *true
		host?:   string
	}
	resources?: {
		server?: #ResourceLimits
		worker?: #ResourceLimits
	}
}

// =============================================================================
// LAYER 1: FOUNDATION - REQUIRED
// =============================================================================

// #Layer1Foundation validates Layer 1 requirements
#Layer1Foundation: {
	// System configuration MUST be present
	system: #SystemConfig

	// Base packages MUST be defined
	packages: #BasePackages

	// Security settings MUST be configured
	security: {
		// SSH hardening is REQUIRED
		ssh: #SSHHardening

		// Firewall policy is REQUIRED
		firewall: #FirewallPolicy
	}

	// Identity services MUST be configured (Zero-Trust requirement)
	identity: {
		// LLDAP for directory services - REQUIRED and MUST be enabled
		lldap: #LLDAPConfig & {
			enabled: true // Zero-Trust: LLDAP MUST be enabled for all StackKits
		}

		// Step-CA for certificate authority - REQUIRED and MUST be enabled
		stepCA: #StepCAConfig & {
			enabled: true // Zero-Trust: Step-CA MUST be enabled for mTLS
		}
	}

	// Virtualization environment MUST be declared
	// Defines the minimum kernel features and supported VPS environments.
	// Detected at runtime by `stackkit prepare` and enforced before deployment.
	virtualization: #VirtualizationConfig
}

// =============================================================================
// LAYER 2: PLATFORM - REQUIRED
// =============================================================================

// Platform types supported (includes bare-metal for non-container deployments)
// Note: Kubernetes (K3s/K8s) intentionally excluded — Docker-first strategy (ADR-0002)
#PlatformType: "docker" | "docker-swarm" | "bare-metal"

// #Layer2Platform validates Layer 2 requirements
#Layer2Platform: {
	// Platform type MUST be explicitly declared
	platform: #PlatformType

	// Container runtime or bare-metal configuration
	// When platform is "bare-metal", container is optional
	if platform != "bare-metal" {
		container: #ContainerRuntime
	}

	// Bare-metal installation config (when platform is "bare-metal")
	if platform == "bare-metal" {
		bareMetal?: #BareMetalInstall
	}

	// PAAS / Management services (Dokploy, Coolify, etc.)
	paas?: #PAASConfig

	// Platform-level identity services (TinyAuth, PocketID, etc.)
	// These are distinct from Layer 1 identity (LLDAP, Step-CA)
	identity?: #PlatformIdentityConfig

	// Networking base MUST be configured
	network: {
		defaults: #NetworkDefaults
	}

	// Ingress controller (Traefik is default)
	ingress?: {
		type:  "traefik" | "nginx" | "caddy" | *"traefik"
		traefik?: {
			enabled: bool | *true
			version: string | *"v3.1"
		}
	}
}

// =============================================================================
// LAYER 3: APPLICATIONS - REQUIRED
// =============================================================================

// Service role classification for Layer 3 applications
// NOTE: "paas" type is now in Layer 2, not Layer 3
#ServiceRole: "monitoring" | "management" | "proxy" | "utility" | "test" | "application" | "database" | "cache"

// #Layer3Applications validates Layer 3 requirements
#Layer3Applications: {
	// Services map
	services: [string]: #ServiceDefinition

	// Validation: All services must be proper application types (not PAAS)
	// PAAS services should be defined in Layer 2 (#PAASConfig)
	_applicationServices: [
		for name, svc in services
		if svc.type != "paas" {
			name
		},
	]

	// Layer 3 should only contain user applications
	// The PAAS platform (Dokploy, Coolify, etc.) is in Layer 2
	_hasOnlyApplicationServices: len(_applicationServices) == len(services)
}

// =============================================================================
// COMPLETE STACKKIT VALIDATION
// =============================================================================

// #ValidatedStackKit combines all 3 layers
#ValidatedStackKit: {
	// Layer 1: Foundation (embedded)
	#Layer1Foundation

	// Layer 2: Platform (embedded)
	#Layer2Platform

	// Layer 3: Applications (embedded)
	#Layer3Applications

	// Metadata requirements
	metadata: #StackKitMetadata
}

// =============================================================================
// VALIDATION ERROR TYPES
// =============================================================================

// #LayerValidationError represents a layer validation failure
#LayerValidationError: {
	layer:   "1" | "2" | "3"
	code:    string
	message: string
	field?:  string
	hint?:   string
}

// #LayerValidationResult contains validation results
#LayerValidationResult: {
	valid:  bool
	layer:  "1" | "2" | "3" | "all"
	errors: [...#LayerValidationError]
}

// =============================================================================
// LAYER METADATA
// =============================================================================

// #LayerMetadata provides information about layer configuration
#LayerMetadata: {
	// Layer name
	name: string

	// Layer version
	version: string

	// Layer description
	description: string

	// Required components
	required: [...string]

	// Optional components
	optional: [...string]
}

// Default layer metadata
#DefaultLayerMetadata: {
	layer1: #LayerMetadata & {
		name:        "foundation"
		version:     "1.0.0"
		description: "System configuration, packages, security, Layer 1 identity (LLDAP, Step-CA), and virtualization requirements"
		required: ["system", "packages", "security.ssh", "security.firewall", "identity.lldap", "identity.stepCA", "virtualization"]
		optional: ["security.container", "security.secrets", "security.tls", "security.audit", "identity.provider", "identity.pki", "identity.rbac"]
	}

	layer2: #LayerMetadata & {
		name:        "platform"
		version:     "1.0.0"
		description: "Container runtime, networking platform, PAAS management, and platform identity (TinyAuth, PocketID)"
		required: ["platform", "network.defaults"]
		optional: [
			"container",           // Optional for bare-metal platform
			"bareMetal",           // Required when platform is bare-metal
			"paas",                // PAAS management (Dokploy, Coolify, etc.)
			"identity.tinyauth",   // Platform identity proxy
			"identity.pocketid",   // Platform OIDC provider
			"identity.authelia",   // Advanced auth
			"ingress",             // Ingress controller
			"network.dns",
			"network.ntp",
			"network.vpn",
			"network.proxy",
		]
	}

	layer3: #LayerMetadata & {
		name:        "applications"
		version:     "1.0.0"
		description: "User applications and services deployed via Layer 2 PAAS"
		required: ["services"]
		optional: []
	}
}
