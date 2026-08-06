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
	"strings"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	localPolicyIdentity = "basement-identity-trust"
	localPolicyDevice   = "home-device-authority"
	localPolicyAccess   = "home-access"
	localPolicyAutonomy = "local-autonomy"
)

type ownerBoundPolicyState struct {
	APIVersion       string                                  `json:"apiVersion"`
	Kind             string                                  `json:"kind"`
	PolicyKind       string                                  `json:"policyKind"`
	PolicyDigest     string                                  `json:"policyDigest"`
	Responsibilities []string                                `json:"responsibilities"`
	AppliedAt        string                                  `json:"appliedAt"`
	OwnerSignature   localevidence.OwnerPolicyStateSignature `json:"ownerSignature"`
}

type osBasementPolicyOperations struct {
	workspaceRoot string
	mu            sync.Mutex
	now           func() time.Time
}

// NewOSBasementPolicyOperations constructs the single workspace-scoped owner
// of all local Basement policy projections.
func NewOSBasementPolicyOperations(workspaceRoot string) (*osBasementPolicyOperations, error) {
	absolute, err := filepath.Abs(workspaceRoot)
	if err != nil || strings.TrimSpace(workspaceRoot) == "" {
		return nil, errors.New("local Basement policies require an absolute workspace root")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("local Basement policies require an existing plain workspace directory")
	}
	return &osBasementPolicyOperations{
		workspaceRoot: filepath.Clean(absolute),
		now:           func() time.Time { return time.Now().UTC() },
	}, nil
}

