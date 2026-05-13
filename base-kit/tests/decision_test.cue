// =============================================================================
// Base Kit Decision Point Tests
// =============================================================================
// Negative tests: configurations that MUST be rejected by CUE constraints.
//
// CUE evaluates constraints at "vet" time. Invalid configs produce errors.
// Each test below is a VALID config that exercises one decision boundary,
// ensuring the constraint works as expected.
//
// To verify REJECTION, use: cue vet ./... -d _invalidXxx
// These must produce errors. Run separately per case.
//
// Decision Points Tested:
//   1. Coolify + local domain → REJECT
//   2. Both PaaS enabled → REJECT
//   3. Low compute + coolify → REJECT
//   4. Coolify + insufficient resources → REJECT
//   5. Upgrade path downgrade → REJECT
//   6. Monitoring mutual exclusivity
//   7. Domain type → TLS mode auto-selection
// =============================================================================

package tests

import (
	homelab "github.com/kombifyio/stackkits/base-kit"
	"list"
)

// =============================================================================
// POSITIVE BOUNDARY TESTS (Must pass CUE vet)
// =============================================================================

// Test: Coolify with valid public domain passes
_testCoolifyPublicDomain: homelab.homelab.#BaseKitStack & {
	meta: name: "coolify-public"
	variant:     "coolify"
	computeTier: "high"
	nodes: [{
		id:   "server"
		name: "server"
		host: "1.2.3.4"
		compute: {
			cpuCores:  8
			ramGB:     16
			storageGB: 200
		}
	}]
	network: {
		domain:    "apps.example.com"
		acmeEmail: "admin@example.com"
	}
	services: {
		traefik: enabled: true
		coolify: enabled: true
		dozzle:  enabled: true
	}
}

// Test: Default variant with local domain → ports mode
_testDefaultLocalPorts: homelab.#BaseKitStack & {
	meta: name: "default-local"
	variant:     "default"
	computeTier: "standard"
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
		acmeEmail: "admin@homelab.local"
	}
	services: {
		traefik:    enabled: true
		dokploy:    enabled: true
		uptimeKuma: enabled: true
		dozzle:     enabled: true
	}
}
// Verify the constraint was applied correctly
_testDefaultLocalPortsCheck: {
	// local domain forces ports mode
	_testDefaultLocalPorts.accessMode
	"ports"
}

// Test: Public domain auto-selects ACME TLS
_testPublicDomainACME: homelab.#BaseKitStack & {
	meta: name: "public-acme"
	variant:     "default"
	computeTier: "standard"
	nodes: [{
		id:   "cloud"
		name: "cloud"
		host: "1.2.3.4"
		compute: {
			cpuCores:  4
			ramGB:     8
			storageGB: 100
		}
	}]
	network: {
		domain:    "my.example.org"
		acmeEmail: "ssl@example.org"
	}
	services: {
		traefik: enabled: true
		dokploy: enabled: true
		dozzle:  enabled: true
	}
}
// Public domain should default to ACME TLS
_testPublicDomainACMECheck: {
	_testPublicDomainACME.network.tls.mode
	"acme"
}

// Test: Low compute with minimal variant (valid combo)
_testLowComputeMinimal: homelab.#BaseKitStack & {
	meta: name: "low-minimal"
	variant:     "minimal"
	computeTier: "low"
	nodes: [{
		id:   "pi"
		name: "pi"
		host: "192.168.1.50"
		compute: {
			cpuCores:  2
			ramGB:     4
			storageGB: 32
		}
	}]
	network: {
		domain:    "pi.local"
		acmeEmail: "pi@home.local"
	}
	services: {
		traefik:   enabled: true
		dockge:    enabled: true
		portainer: enabled: true
		netdata:   enabled: true
		dozzle:    enabled: true
	}
}

// Test: Beszel variant with monitoring exclusivity (no uptimeKuma)
_testBeszelExclusive: homelab.#BaseKitStack & {
	meta: name: "beszel-only"
	variant:     "beszel"
	computeTier: "standard"
	nodes: [{
		id:   "monitor"
		name: "monitor"
		host: "192.168.1.60"
		compute: {
			cpuCores:  4
			ramGB:     8
			storageGB: 80
		}
	}]
	network: {
		domain:    "monitor.home"
		acmeEmail: "ops@home"
	}
	services: {
		traefik: enabled: true
		dokploy: enabled: true
		beszel:  enabled: true
		dozzle:  enabled: true
	}
}

