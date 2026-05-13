// Package devplatform - Development Platform Add-On
//
// Self-hosted development platform:
//   - Gitea: Lightweight Git hosting (GitHub alternative)
//   - Woodpecker CI: Container-native CI/CD
//
// License:
//   - Gitea: MIT
//   - Woodpecker CI: Apache-2.0
//
// Placement: Local node
//
// Usage:
//   addons: "dev-platform": devplatform.#Config & {
//       woodpecker: enabled: true
//   }

package devplatform

// #Config defines dev platform add-on configuration
#Config: {
	_addon: {
		name:        "dev-platform"
		displayName: "Development Platform"
		version:     "1.0.0"
		layer:       "APPLICATION"
		description: "Gitea + Woodpecker CI for self-hosted development"
	}

	enabled: bool | *true

	// Gitea configuration
	gitea: #GiteaConfig

	// Woodpecker CI configuration
	woodpecker: #WoodpeckerConfig
}

#GiteaConfig: {
	// Database backend
	database: *"sqlite" | "postgres" | "mysql"

	// SSH port for git operations
	sshPort: uint16 | *2222

	// Disable registration after setup
	disableRegistration: bool | *true

	// LFS (Large File Storage)
	lfsEnabled: bool | *true

	// Resource limits
	resources: {
		memory: string | *"512m"
		cpus:   number | *1.0
	}
}

#WoodpeckerConfig: {
	enabled: bool | *true

	// Max concurrent pipelines
	maxProcs: int | *2

	// Agent configuration
	agent: {
		// Number of agents
		count: int | *1

		// Resource limits per agent
		resources: {
			memory: string | *"512m"
			cpus:   number | *1.0
		}
	}
}

// Service definitions

#GiteaService: {
	name:        "gitea"
	displayName: "Gitea"
	image:       "gitea/gitea:1.22"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 3000, host: 3005, protocol: "tcp", name: "web"},
		{container: 22, host: 2222, protocol: "tcp", name: "ssh"},
	]

	volumes: [
		{name: "gitea-data", path: "/data", type: "volume"},
		{host: "/etc/timezone", path: "/etc/timezone", type: "bind", readOnly: true},
		{host: "/etc/localtime", path: "/etc/localtime", type: "bind", readOnly: true},
	]

	traefik: {
		enabled: true
		rule:    "Host(`git.{{.domain}}`)"
	}

	environment: {
		GITEA__database__DB_TYPE: string | *"sqlite3"
		GITEA__server__SSH_PORT:  "22"
		GITEA__server__SSH_LISTEN_PORT: "22"
	}
}

#WoodpeckerServerService: {
	name:        "woodpecker-server"
	displayName: "Woodpecker CI"
	image:       "woodpeckerci/woodpecker-server:v2"
	category:    "ci-cd"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 8000, host: 8087, protocol: "tcp", name: "web"},
		{container: 9000, host: 9002, protocol: "tcp", name: "grpc"},
	]

	volumes: [
		{name: "woodpecker-data", path: "/var/lib/woodpecker", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`ci.{{.domain}}`)"
	}

	environment: {
		WOODPECKER_HOST:         string
		WOODPECKER_GITEA:        "true"
		WOODPECKER_GITEA_URL:    string
		WOODPECKER_GITEA_SECRET: string
	}
}

#WoodpeckerAgentService: {
	name:        "woodpecker-agent"
	displayName: "Woodpecker Agent"
	image:       "woodpeckerci/woodpecker-agent:v2"
	category:    "ci-cd"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	volumes: [
		{host: "/var/run/docker.sock", path: "/var/run/docker.sock", type: "bind"},
	]

	environment: {
		WOODPECKER_SERVER:       string
		WOODPECKER_AGENT_SECRET: string
		WOODPECKER_MAX_PROCS:    string | *"2"
	}
}

#Outputs: {
	giteaUrl:      string | *"https://git.{{.domain}}"
	giteaSshUrl:   string | *"ssh://git@{{.host}}:2222"
	woodpeckerUrl: string | *"https://ci.{{.domain}}"
}
