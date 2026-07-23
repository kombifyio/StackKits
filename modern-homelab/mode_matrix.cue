// Package modern_homelab declares compatibility evidence only. It is not a
// product discriminator and does not select v2 topology, context, PaaS, or
// runtime behavior. Modern is defined solely by required Home+Cloud federation.
package modern_homelab

import (
	"github.com/kombifyio/stackkits/base"
)

modeMatrix: base.#KitModeSupport & {
	kit: "modern-homelab"

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
		pi:    "scaffolding"
	}

	paas: {
		// No PaaS is selected or offered by the Modern kit. Concrete workload
		// executors are catalog modules and remain independently evidenced.
	}
}
