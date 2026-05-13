// Test variants for base-kit
package tests

import (
	homelab "github.com/kombifyio/stackkits/base-kit"
	"list"
)

// Test: Default variant configuration
testDefaultStack: homelab.#BaseKitStack & {
	meta: {
		name:    "test-default"
		version: "3.1.0"
	}

	variant:      "default"
	computeTier:  "standard"

	nodes: [{
		id:   "homeserver"
		name: "homeserver"
		host: "192.168.1.100"
		compute: {
			cpuCores:  4
			ramGB:     8
			storageGB: 100
		}
	}]

	network: {
		domain:    "homelab.local"
		acmeEmail: "admin@example.com"
	}

	services: {
		traefik:    {enabled: true}
		dozzle:     {enabled: true}
		dokploy:    {enabled: true}
		uptimeKuma: {enabled: true}
	}
}

// Test: Coolify variant (requires domain)
testCoolifyStack: homelab.#BaseKitStack & {
	meta: {
		name:    "test-coolify"
		version: "3.1.0"
	}

	variant:      "coolify"
	computeTier:  "high"

	nodes: [{
		id:   "cloudserver"
		name: "cloudserver"
		host: "cloud.example.com"
		compute: {
			cpuCores:  8
			ramGB:     16
			storageGB: 200
		}
	}]

	network: {
		domain:    "apps.example.com"
		acmeEmail: "ssl@example.com"
	}

	services: {
		traefik:    {enabled: true}
		dozzle:     {enabled: true}
		coolify:    {enabled: true}
		uptimeKuma: {enabled: true}
	}
}

// Test: Beszel variant (monitoring focus)
testBeszelStack: homelab.#BaseKitStack & {
	meta: {
		name:    "test-beszel"
		version: "3.1.0"
	}

	variant:      "beszel"
	computeTier:  "standard"

	nodes: [{
		id:   "monitorserver"
		name: "monitorserver"
		host: "192.168.1.50"
		compute: {
			cpuCores:  4
			ramGB:     8
			storageGB: 80
		}
	}]

	network: {
		domain:    "monitor.local"
		acmeEmail: "ops@example.com"
	}

	services: {
		traefik: {enabled: true}
		dozzle:  {enabled: true}
		dokploy: {enabled: true}
		beszel:  {enabled: true}
	}
}

// Test: Minimal variant (low resources)
testMinimalStack: homelab.#BaseKitStack & {
	meta: {
		name:    "test-minimal"
		version: "3.1.0"
	}

	variant:      "minimal"
	computeTier:  "low"

	nodes: [{
		id:   "piserver"
		name: "piserver"
		host: "192.168.1.200"
		compute: {
			cpuCores:  2
			ramGB:     4
			storageGB: 32
		}
	}]

	network: {
		domain:    "pi.local"
		acmeEmail: "pi@example.com"
	}

	services: {
		traefik:   {enabled: true}
		dozzle:    {enabled: true}
		dockge:    {enabled: true}
		portainer: {enabled: true}
		netdata:   {enabled: true}
	}
}

// Validation: All test stacks have exactly one node
_nodeCountValidation: {
	testDefaultStack: list.MaxItems(testDefaultStack.nodes, 1)
	testCoolifyStack: list.MaxItems(testCoolifyStack.nodes, 1)
	testBeszelStack:  list.MaxItems(testBeszelStack.nodes, 1)
	testMinimalStack: list.MaxItems(testMinimalStack.nodes, 1)
}
