// Package base — CUE constraint checks for mode_matrix.cue.
// NOT named *_test.cue on purpose: CUE 0.15 excludes *_test.cue files from
// `cue vet ./base/...`, so this file uses a plain name to be vet-enforced.
// Run via: cue vet ./base/...
package base

// A complete, valid kit declaration unifies.
_test_matrix_valid: #KitModeSupport & {
	kit: "test-kit"
	placement: {
		"local-only": "scaffolding"
		standard:     "supported"
	}
	install: {
		bare:         "scaffolding"
		bootstrapped: "supported"
		advanced:     "scaffolding"
	}
	context: {
		local: "supported"
		cloud: "scaffolding"
		pi:    "unsupported"
	}
	paas: {
		coolify: "default"
		dokploy: "draft"
	}
	evidence: ["SK-S1"]
}

// The managed-serverless cell is forced to control-plane: the schema must
// fill it without the kit declaring it, and it must never be claimable.
_assert_matrix_ms_forced: true & (_test_matrix_valid.placement."managed-serverless" == "control-plane")
