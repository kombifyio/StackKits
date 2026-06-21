// Package modern_homelab — mode-support matrix declaration (see base/mode_matrix.cue).
//
// The kit is 1.0.0-alpha scaffolding: no cell has a proven verification
// path yet, so nothing may claim "supported". Cells flip individually once
// their E2E evidence lands.
package modern_homelab

import (
	"github.com/kombifyio/stackkits/base"
)

modeMatrix: base.#KitModeSupport & {
	kit: "modern-homelab"

	placement: {
		"local-only": "scaffolding"
		standard:     "scaffolding"
	}

	install: {
		bare:         "scaffolding"
		bootstrapped: "scaffolding"
		advanced:     "scaffolding"
	}

	context: {
		local: "scaffolding"
		cloud: "scaffolding"
		pi:    "scaffolding"
	}

	paas: {
		coolify: "default"
		komodo:  "supported"
		dokploy: "draft"
	}
}
