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

// Semantic backup classes are explicit curated component/volume facts.
// Privacy dataClasses and storage classes keep their existing meanings.
// A future unclassified volume cannot enter a class-based source policy.
_architectureV2VolumeBackupClasses: {
	"immich-server/library":       "photos"
	"immich-postgres/database":    "database"
	"cloudreve/data":              "user-content"
	"opencloud/config":            "config"
	"opencloud/data":              "user-content"
	"nextcloud/html":              "user-content"
	"nextcloud-postgres/database": "database"
	"vaultwarden/data":            "secret-material"
	"passbolt/gpg":                "secret-material"
	"passbolt/jwt":                "secret-material"
	"passbolt-mariadb/database":   "database"
	"open-webui/data":             "user-content"
	"anythingllm/storage":         "user-content"
	"comfyui/user":                "config"
	"comfyui/input":               "user-content"
	"comfyui/output":              "user-content"
	"hermes/data":                 "user-content"
	"openhands/settings":          "config"
	"openhands/workspace":         "user-content"
	"paperclip/home":              "user-content"
	"paperclip-postgres/database": "database"
	"speechkit/data":              "user-content"
	"qdrant/storage":              "user-content"
	"gitea/data":                  "user-content"
	"gitea/config":                "config"
	"forgejo/data":                "user-content"
	"paperless/data":              "config"
	"paperless/media":             "documents"
	"paperless/consume":           "documents"
	"paperless-postgres/database": "database"
	"wings/data":                  "user-content"
	"panel-database/database":     "database"
	"panel/data":                  "config"
	"panel/var":                   "config"
	"roundcube/database":          "database"
	"roundcube/mailbox":           "user-content"
	"stalwart/data":               "user-content"
	"jellyfin/config":             "config"
	"emby/config":                 "config"
	"navidrome/data":              "config"
	"audiobookshelf/config":       "config"
	"audiobookshelf/metadata":     "config"
	"esphome/config":              "config"
	"euro-office/data":            "documents"
	"mosquitto/data":              "config"
	"zigbee2mqtt/data":            "config"
	"immich-kiosk/config":         "config"
	"immich-power-tools/data":     "config"
	"home-assistant/config":       "config"
	"adguard-home/config":         "config"
}