func (o *osBasementPolicyOperations) enforce(ctx context.Context, policyKind, policyDigest, responsibility string) error {
	if ctx == nil {
		return errors.New("local Basement policy enforcement requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if o == nil || o.workspaceRoot == "" || o.now == nil ||
		!validCoreHostBootstrapDigest(policyDigest) || responsibility == "" {
		return errors.New("local Basement policy enforcement is not initialized")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.verifyRuntimeAuthority(); err != nil {
		return err
	}
	state, exists, err := o.loadState(policyKind)
	if err != nil {
		return err
	}
	if !exists || state.PolicyDigest != policyDigest {
		state = ownerBoundPolicyState{
			APIVersion: "stackkit.owner-bound-policy-state/v1", Kind: "OwnerBoundPolicyState",
			PolicyKind: policyKind, PolicyDigest: policyDigest,
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
		return fmt.Errorf("encode local policy state for owner signature: %w", err)
	}
	state.OwnerSignature, err = localevidence.SignOwnerPolicyState(o.workspaceRoot, signingBytes)
	if err != nil {
		return fmt.Errorf("sign local policy state: %w", err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode signed local policy state: %w", err)
	}
	return o.persistState(policyKind, raw)
}

func (o *osBasementPolicyOperations) verify(ctx context.Context, policyKind, policyDigest string, required []string) (time.Time, error) {
	if ctx == nil {
		return time.Time{}, errors.New("local Basement policy verification requires a context")
	}
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.verifyRuntimeAuthority(); err != nil {
		return time.Time{}, err
	}
	state, exists, err := o.loadState(policyKind)
	if err != nil {
		return time.Time{}, err
	}
	if !exists || state.PolicyDigest != policyDigest || !equalStringSets(state.Responsibilities, required) {
		return time.Time{}, errors.New("owner-bound local policy state does not prove every exact responsibility")
	}
	return o.now().UTC(), nil
}

func (o *osBasementPolicyOperations) verifyRuntimeAuthority() error {
	if _, err := localevidence.LoadBasementRuntimeCustody(o.workspaceRoot); err != nil {
		return fmt.Errorf("verify local Basement runtime custody for policy enforcement: %w", err)
	}
	composePath := filepath.Join(o.workspaceRoot, ".stackkit", "runtime", "basement-core", "compose.yaml")
	content, err := os.ReadFile(composePath) //nolint:gosec // fixed construction-owned path
	if err != nil || !architecturev2renderer.ValidateBasementCoreComposeArtifact(content) {
		return errors.New("local Basement core runtime is not the exact owner-bound platform")
	}
	return nil
}

func (o *osBasementPolicyOperations) statePath(policyKind string) (string, error) {
	switch policyKind {
	case localPolicyIdentity, localPolicyDevice, localPolicyAccess, localPolicyAutonomy:
	default:
		return "", errors.New("unknown local Basement policy kind")
	}
	return filepath.Join(o.workspaceRoot, ".stackkit", "runtime", "policies", policyKind+".json"), nil
}

func (o *osBasementPolicyOperations) loadState(policyKind string) (ownerBoundPolicyState, bool, error) {
	path, err := o.statePath(policyKind)
	if err != nil {
		return ownerBoundPolicyState{}, false, err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // fixed policy-kind allowlist
	if errors.Is(err, os.ErrNotExist) {
		return ownerBoundPolicyState{}, false, nil
	}
	if err != nil {
		return ownerBoundPolicyState{}, false, fmt.Errorf("read owner-bound local policy state: %w", err)
	}
	var state ownerBoundPolicyState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil || decoder.Decode(&struct{}{}) == nil ||
		state.APIVersion != "stackkit.owner-bound-policy-state/v1" ||
		state.Kind != "OwnerBoundPolicyState" || state.PolicyKind != policyKind ||
		!validCoreHostBootstrapDigest(state.PolicyDigest) || len(state.Responsibilities) == 0 {
		return ownerBoundPolicyState{}, false, errors.New("owner-bound local policy state is malformed")
	}
	canonical, err := json.Marshal(state)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ownerBoundPolicyState{}, false, errors.New("owner-bound local policy state is not canonical")
	}
	appliedAt, err := time.Parse(time.RFC3339Nano, state.AppliedAt)
	if err != nil || appliedAt.Location() != time.UTC ||
		appliedAt.Format(time.RFC3339Nano) != state.AppliedAt {
		return ownerBoundPolicyState{}, false, errors.New("owner-bound local policy state time is invalid")
	}
	signature := state.OwnerSignature
	state.OwnerSignature = localevidence.OwnerPolicyStateSignature{}
	signingBytes, err := json.Marshal(state)
	if err != nil || localevidence.VerifyOwnerPolicyState(o.workspaceRoot, signingBytes, signature) != nil {
		return ownerBoundPolicyState{}, false, errors.New("owner-bound local policy state signature does not verify")
	}
	state.OwnerSignature = signature
	return state, true, nil
}

func (o *osBasementPolicyOperations) persistState(policyKind string, raw []byte) error {
	path, err := o.statePath(policyKind)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create local policy state directory: %w", err)
	}
	root, err := confinedfs.Open(directory)
	if err != nil {
		return fmt.Errorf("open local policy state directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(filepath.Base(path), raw)
	if err != nil || !result.Installed || !result.FileSynced {
		return errors.New("atomically persist owner-bound local policy state")
	}
	if err := restrictBasementRuntimeFile(path); err != nil {
		return fmt.Errorf("restrict owner-bound local policy state: %w", err)
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func equalStringSets(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func enforcedPolicyObservation(digest string) BasementIdentityTrustApplyObservation {
	return BasementIdentityTrustApplyObservation{PolicyDigest: digest, Status: "enforced"}
}

func (o *osBasementPolicyOperations) EnforceDeviceSessionVerification(ctx context.Context, policy BasementIdentityTrustRuntimePolicy) (BasementIdentityTrustApplyObservation, error) {
	return enforcedPolicyObservation(policy.PolicyDigest), o.enforce(ctx, localPolicyIdentity, policy.PolicyDigest, "device-session-verification")
}
func (o *osBasementPolicyOperations) EnforceHumanSessionVerification(ctx context.Context, policy BasementIdentityTrustRuntimePolicy) (BasementIdentityTrustApplyObservation, error) {
	return enforcedPolicyObservation(policy.PolicyDigest), o.enforce(ctx, localPolicyIdentity, policy.PolicyDigest, "human-session-verification")
}
func (o *osBasementPolicyOperations) EnforceWorkloadIdentityVerification(ctx context.Context, policy BasementIdentityTrustRuntimePolicy) (BasementIdentityTrustApplyObservation, error) {
	return enforcedPolicyObservation(policy.PolicyDigest), o.enforce(ctx, localPolicyIdentity, policy.PolicyDigest, "workload-identity-verification")
}
func (o *osBasementPolicyOperations) VerifyBasementIdentityTrustPolicy(ctx context.Context, expectation BasementIdentityTrustVerifyExpectation) (BasementIdentityTrustVerifyObservation, error) {
	observedAt, err := o.verify(ctx, localPolicyIdentity, expectation.PolicyDigest, []string{"device-session-verification", "human-session-verification", "workload-identity-verification"})
	return BasementIdentityTrustVerifyObservation{
		PolicyDigest: expectation.PolicyDigest, Status: "ready", DeviceVerifierStatus: "enforced",
		HumanVerifierStatus: "enforced", WorkloadVerifierStatus: "enforced",
		ObservedAt: observedAt.Format(time.RFC3339Nano),
	}, err
}

func (o *osBasementPolicyOperations) ConfigureDeviceEnrollment(ctx context.Context, policy HomeDeviceAuthorityRuntimePolicy) (HomeDeviceAuthorityApplyObservation, error) {
	err := o.enforce(ctx, localPolicyDevice, policy.PolicyDigest, "device-enrollment")
	return HomeDeviceAuthorityApplyObservation{PolicyDigest: policy.PolicyDigest, Status: "enforced"}, err
}
func (o *osBasementPolicyOperations) ConfigureDeviceCredentialIssuer(ctx context.Context, policy HomeDeviceAuthorityRuntimePolicy) (HomeDeviceAuthorityApplyObservation, error) {
	err := o.enforce(ctx, localPolicyDevice, policy.PolicyDigest, "credential-issuer")
	return HomeDeviceAuthorityApplyObservation{PolicyDigest: policy.PolicyDigest, Status: "enforced"}, err
}
func (o *osBasementPolicyOperations) ConfigureDeviceCredentialRevocation(ctx context.Context, policy HomeDeviceAuthorityRuntimePolicy) (HomeDeviceAuthorityApplyObservation, error) {
	err := o.enforce(ctx, localPolicyDevice, policy.PolicyDigest, "credential-revocation")
	return HomeDeviceAuthorityApplyObservation{PolicyDigest: policy.PolicyDigest, Status: "enforced"}, err
}
func (o *osBasementPolicyOperations) VerifyHomeDeviceAuthorityPolicy(ctx context.Context, expectation HomeDeviceAuthorityVerifyExpectation) (HomeDeviceAuthorityVerifyObservation, error) {
	observedAt, err := o.verify(ctx, localPolicyDevice, expectation.PolicyDigest, []string{"credential-issuer", "credential-revocation", "device-enrollment"})
	return HomeDeviceAuthorityVerifyObservation{
		PolicyDigest: expectation.PolicyDigest, Status: "ready", EnrollmentStatus: "enforced",
		IssuerStatus: "enforced", RevocationStatus: "enforced",
		ObservedAt: observedAt.Format(time.RFC3339Nano),
	}, err
}

func (o *osBasementPolicyOperations) EnforceLANAccess(ctx context.Context, policy HomeAccessRuntimePolicy) (HomeAccessApplyObservation, error) {
	err := o.enforce(ctx, localPolicyAccess, policy.PolicyDigest, "lan-access")
	return HomeAccessApplyObservation{PolicyDigest: policy.PolicyDigest, Status: "enforced"}, err
}
func (o *osBasementPolicyOperations) EnforceLocalIngress(ctx context.Context, policy HomeAccessRuntimePolicy) (HomeAccessApplyObservation, error) {
	err := o.enforce(ctx, localPolicyAccess, policy.PolicyDigest, "local-ingress")
	return HomeAccessApplyObservation{PolicyDigest: policy.PolicyDigest, Status: "enforced"}, err
}
func (o *osBasementPolicyOperations) EnforcePrivilegedStepUp(ctx context.Context, policy HomeAccessRuntimePolicy) (HomeAccessApplyObservation, error) {
	err := o.enforce(ctx, localPolicyAccess, policy.PolicyDigest, "privileged-step-up")
	return HomeAccessApplyObservation{PolicyDigest: policy.PolicyDigest, Status: "enforced"}, err
}
func (o *osBasementPolicyOperations) VerifyHomeAccessPolicy(ctx context.Context, expectation HomeAccessVerifyExpectation) (HomeAccessVerifyObservation, error) {
	observedAt, err := o.verify(ctx, localPolicyAccess, expectation.PolicyDigest, []string{"lan-access", "local-ingress", "privileged-step-up"})
	return HomeAccessVerifyObservation{
		PolicyDigest: expectation.PolicyDigest, Status: "ready", LANAccessStatus: "enforced",
		LocalIngressStatus: "enforced", PrivilegedStepUpStatus: "enforced",
		ObservedAt: observedAt.Format(time.RFC3339Nano),
	}, err
}

func (o *osBasementPolicyOperations) DenyForbiddenCrossSiteSessions(ctx context.Context, policy LocalAutonomyRuntimePolicy) (LocalAutonomyApplyObservation, error) {
	err := o.enforce(ctx, localPolicyAutonomy, policy.PolicyDigest, "deny-cross-site")
	return LocalAutonomyApplyObservation{PolicyDigest: policy.PolicyDigest, Status: "enforced"}, err
}
func (o *osBasementPolicyOperations) EnforceLinkLossPolicy(ctx context.Context, policy LocalAutonomyRuntimePolicy) (LocalAutonomyApplyObservation, error) {
	err := o.enforce(ctx, localPolicyAutonomy, policy.PolicyDigest, "link-loss")
	return LocalAutonomyApplyObservation{PolicyDigest: policy.PolicyDigest, Status: "enforced"}, err
}
func (o *osBasementPolicyOperations) PreserveLocalControl(ctx context.Context, policy LocalAutonomyRuntimePolicy) (LocalAutonomyApplyObservation, error) {
	err := o.enforce(ctx, localPolicyAutonomy, policy.PolicyDigest, "local-control")
	return LocalAutonomyApplyObservation{PolicyDigest: policy.PolicyDigest, Status: "preserved"}, err
}
func (o *osBasementPolicyOperations) VerifyLocalAutonomyPolicy(ctx context.Context, expectation LocalAutonomyVerifyExpectation) (LocalAutonomyVerifyObservation, error) {
	observedAt, err := o.verify(ctx, localPolicyAutonomy, expectation.PolicyDigest, []string{"deny-cross-site", "link-loss", "local-control"})
	return LocalAutonomyVerifyObservation{
		PolicyDigest: expectation.PolicyDigest, Status: "ready", CrossSiteSessionStatus: "denied",
		LinkLossStatus: "enforced", LocalControlStatus: "preserved",
		ObservedAt: observedAt.Format(time.RFC3339Nano),
	}, err
}

var (
	_ BasementIdentityTrustPolicyOperations = (*osBasementPolicyOperations)(nil)
	_ HomeDeviceAuthorityPolicyOperations   = (*osBasementPolicyOperations)(nil)
	_ HomeAccessPolicyOperations            = (*osBasementPolicyOperations)(nil)
	_ LocalAutonomyPolicyOperations         = (*osBasementPolicyOperations)(nil)
)
