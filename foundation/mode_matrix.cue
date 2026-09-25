// Package foundation — Kit mode-support matrix (mode-matrix epic kombify-StackKits-vwe).
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
// Run via: cue vet ./foundation/...
package foundation

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

	// Per-cell evidence citations proving each "supported"/"default" cell.
	// Keyed by "<axis>.<key>" (the same cell path the citation validator
	// computes, e.g. "install.advanced", "context.cloud", "paas.coolify");
	// each value is the list of citations for that one cell. A citation is
	// either a canonical E2E scenario ID (e.g. "SK-S1") or an os-compat lab
	// receipt run id (a directory name under
	// docs/data/os-compat/receipts/<runId>/, e.g. "pve_e20d4a0a77"). A cell
	// graded "supported"/"default" with no entry here, or whose citations do
	// not resolve to a real artifact, fails `mise run
	// release:mode-matrix-citations`; a citation clears only the cell it is
	// filed under, never a kit's other supported cells.
	//
	// install.advanced is the go-live gate cell (ADR-0045 Acceptance;
	// docs/plans/2026-09-24-replanning/11-steering-2026-09-25.md "Gate for
	// go-live" item 4): its citations must resolve to an os-compat receipt
	// with `dispatcher: "techstack-core"`, a real substrate (`"proxmox-ve"`
	// for the Basement/Proxmox lane or `"managed-vps"` for the
	// Techstack-managed Cloud VPS lane), overall `status: "passed"`, and a
	// passed phase entry for every one of: apply, verify, drift-detect,
	// change-set, reconcile, rollback, restore-drill. A receipt missing any
	// one of those phases (for example the unmanaged proxmox-lab.py or
	// managed-vps Standard lifecycle, which never runs change-set/
	// reconcile/rollback/restore-drill, or a Techstack-dispatched run whose
	// change-set/reconcile/rollback failed) never satisfies install.advanced,
	// no matter how many other phases passed.
	evidence?: {
		[string]: [...string]
	}
}
