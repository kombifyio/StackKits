// Package socket_proxy — Docker socket proxy module.
//
// The ONLY service that mounts docker.sock. All other services that need
// Docker API access (Traefik, TinyAuth, Dozzle) connect through this proxy.
//
// Filters Docker API: read-only endpoints enabled (containers, events, networks, info, ping, version),
// all write operations blocked (POST, EXEC, BUILD, etc.).
//
// Exception: Dokploy/Coolify (PaaS) keeps direct docker.sock because it needs
// full container lifecycle management (POST, EXEC). This is the only exception.
//
// Reference: NETWORK-SECURITY-STACKKITS_1.md §4.2
package socket_proxy

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "socket-proxy"
		displayName: "Docker Socket Proxy"
		version:     "1.0.0"
		layer:       "L1-foundation"
		description: "Filtering proxy for Docker socket — eliminates root-equivalent socket exposure"
	}

	// No service dependencies — this is L1 foundation.
	requires: {
		infrastructure: {
			docker:       true
			dockerSocket: true
			network:      "shared"
		}
	}

	provides: {
		capabilities: {
			"docker-api-proxy":     true
			"socket-isolation":     true
			"read-only-docker-api": true
		}
		endpoints: {
			api: {
				url:         "tcp://socket-proxy:2375"
				internal:    true
				description: "Filtered Docker API (container-to-container only)"
			}
		}
	}

	settings: {
		perma: {
			// API access flags — set at deploy, changing requires redeploy
			containers: *true | bool // Traefik, Dozzle need container listing
			networks:   *true | bool // Traefik needs network info
			services:   *true | bool // Swarm service discovery (future High Availability Kit)
			tasks:      *true | bool // Swarm task info (future High Availability Kit)
			info:       *true | bool // Docker info endpoint
			// Write operations — all blocked by default
			post:    *false | bool
			build:   *false | bool
			exec:    *false | bool // Critical: block exec into containers
			images:  *false | bool
			volumes: *false | bool
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {}
	}

	services: "socket-proxy": base.#ServiceDefinition & {
		name:     "socket-proxy"
		type:     "infrastructure"
		image:    "tecnativa/docker-socket-proxy"
		tag:      "latest"
		required: true
		status:   "implemented"

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			traefik: enabled: false // Not routed via Traefik — internal only
			networks: ["socket-proxy-net"]
		}

		volumes: [{
			source:      "/var/run/docker.sock"
			target:      "/var/run/docker.sock"
			type:        "bind"
			readOnly:    true
			backup:      false
			description: "Docker socket (the only service that mounts this)"
		}]

		environment: {
			CONTAINERS:   "1"
			EVENTS:       "1"
			NETWORKS:     "1"
			SERVICES:     "1"
			TASKS:        "1"
			INFO:         "1"
			PING:         "1"
			VERSION:      "1"
			POST:         "0"
			BUILD:        "0"
			COMMIT:       "0"
			CONFIGS:      "0"
			DISTRIBUTION: "0"
			EXEC:         "0"
			GRPC:         "0"
			IMAGES:       "0"
			NODES:        "0"
			PLUGINS:      "0"
			SECRETS:      "0"
			SESSION:      "0"
			SWARM:        "0"
			SYSTEM:       "0"
			VOLUMES:      "0"
		}

		resources: {
			memory:    "32m"
			memoryMax: "64m"
			cpus:      0.1
		}

		healthCheck: {
			enabled: true
			test: ["CMD-SHELL", "wget -q --spider http://localhost:2375/version || exit 1"]
			interval: "30s"
			timeout:  "5s"
			retries:  3
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
			readOnly: true
			tmpfs: ["/run"]
		}

		output: {
			url:         "tcp://socket-proxy:2375"
			description: "Docker socket proxy (internal only, never exposed externally)"
		}
	}
}
