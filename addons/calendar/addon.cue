// Package calendar - Calendar/Contacts Add-On
//
// CalDAV/CardDAV standards-based calendar and contacts:
//   - Radicale: Lightweight CalDAV/CardDAV server (Python)
//   - Bloben: Modern web UI for CalDAV calendars
//
// License:
//   - Radicale: GPL-3.0
//   - Bloben: AGPL-3.0
//
// Note: When the mail add-on (Stalwart) is active, it provides
// CalDAV natively, making this add-on redundant.
//
// Placement: Local node
//
// Usage:
//   addons: calendar: calendar.#Config & {}

package calendar

// #Config defines calendar add-on configuration
#Config: {
	_addon: {
		name:        "calendar"
		displayName: "Calendar & Contacts"
		version:     "1.0.0"
		layer:       "APPLICATION"
		description: "Radicale CalDAV/CardDAV + Bloben web UI"
	}

	enabled: bool | *true

	// Radicale configuration
	radicale: #RadicaleConfig

	// Bloben UI configuration
	bloben: #BlobenConfig
}

#RadicaleConfig: {
	// Authentication backend
	auth: *"htpasswd" | "ldap"

	// LDAP configuration (connects to LLDAP from identity stack)
	if auth == "ldap" {
		ldap: {
			url:      string | *"ldap://lldap:3890"
			baseDn:   string | *"dc=homelab,dc=local"
			bindDn:   string
			password: =~"^secret://"
		}
	}

	// Resource limits
	resources: {
		memory: string | *"128m"
		cpus:   number | *0.25
	}
}

#BlobenConfig: {
	enabled: bool | *true

	// Resource limits
	resources: {
		memory: string | *"256m"
		cpus:   number | *0.5
	}
}

// Service definitions

#RadicaleService: {
	name:        "radicale"
	displayName: "Radicale"
	image:       "tomsquest/docker-radicale:3"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 5232, host: 5232, protocol: "tcp", name: "caldav"},
	]

	volumes: [
		{name: "radicale-data", path: "/data", type: "volume"},
		{name: "radicale-config", path: "/config", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`cal.{{.domain}}`)"
	}
}

#BlobenService: {
	name:        "bloben"
	displayName: "Bloben"
	image:       "bloben/bloben-app:latest"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 8080, host: 8083, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "bloben-data", path: "/data", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`calendar.{{.domain}}`)"
	}
}

#Outputs: {
	caldavUrl:    string | *"https://cal.{{.domain}}"
	blobenUrl:    string | *"https://calendar.{{.domain}}"
	carddavUrl:   string | *"https://cal.{{.domain}}"
}
