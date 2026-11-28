package stackspecintent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SetupStyle selects the two-tier rollout behavior defined by ADR-0055 §3.
//
// Tier 1 (Core rollout + Required Safety Init) is mandatory in BOTH styles and
// always fails closed. SetupStyle controls ONLY Tier 2 (optional automatic
// application personalization such as owner/OIDC bootstrap).
type SetupStyle string

const (
	// SetupStyleBootstrapped runs Tier-2 automatic application personalization
	// after Tier 1 is healthy. A Tier-2 failure degrades only that application
	// (recording bootstrap_degraded while keeping it private behind Access Gate)
	// and never fails Core/OpenTofu apply.
	SetupStyleBootstrapped SetupStyle = "bootstrapped"
	// SetupStyleClean runs mandatory Tier-1 safety init only and explicitly
	// skips Tier-2 automatic app personalization (outcome skipped-clean-rollout),
	// leaving every application private behind Access Gate for its own first-run
	// wizard.
	SetupStyleClean SetupStyle = "clean"
)

const (
	// ApplicationDefaultBootstrapped is the canonical CUE
	// install.platform.setupPolicy.applicationDefault value for Bootstrapped
	// rollouts.
	ApplicationDefaultBootstrapped = "on-demand"
	// ApplicationDefaultCleanRollout is the canonical CUE
	// install.platform.setupPolicy.applicationDefault value for Clean Rollouts.
	ApplicationDefaultCleanRollout = "manual"
)

// Tier1SafetyRequirements enumerates the non-negotiable Tier-1 invariants that
// MUST hold in both Clean Rollout and Bootstrapped rollouts (ADR-0055 §3).
type Tier1SafetyRequirements struct {
	DigestPinnedImages        bool `json:"digestPinnedImages"`
	PrivateNetworks           bool `json:"privateNetworks"`
	WalletSecretCustody       bool `json:"walletSecretCustody"`
	ZeroDefaultPasswords      bool `json:"zeroDefaultPasswords"`
	UnsafeRegistrationClosed  bool `json:"unsafeRegistrationClosed"`
	CorePocketIDAndAccessGate bool `json:"corePocketIdAndAccessGate"`
	PrivateByDefaultRouting   bool `json:"privateByDefaultRouting"`
	FailClosed                bool `json:"failClosed"`
}

// MandatoryTier1Safety returns the invariant Tier-1 safety baseline. Clean
// Rollout never weakens or skips any field here.
func MandatoryTier1Safety() Tier1SafetyRequirements {
	return Tier1SafetyRequirements{
		DigestPinnedImages:        true,
		PrivateNetworks:           true,
		WalletSecretCustody:       true,
		ZeroDefaultPasswords:      true,
		UnsafeRegistrationClosed:  true,
		CorePocketIDAndAccessGate: true,
		PrivateByDefaultRouting:   true,
		FailClosed:                true,
	}
}

// RolloutTierPolicy describes how Tier 1 and Tier 2 execute for a SetupStyle.
type RolloutTierPolicy struct {
	SetupStyle               SetupStyle              `json:"setupStyle"`
	Tier1Mandatory           bool                    `json:"tier1Mandatory"`
	Tier1Safety              Tier1SafetyRequirements `json:"tier1Safety"`
	Tier2AutoPersonalize     bool                    `json:"tier2AutoPersonalize"`
	Tier2FailureDegradesOnly bool                    `json:"tier2FailureDegradesOnly"`
	Tier2CleanSkipOutcome    string                  `json:"tier2CleanSkipOutcome"`
}

// ParseSetupStyle normalizes and validates a setup style string. An empty
// value defaults to SetupStyleBootstrapped.
func ParseSetupStyle(raw string) (SetupStyle, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(SetupStyleBootstrapped):
		return SetupStyleBootstrapped, nil
	case string(SetupStyleClean), "clean-rollout":
		return SetupStyleClean, nil
	default:
		return "", fmt.Errorf("invalid setup style %q: expected %q or %q", raw, SetupStyleBootstrapped, SetupStyleClean)
	}
}

