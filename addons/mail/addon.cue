// Package mail - Mail Server Add-On
//
// Stalwart: Modern all-in-one mail server.
// Supports IMAP, JMAP, SMTP, and includes CalDAV/CardDAV built-in.
//
// License: AGPL-3.0 (dual-licensed with commercial option)
//
// Key features:
//   - IMAP4rev2/JMAP for email
//   - SMTP with DKIM, SPF, DMARC
//   - CalDAV/CardDAV built-in (replaces standalone Radicale)
//   - Web-based admin interface
//   - Spam filter (built-in sieve)
//   - Full-text search
//
// Note: When this add-on is active, the calendar add-on is
// automatically disabled since Stalwart provides CalDAV natively.
//
// Placement: Cloud node (needs reliable MX records)
//
// Usage:
//   addons: mail: mail.#Config & {
//       domain: "mail.example.com"
//   }

package mail

// #Config defines mail add-on configuration
#Config: {
	_addon: {
		name:        "mail"
		displayName: "Mail Server"
		version:     "1.0.0"
		layer:       "APPLICATION"
		description: "Stalwart - All-in-one mail server (IMAP/JMAP/SMTP + CalDAV)"
	}

	enabled: bool | *true

	// Mail domain
	domain: string

	// Hostname for the mail server
	hostname: string | *"mail.{{.domain}}"

	// DKIM configuration
	dkim: {
		enabled: bool | *true
		selector: string | *"default"
	}

	// Spam filtering
	spam: {
		enabled: bool | *true
	}

	// CalDAV/CardDAV (built-in to Stalwart)
	caldav: {
		enabled: bool | *true
	}

	// Storage backend
	storage: {
		backend: *"rocksdb" | "sqlite" | "postgres"
		dataPath: string | *"/data/mail"
	}

	// TLS (via Traefik or standalone)
	tls: {
		provider: *"traefik" | "letsencrypt-standalone"
	}

	// Resource limits
	resources: {
		memory: string | *"1024m"
		cpus:   number | *1.0
	}
}

// Service definition

#StalwartService: {
	name:        "stalwart"
	displayName: "Stalwart Mail"
	image:       "stalwartlabs/mail-server:v0.10"
	category:    "application"

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	ports: [
		{container: 25, host: 25, protocol: "tcp", name: "smtp"},
		{container: 465, host: 465, protocol: "tcp", name: "smtps"},
		{container: 587, host: 587, protocol: "tcp", name: "submission"},
		{container: 993, host: 993, protocol: "tcp", name: "imaps"},
		{container: 443, host: 8443, protocol: "tcp", name: "jmap-https"},
		{container: 8080, host: 8084, protocol: "tcp", name: "admin"},
	]

	volumes: [
		{name: "stalwart-data", path: "/opt/stalwart-mail", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`mail.{{.domain}}`)"
	}

	environment: {
		STALWART_HOSTNAME: string
	}
}

#Outputs: {
	smtpHost:  string | *"mail.{{.domain}}"
	imapHost:  string | *"mail.{{.domain}}"
	jmapUrl:   string | *"https://mail.{{.domain}}"
	adminUrl:  string | *"https://mail.{{.domain}}:8084"
	caldavUrl: string | *"https://mail.{{.domain}}/dav"
}
