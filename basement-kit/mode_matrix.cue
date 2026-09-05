// Package basement_kit — mode-support matrix declaration (see foundation/mode_matrix.cue).
//
// Honest values, not aspiration: "supported" cells cite the canonical
// verification path in `evidence`; everything that exists as code but has
// no proven verification cell stays "scaffolding" until its E2E cell lands.
package basement_kit

import (
	"github.com/kombifyio/stackkits/foundation"
)

modeMatrix: foundation.#KitModeSupport & {
	kit: "basement-kit"

	placement: {
		// Resolver + capability bindings are live (sqlite/local-fs/...), but the
		// local-only Tier-3 E2E cell is still open (kombify-StackKits-vwe.12).
		"local-only": "scaffolding"
		// The historical SK-S1 run predates the current runtime. Current-line
		// evidence from the external producer remains pending.
		standard: "scaffolding"
	}

	install: {
		// Composes and generates; no automated verification cell yet.
		bare: "scaffolding"
		// Implemented; the current released-archive lifecycle is not yet proven.
		bootstrapped: "scaffolding"
		// Advanced now means the Terramate Plus lifecycle contract, but the
		// full Advanced E2E cell is still open.
		advanced: "scaffolding"
	}

	context: {
		local: "scaffolding"
		// SK-S2/SK-S3 live infrastructure is open (kombify-StackKits-4c3).
		cloud: "unsupported"
		pi:    "scaffolding"
	}

	paas: {
		coolify: "default"
		komodo:  "supported"
		dokploy: "draft"
		dockge:  "experimental"
	}

	evidence: []
}
