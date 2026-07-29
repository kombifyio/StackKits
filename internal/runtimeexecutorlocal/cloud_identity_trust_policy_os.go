package runtimeexecutorlocal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	cloudPolicyIdentityTrust = "cloud-identity-trust"

	cloudIdentityTrustResponsibilityHumanIssuer      = "human-credential-issuer"
	cloudIdentityTrustResponsibilityWorkloadIssuer   = "workload-credential-issuer"
	cloudIdentityTrustResponsibilityDeviceSession    = "device-session-verification"
	cloudIdentityTrustResponsibilityHumanSession     = "human-session-verification"
	cloudIdentityTrustResponsibilityWorkloadIdentity = "workload-identity-verification"
)

// osCloudIdentityTrustPolicyOperations is the workspace-scoped owner of the
// Cloud identity-trust policy projection, mirroring the Basement policy owner:
// every enforced responsibility becomes owner-signed durable state anchored to
// the same local owner custody that signs Apply evidence. It issues no
// credentials and runs no processes.
type osCloudIdentityTrustPolicyOperations struct {
	workspaceRoot string
	mu            sync.Mutex
	now           func() time.Time
}

// NewOSCloudIdentityTrustPolicyOperations constructs the single
// workspace-scoped owner of the local Cloud identity-trust policy projection.
func NewOSCloudIdentityTrustPolicyOperations(workspaceRoot string) (*osCloudIdentityTrustPolicyOperations, error) {
	root, err := ownerWorkspaceRoot(workspaceRoot, "local Cloud identity-trust policies")
	if err != nil {
		return nil, err
	}
	return &osCloudIdentityTrustPolicyOperations{
		workspaceRoot: root,
		now:           func() time.Time { return time.Now().UTC() },
	}, nil
}

