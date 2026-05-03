// Package base - Identity schemas for Layer 1 Foundation
package base

// =============================================================================
// LLDAP (Lightweight LDAP)
// =============================================================================

// #LLDAPConfig defines the Lightweight LDAP server configuration
// LLDAP provides a simplified LDAP server for homelab identity management
#LLDAPConfig: {
	// Enable LLDAP
	enabled: bool | *true

	// LLDAP version
	version: string | *"0.6.1"

	// Docker image
	image: string | *"ghcr.io/lldap/lldap"

	// Domain configuration
	domain: {
		// Base domain for LDAP (e.g., "example.com")
		base: string

		// LDAP organization name
		organization: string | *"Homelab"
	}

	// Admin configuration
	admin: {
		// Admin username
		username: string | *"admin"

		// Admin email
		email: string

		// Admin password (secret reference)
		passwordSecret: string | *"secret://lldap/admin-password"
	}

	// Network configuration
	network: {
		// HTTP port for web UI
		httpPort: uint16 & >0 & <=65535 | *17170

		// LDAP port (plaintext, usually behind firewall)
		ldapPort: uint16 & >0 & <=65535 | *3890

		// LDAPS port (TLS, recommended)
		ldapsPort: uint16 & >0 & <=65535 | *6360

		// Internal network name
		networkName: string | *"identity_net"
	}

	// Storage configuration
	storage: {
		// Data volume name
		dataVolume: string | *"lldap-data"

		// Backup enabled
		backup: bool | *true
	}

	// Traefik integration
	traefik: {
		// Enable Traefik routing
		enabled: bool | *true

		// Host rule for web UI
		host: string | *"lldap.local"

		// Enable TLS
		tls: bool | *true
	}

	// Authentication methods
	auth: {
		// Allow password authentication
		allowPassword: bool | *true

		// Allow LDAP bind authentication
		allowLdapBind: bool | *true

		// JWT token expiration (hours)
		jwtExpiration: int | *24
	}

	// Default groups to create
	defaultGroups: [...string] | *["lldap_admin", "lldap_password_manager", "users"]

	// Logging
	logging: {
		level:  "error" | "warn" | "info" | "debug" | *"info"
		format: "text" | "json" | *"text"
	}
}

// #LLDAPService generates a complete service definition for LLDAP
#LLDAPService: #ServiceDefinition & {
	name:        "lldap"
	displayName: "LLDAP Identity Server"
	image:       "ghcr.io/lldap/lldap"
	tag:         string | *"stable"
	type:        "auth"
	required:    true

	network: {
		ports: [
			{host: 17170, container: 17170, protocol: "tcp", description: "LLDAP Web UI"},
			{host: 3890, container: 3890, protocol: "tcp", description: "LDAP plaintext"},
			{host: 6360, container: 6360, protocol: "tcp", description: "LDAPS TLS"},
		]
		traefik: {
			enabled: true
			rule:    "Host(`lldap.local`)"
			port:    17170
		}
		networks: ["identity_net"]
	}

	volumes: [
		{source: "lldap-data", target: "/data", type: "volume", backup: true},
	]

	environment: {
		"LLDAP_VERBOSE":     "false"
		"LLDAP_LOG_FORMAT":  "text"
		"LLDAP_SMTP_ENABLE": "false"
	}

	environmentSecrets: {
		"LLDAP_LDAP_USER_PASS": "secret://lldap/admin-password"
		"LLDAP_JWT_SECRET":     "secret://lldap/jwt-secret"
	}

	healthCheck: {
		test:     ["CMD", "wget", "-q", "--spider", "http://localhost:17170/health"]
		interval: "30s"
		timeout:  "5s"
		retries:  3
	}

	resources: {
		memory: "256m"
		cpus:   0.2
	}

	securityContext: {
		runAsUser:              1000
		runAsGroup:             1000
		readOnlyRootFilesystem: true
		noNewPrivileges:        true
		capabilitiesDrop: ["ALL"]
	}

	labels: {
		"traefik.enable":                          "true"
		"stackkit.layer":                          "1-foundation"
		"stackkit.managed-by":                     "terraform"
		"stackkit.identity-provider":              "lldap"
	}

	config: {
		baseDN:         string
		adminEmail:     string
		adminUsername:  string | *"admin"
		defaultGroups: [...string] | *["lldap_admin", "lldap_password_manager", "users"]
	}
}

