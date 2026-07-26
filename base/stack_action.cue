// Package base - canonical StackAction wire contract and generation model.
package base

import "list"

#NonEmptyString: string & =~"^.+$"

// stackActionContract is the only authority for the shared StackKits action
// wire. Go DTOs and the StackAction OpenAPI paths/components are generated
// from the concrete projection at stackActionGeneration.
stackActionContract: {
	wireVersion:        "stackkit.stack-action/v1"
	target:             "stackkits"
	pathPrefix:         "/api/v1/internal/stack-actions"
	observationVersion: "stackkit.runtime-observation/v1"

	enums: {
		Action: {
			order:  10
			goName: "Action"
			values: [
				{goConst: "ActionStackKitRollout", value: "stackkit_rollout"},
				{goConst: "ActionVerifyRollout", value: "verify_rollout"},
				{goConst: "ActionRestoreDrill", value: "restore_drill"},
				{goConst: "ActionBackupRun", value: "backup_run", backup: true},
				{goConst: "ActionBackupStatus", value: "backup_status", backup: true},
				{goConst: "ActionBackupRestore", value: "backup_restore", backup: true},
				{goConst: "ActionBackupWipe", value: "backup_wipe", backup: true},
			]
		}
		Mode: {
			order:  20
			goName: "Mode"
			values: [
				{goConst: "ModeDryRun", value: "dry-run"},
				{goConst: "ModeApply", value: "apply"},
			]
		}
		Status: {
			order:  30
			goName: "Status"
			values: [
				{goConst: "StatusAccepted", value: "accepted"},
				{goConst: "StatusReady", value: "ready"},
				{goConst: "StatusApplied", value: "applied"},
				{goConst: "StatusVerified", value: "verified"},
				{goConst: "StatusSkipped", value: "skipped"},
				{goConst: "StatusFailed", value: "failed"},
			]
		}
		CheckStatus: {
			order:  40
			goName: "CheckStatus"
			values: [
				{goConst: "CheckStatusOK", value: "ok"},
				{goConst: "CheckStatusWarning", value: "warning"},
				{goConst: "CheckStatusMissing", value: "missing"},
				{goConst: "CheckStatusReference", value: "reference"},
				{goConst: "CheckStatusSkipped", value: "skipped"},
				{goConst: "CheckStatusFailed", value: "failed"},
			]
		}
		ServiceHealthStatus: {
			order:  50
			goName: "ServiceHealthStatus"
			values: [
				{goConst: "ServiceHealthStarting", value: "starting"},
				{goConst: "ServiceHealthHealthy", value: "healthy"},
				{goConst: "ServiceHealthUnhealthy", value: "unhealthy"},
				{goConst: "ServiceHealthUnknown", value: "unknown"},
			]
		}
		SupplementalNodeRole: {
			order:  60
			goName: "SupplementalNodeRole"
			values: [
				{goConst: "SupplementalNodeRoleWorker", value: "worker"},
				{goConst: "SupplementalNodeRoleStorage", value: "storage"},
			]
		}
		PlatformManagement: {
			order:  70
			goName: "PlatformManagement"
			values: [
				{goConst: "PlatformManagementManaged", value: "managed"},
				{goConst: "PlatformManagementHandoff", value: "handoff"},
				{goConst: "PlatformManagementUnmanaged", value: "unmanaged"},
				{goConst: "PlatformManagementFallback", value: "fallback"},
			]
		}
		SetupPolicy: {
			order:  80
			goName: "SetupPolicy"
			values: [
				{goConst: "SetupPolicyManual", value: "manual"},
				{goConst: "SetupPolicyOnDemand", value: "on_demand"},
				{goConst: "SetupPolicyAutomatic", value: "automatic"},
			]
		}
		ContainerHealth: {
			order:  90
			goName: "ContainerHealth"
			values: [
				{goConst: "ContainerHealthHealthy", value: "healthy"},
				{goConst: "ContainerHealthUnhealthy", value: "unhealthy"},
				{goConst: "ContainerHealthStarting", value: "starting"},
				{goConst: "ContainerHealthNone", value: "none"},
				{goConst: "ContainerHealthUnknown", value: "unknown"},
			]
		}
	}

	paths: [
		{goConst: "PathStackKitRollout", suffix: "/stackkit-rollout", action: "stackkit_rollout", operationID: "stackActionStackKitRollout", summary: "Run or dry-run StackKits rollout for TechStack"},
		{goConst: "PathStackKitVerify", suffix: "/stackkit-verify", action: "verify_rollout", operationID: "stackActionStackKitVerify", summary: "Verify StackKits rollout state for TechStack"},
		{goConst: "PathRestoreDrill", suffix: "/restore-drill", action: "restore_drill", operationID: "stackActionRestoreDrill", summary: "Run or dry-run StackKits restore drill for TechStack"},
		{goConst: "PathBackupRun", suffix: "/backup-run", action: "backup_run", operationID: "stackActionBackupRun", summary: "Start a StackKits backup run"},
		{goConst: "PathBackupStatus", suffix: "/backup-status", action: "backup_status", operationID: "stackActionBackupStatus", summary: "Inspect StackKits backup status"},
		{goConst: "PathBackupRestore", suffix: "/backup-restore", action: "backup_restore", operationID: "stackActionBackupRestore", summary: "Restore a StackKits backup snapshot"},
		{goConst: "PathBackupWipe", suffix: "/backup-wipe", action: "backup_wipe", operationID: "stackActionBackupWipe", summary: "Wipe the configured StackKits backup repository"},
	]

	types: {
		ScopedReference: #StackActionType & {
			order:       10, goName: "ScopedReference", openapiName: "StackActionScopedReference"
			description: "Opaque versioned reference whose scope and expiry are revalidated by internal custody."
			fields: [
				(#StackActionField & {json: "ref", goName: "Ref", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "version", goName: "Version", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "scopes", goName: "Scopes", goType: "[]string", required: true, value: [...#NonEmptyString] & list.MinItems(1), openapi: {kind: "array", itemsKind: "string", minItems: 1}}),
				(#StackActionField & {json: "expires_at", goName: "ExpiresAt", goType: "time.Time", required: true, value: string, openapi: {kind: "string", format: "date-time"}}),
			]
		}
		TechStackEnrollment: #StackActionType & {
			order:       20, goName: "TechStackEnrollment", openapiName: "StackActionTechStackEnrollment"
			description: "Generation-bound managed runtime enrollment and callback channels."
			anyOfRequired: [["heartbeat_url"], ["inventory_url"]]
			fields: [
				(#StackActionField & {json: "tenant_id", goName: "TenantID", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "owner_id", goName: "OwnerID", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "stack_id", goName: "StackID", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "lease_id", goName: "LeaseID", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "server_url", goName: "ServerURL", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string", format: "uri"}}),
				(#StackActionField & {json: "server_id", goName: "ServerID", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "runtime_agent_id", goName: "RuntimeAgentID", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "enrollment_access_ref", goName: "EnrollmentAccessRef", goType: "*ScopedReference", required: true, value: types.ScopedReference.schema, openapi: {kind: "ref", ref: "StackActionScopedReference"}}),
				(#StackActionField & {json: "heartbeat_url", goName: "HeartbeatURL", goType: "string", required: false, value: #NonEmptyString, openapi: {kind: "string", format: "uri"}}),
				(#StackActionField & {json: "inventory_url", goName: "InventoryURL", goType: "string", required: false, value: #NonEmptyString, openapi: {kind: "string", format: "uri"}}),
				(#StackActionField & {json: "control_urls", goName: "ControlURLs", goType: "[]string", required: false, value: [...string], openapi: {kind: "array", itemsKind: "string", itemsFormat: "uri"}}),
			]
		}
		RuntimeTarget: #StackActionType & {
			order:       30, goName: "RuntimeTarget", openapiName: "StackActionTarget"
			description: "Primary runtime host with an opaque access-profile reference."
			fields: [
				(#StackActionField & {json: "host", goName: "Host", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "public_ip", goName: "PublicIP", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "private_ip", goName: "PrivateIP", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "user", goName: "User", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "port", goName: "Port", goType: "int", required: false, value: int & >=1 & <=65535, openapi: {kind: "integer", minimum: 1, maximum: 65535}}),
				(#StackActionField & {json: "docker_host", goName: "DockerHost", goType: "string", required: false, value: string & =~"^ssh://[^/?#]+$", openapi: {kind: "string", format: "uri", pattern: "^ssh://[^/?#]+$"}}),
				(#StackActionField & {json: "access_profile_ref", goName: "AccessProfileRef", goType: "*ScopedReference", required: true, value: types.ScopedReference.schema, openapi: {kind: "ref", ref: "StackActionScopedReference"}}),
			]
		}
		NodePlatformTarget: #StackActionType & {
			order:       40, goName: "NodePlatformTarget", openapiName: "StackActionNodePlatformTarget"
			description: "Observed platform placement identity; values are never synthesized."
			anyOfRequired: [["server_id"], ["destination_uuid"], ["environment_id"], ["project_uuid"], ["environment_uuid"]]
			fields: [
				(#StackActionField & {json: "server_id", goName: "ServerID", goType: "string", required: false, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "destination_uuid", goName: "DestinationUUID", goType: "string", required: false, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "environment_id", goName: "EnvironmentID", goType: "string", required: false, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "project_uuid", goName: "ProjectUUID", goType: "string", required: false, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "environment_uuid", goName: "EnvironmentUUID", goType: "string", required: false, value: #NonEmptyString, openapi: {kind: "string"}}),
			]
		}
		SSHBootstrap: #StackActionType & {
			order:       50, goName: "SSHBootstrap", openapiName: "StackActionSSHBootstrap"
			description: "Supplemental-node SSH endpoint with an opaque access-profile reference."
			fields: [
				(#StackActionField & {json: "host", goName: "Host", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "user", goName: "User", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "port", goName: "Port", goType: "int", required: false, value: int & >=1 & <=65535, openapi: {kind: "integer", minimum: 1, maximum: 65535}}),
				(#StackActionField & {json: "access_profile_ref", goName: "AccessProfileRef", goType: "*ScopedReference", required: true, value: types.ScopedReference.schema, openapi: {kind: "ref", ref: "StackActionScopedReference"}}),
				(#StackActionField & {json: "proxy_jump", goName: "ProxyJump", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
			]
		}
		NodeBootstrap: #StackActionType & {
			order:       60, goName: "NodeBootstrap", openapiName: "StackActionNodeBootstrap"
			description: "Platform-specific supplemental-node bootstrap."
			fields: [
				(#StackActionField & {json: "komodo_core_address", goName: "KomodoCoreAddress", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string", format: "uri"}}),
				(#StackActionField & {json: "onboarding_ref", goName: "OnboardingRef", goType: "*ScopedReference", required: true, value: types.ScopedReference.schema, openapi: {kind: "ref", ref: "StackActionScopedReference"}}),
				(#StackActionField & {json: "ssh", goName: "SSH", goType: "*SSHBootstrap", required: true, value: types.SSHBootstrap.schema, openapi: {kind: "ref", ref: "StackActionSSHBootstrap"}}),
			]
		}
		PlatformNode: #StackActionType & {
			order:       70, goName: "PlatformNode", openapiName: "StackActionPlatformNode"
			description: "Supplemental worker or storage node."
			anyOfRequired: [["platform"], ["bootstrap"]]
			fields: [
				(#StackActionField & {json: "name", goName: "Name", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "role", goName: "Role", goType: "string", required: false, value: string, openapi: {kind: "enum", enum: "SupplementalNodeRole"}}),
				(#StackActionField & {json: "ip", goName: "IP", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "host", goName: "Host", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "services", goName: "Services", goType: "[]string", required: false, value: [...string], openapi: {kind: "array", itemsKind: "string"}}),
				(#StackActionField & {json: "platform", goName: "Platform", goType: "NodePlatformTarget", required: false, value: types.NodePlatformTarget.schema, openapi: {kind: "ref", ref: "StackActionNodePlatformTarget"}}),
				(#StackActionField & {json: "bootstrap", goName: "Bootstrap", goType: "*NodeBootstrap", required: false, value: types.NodeBootstrap.schema, openapi: {kind: "ref", ref: "StackActionNodeBootstrap"}}),
			]
		}
		BackupRepoTarget: #StackActionType & {
			order:       80, goName: "BackupRepoTarget", openapiName: "StackActionBackupRepoTarget"
			description: "Backup repository target with an opaque credential reference."
			fields: [
				(#StackActionField & {json: "type", goName: "Type", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "endpoint", goName: "Endpoint", goType: "string", required: false, value: string, openapi: {kind: "string", format: "uri"}}),
				(#StackActionField & {json: "bucket", goName: "Bucket", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "region", goName: "Region", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "prefix", goName: "Prefix", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "credential_ref", goName: "CredentialRef", goType: "*ScopedReference", required: false, value: types.ScopedReference.schema, openapi: {kind: "ref", ref: "StackActionScopedReference"}}),
			]
		}
		BackupRequest: #StackActionType & {
			order:       90, goName: "BackupRequest", openapiName: "StackActionBackupRequest"
			description: "Backup action parameters containing no credential material."
			fields: [
				(#StackActionField & {json: "classes", goName: "Classes", goType: "[]string", required: false, value: [...string], openapi: {kind: "array", itemsKind: "string"}}),
				(#StackActionField & {json: "snapshot_id", goName: "SnapshotID", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "repo", goName: "Repo", goType: "*BackupRepoTarget", required: false, value: types.BackupRepoTarget.schema, openapi: {kind: "ref", ref: "StackActionBackupRepoTarget"}}),
				(#StackActionField & {json: "confirm", goName: "Confirm", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
			]
		}
		BackupSnapshot: #StackActionType & {
			order:       100, goName: "BackupSnapshot", openapiName: "StackActionBackupSnapshot"
			description: "One observed backup snapshot."
			fields: [
				(#StackActionField & {json: "id", goName: "ID", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "source", goName: "Source", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "classes", goName: "Classes", goType: "[]string", required: false, value: [...string], openapi: {kind: "array", itemsKind: "string"}}),
				(#StackActionField & {json: "started_at", goName: "StartedAt", goType: "string", required: false, value: string, openapi: {kind: "string", format: "date-time"}}), (#StackActionField & {json: "finished_at", goName: "FinishedAt", goType: "string", required: false, value: string, openapi: {kind: "string", format: "date-time"}}),
				(#StackActionField & {json: "total_bytes", goName: "TotalBytes", goType: "int64", required: false, value: int & >=0, openapi: {kind: "integer", minimum: 0, format: "int64"}}),
			]
		}
		BackupResult: #StackActionType & {
			order:       110, goName: "BackupResult", openapiName: "StackActionBackupResult"
			description: "Backup engine state and snapshots."
			fields: [
				(#StackActionField & {json: "engine", goName: "Engine", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "phase", goName: "Phase", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "classes", goName: "Classes", goType: "[]string", required: false, value: [...string], openapi: {kind: "array", itemsKind: "string"}}),
				(#StackActionField & {json: "snapshots", goName: "Snapshots", goType: "[]BackupSnapshot", required: false, value: [...types.BackupSnapshot.schema], openapi: {kind: "array", itemsKind: "ref", itemsRef: "StackActionBackupSnapshot"}}),
				(#StackActionField & {json: "repo_size_bytes", goName: "RepoSizeBytes", goType: "int64", required: false, value: int & >=0, openapi: {kind: "integer", minimum: 0, format: "int64"}}), (#StackActionField & {json: "wiped", goName: "Wiped", goType: "bool", required: false, value: bool, openapi: {kind: "boolean"}}),
			]
		}
		Check: #StackActionType & {
			order:       120, goName: "Check", openapiName: "StackActionCheck"
			description: "One named action check."
			fields: [(#StackActionField & {json: "name", goName: "Name", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}), (#StackActionField & {json: "status", goName: "Status", goType: "CheckStatus", required: true, value: string, openapi: {kind: "enum", enum: "CheckStatus"}}), (#StackActionField & {json: "detail", goName: "Detail", goType: "string", required: false, value: string, openapi: {kind: "string"}})]
		}
		HostObservation: #StackActionType & {
			order:       130, goName: "HostObservation", openapiName: "StackActionObservationHost"
			description: "Measured host and Docker reachability."
			fields: [(#StackActionField & {json: "host", goName: "Host", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "reachable", goName: "Reachable", goType: "bool", required: true, value: bool, openapi: {kind: "boolean"}}), (#StackActionField & {json: "docker_reachable", goName: "DockerReachable", goType: "bool", required: true, value: bool, openapi: {kind: "boolean"}}), (#StackActionField & {json: "failure_class", goName: "FailureClass", goType: "string", required: false, value: string, openapi: {kind: "string"}})]
		}
		PlatformObservation: #StackActionType & {
			order:       140, goName: "PlatformObservation", openapiName: "StackActionObservationPlatform"
			description: "Observed selected-PaaS identity."
			fields: [
				(#StackActionField & {json: "name", goName: "Name", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "endpoint", goName: "Endpoint", goType: "string", required: false, value: string, openapi: {kind: "string", format: "uri"}}), (#StackActionField & {json: "server_id", goName: "ServerID", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "project_uuid", goName: "ProjectUUID", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "environment_uuid", goName: "EnvironmentUUID", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "destination_uuid", goName: "DestinationUUID", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
			]
		}
		ContainerObservation: #StackActionType & {
			order:       150, goName: "ContainerObservation", openapiName: "StackActionContainerObservation"
			description: "Measured Docker container state."
			fields: [
				(#StackActionField & {json: "id", goName: "ID", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "name", goName: "Name", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "state", goName: "State", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "running", goName: "Running", goType: "bool", required: true, value: bool, openapi: {kind: "boolean"}}), (#StackActionField & {json: "health", goName: "Health", goType: "string", required: false, value: string, openapi: {kind: "enum", enum: "ContainerHealth"}}),
			]
		}
		HTTPProbeObservation: #StackActionType & {
			order:       160, goName: "HTTPProbeObservation", openapiName: "StackActionHTTPProbeObservation"
			description: "Measured HTTP health probe."
			fields: [(#StackActionField & {json: "url", goName: "URL", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string", format: "uri"}}), (#StackActionField & {json: "reached", goName: "Reached", goType: "bool", required: true, value: bool, openapi: {kind: "boolean"}}), (#StackActionField & {json: "status_code", goName: "StatusCode", goType: "int", required: false, value: int & >=100 & <=599, openapi: {kind: "integer", minimum: 100, maximum: 599}}), (#StackActionField & {json: "failure_class", goName: "FailureClass", goType: "string", required: false, value: string, openapi: {kind: "string"}})]
		}
		ServiceObservation: #StackActionType & {
			order:       170, goName: "ServiceObservation", openapiName: "StackActionServiceObservation"
			description: "Measured service health and evidence."
			fields: [
				(#StackActionField & {json: "name", goName: "Name", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}), (#StackActionField & {json: "status", goName: "Status", goType: "ServiceHealthStatus", required: true, value: string, openapi: {kind: "enum", enum: "ServiceHealthStatus"}}),
				(#StackActionField & {json: "platform_app_id", goName: "PlatformAppID", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "platform_status", goName: "PlatformStatus", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "containers", goName: "Containers", goType: "[]ContainerObservation", required: false, value: [...types.ContainerObservation.schema], openapi: {kind: "array", itemsKind: "ref", itemsRef: "StackActionContainerObservation"}}),
				(#StackActionField & {json: "health_path", goName: "HealthPath", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "probe", goName: "Probe", goType: "*HTTPProbeObservation", required: false, value: types.HTTPProbeObservation.schema, openapi: {kind: "ref", ref: "StackActionHTTPProbeObservation"}}),
				(#StackActionField & {json: "failure_class", goName: "FailureClass", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
			]
		}
		LiveObservation: #StackActionType & {
			order:       180, goName: "LiveObservation", openapiName: "StackActionLiveObservation"
			description: "Versioned persistable live-runtime evidence."
			fields: [
				(#StackActionField & {json: "version", goName: "Version", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "observed_at", goName: "ObservedAt", goType: "time.Time", required: true, value: string, openapi: {kind: "string", format: "date-time"}}),
				(#StackActionField & {json: "host", goName: "Host", goType: "HostObservation", required: true, value: types.HostObservation.schema, openapi: {kind: "ref", ref: "StackActionObservationHost"}}),
				(#StackActionField & {json: "platform", goName: "Platform", goType: "*PlatformObservation", required: false, value: types.PlatformObservation.schema, openapi: {kind: "ref", ref: "StackActionObservationPlatform"}}),
				(#StackActionField & {json: "services", goName: "Services", goType: "[]ServiceObservation", required: false, value: [...types.ServiceObservation.schema], openapi: {kind: "array", itemsKind: "ref", itemsRef: "StackActionServiceObservation"}}),
				(#StackActionField & {json: "failure_class", goName: "FailureClass", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
			]
		}
		OwnerIdentity: #StackActionType & {
			order:       190, goName: "OwnerIdentity", openapiName: "StackActionOwnerIdentity"
			description: "Configured owner identity."
			fields: [(#StackActionField & {json: "username", goName: "Username", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "email", goName: "Email", goType: "string", required: false, value: string, openapi: {kind: "string", format: "email"}}), (#StackActionField & {json: "display_name", goName: "DisplayName", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "subject", goName: "Subject", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "provider", goName: "Provider", goType: "string", required: false, value: string, openapi: {kind: "string"}})]
		}
		RecoveryOutput: #StackActionType & {
			order:       200, goName: "RecoveryOutput", openapiName: "StackActionRecoveryOutput"
			description: "Opaque recovery references; no secret material."
			fields: [(#StackActionField & {json: "bundle_ref", goName: "BundleRef", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "secret_ref", goName: "SecretRef", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "machine_secret_ref", goName: "MachineSecretRef", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "passphrase_hash_present", goName: "PassphraseHashPresent", goType: "bool", required: false, value: bool, openapi: {kind: "boolean"}})]
		}
		IdentityOutputs: #StackActionType & {
			order:       210, goName: "IdentityOutputs", openapiName: "StackActionIdentityOutputs"
			description: "Owner and recovery output projection."
			fields: [(#StackActionField & {json: "owner", goName: "Owner", goType: "*OwnerIdentity", required: false, value: types.OwnerIdentity.schema, openapi: {kind: "ref", ref: "StackActionOwnerIdentity"}}), (#StackActionField & {json: "recovery", goName: "Recovery", goType: "*RecoveryOutput", required: false, value: types.RecoveryOutput.schema, openapi: {kind: "ref", ref: "StackActionRecoveryOutput"}})]
		}
		LoginGatewayOutput: #StackActionType & {
			order:       220, goName: "LoginGatewayOutput", openapiName: "StackActionLoginGatewayOutput"
			description: "Login gateway endpoints."
			fields: [(#StackActionField & {json: "url", goName: "URL", goType: "string", required: false, value: string, openapi: {kind: "string", format: "uri"}}), (#StackActionField & {json: "label", goName: "Label", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "admin_url", goName: "AdminURL", goType: "string", required: false, value: string, openapi: {kind: "string", format: "uri"}})]
		}
		ServiceOutput: #StackActionType & {
			order:       230, goName: "ServiceOutput", openapiName: "StackActionServiceOutput"
			description: "One exposed StackKit service."
			fields: [(#StackActionField & {json: "name", goName: "Name", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "url", goName: "URL", goType: "string", required: false, value: string, openapi: {kind: "string", format: "uri"}}), (#StackActionField & {json: "admin_url", goName: "AdminURL", goType: "string", required: false, value: string, openapi: {kind: "string", format: "uri"}}), (#StackActionField & {json: "metadata", goName: "Metadata", goType: "map[string]string", required: false, value: {[string]: _}, openapi: {kind: "object", additionalProperties: "string"}})]
		}
		StackKitOutputs: #StackActionType & {
			order:       240, goName: "StackKitOutputs", openapiName: "StackActionStackKitOutputs"
			description: "Structured StackKit rollout outputs."
			fields: [
				(#StackActionField & {json: "identity", goName: "Identity", goType: "*IdentityOutputs", required: false, value: types.IdentityOutputs.schema, openapi: {kind: "ref", ref: "StackActionIdentityOutputs"}}),
				(#StackActionField & {json: "login_gateway", goName: "LoginGateway", goType: "*LoginGatewayOutput", required: false, value: types.LoginGatewayOutput.schema, openapi: {kind: "ref", ref: "StackActionLoginGatewayOutput"}}),
				(#StackActionField & {json: "recovery", goName: "Recovery", goType: "*RecoveryOutput", required: false, value: types.RecoveryOutput.schema, openapi: {kind: "ref", ref: "StackActionRecoveryOutput"}}),
				(#StackActionField & {json: "services", goName: "Services", goType: "[]ServiceOutput", required: false, value: [...types.ServiceOutput.schema], openapi: {kind: "array", itemsKind: "ref", itemsRef: "StackActionServiceOutput"}}),
				(#StackActionField & {json: "metadata", goName: "Metadata", goType: "map[string]string", required: false, value: {[string]: _}, openapi: {kind: "object", additionalProperties: "string"}}),
			]
		}
		RuntimeMetrics: #StackActionType & {
			order:       250, goName: "RuntimeMetrics", openapiName: "StackActionRuntimeMetrics"
			description: "Measured host utilization."
			fields: [(#StackActionField & {json: "cpu_percent", goName: "CPUPercent", goType: "float64", required: true, value: number, openapi: {kind: "number"}}), (#StackActionField & {json: "memory_percent", goName: "MemoryPercent", goType: "float64", required: true, value: number, openapi: {kind: "number"}}), (#StackActionField & {json: "disk_percent", goName: "DiskPercent", goType: "float64", required: true, value: number, openapi: {kind: "number"}}), (#StackActionField & {json: "uptime_seconds", goName: "UptimeSeconds", goType: "float64", required: true, value: number, openapi: {kind: "number"}}), (#StackActionField & {json: "updated_at", goName: "UpdatedAt", goType: "string", required: false, value: string, openapi: {kind: "string", format: "date-time"}})]
		}
		DeploymentRef: #StackActionType & {
			order:       260, goName: "DeploymentRef", openapiName: "StackActionDeploymentRef"
			description: "Selected-PaaS deployment reference."
			fields: [
				(#StackActionField & {json: "platform", goName: "Platform", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}), (#StackActionField & {json: "appName", goName: "AppName", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}), (#StackActionField & {json: "externalId", goName: "ExternalID", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "deploymentId", goName: "DeploymentID", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "observedStatus", goName: "ObservedStatus", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "observedAt", goName: "ObservedAt", goType: "time.Time", required: false, value: string, openapi: {kind: "string", format: "date-time"}}), (#StackActionField & {json: "lastDeployed", goName: "LastDeployed", goType: "time.Time", required: false, value: string, openapi: {kind: "string", format: "date-time"}}),
			]
		}
		SetupDrop: #StackActionType & {
			order:       270, goName: "SetupDrop", openapiName: "StackActionSetupDrop"
			description: "Secret-free first-run setup evidence."
			fields: [
				(#StackActionField & {json: "name", goName: "Name", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}), (#StackActionField & {json: "version", goName: "Version", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "runner", goName: "Runner", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "description", goName: "Description", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "rollbackNotes", goName: "RollbackNotes", goType: "[]string", required: false, value: [...string], openapi: {kind: "array", itemsKind: "string"}}),
			]
		}
		PlatformAppState: #StackActionType & {
			order:       280, goName: "PlatformAppState", openapiName: "StackActionPlatformAppState"
			description: "Artifact-ready selected-PaaS application state."
			fields: [
				(#StackActionField & {json: "name", goName: "Name", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}), (#StackActionField & {json: "role", goName: "Role", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "platform", goName: "Platform", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "management", goName: "Management", goType: "string", required: false, value: string, openapi: {kind: "enum", enum: "PlatformManagement"}}), (#StackActionField & {json: "externalId", goName: "ExternalID", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "deploymentId", goName: "DeploymentID", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "observedStatus", goName: "ObservedStatus", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "observedAt", goName: "ObservedAt", goType: "time.Time", required: false, value: string, openapi: {kind: "string", format: "date-time"}}),
				(#StackActionField & {json: "composePath", goName: "ComposePath", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "setupPolicy", goName: "SetupPolicy", goType: "string", required: false, value: string, openapi: {kind: "enum", enum: "SetupPolicy"}}),
				(#StackActionField & {json: "setupDrops", goName: "SetupDrops", goType: "[]SetupDrop", required: false, value: [...types.SetupDrop.schema], openapi: {kind: "array", itemsKind: "ref", itemsRef: "StackActionSetupDrop"}}), (#StackActionField & {json: "lastDeployed", goName: "LastDeployed", goType: "time.Time", required: false, value: string, openapi: {kind: "string", format: "date-time"}}),
			]
		}
		Request: #StackActionType & {
			order:       290, goName: "Request", openapiName: "StackActionRequest"
			description: "StackKits-owned action request."
			fields: [
				(#StackActionField & {json: "action", goName: "Action", goType: "Action", required: true, value: string, openapi: {kind: "enum", enum: "Action"}}), (#StackActionField & {json: "stack_id", goName: "StackID", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "stack_name", goName: "StackName", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "stackkit", goName: "StackKit", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "mode", goName: "Mode", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "tenant_id", goName: "TenantID", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "owner_id", goName: "OwnerID", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "tofu_dir", goName: "TofuDir", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "unified_path", goName: "UnifiedPath", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "owner_spec_ref", goName: "OwnerSpecRef", goType: "*ScopedReference", required: false, value: types.ScopedReference.schema, openapi: {kind: "ref", ref: "StackActionScopedReference"}}),
				(#StackActionField & {json: "runtime_target", goName: "RuntimeTarget", goType: "*RuntimeTarget", required: false, value: types.RuntimeTarget.schema, openapi: {kind: "ref", ref: "StackActionTarget"}}),
				(#StackActionField & {json: "platform_nodes", goName: "PlatformNodes", goType: "[]PlatformNode", required: false, value: [...#PlatformNodeHandoff], openapi: {kind: "array", itemsKind: "ref", itemsRef: "StackActionPlatformNode"}}),
				(#StackActionField & {json: "techstack_enrollment", goName: "TechStackEnrollment", goType: "*TechStackEnrollment", required: false, value: #ManagedEnrollment, openapi: {kind: "ref", ref: "StackActionTechStackEnrollment"}}),
				(#StackActionField & {json: "backup", goName: "Backup", goType: "*BackupRequest", required: false, value: types.BackupRequest.schema, openapi: {kind: "ref", ref: "StackActionBackupRequest"}}),
			]
		}
		Response: #StackActionType & {
			order:       300, goName: "Response", openapiName: "StackActionResponse"
			description: "StackKits-owned action response and evidence."
			fields: [
				(#StackActionField & {json: "status", goName: "Status", goType: "Status", required: true, value: string, openapi: {kind: "enum", enum: "Status"}}), (#StackActionField & {json: "action", goName: "Action", goType: "Action", required: true, value: string, openapi: {kind: "enum", enum: "Action"}}), (#StackActionField & {json: "stack_id", goName: "StackID", goType: "string", required: true, value: #NonEmptyString, openapi: {kind: "string"}}),
				(#StackActionField & {json: "stack_name", goName: "StackName", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "stackkit", goName: "StackKit", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "tofu_dir", goName: "TofuDir", goType: "string", required: false, value: string, openapi: {kind: "string"}}), (#StackActionField & {json: "unified_path", goName: "UnifiedPath", goType: "string", required: false, value: string, openapi: {kind: "string"}}),
				(#StackActionField & {json: "mode", goName: "Mode", goType: "Mode", required: true, value: string, openapi: {kind: "enum", enum: "Mode"}}), (#StackActionField & {json: "checks", goName: "Checks", goType: "[]Check", required: false, value: [...types.Check.schema], openapi: {kind: "array", itemsKind: "ref", itemsRef: "StackActionCheck"}}),
				(#StackActionField & {json: "stackkit_outputs", goName: "StackKitOutputs", goType: "*StackKitOutputs", required: false, value: types.StackKitOutputs.schema, openapi: {kind: "ref", ref: "StackActionStackKitOutputs"}}),
				(#StackActionField & {json: "observation", goName: "Observation", goType: "*LiveObservation", required: false, value: types.LiveObservation.schema, openapi: {kind: "ref", ref: "StackActionLiveObservation"}}),
				(#StackActionField & {json: "runtime_metrics", goName: "RuntimeMetrics", goType: "*RuntimeMetrics", required: false, value: types.RuntimeMetrics.schema, openapi: {kind: "ref", ref: "StackActionRuntimeMetrics"}}),
				(#StackActionField & {json: "platform_refs", goName: "PlatformRefs", goType: "[]DeploymentRef", required: false, value: [...types.DeploymentRef.schema], openapi: {kind: "array", itemsKind: "ref", itemsRef: "StackActionDeploymentRef"}}),
				(#StackActionField & {json: "platform_system_apps", goName: "PlatformSystemApps", goType: "[]PlatformAppState", required: false, value: [...types.PlatformAppState.schema], openapi: {kind: "array", itemsKind: "ref", itemsRef: "StackActionPlatformAppState"}}),
				(#StackActionField & {json: "platform_apps", goName: "PlatformApps", goType: "[]PlatformAppState", required: false, value: [...types.PlatformAppState.schema], openapi: {kind: "array", itemsKind: "ref", itemsRef: "StackActionPlatformAppState"}}),
				(#StackActionField & {json: "backup", goName: "Backup", goType: "*BackupResult", required: false, value: types.BackupResult.schema, openapi: {kind: "ref", ref: "StackActionBackupResult"}}),
			]
		}
	}
}

// Real CUE schemas are derived from the same field catalog consumed by the
// generators. There is no second hand-written request/response model.
#StackActionRequest:  stackActionContract.types.Request.schema
#StackActionResponse: stackActionContract.types.Response.schema

#ManagedEnrollment: stackActionContract.types.TechStackEnrollment.schema

#PlatformNodeHandoff: stackActionContract.types.PlatformNode.schema

#ServicePlacementNodeHandoff: #PlatformNodeHandoff & {
	services: [...#NonEmptyString] & list.MinItems(1)
}

#ExistingPlatformTarget: stackActionContract.types.NodePlatformTarget.schema

#KomodoPeripheryBootstrap: stackActionContract.types.NodeBootstrap.schema

// stackActionGeneration deliberately contains metadata only, making it a
// concrete and deterministic generator input while the sibling schemas remain
// available to cue vet.
stackActionGeneration: {
	wireVersion:        stackActionContract.wireVersion
	target:             stackActionContract.target
	pathPrefix:         stackActionContract.pathPrefix
	observationVersion: stackActionContract.observationVersion
	enums: {
		for name, enum in stackActionContract.enums {
			(name): {order: enum.order, goName: enum.goName, values: enum.values}
		}
	}
	paths: stackActionContract.paths
	types: {
		for name, typ in stackActionContract.types {
			(name): {
				order: typ.order, goName: typ.goName, openapiName: typ.openapiName, description: typ.description
				if typ.anyOfRequired != _|_ {
					anyOfRequired: typ.anyOfRequired
				}
				fields: [for field in typ.fields {
					json: field.json, goName: field.goName, goType: field.goType, required: field.required, openapi: field.openapi
				}]
			}
		}
	}
}

#StackActionType: {
	order:       int
	goName:      #NonEmptyString
	openapiName: #NonEmptyString
	description: #NonEmptyString
	anyOfRequired?: [...[...#NonEmptyString]]
	fields: [...#StackActionField]
	schema: close({
		for field in fields {
			if field.required {(field.json): field.value}
			if !field.required {(field.json)?: field.value}
		}
	})
	if anyOfRequired != _|_ {
		schema: or([for group in anyOfRequired {{for field in group {(field): !=null}}}])
	}
}

#StackActionField: {
	json:     #NonEmptyString
	goName:   #NonEmptyString
	goType:   #NonEmptyString
	required: bool
	value:    _
	openapi: {
		kind:                  "string" | "integer" | "number" | "boolean" | "object" | "array" | "ref" | "enum"
		format?:               string
		pattern?:              string
		minimum?:              number
		maximum?:              number
		ref?:                  string
		enum?:                 string
		itemsKind?:            "string" | "integer" | "number" | "boolean" | "ref"
		itemsFormat?:          string
		itemsRef?:             string
		minItems?:             int & >=0
		additionalProperties?: "string" | "any"
	}
	if openapi.kind == "enum" {
		value: or([for candidate in stackActionContract.enums[openapi.enum].values {candidate.value}])
	}
}
