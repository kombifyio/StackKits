package diagnose

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// RepairAction identifies a typed host-side repair effect (ADR-0051).
type RepairAction string

const (
	// Open-default reversible, non-destructive repairs (D-2026-11-26-08,
	// D-2026-11-27-09; owner can narrow anytime via RepairPolicyFile).
	RepairRestartService        RepairAction = "restart-service"
	RepairReconcileCompose      RepairAction = "reconcile-compose"
	RepairReloadRouter          RepairAction = "reload-router"
	RepairRenewTLS              RepairAction = "renew-tls"
	RepairRetryAppSetup         RepairAction = "retry-appsetup"
	RepairReconcileCoolifyProxy RepairAction = "reconcile-coolify-proxy"

	// Explicitly EXCLUDED from open default — require fresh human approval
	// (D-2026-11-20-06, ADR-0051).
	RepairGCDanglingImages   RepairAction = "gc-dangling-images"
	RepairWipeVolume         RepairAction = "wipe-volume"
	RepairClearActiveLease   RepairAction = "clear-active-lease"
	RepairRotateAppKey       RepairAction = "rotate-app-key"
	RepairRestoreOverLive    RepairAction = "restore-over-live"
	RepairWidenIngress       RepairAction = "widen-ingress"
	RepairBindNewPublicRoute RepairAction = "bind-new-public-domain"
	RepairDeleteResource     RepairAction = "delete-resource"
)

const (
	// CircuitBreakerMaxPerHour enforces ADR-0051's cap: max 2 auto-repairs per
	// failure signature per rolling 1-hour window.
	CircuitBreakerMaxPerHour = 2
	CircuitBreakerWindow     = time.Hour
	RepairPolicyRelPath      = ".stackkit/mcp/repair-policy.json"
	CircuitBreakerRelPath    = ".stackkit/runtime/.repair-circuit-breaker.json"
)

var (
	// ErrRepairRequiresHumanApproval signals an operation guarded by
	// D-2026-11-20-06 (including gc-dangling-images and owner-narrowed actions).
	ErrRepairRequiresHumanApproval = errors.New("repair action requires explicit human owner approval (D-2026-11-20-06)")
	// ErrRepairActionUnknown rejects unrecognized repair effects fail-closed.
	ErrRepairActionUnknown = errors.New("unknown repair action")
	// ErrCircuitBreakerTripped stops autonomous repair loops after 2/sig/hour.
	ErrCircuitBreakerTripped = errors.New("repair circuit breaker tripped (max 2 auto-repairs per signature per hour)")
)

// RepairPolicy allows the owner to narrow any open-default repair back to
// approval-required at any time (D-2026-11-26-08). Note: an owner policy may
// NARROW open defaults, never widen guarded destructive effects without code
// and owner governance change.
type RepairPolicy struct {
	SchemaVersion           string         `json:"schemaVersion"`
	RequireApprovalFor      []RepairAction `json:"requireApprovalFor,omitempty"`
	DisableAutoRepairGlobal bool           `json:"disableAutoRepairGlobal,omitempty"`
}

// RepairDecision is the deterministic authorization outcome for one repair call.
type RepairDecision struct {
	Action                RepairAction `json:"action"`
	AllowedWithoutApprove bool         `json:"allowedWithoutApproval"`
	RequiresHumanApproval bool         `json:"requiresHumanApproval"`
	Reason                string       `json:"reason"`
}

// OpenDefaultRepairActions returns the closed allowlist of reversible,
// non-destructive repairs permitted without per-call approval by default.
func OpenDefaultRepairActions() []RepairAction {
	return []RepairAction{
		RepairRestartService,
		RepairReconcileCompose,
		RepairReloadRouter,
		RepairRenewTLS,
		RepairRetryAppSetup,
		RepairReconcileCoolifyProxy,
	}
}

// IsOpenDefaultRepair reports whether action is in the closed reversible
// open-default allowlist.
func IsOpenDefaultRepair(action RepairAction) bool {
	return slices.Contains(OpenDefaultRepairActions(), action)
}

// IsGuardedHumanApprovalAction reports whether action is strictly excluded
// from open default and requires fresh D-2026-11-20-06 owner approval.
func IsGuardedHumanApprovalAction(action RepairAction) bool {
	switch action {
	case RepairGCDanglingImages,
		RepairWipeVolume,
		RepairClearActiveLease,
		RepairRotateAppKey,
		RepairRestoreOverLive,
		RepairWidenIngress,
		RepairBindNewPublicRoute,
		RepairDeleteResource:
		return true
	default:
		return false
	}
}