// #LLDAPGroup defines an LDAP group structure
#LLDAPGroup: {
	// Group name (unique identifier)
	name: =~"^[a-zA-Z][a-zA-Z0-9_-]*$"

	// Display name
	displayName: string

	// Group description
	description?: string

	// Group members (usernames)
	members: [...string] | *[]

	// Is this an admin group?
	isAdmin: bool | *false
}

// #LLDAPUser defines an LDAP user structure
#LLDAPUser: {
	// Username (unique identifier)
	username: =~"^[a-zA-Z][a-zA-Z0-9_-]*$"

	// Email address (required)
	email: string

	// Display name
	displayName?: string

	// First name
	firstName?: string

	// Last name
	lastName?: string

	// Groups the user belongs to
	groups: [...string] | *["users"]

	// User enabled
	enabled: bool | *true

	// Password (secret reference)
	passwordSecret?: string
}

// =============================================================================
// Step-CA (Certificate Authority)
// =============================================================================

// #StepCAConfig defines the Step-CA (Smallstep) configuration
// Provides an internal Certificate Authority for mTLS and service certificates
#StepCAConfig: {
	// Enable Step-CA
	enabled: bool | *true

	// Step-CA version
	version: string | *"0.26.1"

	// Docker image
	image: string | *"smallstep/step-ca"

	// PKI Configuration
	pki: {
		// Common name for root CA
		rootCommonName: string | *"StackKits Root CA"

		// Common name for intermediate CA
		intermediateCommonName: string | *"StackKits Intermediate CA"

		// Default certificate lifetime (hours)
		defaultCertLifetime: int | *720 // 30 days

		// Maximum certificate lifetime (hours)
		maxCertLifetime: int | *8760 // 1 year

		// Minimum key size (bits)
		minKeySize: int | *2048

		// Default key type
		keyType: "EC" | "RSA" | "OKP" | *"EC"

		// Default key curve (for EC keys)
		keyCurve: "P-256" | "P-384" | "P-521" | *"P-256"
	}

	// Network configuration
	network: {
		// CA port (HTTPS)
		port: uint16 & >0 & <=65535 | *8443

		// Health port
		healthPort: uint16 & >0 & <=65535 | *8080

		// Internal network name
		networkName: string | *"identity_net"
	}

	// Storage configuration
	storage: {
		// Data volume name
		dataVolume: string | *"step-ca-data"

		// Secrets volume name (for password and root cert)
		secretsVolume: string | *"step-ca-secrets"

		// Backup enabled
		backup: bool | *true
	}

	// Traefik integration
	traefik: {
		// Enable Traefik routing
		enabled: bool | *true

		// Host rule for CA API
		host: string | *"ca.local"

		// Enable TLS
		tls: bool | *true
	}

	// ACME configuration
	acme: {
		// Enable ACME protocol
		enabled: bool | *true

		// ACME directory path
		directory: string | *"/acme/acme/directory"
	}

	// SCEP configuration (for device enrollment)
	scep: {
		// Enable SCEP
		enabled: bool | *true

		// Challenge password (secret reference)
		challengeSecret: string | *"secret://step-ca/scep-challenge"
	}

	// JWK provisioner for automated enrollment
	jwk: {
		// Enable JWK provisioner
		enabled: bool | *true

		// Provisioner name
		name: string | *"stackkits"

		// JWK password (secret reference)
		passwordSecret: string | *"secret://step-ca/jwk-password"
	}

	// OIDC provisioner (optional, links to IdP)
	oidc: {
		// Enable OIDC provisioner
		enabled: bool | *false

		// OIDC provider name
		name: string | *"default"

		// OIDC discovery URL
		discoveryURL?: string

		// OIDC client ID
		clientId?: string

		// OIDC client secret (secret reference)
		clientSecret?: string
	}

	// Administrative access
	admin: {
		// Admin fingerprints (SSH public key fingerprints)
		fingerprints: [...string] | *[]
	}

	// Logging
	logging: {
		level:  "error" | "warn" | "info" | "debug" | *"info"
		format: "text" | "json" | *"text"
	}
}