// ResolveSetupStyleFlags reconciles --setup-style and the --clean-rollout
// shorthand. Conflicting explicit values fail closed.
func ResolveSetupStyleFlags(setupStyleFlag string, cleanRolloutFlag bool, setupStyleChanged bool) (SetupStyle, error) {
	if cleanRolloutFlag {
		if setupStyleChanged {
			parsed, err := ParseSetupStyle(setupStyleFlag)
			if err != nil {
				return "", err
			}
			if parsed != SetupStyleClean {
				return "", fmt.Errorf("--clean-rollout cannot be combined with --setup-style=%s", parsed)
			}
		}
		return SetupStyleClean, nil
	}
	return ParseSetupStyle(setupStyleFlag)
}

// RolloutPolicyForSetupStyle returns the exact two-tier rollout contract for
// style. Tier 1 is always mandatory and fails closed; Tier 2 is enabled only
// for SetupStyleBootstrapped and degrades per workload without failing Core.
func RolloutPolicyForSetupStyle(style SetupStyle) (RolloutTierPolicy, error) {
	normalized, err := ParseSetupStyle(string(style))
	if err != nil {
		return RolloutTierPolicy{}, err
	}
	return RolloutTierPolicy{
		SetupStyle:               normalized,
		Tier1Mandatory:           true,
		Tier1Safety:              MandatoryTier1Safety(),
		Tier2AutoPersonalize:     normalized == SetupStyleBootstrapped,
		Tier2FailureDegradesOnly: true,
		Tier2CleanSkipOutcome:    "skipped-clean-rollout",
	}, nil
}

// ApplicationDefaultForSetupStyle maps SetupStyle onto the canonical CUE
// install.platform.setupPolicy.applicationDefault value.
func ApplicationDefaultForSetupStyle(style SetupStyle) (string, error) {
	normalized, err := ParseSetupStyle(string(style))
	if err != nil {
		return "", err
	}
	if normalized == SetupStyleClean {
		return ApplicationDefaultCleanRollout, nil
	}
	return ApplicationDefaultBootstrapped, nil
}

// SetupStyleFromApplicationDefault maps canonical CUE setupPolicy values back
// to SetupStyle.
func SetupStyleFromApplicationDefault(applicationDefault string) SetupStyle {
	if strings.EqualFold(strings.TrimSpace(applicationDefault), ApplicationDefaultCleanRollout) {
		return SetupStyleClean
	}
	return SetupStyleBootstrapped
}

// SetupStyleFromCanonicalStackSpec reads the persisted SetupStyle from a
// canonical StackSpec v2 JSON document. Defaults to SetupStyleBootstrapped.
func SetupStyleFromCanonicalStackSpec(canonical []byte) SetupStyle {
	if len(canonical) == 0 {
		return SetupStyleBootstrapped
	}
	var spec struct {
		Install struct {
			Mode     string `json:"mode"`
			Platform struct {
				SetupPolicy struct {
					ApplicationDefault string `json:"applicationDefault"`
				} `json:"setupPolicy"`
			} `json:"platform"`
		} `json:"install"`
	}
	if err := json.Unmarshal(canonical, &spec); err != nil {
		return SetupStyleBootstrapped
	}
	if strings.EqualFold(strings.TrimSpace(spec.Install.Platform.SetupPolicy.ApplicationDefault), ApplicationDefaultCleanRollout) ||
		strings.EqualFold(strings.TrimSpace(spec.Install.Mode), "bare") {
		return SetupStyleClean
	}
	return SetupStyleBootstrapped
}

// SetupStyleFromResolvedPlan reads the persisted SetupStyle from a decoded
// canonical ResolvedPlan map. Defaults to SetupStyleBootstrapped.
func SetupStyleFromResolvedPlan(plan map[string]any) SetupStyle {
	if plan == nil {
		return SetupStyleBootstrapped
	}
	install, _ := plan["install"].(map[string]any)
	if install == nil {
		return SetupStyleBootstrapped
	}
	if mode, _ := install["mode"].(string); strings.EqualFold(strings.TrimSpace(mode), "bare") {
		return SetupStyleClean
	}
	platform, _ := install["platform"].(map[string]any)
	setupPolicy, _ := platform["setupPolicy"].(map[string]any)
	applicationDefault, _ := setupPolicy["applicationDefault"].(string)
	return SetupStyleFromApplicationDefault(applicationDefault)
}
