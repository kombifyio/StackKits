// Package cloud_kit — mode-support matrix declaration (see base/mode_matrix.cue).
//
// Cloud Kit is the cloud profile; cells stay "scaffolding" until a canonical
// cloud verification path (SK-S3 provider-leased custom domain + SK-S2 managed
// kombify.me subdomain) proves them from cloud-kit released contents.
package cloud_kit

import (
	"github.com/kombifyio/stackkits/base"
)

modeMatrix: base.#KitModeSupport & {
	kit: "cloud-kit"

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
		local: "unsupported"
		cloud: "scaffolding"
		pi:    "unsupported"
	}

	paas: {
		coolify: "default"
		komodo:  "supported"
		dokploy: "draft"
	}

	evidence: ["SK-S3", "SK-S2"]
}
