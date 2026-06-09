// Package runtimeaction defines the servicecall-protected rollout contract
// shared by TechStack, StackKits, and Simulate.
package runtimeaction

import "strings"

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
	Action             Action              `json:"action"`
	StackID            string              `json:"stack_id"`
	StackName          string              `json:"stack_name,omitempty"`
	StackKit           string              `json:"stackkit,omitempty"`
	TofuDir            string              `json:"tofu_dir,omitempty"`
	UnifiedPath        string              `json:"unified_path,omitempty"`
	OwnerSpecBootstrap *OwnerSpecBootstrap `json:"owner_spec_bootstrap,omitempty"`
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
