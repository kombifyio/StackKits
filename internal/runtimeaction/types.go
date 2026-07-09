// Package runtimeaction defines the servicecall-protected rollout contract
// shared by TechStack, StackKits, and Simulate.
package runtimeaction

import (
	"encoding/json"
	"strings"
)

const (
	TargetStackKits = "stackkits"
	TargetSimulate  = "simulate"

	PathPrefix          = "/api/v1/internal/runtime-actions"
	PathSimulateUpdate  = PathPrefix + "/simulate-update"
	PathStackKitRollout = PathPrefix + "/stackkit-rollout"
	PathStackKitVerify  = PathPrefix + "/stackkit-verify"
	PathRestoreDrill    = PathPrefix + "/restore-drill"
	PathKitUpgrade      = PathPrefix + "/kit-upgrade"
	PathBackupRun       = PathPrefix + "/backup-run"
	PathBackupStatus    = PathPrefix + "/backup-status"
	PathBackupRestore   = PathPrefix + "/backup-restore"
	PathBackupWipe      = PathPrefix + "/backup-wipe"
)

type Action string

const (
	ActionSimulateUpdate  Action = "simulate_update"
	ActionStackKitRollout Action = "stackkit_rollout"
	ActionVerifyRollout   Action = "verify_rollout"
	ActionRestoreDrill    Action = "restore_drill"
	ActionKitUpgrade      Action = "kit_upgrade"
	ActionBackupRun       Action = "backup_run"
	ActionBackupStatus    Action = "backup_status"
	ActionBackupRestore   Action = "backup_restore"
	ActionBackupWipe      Action = "backup_wipe"
)

type Mode string

const (
	ModeDryRun Mode = "dry-run"
	ModeApply  Mode = "apply"
)

type Status string

const (
	StatusAccepted Status = "accepted"
	StatusReady    Status = "ready"
	StatusApplied  Status = "applied"
	StatusVerified Status = "verified"
	StatusSkipped  Status = "skipped"
	StatusFailed   Status = "failed"
)

type CheckStatus string

const (
	CheckStatusOK        CheckStatus = "ok"
	CheckStatusWarning   CheckStatus = "warning"
	CheckStatusMissing   CheckStatus = "missing"
	CheckStatusReference CheckStatus = "reference"
	CheckStatusSkipped   CheckStatus = "skipped"
	CheckStatusFailed    CheckStatus = "failed"
)

type Request struct {
	Action              Action               `json:"action"`
	StackID             string               `json:"stack_id"`
	StackName           string               `json:"stack_name,omitempty"`
	StackKit            string               `json:"stackkit,omitempty"`
	Mode                string               `json:"mode,omitempty"`
	TenantID            string               `json:"tenant_id,omitempty"`
	OwnerID             string               `json:"owner_id,omitempty"`
	TofuDir             string               `json:"tofu_dir,omitempty"`
	UnifiedPath         string               `json:"unified_path,omitempty"`
	OwnerSpecBootstrap  *OwnerSpecBootstrap  `json:"owner_spec_bootstrap,omitempty"`
	RuntimeTarget       *RuntimeTarget       `json:"runtime_target,omitempty"`
	PlatformNodes       []PlatformNode       `json:"platform_nodes,omitempty"`
	TechStackEnrollment *TechStackEnrollment `json:"techstack_enrollment,omitempty"`
	Backup              *BackupRequest       `json:"backup,omitempty"`
	Upgrade             *UpgradeRequest      `json:"upgrade,omitempty"`
}

type Response struct {
	Status          Status           `json:"status"`
	Action          Action           `json:"action"`
	StackID         string           `json:"stack_id"`
	StackName       string           `json:"stack_name,omitempty"`
	StackKit        string           `json:"stackkit,omitempty"`
	TofuDir         string           `json:"tofu_dir,omitempty"`
	UnifiedPath     string           `json:"unified_path,omitempty"`
	Mode            Mode             `json:"mode"`
	Checks          []Check          `json:"checks,omitempty"`
	StackKitOutputs *StackKitOutputs `json:"stackkit_outputs,omitempty"`
	Backup          *BackupResult    `json:"backup,omitempty"`
	Upgrade         *UpgradeResult   `json:"upgrade,omitempty"`
}

