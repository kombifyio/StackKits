// Package cloud_kit — mode-support matrix declaration (see foundation/mode_matrix.cue).
//
// Historical Cloud capability evidence was first recorded with v0.5.1
// (2026-07-07): SK-S2 managed kombify.me subdomain (Komodo) and SK-S3
// provider-leased custom domain (Coolify) were reported to pass on
// production-tests run 28881686758 at SHA 4d0a34c3
// (docs/roadmap/v0.5.1-cloud-kit-graduation.md), and the cells below were
// flipped "scaffolding -> supported" as post-release bookkeeping. That run
// left no citable artifact under docs/data/use-case-runtime-evidence or
// artifacts/scenarios in this repository: `mise run
// release:mode-matrix-citations` fails on main citing SK-S2/SK-S3 as "not a
// runtime receipt". Per ADR-0045 Acceptance ("a contract or generated
// artifact never promotes a cell... missing evidence stays pending") and the
// per-cell citation rule (foundation/mode_matrix.cue), every cell that only
// cited SK-S2/SK-S3 drops back to "scaffolding"/"draft" here until a real,
// resolvable receipt exists. Cells without a proven verification path stay
// "scaffolding".
package cloud_kit

import (
	"github.com/kombifyio/stackkits/foundation"
)

modeMatrix: foundation.#KitModeSupport & {
	kit: "cloud-kit"

	placement: {
		"local-only": "unsupported"
		// SK-S2/SK-S3 do not resolve to a citable receipt (see package doc).
		standard: "scaffolding"
	}

	install: {
		// Composes and generates; no automated verification cell yet.
		bare: "scaffolding"
		// SK-S2 (kombify.me/Komodo) and SK-S3 (custom domain/Coolify) ran the
		// bootstrapped path end-to-end on externally supplied fresh Ubuntu,
		// but that run left no citable receipt in this repository.
		bootstrapped: "scaffolding"
		// Advanced is the Terramate Plus lifecycle contract, proven by the
		// go-live gate run mvps_4061f9f196: a Techstack-dispatched Advanced
		// run on a managed IONOS Cloud VPS (local Core, StackKits snapshot
		// 80d57455) with every gate phase passed. Backups there are
		// kombify-managed custody, so the drill is the managed rollout's own
		// verified restore drill.
		advanced: "supported"
	}

	context: {
		local: "unsupported"
		// SK-S2/SK-S3 do not resolve to a citable receipt (see package doc).
		cloud: "scaffolding"
		pi:    "unsupported"
	}

	paas: {
		// Explicit adapter availability; native execution defaults are CUE-owned
		// by the product Definition (ADR-0042), not this legacy matrix.
		// coolify/komodo were graded "supported" on the same unresolved
		// SK-S2/SK-S3 citation; both drop to "draft" (implemented, unproven)
		// until a real receipt cites them.
		coolify: "draft"
		komodo:  "draft"
		dokploy: "draft"
	}

	evidence: {
		"install.advanced": ["mvps_4061f9f196"]
	}
}
