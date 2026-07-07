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
)

type Action string

const (
	ActionSimulateUpdate  Action = "simulate_update"
	ActionStackKitRollout Action = "stackkit_rollout"
	ActionVerifyRollout   Action = "verify_rollout"
	ActionRestoreDrill    Action = "restore_drill"
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
	case ActionStackKitRollout, ActionVerifyRollout, ActionRestoreDrill:
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
