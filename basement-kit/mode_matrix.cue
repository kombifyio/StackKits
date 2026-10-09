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
		// This cell and install.bootstrapped never claim more than the
		// receipt-backed compat projection (docs/data/os-compat/latest.json,
		// environment basement-kit/ci-vm): "supported" only while that
		// projection grades the fresh-VM lifecycle supported on the current
		// release. The projection names v0.50.3; the current release has no
		// producer receipt yet. `mise run compat:ledger:check` enforces this.
		standard: "scaffolding"
	}

	install: {
		// Composes and generates; no automated verification cell yet.
		bare: "scaffolding"
		// Implemented; the released-archive lifecycle has no receipt on the
		// current release (see placement.standard: one ledger, the projection).
		bootstrapped: "scaffolding"
		// Techstack-dispatched Advanced lifecycle with full bootstrap on a real
		// Proxmox VE host from the published v0.47.7 archive (managed lab run
		// tsl_339d8557e0, 35/35 phases): apply, verify, per-stack drift detect,
		// owner setup, a change set adding Photos/Files/Vault with application
		// setup, household user add, OIDC login proof, induced drift detected
		// and reconciled, coordinated rollback, two restore drills, each
		// followed by an owner and OIDC re-proof.
		advanced: "supported"
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

	evidence: {"install.advanced": ["tsl_339d8557e0"]}
}
