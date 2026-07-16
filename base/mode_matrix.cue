// Package base — Kit mode-support matrix (mode-matrix epic kombify-StackKits-vwe).
//
// Machine-readable declaration of which mode cells each kit actually covers,
// replacing the per-kit ad-hoc prose in stackkit.yaml `modes:` blocks as the
// source of truth. Every kit declares one #KitModeSupport in its own package
// (basement-kit/mode_matrix.cue, cloud-kit/mode_matrix.cue, ...); the architecture snapshot derives the
// downstream `modeMatrix` contract from these declarations.
//
// The matrix states REALITY, not aspiration: a cell is "supported" only when
// a canonical verification path proves it (cite it in `evidence`). Everything
// between "exists as code" and "proven" is "scaffolding".
//
// Run via: cue vet ./base/...
package base

// #SupportLevel grades one mode cell of a kit.
//   supported     — proven by a canonical verification path (cite evidence)
//   scaffolding   — code exists, no proven verification cell yet
//   unsupported   — kit explicitly does not cover this cell
//   control-plane — realized outside OSS (S2/S3); never claimable as OSS support
#SupportLevel: "supported" | "scaffolding" | "unsupported" | "control-plane"

// #PaasStatus grades a PAAS option within a kit.
#PaasStatus: "default" | "supported" | "draft" | "experimental"

// #KitModeSupport is one kit's row set in the mode matrix.
// Axes are orthogonal: placement (where/coupling), install (automation
// degree), and the legacy-v1 context compatibility/evidence axis. Context is
// never product identity and does not select canonical Architecture v2
// behavior. Legacy install-mode aliases
// ("simple", "terramate") are normalized at the model boundary
// (pkg/models/install_modes.go) and never appear here.
#KitModeSupport: {
	kit: string

	placement: {
		"local-only": #SupportLevel
		standard:     #SupportLevel
		// StackKits-OSS realizes only S1. The managed-serverless cell is named
		// so downstream contracts can see it, but it is constrained to
		// control-plane — claiming OSS support is a cue vet failure.
		"managed-serverless": "control-plane"
	}

	install: {
		bare:         #SupportLevel
		bootstrapped: #SupportLevel
		advanced:     #SupportLevel
	}

	context: {
		local: #SupportLevel
		cloud: #SupportLevel
		pi:    #SupportLevel
	}

	// PAAS options the kit names; omitted PAAS = not offered by this kit.
	paas: {
		coolify?: #PaasStatus
		komodo?:  #PaasStatus
		dokploy?: #PaasStatus
		dockge?:  #PaasStatus
	}

	// Canonical E2E scenario IDs backing the "supported" cells (e.g. "SK-S1").
	evidence?: [...string]
}