// #StepCAService generates a complete service definition for Step-CA
#StepCAService: #ServiceDefinition & {
	name:        "step-ca"
	displayName: "Step-CA Certificate Authority"
	image:       "smallstep/step-ca"
	tag:         string | *"latest"
	type:        "auth"
	required:    true

	network: {
		ports: [
			{host: 8443, container: 8443, protocol: "tcp", description: "Step-CA HTTPS API"},
			{host: 8080, container: 8080, protocol: "tcp", description: "Step-CA Health"},
		]
		traefik: {
			enabled: true
			rule:    "Host(`ca.local`)"
			port:    8443
		}
		networks: ["identity_net"]
	}

	volumes: [
		{source: "step-ca-data", target: "/home/step", type: "volume", backup: true},
	]

	environment: {
		"TZ":                     "Europe/Berlin"
		"STEP_CA_URL":            "https://localhost:8443"
		"STEPPATH":               "/home/step"
	}

	environmentSecrets: {
		"STEP_CA_PASSWORD": "secret://step-ca/ca-password"
	}

	healthCheck: {
		test:     ["CMD", "wget", "-q", "--spider", "--no-check-certificate", "https://localhost:8080/health"]
		interval: "30s"
		timeout:  "5s"
		retries:  3
	}

	resources: {
		memory: "256m"
		cpus:   0.2
	}

	securityContext: {
		runAsUser:              1000
		runAsGroup:             1000
		readOnlyRootFilesystem: false // Step-CA needs to write to its home directory
		noNewPrivileges:        true
		capabilitiesDrop: ["ALL"]
	}

	labels: {
		"traefik.enable":                            "true"
		"stackkit.layer":                            "1-foundation"
		"stackkit.managed-by":                       "terraform"
		"stackkit.certificate-authority":            "step-ca"
	}

	config: {
		rootCN:         string | *"StackKits Root CA"
		intermediateCN: string | *"StackKits Intermediate CA"
		defaultTTL:     string | *"720h"
		maxTTL:         string | *"8760h"
	}
}

// #StepCAProvisioner defines a certificate provisioner
#StepCAProvisioner: {
	// Provisioner type
	type: "JWK" | "OIDC" | "AWS" | "GCP" | "Azure" | "ACME" | "SCEP"

	// Provisioner name (unique)
	name: string

	// Configuration based on type
	config?: _

	// Claims for this provisioner
	claims: {
		// Max certificate duration
		maxTLSDuration: string | *"720h"

		// Enable renewal
		enableRenewal: bool | *true

		// Enable revocation
		enableRevocation: bool | *true
	}
}

// #StepCACertificateRequest defines a certificate request
#StepCACertificateRequest: {
	// Subject common name
	commonName: string

	// Subject alternative names (DNS)
	dnsNames: [...string] | *[]

	// Subject alternative names (IP)
	ipAddresses: [...string] | *[]

	// Certificate duration
	duration: string | *"720h"

	// Provisioner to use
	provisioner: string | *"stackkits"
}

// #StepCAMTLSPolicy defines mTLS configuration for services
#StepCAMTLSPolicy: {
	// Enable mTLS
	enabled: bool | *true

	// Require mTLS for all internal communication
	required: bool | *true

	// Certificate renewal threshold (hours before expiry)
	renewalThreshold: int | *72

	// Auto-enrollment enabled
	autoEnrollment: bool | *true

	// Bootstrap token for initial enrollment (secret reference)
	bootstrapToken?: string
}

// =============================================================================
// PocketID Identity (replaces LLDAP as default per pocketid-postgres roadmap)
// =============================================================================

#PocketIDOwner: {
	source:   "local" | "cloud"
	email:    =~"^[^@]+@[^@]+\\.[^@]+$"
	username: =~"^[a-zA-Z][a-zA-Z0-9_-]*$"

	// PocketID v2 is passkey-only — there is no password concept.
	// Local owners are activated via a one-time-access token rendered into a
	// /setup-account?token=... URL the operator clicks once to enroll a
	// WebAuthn credential. Cloud owners federate from an external IdP.
	foreignSubjectId?:       string // for source=cloud
	passkeyEnrollmentToken?: string // optional first-login passkey link

	if source == "cloud" {
		foreignSubjectId!: string
	}
}

#TinyAuthStaticCred: {
	enabled:         bool | *true
	usernamePattern: string | *"bg-{nodename}-static"
	passwordLength:  int & >=24 | *32
	bcryptCost:      int & >=10 & <=14 | *12
	storageEnvVar:   string | *"USERS"
}