// EvaluateRepairAuthorization classifies a repair request under ADR-0051,
// D-2026-11-20-06, D-2026-11-26-08, D-2026-11-27-09, and any owner narrowing.
func EvaluateRepairAuthorization(action RepairAction, policy RepairPolicy, hasVerifiedOwnerApproval bool) (RepairDecision, error) {
	action = RepairAction(strings.TrimSpace(string(action)))
	if IsGuardedHumanApprovalAction(action) {
		reason := fmt.Sprintf("%s is guarded by D-2026-11-20-06 human-approval policy", action)
		if action == RepairGCDanglingImages {
			reason = "gc-dangling-images is excluded from open default (irreversible on offline nodes and prunes build cache); requires explicit owner approval"
		}
		if hasVerifiedOwnerApproval {
			return RepairDecision{Action: action, AllowedWithoutApprove: false, RequiresHumanApproval: true, Reason: reason + " (verified owner approval supplied)"}, nil
		}
		return RepairDecision{Action: action, AllowedWithoutApprove: false, RequiresHumanApproval: true, Reason: reason}, fmt.Errorf("%w: %s", ErrRepairRequiresHumanApproval, reason)
	}
	if !IsOpenDefaultRepair(action) {
		return RepairDecision{Action: action, RequiresHumanApproval: true, Reason: "unrecognized repair action"}, fmt.Errorf("%w: %q", ErrRepairActionUnknown, action)
	}
	if policy.DisableAutoRepairGlobal || slices.Contains(policy.RequireApprovalFor, action) {
		reason := fmt.Sprintf("%s narrowed by owner repair policy to require approval", action)
		if hasVerifiedOwnerApproval {
			return RepairDecision{Action: action, AllowedWithoutApprove: false, RequiresHumanApproval: true, Reason: reason + " (verified owner approval supplied)"}, nil
		}
		return RepairDecision{Action: action, AllowedWithoutApprove: false, RequiresHumanApproval: true, Reason: reason}, fmt.Errorf("%w: %s", ErrRepairRequiresHumanApproval, reason)
	}
	return RepairDecision{
		Action:                action,
		AllowedWithoutApprove: true,
		RequiresHumanApproval: false,
		Reason:                "reversible non-destructive repair admitted under ADR-0051 open default",
	}, nil
}

// LoadRepairPolicy reads the optional owner narrowing policy if present.
func LoadRepairPolicy(workspace string) (RepairPolicy, error) {
	path := filepath.Join(workspace, filepath.FromSlash(RepairPolicyRelPath))
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RepairPolicy{SchemaVersion: "stackkit.repair-policy/v1"}, nil
		}
		return RepairPolicy{}, err
	}
	var policy RepairPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return RepairPolicy{}, fmt.Errorf("decode repair policy: %w", err)
	}
	return policy, nil
}

// CircuitBreakerState tracks rolling auto-repair timestamps per signature ID.
type CircuitBreakerState struct {
	SchemaVersion string                 `json:"schemaVersion"`
	Attempts      map[string][]time.Time `json:"attempts"`
}

// CircuitBreakerStore enforces max 2 auto-repairs per signature per hour.
type CircuitBreakerStore struct {
	Path string
	Now  func() time.Time
}

// OpenCircuitBreaker opens the workspace's durable circuit breaker ledger.
func OpenCircuitBreaker(workspace string) *CircuitBreakerStore {
	return &CircuitBreakerStore{
		Path: filepath.Join(workspace, filepath.FromSlash(CircuitBreakerRelPath)),
		Now:  time.Now,
	}
}

// CheckAndRecord enforces the rolling 2/signature/hour cap and records one
// attempt when admitted.
func (c *CircuitBreakerStore) CheckAndRecord(signatureID string) (int, error) {
	signatureID = strings.TrimSpace(signatureID)
	if signatureID == "" {
		return 0, errors.New("circuit breaker requires a signature ID")
	}
	nowFn := c.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()
	cutoff := now.Add(-CircuitBreakerWindow)

	state := CircuitBreakerState{
		SchemaVersion: "stackkit.repair-circuit-breaker/v1",
		Attempts:      map[string][]time.Time{},
	}
	if raw, err := os.ReadFile(c.Path); err == nil {
		_ = json.Unmarshal(raw, &state)
		if state.Attempts == nil {
			state.Attempts = map[string][]time.Time{}
		}
	}

	recent := make([]time.Time, 0, len(state.Attempts[signatureID])+1)
	for _, ts := range state.Attempts[signatureID] {
		if ts.After(cutoff) {
			recent = append(recent, ts)
		}
	}
	if len(recent) >= CircuitBreakerMaxPerHour {
		return len(recent), fmt.Errorf("%w: signature %q already attempted %d time(s) in the last hour", ErrCircuitBreakerTripped, signatureID, len(recent))
	}
	recent = append(recent, now)
	state.Attempts[signatureID] = recent

	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return len(recent), err
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return len(recent), err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", c.Path, os.Getpid())
	if err := os.WriteFile(tmp, append(encoded, '\n'), 0o600); err != nil {
		return len(recent), err
	}
	if err := os.Rename(tmp, c.Path); err != nil {
		_ = os.Remove(tmp)
		return len(recent), err
	}
	return len(recent), nil
}
