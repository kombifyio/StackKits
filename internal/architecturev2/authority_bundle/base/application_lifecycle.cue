package base

import "list"

// #StandardUseCaseLifecycle is the reusable standalone Application Kit
// lifecycle. Product packages may add setup and evidence requirements, but
// they must not redefine commands, approval semantics, or surface ownership.
// The standalone operation registry remains the sole implementation.
#StandardUseCaseLifecycle: {
	referenceVertical: bool | *false
	stages: {
		install: {
			name: "install"
			operations: ["stackkit.init", "stackkit.validate", "stackkit.resolve", "stackkit.generate", "stackkit.plan", "stackkit.apply", "stackkit.verify"]
			phases: ["resolve", "generate", "apply", "verify"]
			surfaces: ["installer", "cli", "mcp", "state-console"]
			evidence: *["resolved-plan", "generation-receipt", "apply-result", "owner-observation"] | ([...("resolved-plan" | "generation-receipt" | "apply-result" | "owner-observation")] & list.MinItems(4) & list.MaxItems(4))
			_evidenceUnique: list.UniqueItems(evidence) & true
			mutation:        true
			destructive:     false
			ownerApproval:   true
		}
		manage: {
			name: "manage"
			operations: ["stackkit.status", "stackkit.logs", "stackkit.verify"]
			phases: ["observe", "inspect", "verify"]
			surfaces: ["cli", "mcp", "state-console"]
			evidence: *["apply-result", "owner-observation"] | ([...("apply-result" | "owner-observation")] & list.MinItems(2) & list.MaxItems(2))
			_evidenceUnique: list.UniqueItems(evidence) & true
			mutation:        false
			destructive:     false
			ownerApproval:   false
		}
		backup: {
			name: "backup"
			operations: ["stackkit.backup"]
			phases: ["snapshot", "verify"]
			surfaces: ["cli", "mcp", "state-console"]
			evidence: ["snapshot-anchor"]
			mutation:      true
			destructive:   false
			ownerApproval: true
		}
		upgrade: {
			name: "upgrade"
			operations: ["stackkit.upgrade", "stackkit.advanced.change-set.apply", "stackkit.drift.reconcile.advanced"]
			phases: ["preflight", "snapshot", "update", "migrate", "verify", "rollback"]
			surfaces: ["cli", "mcp", "state-console"]
			evidence: *["snapshot-anchor", "upgrade-result", "owner-observation"] | ([...("snapshot-anchor" | "upgrade-result" | "owner-observation")] & list.MinItems(3) & list.MaxItems(3))
			_evidenceUnique: list.UniqueItems(evidence) & true
			mutation:        true
			destructive:     true
			ownerApproval:   true
		}
		restore: {
			name: "restore"
			operations: ["stackkit.restore", "stackkit.verify"]
			phases: ["stage", "safety-snapshot", "activate", "verify", "recover"]
			surfaces: ["cli", "mcp", "state-console"]
			evidence: *["snapshot-anchor", "restore-result", "owner-observation"] | ([...("snapshot-anchor" | "restore-result" | "owner-observation")] & list.MinItems(3) & list.MaxItems(3))
			_evidenceUnique: list.UniqueItems(evidence) & true
			mutation:        true
			destructive:     true
			ownerApproval:   true
		}
		drift: {
			name: "drift"
			operations: ["stackkit.drift"]
			phases: ["observe", "compare"]
			surfaces: ["cli", "mcp", "state-console"]
			evidence: ["drift-report"]
			mutation:      false
			destructive:   false
			ownerApproval: false
		}
		remove: {
			name: "remove"
			operations: ["stackkit.remove"]
			phases: ["authorize", "remove", "verify"]
			surfaces: ["cli", "mcp", "state-console"]
			evidence: ["removal-result"]
			mutation:      true
			destructive:   true
			ownerApproval: true
		}
	}
}