// BackupRequest parameterizes the backup_run, backup_status, backup_restore,
// and backup_wipe actions. The data-class vocabulary is owned by the CUE
// backup add-on contract; Go treats classes as opaque strings so the contract
// evolves in CUE, not here.
type BackupRequest struct {
	Classes    []string          `json:"classes,omitempty"`
	SnapshotID string            `json:"snapshot_id,omitempty"`
	Repo       *BackupRepoTarget `json:"repo,omitempty"`
	// RepoPassword is the Kopia repository password. Like RuntimeTarget SSH
	// material it travels by value inside the service-auth-protected request
	// and must never be persisted into evidence, artifacts, or logs.
	RepoPassword string `json:"repo_password,omitempty"`
	// Confirm must equal the stack ID for backup_wipe; other backup actions
	// ignore it. Wipe without a matching confirmation is a validation error.
	Confirm string `json:"confirm,omitempty"`
}

// BackupRepoTarget points the node-side backup engine at the offsite
// repository. Type "s3" covers any S3-compatible store including the
// kombify-managed R2 default; bring-your-own targets use the same shape.
type BackupRepoTarget struct {
	Type            string `json:"type,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	Bucket          string `json:"bucket,omitempty"`
	Region          string `json:"region,omitempty"`
	Prefix          string `json:"prefix,omitempty"`
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
}

// UpgradeRequest parameterizes kit_upgrade. To accepts a semver or a channel
// reference (channel:stable|beta|edge), mirroring `stackkit kit upgrade --to`.
type UpgradeRequest struct {
	To          string `json:"to,omitempty"`
	AutoApprove bool   `json:"auto_approve,omitempty"`
}

// BackupResult reports engine state for backup actions. Phase "running"
// together with StatusAccepted models long first snapshots that exceed the
// bounded runtime-action budget; callers poll backup_status until the phase
// reports "completed".
type BackupResult struct {
	Engine        string           `json:"engine,omitempty"`
	Phase         string           `json:"phase,omitempty"`
	Classes       []string         `json:"classes,omitempty"`
	Snapshots     []BackupSnapshot `json:"snapshots,omitempty"`
	RepoSizeBytes int64            `json:"repo_size_bytes,omitempty"`
	Wiped         bool             `json:"wiped,omitempty"`
}

type BackupSnapshot struct {
	ID         string   `json:"id,omitempty"`
	Source     string   `json:"source,omitempty"`
	Classes    []string `json:"classes,omitempty"`
	StartedAt  string   `json:"started_at,omitempty"`
	FinishedAt string   `json:"finished_at,omitempty"`
	TotalBytes int64    `json:"total_bytes,omitempty"`
}

// UpgradeResult reports the kit_upgrade outcome, anchored to the atomic
// pre-apply snapshot consumed by `stackkit kit rollback`.
type UpgradeResult struct {
	FromVersion string `json:"from_version,omitempty"`
	ToVersion   string `json:"to_version,omitempty"`
	SnapshotID  string `json:"snapshot_id,omitempty"`
	RolledBack  bool   `json:"rolled_back,omitempty"`
}

type Check struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

// OwnerSpecBootstrap is a short-lived TechStack callback capability that lets
// StackKits fetch owner identity and recovery bootstrap data during managed
// rollouts without embedding recovery material in the runtime-action payload.
type OwnerSpecBootstrap struct {
	Endpoint  string   `json:"endpoint"`
	Token     string   `json:"token"`
	ExpiresAt string   `json:"expires_at"`
	Scopes    []string `json:"scopes,omitempty"`
}

type TechStackEnrollment struct {
	TenantID         string         `json:"tenant_id,omitempty"`
	OwnerID          string         `json:"owner_id,omitempty"`
	StackID          string         `json:"stack_id,omitempty"`
	ServerURL        string         `json:"server_url,omitempty"`
	ServerID         string         `json:"server_id"`
	RuntimeAgentID   string         `json:"runtime_agent_id"`
	AgentToken       string         `json:"agent_token,omitempty"`
	HeartbeatURL     string         `json:"heartbeat_url,omitempty"`
	InventoryURL     string         `json:"inventory_url,omitempty"`
	ControlURLs      []string       `json:"control_urls,omitempty"`
	ChannelBootstrap map[string]any `json:"channel_bootstrap,omitempty"`
}

// RuntimeTarget describes the primary runtime host used by a StackKit rollout.
// It is the main/foundation node for single-node rollouts and the control
// target when supplemental platform nodes are attached.
type RuntimeTarget struct {
	Host             string `json:"host,omitempty"`
	PublicIP         string `json:"public_ip,omitempty"`
	PrivateIP        string `json:"private_ip,omitempty"`
	User             string `json:"user,omitempty"`
	Port             int    `json:"port,omitempty"`
	DockerHost       string `json:"docker_host,omitempty"`
	KeyPath          string `json:"key_path,omitempty"`
	PrivateKey       string `json:"private_key,omitempty"`
	ClientPrivateKey string `json:"client_private_key,omitempty"`
	Password         string `json:"password,omitempty"`
}

// PlatformNode carries a real supplemental-node handoff from TechStack into
// StackKits. It must contain either already-observed platform identities or a
// bootstrap channel that can register the node with the selected platform.
type PlatformNode struct {
	Name      string             `json:"name,omitempty"`
	Role      string             `json:"role,omitempty"`
	IP        string             `json:"ip,omitempty"`
	Host      string             `json:"host,omitempty"`
	Services  []string           `json:"services,omitempty"`
	Platform  NodePlatformTarget `json:"platform,omitempty"`
	Bootstrap *NodeBootstrap     `json:"bootstrap,omitempty"`
}

type NodePlatformTarget struct {
	ServerID        string `json:"server_id,omitempty"`
	DestinationUUID string `json:"destination_uuid,omitempty"`
	EnvironmentID   string `json:"environment_id,omitempty"`
	ProjectUUID     string `json:"project_uuid,omitempty"`
	EnvironmentUUID string `json:"environment_uuid,omitempty"`
}

func (target *NodePlatformTarget) UnmarshalJSON(data []byte) error {
	var raw struct {
		ServerID             string `json:"server_id"`
		ServerIDCamel        string `json:"serverId"`
		DestinationUUID      string `json:"destination_uuid"`
		DestinationUUIDCamel string `json:"destinationUuid"`
		EnvironmentID        string `json:"environment_id"`
		EnvironmentIDCamel   string `json:"environmentId"`
		ProjectUUID          string `json:"project_uuid"`
		ProjectUUIDCamel     string `json:"projectUuid"`
		EnvironmentUUID      string `json:"environment_uuid"`
		EnvironmentUUIDCamel string `json:"environmentUuid"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	target.ServerID = firstNonEmpty(raw.ServerID, raw.ServerIDCamel)
	target.DestinationUUID = firstNonEmpty(raw.DestinationUUID, raw.DestinationUUIDCamel)
	target.EnvironmentID = firstNonEmpty(raw.EnvironmentID, raw.EnvironmentIDCamel)
	target.ProjectUUID = firstNonEmpty(raw.ProjectUUID, raw.ProjectUUIDCamel)
	target.EnvironmentUUID = firstNonEmpty(raw.EnvironmentUUID, raw.EnvironmentUUIDCamel)
	return nil
}

