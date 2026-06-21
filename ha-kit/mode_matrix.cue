// Package ha_kit — mode-support matrix declaration (see base/mode_matrix.cue).
//
// The kit is 1.0.0-alpha scaffolding: no cell has a proven verification
// path yet, so nothing may claim "supported". Advanced is the recommended
// install mode for HA, but recommendation is not proof — it stays
// "scaffolding" until its verification cell lands. local-only placement is
// structurally unsupported: HA requires 3+ manager nodes, local-only
// targets a single developer machine.
package ha_kit

import (
	"github.com/kombifyio/stackkits/base"
)

modeMatrix: base.#KitModeSupport & {
	kit: "ha-kit"

	placement: {
		"local-only": "unsupported"
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
		pi:    "unsupported"
	}

	paas: {
		dokploy: "draft"
	}
}