func (o *osCloudIdentityTrustPolicyOperations) enforce(ctx context.Context, policyDigest, responsibility string) error {
	if ctx == nil {
		return errors.New("local Cloud identity-trust policy enforcement requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if o == nil || o.workspaceRoot == "" || o.now == nil ||
		!validCoreHostBootstrapDigest(policyDigest) || responsibility == "" {
		return errors.New("local Cloud identity-trust policy enforcement is not initialized")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.verifyOwnerAuthority(); err != nil {
		return err
	}
	state, exists, err := o.loadState()
	if err != nil {
		return err
	}
	if !exists || state.PolicyDigest != policyDigest {
		state = ownerBoundPolicyState{
			APIVersion: "stackkit.owner-bound-policy-state/v1", Kind: "OwnerBoundPolicyState",
			PolicyKind: cloudPolicyIdentityTrust, PolicyDigest: policyDigest,
		}
	}
	if !containsString(state.Responsibilities, responsibility) {
		state.Responsibilities = append(state.Responsibilities, responsibility)
		sort.Strings(state.Responsibilities)
	}
	state.AppliedAt = o.now().UTC().Format(time.RFC3339Nano)
	state.OwnerSignature = localevidence.OwnerPolicyStateSignature{}
	signingBytes, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode local Cloud policy state for owner signature: %w", err)
	}
	state.OwnerSignature, err = localevidence.SignOwnerPolicyState(o.workspaceRoot, signingBytes)
	if err != nil {
		return fmt.Errorf("sign local Cloud policy state: %w", err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode signed local Cloud policy state: %w", err)
	}
	return o.persistState(raw)
}

func (o *osCloudIdentityTrustPolicyOperations) verify(ctx context.Context, policyDigest string, required []string) (time.Time, error) {
	if ctx == nil {
		return time.Time{}, errors.New("local Cloud identity-trust policy verification requires a context")
	}
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.verifyOwnerAuthority(); err != nil {
		return time.Time{}, err
	}
	state, exists, err := o.loadState()
	if err != nil {
		return time.Time{}, err
	}
	if !exists || state.PolicyDigest != policyDigest || !equalStringSets(state.Responsibilities, required) {
		return time.Time{}, errors.New("owner-bound local Cloud policy state does not prove every exact responsibility")
	}
	return o.now().UTC(), nil
}

// verifyOwnerAuthority anchors every mutation and verification to the local
// owner custody this workspace established at init. Unlike the Basement owner
// there is no core compose runtime to bind to on a Cloud node; the owner key
// custody is the authority the executor's evidence chain already trusts.
func (o *osCloudIdentityTrustPolicyOperations) verifyOwnerAuthority() error {
	if _, err := localevidence.LoadOwnerCustody(o.workspaceRoot); err != nil {
		return fmt.Errorf("verify local owner custody for Cloud policy enforcement: %w", err)
	}
	return nil
}

func (o *osCloudIdentityTrustPolicyOperations) statePath() string {
	return filepath.Join(o.workspaceRoot, ".stackkit", "runtime", "policies", cloudPolicyIdentityTrust+".json")
}

func (o *osCloudIdentityTrustPolicyOperations) loadState() (ownerBoundPolicyState, bool, error) {
	path := o.statePath()
	raw, err := os.ReadFile(path) //nolint:gosec // fixed construction-owned path
	if errors.Is(err, os.ErrNotExist) {
		return ownerBoundPolicyState{}, false, nil
	}
	if err != nil {
		return ownerBoundPolicyState{}, false, fmt.Errorf("read owner-bound local Cloud policy state: %w", err)
	}
	var state ownerBoundPolicyState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil || decoder.Decode(&struct{}{}) == nil ||
		state.APIVersion != "stackkit.owner-bound-policy-state/v1" ||
		state.Kind != "OwnerBoundPolicyState" || state.PolicyKind != cloudPolicyIdentityTrust ||
		!validCoreHostBootstrapDigest(state.PolicyDigest) || len(state.Responsibilities) == 0 {
		return ownerBoundPolicyState{}, false, errors.New("owner-bound local Cloud policy state is malformed")
	}
	canonical, err := json.Marshal(state)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ownerBoundPolicyState{}, false, errors.New("owner-bound local Cloud policy state is not canonical")
	}
	appliedAt, err := time.Parse(time.RFC3339Nano, state.AppliedAt)
	if err != nil || appliedAt.Location() != time.UTC ||
		appliedAt.Format(time.RFC3339Nano) != state.AppliedAt {
		return ownerBoundPolicyState{}, false, errors.New("owner-bound local Cloud policy state time is invalid")
	}
	signature := state.OwnerSignature
	state.OwnerSignature = localevidence.OwnerPolicyStateSignature{}
	signingBytes, err := json.Marshal(state)
	if err != nil || localevidence.VerifyOwnerPolicyState(o.workspaceRoot, signingBytes, signature) != nil {
		return ownerBoundPolicyState{}, false, errors.New("owner-bound local Cloud policy state signature does not verify")
	}
	state.OwnerSignature = signature
	return state, true, nil
}

func (o *osCloudIdentityTrustPolicyOperations) persistState(raw []byte) error {
	path := o.statePath()
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create local Cloud policy state directory: %w", err)
	}
	root, err := confinedfs.Open(directory)
	if err != nil {
		return fmt.Errorf("open local Cloud policy state directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(filepath.Base(path), raw)
	if err != nil || !result.Installed || !result.FileSynced {
		return errors.New("atomically persist owner-bound local Cloud policy state")
	}
	if err := restrictBasementRuntimeFile(path); err != nil {
		return fmt.Errorf("restrict owner-bound local Cloud policy state: %w", err)
	}
	return nil
}

func cloudIdentityTrustEnforcedObservation(digest string) CloudIdentityTrustApplyObservation {
	return CloudIdentityTrustApplyObservation{PolicyDigest: digest, Status: "enforced"}
}

func (o *osCloudIdentityTrustPolicyOperations) ConfigureHumanCredentialIssuer(ctx context.Context, policy CloudIdentityTrustRuntimePolicy) (CloudIdentityTrustApplyObservation, error) {
	return cloudIdentityTrustEnforcedObservation(policy.PolicyDigest), o.enforce(ctx, policy.PolicyDigest, cloudIdentityTrustResponsibilityHumanIssuer)
}

func (o *osCloudIdentityTrustPolicyOperations) ConfigureWorkloadCredentialIssuer(ctx context.Context, policy CloudIdentityTrustRuntimePolicy) (CloudIdentityTrustApplyObservation, error) {
	return cloudIdentityTrustEnforcedObservation(policy.PolicyDigest), o.enforce(ctx, policy.PolicyDigest, cloudIdentityTrustResponsibilityWorkloadIssuer)
}

func (o *osCloudIdentityTrustPolicyOperations) EnforceDeviceSessionVerification(ctx context.Context, policy CloudIdentityTrustRuntimePolicy) (CloudIdentityTrustApplyObservation, error) {
	return cloudIdentityTrustEnforcedObservation(policy.PolicyDigest), o.enforce(ctx, policy.PolicyDigest, cloudIdentityTrustResponsibilityDeviceSession)
}

func (o *osCloudIdentityTrustPolicyOperations) EnforceHumanSessionVerification(ctx context.Context, policy CloudIdentityTrustRuntimePolicy) (CloudIdentityTrustApplyObservation, error) {
	return cloudIdentityTrustEnforcedObservation(policy.PolicyDigest), o.enforce(ctx, policy.PolicyDigest, cloudIdentityTrustResponsibilityHumanSession)
}

func (o *osCloudIdentityTrustPolicyOperations) EnforceWorkloadIdentityVerification(ctx context.Context, policy CloudIdentityTrustRuntimePolicy) (CloudIdentityTrustApplyObservation, error) {
	return cloudIdentityTrustEnforcedObservation(policy.PolicyDigest), o.enforce(ctx, policy.PolicyDigest, cloudIdentityTrustResponsibilityWorkloadIdentity)
}

func (o *osCloudIdentityTrustPolicyOperations) VerifyCloudIdentityTrustPolicy(ctx context.Context, expectation CloudIdentityTrustVerifyExpectation) (CloudIdentityTrustVerifyObservation, error) {
	observedAt, err := o.verify(ctx, expectation.PolicyDigest, []string{
		cloudIdentityTrustResponsibilityHumanIssuer,
		cloudIdentityTrustResponsibilityWorkloadIssuer,
		cloudIdentityTrustResponsibilityDeviceSession,
		cloudIdentityTrustResponsibilityHumanSession,
		cloudIdentityTrustResponsibilityWorkloadIdentity,
	})
	return CloudIdentityTrustVerifyObservation{
		PolicyDigest: expectation.PolicyDigest, Status: "ready",
		HumanIssuerStatus: "enforced", WorkloadIssuerStatus: "enforced",
		DeviceVerifierStatus: "enforced", HumanVerifierStatus: "enforced", WorkloadVerifierStatus: "enforced",
		ObservedAt: observedAt.Format(time.RFC3339Nano),
	}, err
}

var _ CloudIdentityTrustPolicyOperations = (*osCloudIdentityTrustPolicyOperations)(nil)
