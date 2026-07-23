// Package rilactionpolicy contains the small, pure validation boundary between
// CUE-projected RIL primitive policy and shared public-safe action evidence.
// It deliberately has no service, storage, transport, provider, or runtime
// dependencies so policy changes can be tested without linking Architecture v2.
package rilactionpolicy

import (
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/rilactionv2"
)

// RecoveryContract is the CUE-projected recovery policy for one primitive.
type RecoveryContract struct {
	Kind              string `json:"kind"`
	RequiredOnFailure bool   `json:"requiredOnFailure"`
	PrimitiveRef      string `json:"primitiveRef,omitempty"`
}

// ProtectedDiagnosticPolicy is the CUE-projected opaque diagnostic boundary.
type ProtectedDiagnosticPolicy struct {
	Allowed          bool   `json:"allowed"`
	Required         bool   `json:"required"`
	ReferenceScheme  string `json:"referenceScheme"`
	CustodyAuthority string `json:"custodyAuthority"`
	InlineMaterial   bool   `json:"inlineMaterial"`
	DirectAccess     bool   `json:"directAccess"`
}

// ValidateProtectedDiagnostic requires the exact closed CUE policy and rejects
// foreign reference schemes without acquiring diagnostic retrieval authority.
func ValidateProtectedDiagnostic(primitiveID string, policy ProtectedDiagnosticPolicy, evidence rilaction.Evidence) error {
	if !policy.Allowed || policy.Required || policy.ReferenceScheme != "diagnostic" ||
		policy.CustodyAuthority != "techstack" || policy.InlineMaterial || policy.DirectAccess {
		return fmt.Errorf("primitive %q has a widened protected-diagnostic policy", primitiveID)
	}
	if evidence.ProtectedDiagnosticRef != "" && !strings.HasPrefix(evidence.ProtectedDiagnosticRef, policy.ReferenceScheme+":") {
		return fmt.Errorf("primitive %q evidence has a foreign protected-diagnostic reference", primitiveID)
	}
	return nil
}

// ValidateExecutorEvidence binds public evidence to the complete CUE-selected
// executor identity. The contract hash is already folded into the primitive
// request identity; evidence must retain the selected executor reference.
func ValidateExecutorEvidence(primitiveID, support string, identity rilaction.ExecutorIdentity, evidence rilaction.Evidence) error {
	if support != "executor-bound" || identity.Ref == "" || identity.Version == "" || identity.ContractHash == "" {
		return fmt.Errorf("primitive %q has no closed CUE executor identity", primitiveID)
	}
	if evidence.ExecutorRef != identity.Ref {
		return fmt.Errorf("primitive %q evidence names executor %q instead of %q", primitiveID, evidence.ExecutorRef, identity.Ref)
	}
	return nil
}

// ValidateRecovery proves that one evidence document reports only the recovery
// disposition authorized by its CUE primitive. A separate recovery primitive
// never executes inside this evidence record.
func ValidateRecovery(primitiveID string, recovery RecoveryContract, evidence rilaction.Evidence) error {
	if evidence.Status == "succeeded" {
		if evidence.Recovery.Kind != "none" || evidence.Recovery.Status != "not-required" || evidence.Recovery.PrimitiveRef != "" {
			return fmt.Errorf("successful primitive %q cannot require recovery", primitiveID)
		}
		return nil
	}
	if evidence.Status != "failed" {
		return fmt.Errorf("primitive %q returned unsupported evidence status %q", primitiveID, evidence.Status)
	}
	switch recovery.Kind {
	case "none":
		if evidence.Recovery.Kind != "none" || evidence.Recovery.Status != "not-required" || evidence.Recovery.PrimitiveRef != "" {
			return fmt.Errorf("primitive %q has no governed recovery action", primitiveID)
		}
	case "primitive":
		if evidence.Recovery.Kind != "primitive" || evidence.Recovery.Status != "required" ||
			evidence.Recovery.PrimitiveRef != recovery.PrimitiveRef {
			return fmt.Errorf("primitive %q must require separately approved recovery %q", primitiveID, recovery.PrimitiveRef)
		}
	case "manual":
		if evidence.Recovery.Kind != "manual" || evidence.Recovery.Status != "manual-required" || evidence.Recovery.PrimitiveRef != "" {
			return fmt.Errorf("primitive %q must report manual recovery", primitiveID)
		}
	default:
		return fmt.Errorf("primitive %q has unsupported CUE recovery kind %q", primitiveID, recovery.Kind)
	}
	return nil
}
