// Package filesharing - File Sharing Add-On
//
// Lightweight file sharing with multiple provider options:
//   - Cloudreve (default): Go-based, WebDAV, multi-storage, lightweight
//   - OpenCloud: ownCloud successor, modern architecture
//   - Nextcloud: Full-featured collaboration suite (heavier)
//
// License:
//   - Cloudreve: GPL-3.0
//   - OpenCloud: Apache-2.0
//   - Nextcloud: AGPL-3.0
//
// Placement: Local node (data sovereignty)
//
// Usage:
//   addons: "file-sharing": filesharing.#Config & {
//       provider: "cloudreve"
//   }

package filesharing

// #Config defines file sharing add-on configuration
#Config: {
	_addon: {
		name:        "file-sharing"
		displayName: "File Sharing"
		version:     "1.0.0"
		layer:       "APPLICATION"
		description: "Self-hosted file sharing and sync"
	}

	enabled: bool | *true

	// Provider selection
	provider: *"cloudreve" | "opencloud" | "nextcloud"

	// Cloudreve configuration
	if provider == "cloudreve" {
		cloudreve: #CloudreveConfig
	}

	// OpenCloud configuration
	if provider == "opencloud" {
		opencloud: #OpenCloudConfig
	}

	// Nextcloud configuration
	if provider == "nextcloud" {
		nextcloud: #NextcloudConfig
	}

	// Shared storage configuration
	storage: {
		dataPath: string | *"/data/files"
		maxUploadSize: string | *"10G"
	}
}

#CloudreveConfig: {
	// Storage backend
	storageBackend: *"local" | "s3" | "webdav"

	// WebDAV server (built-in)
	webdav: {
		enabled: bool | *true
	}

	// Resource limits
	resources: {
		memory: string | *"256m"
		cpus:   number | *0.5
	}
}

#OpenCloudConfig: {
	// Resource limits
	resources: {
		memory: string | *"512m"
		cpus:   number | *1.0
	}
}

#NextcloudConfig: {
	// Database backend
	database: *"sqlite" | "mysql" | "postgres"

	// PHP memory limit
	phpMemoryLimit: string | *"512M"

	// Resource limits
	resources: {
		memory: string | *"1024m"
		cpus:   number | *1.0
	}
}

// Service definitions

#CloudreveService: {
	name:        "cloudreve"
	displayName: "Cloudreve"
	image:       "cloudreve/cloudreve:latest"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 5212, host: 5212, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "cloudreve-data", path: "/cloudreve/uploads", type: "volume"},
		{name: "cloudreve-config", path: "/cloudreve/config", type: "volume"},
		{name: "cloudreve-db", path: "/cloudreve/db", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`files.{{.domain}}`)"
	}
}

#OpenCloudService: {
	name:        "opencloud"
	displayName: "OpenCloud"
	image:       "owncloud/opencloud:latest"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 9200, host: 9200, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "opencloud-data", path: "/var/lib/opencloud", type: "volume"},
		{name: "opencloud-config", path: "/etc/opencloud", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`files.{{.domain}}`)"
	}
}

#NextcloudService: {
	name:        "nextcloud"
	displayName: "Nextcloud"
	image:       "nextcloud:30-apache"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 80, host: 8880, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "nextcloud-data", path: "/var/www/html", type: "volume"},
		{name: "nextcloud-custom-apps", path: "/var/www/html/custom_apps", type: "volume"},
		{name: "nextcloud-config", path: "/var/www/html/config", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`files.{{.domain}}`)"
	}
}

#Outputs: {
	url:      string | *"https://files.{{.domain}}"
	provider: string
	webdav:   bool
}
