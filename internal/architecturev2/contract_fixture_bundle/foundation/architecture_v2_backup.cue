package foundation

// These four plan-only contracts bind the shared Application Lifecycle to the
// exact workload storage facts in ResolvedPlan. They do not create repositories,
// schedule jobs, hold credentials, choose targets, or execute provider lifecycle.
#BackupSourceModuleContractV1: close({
	apiVersion:                    "stackkit.backup-source/v1"
	kind:                          "BackupSourceContract"
	authority:                     "resolved-plan"
	sourceRef:                     "workloads[].alternative.infrastructure.backupSource"
	storageAllocationModuleRef:    #ContractID
	workloadDataBindingModuleRef:  #ContractID
	eligibleStorageClass:          "persistent"
	backupIntentRequired:          true
	providerLifecycleOwned:        false
	credentialCustodyOwned:        false
	targetLifecycleOwned:          false
	multiServerOrchestrationOwned: false
})

#SnapshotModuleContractV1: close({
	apiVersion: "stackkit.snapshot/v1"
	kind:       "SnapshotContract"
	authority:  "resolved-plan"
	stageRef:   "applicationLifecycles[].lifecycle.stages.backup"
	operation:  "stackkit.backup"
	phases: ["snapshot", "verify"]
	evidence: ["snapshot-anchor"]
	backupSourceModuleRef:         #ContractID
	ownerApprovalRequired:         true
	providerLifecycleOwned:        false
	credentialCustodyOwned:        false
	targetLifecycleOwned:          false
	multiServerOrchestrationOwned: false
})

#RestoreModuleContractV1: close({
	apiVersion: "stackkit.restore/v1"
	kind:       "RestoreContract"
	authority:  "resolved-plan"
	stageRef:   "applicationLifecycles[].lifecycle.stages.restore"
	operations: ["stackkit.restore", "stackkit.verify"]
	phases: ["stage", "safety-snapshot", "activate", "verify", "recover"]
	evidence: ["owner-observation", "restore-result", "snapshot-anchor"]
	snapshotModuleRef:             #ContractID
	stagedActivation:              true
	safetySnapshotRequired:        true
	ownerApprovalRequired:         true
	providerLifecycleOwned:        false
	credentialCustodyOwned:        false
	targetLifecycleOwned:          false
	multiServerOrchestrationOwned: false
})

#RecoveryModuleContractV1: close({
	apiVersion:                    "stackkit.recovery/v1"
	kind:                          "RecoveryContract"
	authority:                     "resolved-plan"
	stageRef:                      "applicationLifecycles[].lifecycle.stages.restore"
	phase:                         "recover"
	restoreModuleRef:              #ContractID
	stateSourceRef:                "application-lifecycle"
	evidenceSourceRef:             "application-lifecycle"
	resumeRequiresOwnerApproval:   true
	recoveryEvidenceRequired:      true
	providerLifecycleOwned:        false
	credentialCustodyOwned:        false
	targetLifecycleOwned:          false
	multiServerOrchestrationOwned: false
})
