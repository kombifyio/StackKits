// Package vault - Password Manager Add-On
//
// Vaultwarden: Bitwarden-compatible password manager.
// Lightweight Rust implementation, perfect for self-hosting.
//
// License: AGPL-3.0
// Placement: Cloud node (always accessible)
//
// Usage:
//   addons: vault: vault.#Config & {
//       signupsAllowed: false
//   }

package vault

// #Config defines vault add-on configuration
#Config: {
	_addon: {
		name:        "vault"
		displayName: "Password Manager"
		version:     "1.0.0"
		layer:       "APPLICATION"
		description: "Vaultwarden - Bitwarden-compatible password manager"
	}

	enabled: bool | *true

	// Allow new user signups
	signupsAllowed: bool | *false

	// Admin panel
	adminToken?: =~"^secret://"

	// WebSocket notifications
	websocketEnabled: bool | *true

	// SMTP for email notifications
	smtp?: {
		host:     string
		port:     uint16 | *587
		from:     string
		username: string
		password: =~"^secret://"
		security: "starttls" | "force_tls" | *"starttls"
	}

	// Resource limits
	resources: {
		memory: string | *"256m"
		cpus:   number | *0.5
	}
}

// Service definition
#VaultwardenService: {
	name:        "vaultwarden"
	displayName: "Vaultwarden"
	image:       "vaultwarden/server:1.32.5"
	category:    "application"

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	ports: [
		{container: 80, host: 8200, protocol: "tcp", name: "web"},
		{container: 3012, host: 3012, protocol: "tcp", name: "websocket"},
	]

	volumes: [
		{name: "vaultwarden-data", path: "/data", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`vault.{{.domain}}`)"
	}

	environment: {
		SIGNUPS_ALLOWED:          string
		WEBSOCKET_ENABLED:        "true"
		DOMAIN:                   string
		ROCKET_PORT:              "80"
	}
}

#Outputs: {
	url:      string | *"https://vault.{{.domain}}"
	webVault: bool | *true
}
