// Package base_kit — mode-support matrix declaration (see base/mode_matrix.cue).
//
// Honest values, not aspiration: "supported" cells cite the canonical
// verification path in `evidence`; everything that exists as code but has
// no proven verification cell stays "scaffolding" until its E2E cell lands.
package base_kit

import (
	"github.com/kombifyio/stackkits/base"
)

modeMatrix: base.#KitModeSupport & {
	kit: "base-kit"

	placement: {
		// Resolver + capability bindings are live (sqlite/local-fs/...), but the
		// local-only Tier-3 E2E cell is still open (kombify-StackKits-vwe.12).
		"local-only": "scaffolding"
		// SK-S1 browser-evidence gate passed 2026-06-12 (standard+cloudless).
		standard: "supported"
	}

	install: {
		// Composes and generates; no automated verification cell yet.
		bare: "scaffolding"
		// SK-S1 runs the bootstrapped path end-to-end.
		bootstrapped: "supported"
		// Terramate templates exist; no E2E.
		advanced: "scaffolding"
	}

	context: {
		local: "supported"
		// SK-S2/SK-S3 live infrastructure is open (kombify-StackKits-4c3).
		cloud: "scaffolding"
		pi:    "scaffolding"
	}

	paas: {
		coolify: "default"
		komodo:  "supported"
		dokploy: "draft"
		dockge:  "experimental"
	}

	evidence: ["SK-S1"]
}
