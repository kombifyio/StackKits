// Package cloud_kit — mode-support matrix declaration (see base/mode_matrix.cue).
//
// Cloud Kit graduated with v0.5.1 (2026-07-07): the canonical cloud
// verification path — SK-S2 managed kombify.me subdomain (Komodo) and SK-S3
// provider-leased custom domain (Coolify) — passed live on the release source
// commit (production-tests run 28881686758, SHA 4d0a34c3). Cells without a
// proven verification path stay "scaffolding".
package cloud_kit

import (
	"github.com/kombifyio/stackkits/base"
)

modeMatrix: base.#KitModeSupport & {
	kit: "cloud-kit"

	placement: {
		"local-only": "unsupported"
		// SK-S2/SK-S3 prove the standard placement from released contents.
		standard: "supported"
	}

	install: {
		// Composes and generates; no automated verification cell yet.
		bare: "scaffolding"
		// SK-S2 (kombify.me/Komodo) and SK-S3 (custom domain/Coolify) run the
		// bootstrapped path end-to-end on externally supplied fresh Ubuntu.
		bootstrapped: "supported"
		// Advanced is the Terramate Plus lifecycle contract; the full
		// Advanced E2E cell is still open.
		advanced: "scaffolding"
	}

	context: {
		local: "unsupported"
		// Proven on externally supplied cloud hosts (SK-S2/SK-S3).
		cloud: "supported"
		pi:    "unsupported"
	}

	paas: {
		coolify: "default"
		komodo:  "supported"
		dokploy: "draft"
	}

	evidence: ["SK-S3", "SK-S2"]
}
