package architecturev2

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productApplyContinuationAPIVersion = "stackkits.product-apply-continuation/v1alpha1"

// productApplyContinuation is the versioned, service-owned replacement for
// only the short-lived evidence authorization inside one persisted recovery
// capsule. It cannot change plan, artifacts, executor identity, requirements,
// or the original recovery lookup identity.
type productApplyContinuation struct {
	APIVersion            string                           `json:"api_version"`
	RecoveryRequestDigest string                           `json:"recovery_request_digest"`
	EvaluatedAt           string                           `json:"evaluated_at"`
	ValidUntil            string                           `json:"valid_until"`
	Request               applyRuntimeExecutionRequest     `json:"request"`
	Shared                runtimeexecutor.ExecutionRequest `json:"shared_request"`
	ContinuationDigest    string                           `json:"continuation_digest"`
}

func newProductApplyContinuation(
	recoveryRequestDigest string,
	capsule productApplyRecoveryCapsule,
	request applyRuntimeExecutionRequest,
	shared runtimeexecutor.ExecutionRequest,
	validUntil time.Time,
) (productApplyContinuation, error) {
	continuation := productApplyContinuation{
		APIVersion: productApplyContinuationAPIVersion, RecoveryRequestDigest: recoveryRequestDigest,
		EvaluatedAt: request.ExecutionAt.Format(time.RFC3339Nano), ValidUntil: validUntil.Format(time.RFC3339Nano),
		Request: request, Shared: runtimeexecutor.CloneExecutionRequest(shared),
	}
	if err := validateProductApplyContinuation(continuation, capsule); err != nil {
		return productApplyContinuation{}, err
	}
	digest, err := productApplyContinuationDigest(continuation)
	if err != nil {
		return productApplyContinuation{}, err
	}
	continuation.ContinuationDigest = digest
	return continuation, validateProductApplyContinuation(continuation, capsule)
}

func validateProductApplyContinuation(continuation productApplyContinuation, capsule productApplyRecoveryCapsule) error {
	if continuation.APIVersion != productApplyContinuationAPIVersion ||
		continuation.RecoveryRequestDigest != capsule.Shared.RequestDigest ||
		!validProductApplyDigest(continuation.RecoveryRequestDigest) {
		return errors.New("Product Apply continuation identity is invalid")
	}
	evaluatedAt, err := time.Parse(time.RFC3339Nano, continuation.EvaluatedAt)
	if err != nil || evaluatedAt.Location() != time.UTC || evaluatedAt.Format(time.RFC3339Nano) != continuation.EvaluatedAt ||
		continuation.Request.ExecutionAt != evaluatedAt || evaluatedAt.Before(capsule.Request.ExecutionAt) {
		return errors.New("Product Apply continuation evaluation instant is invalid")
	}
	validUntil, err := time.Parse(time.RFC3339Nano, continuation.ValidUntil)
	if err != nil || validUntil.Location() != time.UTC || validUntil.Format(time.RFC3339Nano) != continuation.ValidUntil ||
		!evaluatedAt.Before(validUntil) {
		return errors.New("Product Apply continuation validity is invalid")
	}
	if len(continuation.Request.EvidenceBundle) == 0 || !validProductApplyDigest(continuation.Request.EvidenceBundleHash) {
		return errors.New("Product Apply continuation requires fresh verified evidence")
	}
	immutableOriginal := capsule.Request
	immutableOriginal.EvidenceBundle = nil
	immutableOriginal.EvidenceBundleHash = ""
	immutableOriginal.ExecutionAt = time.Time{}
	immutableContinuation := continuation.Request
	immutableContinuation.EvidenceBundle = nil
	immutableContinuation.EvidenceBundleHash = ""
	immutableContinuation.ExecutionAt = time.Time{}
	if !reflect.DeepEqual(immutableOriginal, immutableContinuation) {
		return errors.New("Product Apply continuation changed immutable recovery authority")
	}
	if err := continuation.Shared.Validate(); err != nil {
		return fmt.Errorf("validate Product Apply continuation shared request: %w", err)
	}
	reconstructed, err := (&sharedRuntimeExecutorBridge{executor: nil}).sharedRuntimeRequest(continuation.Request)
	if err != nil {
		// A nil bridge cannot restore service-owned execution channels; compare
		// the path-free immutable fields below and let the owner registry validate
		// the already sealed routed request before execution.
		reconstructed, err = sharedExecutionRequest(continuation.Request)
	}
	if err != nil {
		return fmt.Errorf("reconstruct Product Apply continuation request: %w", err)
	}
	if reconstructed.RequestDigest == continuation.Shared.RequestDigest {
		// This is the local/no-route case. Routed requests are compared by
		// removing only their service-owned execution channel projection.
	} else if !productApplyContinuationSharedEqualIgnoringChannels(reconstructed, continuation.Shared) {
		return errors.New("Product Apply continuation does not bind the exact refreshed request")
	}
	if continuation.ContinuationDigest != "" {
		digest, err := productApplyContinuationDigest(continuation)
		if err != nil || digest != continuation.ContinuationDigest {
			return errors.New("Product Apply continuation digest is invalid")
		}
	}
	return nil
}

func productApplyContinuationSharedEqualIgnoringChannels(first, second runtimeexecutor.ExecutionRequest) bool {
	left := runtimeexecutor.CloneExecutionRequest(first)
	right := runtimeexecutor.CloneExecutionRequest(second)
	for index := range left.RuntimeTargets {
		left.RuntimeTargets[index].ExecutionChannelRef = ""
	}
	for index := range right.RuntimeTargets {
		right.RuntimeTargets[index].ExecutionChannelRef = ""
	}
	left.RequestDigest = ""
	right.RequestDigest = ""
	return reflect.DeepEqual(left, right)
}

func productApplyContinuationDigest(continuation productApplyContinuation) (string, error) {
	unsigned := continuation
	unsigned.ContinuationDigest = ""
	canonical, err := resolvedplan.CanonicalJSON(unsigned)
	if err != nil {
		return "", fmt.Errorf("canonicalize Product Apply continuation: %w", err)
	}
	return productApplyDigest(canonical), nil
}
