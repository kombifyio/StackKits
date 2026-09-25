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
		// Explicit adapter availability; native execution defaults are CUE-owned
		// by the product Definition (ADR-0042), not this legacy matrix.
		// coolify/komodo were graded "supported" without a citable receipt
		// (release:mode-matrix-citations failed on main); no runtime evidence
		// for either adapter exists under docs/data, so both drop to "draft"
		// (implemented, unproven) until a real receipt cites them.
		coolify: "draft"
		komodo:  "draft"
		dokploy: "draft"
		dockge:  "experimental"
	}

	// No cell above is graded "supported"/"default", so no citation is owed
	// yet.
	evidence: {}
}
