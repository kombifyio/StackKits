// Package gameserver - Game Server Add-On
//
// Generic game server framework using container-based deployments.
// Supports popular game servers via LinuxGSM or dedicated images.
//
// Supported games (via container images):
//   - Minecraft (Java/Bedrock)
//   - Valheim
//   - Terraria
//   - Satisfactory
//   - Custom (bring your own image)
//
// License: Varies by game server image
// Placement: Local node (high CPU/RAM usage)
//
// Usage:
//   addons: gameserver: gameserver.#Config & {
//       game: "minecraft-java"
//       minecraft: memory: "4G"
//   }

package gameserver

// #Config defines game server add-on configuration
#Config: {
	_addon: {
		name:        "gameserver"
		displayName: "Game Server"
		version:     "1.0.0"
		layer:       "APPLICATION"
		description: "Self-hosted game server"
	}

	enabled: bool | *true

	// Game selection
	game: *"minecraft-java" | "minecraft-bedrock" | "valheim" | "terraria" | "satisfactory" | "custom"

	// Minecraft Java configuration
	if game == "minecraft-java" {
		minecraft: #MinecraftJavaConfig
	}

	// Minecraft Bedrock configuration
	if game == "minecraft-bedrock" {
		minecraftBedrock: #MinecraftBedrockConfig
	}

	// Valheim configuration
	if game == "valheim" {
		valheim: #ValheimConfig
	}

	// Custom game server
	if game == "custom" {
		custom: #CustomGameConfig
	}

	// Common settings
	maxPlayers: int | *10
	serverName: string | *"Homelab Game Server"
}

#MinecraftJavaConfig: {
	// Server version
	version: string | *"LATEST"

	// Server type
	type: *"VANILLA" | "PAPER" | "FORGE" | "FABRIC"

	// Memory allocation
	memory: string | *"2G"

	// Game port
	port: uint16 | *25565

	// RCON
	rcon: {
		enabled: bool | *true
		port:    uint16 | *25575
	}
}

#MinecraftBedrockConfig: {
	version: string | *"LATEST"
	port:    uint16 | *19132
}

#ValheimConfig: {
	// World name
	worldName: string | *"HomeWorld"

	// Server password
	password: =~"^secret://"

	// Ports
	ports: {
		game:  uint16 | *2456
		query: uint16 | *2457
	}
}

#CustomGameConfig: {
	// Custom container image
	image: string

	// Custom ports
	ports: [...{
		container: uint16
		host:      uint16
		protocol:  *"tcp" | "udp"
		name:      string
	}]

	// Custom volumes
	volumes: [...{
		name: string
		path: string
	}]

	// Custom environment variables
	environment: [string]: string
}

// Service definitions

#MinecraftJavaService: {
	name:        "minecraft"
	displayName: "Minecraft Java"
	image:       "itzg/minecraft-server:latest"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 25565, host: 25565, protocol: "tcp", name: "game"},
		{container: 25575, host: 25575, protocol: "tcp", name: "rcon"},
	]

	volumes: [
		{name: "minecraft-data", path: "/data", type: "volume"},
	]

	environment: {
		EULA:     "TRUE"
		TYPE:     string | *"VANILLA"
		VERSION:  string | *"LATEST"
		MEMORY:   string | *"2G"
	}
}

#ValheimService: {
	name:        "valheim"
	displayName: "Valheim"
	image:       "lloesche/valheim-server:latest"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 2456, host: 2456, protocol: "udp", name: "game"},
		{container: 2457, host: 2457, protocol: "udp", name: "query"},
	]

	volumes: [
		{name: "valheim-config", path: "/config", type: "volume"},
		{name: "valheim-data", path: "/opt/valheim", type: "volume"},
	]

	environment: {
		SERVER_NAME: string
		WORLD_NAME:  string | *"HomeWorld"
		SERVER_PASS: string
	}
}

#Outputs: {
	game:       string
	serverIp:   string | *"{{.host}}"
	serverPort: uint16
}