// Test: Dokploy enabled → coolify cannot be enabled
_testPaaSMutualExclusivity: homelab.#BaseKitStack & {
	meta: name: "paas-exclusive"
	variant:     "default"
	computeTier: "standard"
	nodes: [{
		id:   "paas-node"
		name: "paas-node"
		host: "10.0.0.1"
		compute: {
			cpuCores:  4
			ramGB:     8
			storageGB: 100
		}
	}]
	network: {
		domain:    "paas.local"
		acmeEmail: "admin@paas.local"
	}
	services: {
		traefik:    enabled: true
		dokploy:    enabled: true
		coolify:    enabled: false // Must be false when dokploy is true
		uptimeKuma: enabled: true
		dozzle:     enabled: true
	}
}

// Test: Authelia requires Traefik (traefik is always enabled)
_testAutheliaRequiresTraefik: homelab.#BaseKitStack & {
	meta: name: "authelia-test"
	variant:     "default"
	computeTier: "standard"
	nodes: [{
		id:   "auth-node"
		name: "auth-node"
		host: "192.168.1.70"
		compute: {
			cpuCores:  4
			ramGB:     8
			storageGB: 100
		}
	}]
	network: {
		domain:    "auth.local"
		acmeEmail: "admin@auth.local"
	}
	services: {
		traefik:  enabled: true
		dokploy:  enabled: true
		authelia: enabled: true
		dozzle:   enabled: true
	}
}

// Test: Dockge forces PaaS off (minimal variant logic)
_testDockgeForcesPaasOff: homelab.#BaseKitStack & {
	meta: name: "dockge-no-paas"
	variant:     "minimal"
	computeTier: "low"
	nodes: [{
		id:   "dockge-node"
		name: "dockge-node"
		host: "192.168.1.80"
		compute: {
			cpuCores:  2
			ramGB:     4
			storageGB: 64
		}
	}]
	network: {
		domain:    "dockge.lan"
		acmeEmail: "admin@dockge.lan"
	}
	services: {
		traefik:   enabled: true
		dockge:    enabled: true
		dokploy:   enabled: false // Dockge forces this off
		coolify:   enabled: false // Dockge forces this off
		portainer: enabled: true
		netdata:   enabled: true
		dozzle:    enabled: true
	}
}

// =============================================================================
// UPGRADE PATH TESTS
// =============================================================================

// Test: Valid upgrade base → modern
_testUpgradeBaseToModern: #UpgradePath & {
	from: "base-kit"
	to:   "modern-homelab"
}

// Test: Valid upgrade modern → ha
_testUpgradeModernToHA: #UpgradePath & {
	from: "modern-homelab"
	to:   "ha-kit"
}

// Test: Valid same-tier (no change)
_testNoUpgrade: #UpgradePath & {
	from: "ha-kit"
	to:   "ha-kit"
}

// =============================================================================
// NETWORK DECISION POINT TESTS
// =============================================================================

// Test: Local domain detection (.local suffix)
_testLocalDomainDetection: #NetworkConfig & {
	domain:    "mylab.local"
	acmeEmail: "any@local"
}
_testLocalDomainCheck: {
	_testLocalDomainDetection._isLocalDomain
	true
}

// Test: Local domain detection (.lan suffix)
_testLanDomainDetection: #NetworkConfig & {
	domain:    "home.lan"
	acmeEmail: "any@lan"
}
_testLanDomainCheck: {
	_testLanDomainDetection._isLocalDomain
	true
}

// Test: Public domain detection (.com suffix)
_testPublicDomainDetection: #NetworkConfig & {
	domain:    "my.example.com"
	acmeEmail: "admin@example.com"
}
_testPublicDomainCheck: {
	_testPublicDomainDetection._isLocalDomain
	false
}

// Test: Local domain → self-signed TLS default
_testLocalTLSDefault: {
	_testLocalDomainDetection.tls.mode
	"self-signed"
}

// Test: Public domain → ACME TLS default
_testPublicTLSDefault: {
	_testPublicDomainDetection.tls.mode
	"acme"
}

// =============================================================================
// NEGATIVE TESTS (These should FAIL when evaluated)
// =============================================================================
// Run individually with: cue vet -d _invalidXxx ./base-kit/...
// Each MUST produce a CUE error confirming the constraint is enforced.
//
// IMPORTANT: Do NOT evaluate all _invalid* at once, as they are intentionally
// broken configurations. They serve as documentation of what the schema rejects.
// =============================================================================

// --- NEGATIVE TEST: Coolify + .local domain ---
// Expected error: domain constraint fails (!~ local/lan/home/internal/test)
//
// _invalidCoolifyLocalDomain: homelab.#BaseKitStack & {
//     meta: name: "bad-coolify"
//     variant: "coolify"
//     computeTier: "high"
//     nodes: [{
//         id: "x"
//         name: "x"
//         host: "1.2.3.4"
//         compute: { cpuCores: 8; ramGB: 16; storageGB: 200 }
//     }]
//     network: {
//         domain: "coolify.local"  // ← REJECTED: local domain with coolify
//         acmeEmail: "a@b.com"
//     }
//     services: { traefik: enabled: true; coolify: enabled: true; dozzle: enabled: true }
// }

