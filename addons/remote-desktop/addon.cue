// Package remotedesktop - Remote Desktop Add-On
//
// Apache Guacamole: Clientless remote desktop gateway.
// Supports RDP, SSH, VNC, and Telnet via web browser.
// No client software needed - everything runs in the browser.
//
// License: Apache-2.0
// Placement: Local node (needs access to target machines)
//
// Usage:
//   addons: "remote-desktop": remotedesktop.#Config & {
//       connections: [{
//           name: "workstation"
//           protocol: "rdp"
//           hostname: "192.168.1.100"
//       }]
//   }

package remotedesktop

// #Config defines remote desktop add-on configuration
#Config: {
	_addon: {
		name:        "remote-desktop"
		displayName: "Remote Desktop"
		version:     "1.0.0"
		layer:       "APPLICATION"
		description: "Apache Guacamole - Browser-based remote desktop"
	}

	enabled: bool | *true

	// Pre-configured connections
	connections: [...#ConnectionConfig] | *[]

	// Authentication backend
	auth: *"database" | "ldap"

	// LDAP configuration (connects to LLDAP)
	if auth == "ldap" {
		ldap: {
			hostname: string | *"lldap"
			port:     uint16 | *3890
			baseDn:   string | *"dc=homelab,dc=local"
			userBaseDn: string | *"ou=people,dc=homelab,dc=local"
		}
	}

	// Recording
	recording: {
		enabled: bool | *false
		path:    string | *"/recordings"
	}

	// Resource limits
	resources: {
		guacd: {
			memory: string | *"512m"
			cpus:   number | *1.0
		}
		web: {
			memory: string | *"512m"
			cpus:   number | *0.5
		}
	}
}

#ConnectionConfig: {
	name:     string
	protocol: "rdp" | "vnc" | "ssh" | "telnet"
	hostname: string
	port?:    uint16
	username?: string
	password?: =~"^secret://"
}

// Service definitions

#GuacamoleService: {
	name:        "guacamole"
	displayName: "Apache Guacamole"
	image:       "guacamole/guacamole:1.5.5"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 8080, host: 8088, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "guacamole-drive", path: "/drive", type: "volume"},
		{name: "guacamole-record", path: "/record", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`remote.{{.domain}}`)"
	}

	environment: {
		GUACD_HOSTNAME:   "guacd"
		GUACD_PORT:       "4822"
		POSTGRES_HOSTNAME: "guacamole-postgres"
		POSTGRES_DATABASE: "guacamole_db"
		POSTGRES_USER:     "guacamole_user"
		POSTGRES_PASSWORD: string
	}
}

#GuacdService: {
	name:        "guacd"
	displayName: "Guacamole Daemon"
	image:       "guacamole/guacd:1.5.5"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	volumes: [
		{name: "guacd-drive", path: "/drive", type: "volume"},
		{name: "guacd-record", path: "/record", type: "volume"},
	]
}

#GuacamolePostgresService: {
	name:        "guacamole-postgres"
	displayName: "Guacamole PostgreSQL"
	image:       "postgres:16-alpine"
	category:    "database"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	volumes: [
		{name: "guacamole-pgdata", path: "/var/lib/postgresql/data", type: "volume"},
	]

	environment: {
		POSTGRES_DB:       "guacamole_db"
		POSTGRES_USER:     "guacamole_user"
		POSTGRES_PASSWORD: string
	}
}

#Outputs: {
	url:         string | *"https://remote.{{.domain}}"
	protocols:   [...string] | *["rdp", "ssh", "vnc"]
}
