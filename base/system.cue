// Package base - System configuration schemas
package base

// #SystemConfig defines host-level system settings
#SystemConfig: {
	// Timezone (IANA format)
	timezone: string | *"UTC"

	// System locale
	locale: string | *"en_US.UTF-8"

	// Hostname pattern (will be templated)
	hostname?: string

	// Swap configuration
	swap: "disabled" | "auto" | "manual" | *"auto"

	// Swap size in MB (if manual)
	swapSize?: int & >=0

	// Unattended upgrades policy
	unattendedUpgrades: "disabled" | "security" | "all" | *"security"

	// Kernel parameters (sysctl)
	sysctl?: [string]: string | int

	// Kernel modules to load
	kernelModules?: [...string]
}

// #BasePackages defines the base package set
#BasePackages: {
	// Package manager
	manager: "apt" | "dnf" | "yum" | "apk" | *"apt"

	// Base system packages (always installed)
	base: [...string] | *[
			"curl",
			"wget",
			"ca-certificates",
			"gnupg",
			"lsb-release",
			"apt-transport-https",
			"software-properties-common",
	]

	// CLI tools (modern replacements)
	tools: [...string] | *[
			"htop",
			"btop",
			"tmux",
			"jq",
			"tree",
			"ncdu",
	]

	// Extra packages (StackKit-specific)
	extra: [...string] | *[]

	// Packages to remove
	remove: [...string] | *[]
}

// #SystemUsers defines user account configuration
#SystemUsers: {
	// Admin user configuration
	admin: #UserConfig & {
		name:  string | *"kombi"
		shell: string | *"/bin/bash"
		sudo:  bool | *true
	}

	// Service account (non-login)
	service: #UserConfig & {
		name:  string | *"kombi-svc"
		shell: string | *"/usr/sbin/nologin"
		sudo:  bool | *false
	}

	// Additional users
	additional: [...#UserConfig] | *[]
}

// #UserConfig defines a user account
#UserConfig: {
	// Username
	name: =~"^[a-z_][a-z0-9_-]*$"

	// User ID (optional)
	uid?: int & >=1000

	// Group ID (optional)
	gid?: int & >=1000

	// Home directory
	home?: string

	// Login shell
	shell: string | *"/bin/bash"

	// SSH authorized keys
	authorizedKeys?: [...string]

	// Sudo access
	sudo: bool | *false

	// Groups to add user to
	groups?: [...string]

	// User is system account
	system: bool | *false
}

// #ContainerRuntime defines the container engine configuration
#ContainerRuntime: {
	// Container engine
	engine: "docker" | "podman" | *"docker"

	// Rootless mode
	rootless: bool | *false

	// Live restore (keep containers on daemon restart)
	liveRestore: bool | *true

	// Default logging driver
	logDriver: "json-file" | "journald" | "syslog" | "none" | *"json-file"

	// Log options
	logOpts?: [string]: string

	// Default network driver
	networkDriver: "bridge" | "overlay" | "host" | "none" | *"bridge"

	// Storage driver
	storageDriver?: "overlay2" | "btrfs" | "zfs" | "devicemapper"

	// Data root directory
	dataRoot: string | *"/var/lib/docker"

	// Registry mirrors
	registryMirrors?: [...string]

	// Insecure registries (for development)
	insecureRegistries?: [...string]

	// Default ulimits
	defaultUlimits?: [string]: {
		soft: int
		hard: int
	}

	// Compose file location
	composeFile: string | *"docker-compose.yml"

	// Compose project name
	composeProject?: string
}