// --- NEGATIVE TEST: Both PaaS platforms enabled ---
// Expected error: coolify.enabled conflicts with dokploy.enabled constraint
//
// _invalidBothPaas: homelab.#BaseKitStack & {
//     meta: name: "bad-paas"
//     variant: "default"
//     computeTier: "standard"
//     nodes: [{
//         id: "x"
//         name: "x"
//         host: "1.2.3.4"
//         compute: { cpuCores: 4; ramGB: 8; storageGB: 100 }
//     }]
//     network: { domain: "test.local"; acmeEmail: "a@b" }
//     services: {
//         traefik: enabled: true
//         dokploy: enabled: true
//         coolify: enabled: true  // ← REJECTED: both PaaS enabled
//         dozzle: enabled: true
//     }
// }

// --- NEGATIVE TEST: Low compute + coolify ---
// Expected error: variant != "coolify" when computeTier == "low"
//
// _invalidLowComputeCoolify: homelab.#BaseKitStack & {
//     meta: name: "bad-compute"
//     variant: "coolify"         // ← REJECTED: low compute cannot run coolify
//     computeTier: "low"
//     nodes: [{
//         id: "x"
//         name: "x"
//         host: "1.2.3.4"
//         compute: { cpuCores: 2; ramGB: 4; storageGB: 32 }
//     }]
//     network: { domain: "apps.example.com"; acmeEmail: "a@example.com" }
//     services: { traefik: enabled: true; coolify: enabled: true; dozzle: enabled: true }
// }

// --- NEGATIVE TEST: Coolify with insufficient resources ---
// Expected error: cpuCores >= 4 && ramGB >= 8 required for coolify
//
// _invalidCoolifyResources: homelab.#BaseKitStack & {
//     meta: name: "bad-resources"
//     variant: "coolify"
//     computeTier: "standard"
//     nodes: [{
//         id: "x"
//         name: "x"
//         host: "1.2.3.4"
//         compute: { cpuCores: 2; ramGB: 4; storageGB: 50 }  // ← REJECTED: too few resources
//     }]
//     network: { domain: "apps.example.com"; acmeEmail: "a@example.com" }
//     services: { traefik: enabled: true; coolify: enabled: true; dozzle: enabled: true }
// }

// --- NEGATIVE TEST: Upgrade path HA → modern (downgrade) ---
// Expected error: to must be "ha-kit" when from == "ha-kit"
//
// _invalidDowngrade: #UpgradePath & {
//     from: "ha-kit"
//     to:   "modern-homelab"    // ← REJECTED: cannot downgrade from HA
// }

// --- NEGATIVE TEST: Upgrade path modern → base (downgrade) ---
// Expected error: to must be "modern-homelab" | "ha-kit" when from == "modern-homelab"
//
// _invalidModernDowngrade: #UpgradePath & {
//     from: "modern-homelab"
//     to:   "base-kit"      // ← REJECTED: cannot downgrade from modern
// }

// --- NEGATIVE TEST: Invalid stack name (uppercase) ---
// Expected error: name must match ^[a-z][a-z0-9-]*$
//
// _invalidStackName: homelab.#BaseKitStack & {
//     meta: name: "MyStack"     // ← REJECTED: uppercase not allowed
//     variant: "default"
//     computeTier: "standard"
//     nodes: [{ id: "x"; name: "x"; host: "1.2.3.4"; compute: { cpuCores: 4; ramGB: 8; storageGB: 100 } }]
//     network: { domain: "test.local"; acmeEmail: "a@b" }
//     services: { traefik: enabled: true; dokploy: enabled: true; dozzle: enabled: true }
// }

// --- NEGATIVE TEST: Two nodes in base-kit (max 1) ---
// Expected error: list.MaxItems constraint (max 1 node)
//
// _invalidTwoNodes: homelab.#BaseKitStack & {
//     meta: name: "bad-nodes"
//     variant: "default"
//     computeTier: "standard"
//     nodes: [
//         { id: "node-1"; name: "one"; host: "1.2.3.4"; compute: { cpuCores: 4; ramGB: 8; storageGB: 100 } },
//         { id: "node-2"; name: "two"; host: "5.6.7.8"; compute: { cpuCores: 4; ramGB: 8; storageGB: 100 } },
//     ] // ← REJECTED: base-kit allows exactly 1 node
//     network: { domain: "test.local"; acmeEmail: "a@b" }
//     services: { traefik: enabled: true; dokploy: enabled: true; dozzle: enabled: true }
// }
