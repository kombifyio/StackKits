// Package modern_homelab declares compatibility evidence only. It is not a
// product discriminator and does not select v2 topology, context, PaaS, or
// runtime behavior. Modern is defined solely by required Home+Cloud federation.
//
// That federation is multi-site apply, which runs only under Techstack
// dispatch in Advanced Mode (STACKKITS-PRODUCT-BOUNDARY-STANDARD). Modern
// therefore has no Standard Mode path: the standalone bare and bootstrapped
// install cells are unsupported, and only install.advanced can ever be proven.
package modern_homelab

import (
	"github.com/kombifyio/stackkits/foundation"
)

modeMatrix: foundation.#KitModeSupport & {
	kit: "modern-homelab"

	placement: {
		// "standard" is the S1 node-placement axis, not Standard Mode.
		"local-only": "unsupported"
		standard:     "scaffolding"
	}

	install: {
		bare:         "unsupported"
		bootstrapped: "unsupported"
		advanced:     "scaffolding"
	}

	context: {
		local: "scaffolding"
		cloud: "scaffolding"
		pi:    "scaffolding"
	}

	paas: {
		// No PaaS is selected or offered by the Modern kit. Concrete workload
		// executors are catalog modules and remain independently evidenced.
	}
}