type NodeBootstrap struct {
	KomodoCoreAddress   string        `json:"komodo_core_address,omitempty"`
	KomodoOnboardingKey string        `json:"komodo_onboarding_key,omitempty"`
	SSH                 *SSHBootstrap `json:"ssh,omitempty"`
}

func (bootstrap *NodeBootstrap) UnmarshalJSON(data []byte) error {
	var raw struct {
		KomodoCoreAddress        string        `json:"komodo_core_address"`
		KomodoCoreAddressCamel   string        `json:"komodoCoreAddress"`
		KomodoOnboardingKey      string        `json:"komodo_onboarding_key"`
		KomodoOnboardingKeyCamel string        `json:"komodoOnboardingKey"`
		SSH                      *SSHBootstrap `json:"ssh"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	bootstrap.KomodoCoreAddress = firstNonEmpty(raw.KomodoCoreAddress, raw.KomodoCoreAddressCamel)
	bootstrap.KomodoOnboardingKey = firstNonEmpty(raw.KomodoOnboardingKey, raw.KomodoOnboardingKeyCamel)
	bootstrap.SSH = raw.SSH
	return nil
}

type SSHBootstrap struct {
	Host             string `json:"host,omitempty"`
	User             string `json:"user,omitempty"`
	Port             int    `json:"port,omitempty"`
	KeyPath          string `json:"key_path,omitempty"`
	KeyPEM           string `json:"key_pem,omitempty"`
	PrivateKey       string `json:"private_key,omitempty"`
	ClientPrivateKey string `json:"client_private_key,omitempty"`
	ProxyJump        string `json:"proxy_jump,omitempty"`
}

type StackKitOutputs struct {
	Identity     *IdentityOutputs    `json:"identity,omitempty"`
	LoginGateway *LoginGatewayOutput `json:"login_gateway,omitempty"`
	Recovery     *RecoveryOutput     `json:"recovery,omitempty"`
	Services     []ServiceOutput     `json:"services,omitempty"`
	Metadata     map[string]string   `json:"metadata,omitempty"`
}

type IdentityOutputs struct {
	Owner    *OwnerIdentity  `json:"owner,omitempty"`
	Recovery *RecoveryOutput `json:"recovery,omitempty"`
}

type OwnerIdentity struct {
	Username    string `json:"username,omitempty"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Subject     string `json:"subject,omitempty"`
	Provider    string `json:"provider,omitempty"`
}

type LoginGatewayOutput struct {
	URL      string `json:"url,omitempty"`
	Label    string `json:"label,omitempty"`
	AdminURL string `json:"admin_url,omitempty"`
}

type RecoveryOutput struct {
	BundleRef             string `json:"bundle_ref,omitempty"`
	SecretRef             string `json:"secret_ref,omitempty"`
	MachineSecretRef      string `json:"machine_secret_ref,omitempty"`
	PassphraseHashPresent bool   `json:"passphrase_hash_present,omitempty"`
}

type ServiceOutput struct {
	Name     string            `json:"name,omitempty"`
	URL      string            `json:"url,omitempty"`
	AdminURL string            `json:"admin_url,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func NormalizeAction(action string) Action {
	action = strings.ToLower(strings.TrimSpace(action))
	action = strings.ReplaceAll(action, "-", "_")
	return Action(action)
}

func IsStackKitsAction(action Action) bool {
	switch action {
	case ActionStackKitRollout, ActionVerifyRollout, ActionRestoreDrill,
		ActionKitUpgrade, ActionBackupRun, ActionBackupStatus,
		ActionBackupRestore, ActionBackupWipe:
		return true
	default:
		return false
	}
}

// IsBackupAction reports whether the action is one of the backup lifecycle
// verbs handled by the node-side backup engine.
func IsBackupAction(action Action) bool {
	switch action {
	case ActionBackupRun, ActionBackupStatus, ActionBackupRestore, ActionBackupWipe:
		return true
	default:
		return false
	}
}

func IsSimulateAction(action Action) bool {
	return action == ActionSimulateUpdate
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
