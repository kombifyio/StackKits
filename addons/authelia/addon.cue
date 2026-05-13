// Package authelia - Advanced Auth Add-On
//
// Authelia: Full-featured authentication and authorization server.
// Replaces TinyAuth when advanced features are needed:
//   - Multi-factor authentication (TOTP, WebAuthn, Duo)
//   - Single Sign-On (SSO) via OpenID Connect
//   - Access control policies
//   - Session management
//
// License: Apache-2.0
// Placement: Cloud node (replaces TinyAuth)
//
// Usage:
//   addons: authelia: authelia.#Config & {
//       mfa: totp: enabled: true
//   }

package authelia

// #Config defines authelia add-on configuration
#Config: {
	_addon: {
		name:        "authelia"
		displayName: "Authelia"
		version:     "1.0.0"
		layer:       "IDENTITY"
		description: "Advanced auth server (replaces TinyAuth)"
	}

	enabled: bool | *true

	// MFA configuration
	mfa: {
		totp: {
			enabled: bool | *true
		}
		webauthn: {
			enabled: bool | *false
		}
		duo?: {
			hostname:      string
			integrationKey: =~"^secret://"
			secretKey:      =~"^secret://"
		}
	}

	// Session configuration
	session: {
		domain:     string
		expiration: string | *"1h"
		inactivity: string | *"5m"
		rememberMe: string | *"1M"
	}

	// Storage backend
	storage: {
		backend: *"sqlite" | "postgres" | "mysql"
		path:    string | *"/config/db.sqlite3"
	}

	// LDAP backend (connects to LLDAP)
	ldap: {
		url:             string | *"ldap://lldap:3890"
		baseDn:          string | *"dc=homelab,dc=local"
		usernameAttribute: string | *"uid"
		additionalUsersDn: string | *"ou=people"
		additionalGroupsDn: string | *"ou=groups"
	}

	// Access control policies
	accessControl: {
		defaultPolicy: *"deny" | "one_factor" | "two_factor" | "bypass"
	}

	// Resource limits
	resources: {
		memory: string | *"256m"
		cpus:   number | *0.5
	}
}

// Service definition

#AutheliaService: {
	name:        "authelia"
	displayName: "Authelia"
	image:       "authelia/authelia:4"
	category:    "auth"

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	ports: [
		{container: 9091, host: 9091, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "authelia-config", path: "/config", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`auth.{{.domain}}`)"
	}

	environment: {
		TZ: string | *"Europe/Berlin"
	}
}

#Outputs: {
	url:          string | *"https://auth.{{.domain}}"
	forwardAuth:  string | *"http://authelia:9091/api/verify?rd=https://auth.{{.domain}}"
	mfaEnabled:   bool
}
