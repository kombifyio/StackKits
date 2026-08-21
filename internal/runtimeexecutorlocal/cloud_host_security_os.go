package runtimeexecutorlocal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	cloudHostSecurityTable     = "stackkits_cloud_host_security"
	cloudHostSecurityBaseChain = "stackkits_cloud_host_base"
	cloudHostSecurityEdgeChain = "stackkits_cloud_public_edge"
	cloudHostSecuritySSHDropIn = "/etc/ssh/sshd_config.d/60-stackkits-cloud-host-security.conf"
	// sshd refuses to parse any configuration without this directory.
	cloudHostSecuritySSHDRuntimeDirectory = "/run/sshd"
	cloudHostSecurityAPTDropIn            = "/etc/apt/apt.conf.d/60-stackkits-cloud-host-security"
	cloudHostSecurityEvidenceRoot         = "cloud-host-security"

	cloudHostSecurityApplyOperation     = "apply-cloud-host-firewall"
	cloudHostSecurityReconcileOperation = "reconcile-cloud-host-firewall"
	cloudHostSecurityHardenOperation    = "apply-cloud-host-hardening"

	cloudHostSecurityHardeningProfile = "internet-host-baseline-v1"
)

// osCloudHostSecurityOperations owns the Cloud host firewall and hardening
// posture of exactly one node through the local operating system. It never
// executes caller-supplied commands: every process it starts is one of the
// closed nft, sshd, and systemctl invocations below.
type osCloudHostSecurityOperations struct {
	workspaceRoot string
	runner        cloudHostSecurityProcessRunner
	mu            sync.Mutex
	now           func() time.Time
}

// NewOSCloudHostSecurityOperations explicitly selects the local operating
// system as the closed Cloud host-security capability owner. Constructing an
// executor does not grant this authority; product composition must opt in.
func NewOSCloudHostSecurityOperations(workspaceRoot string) (*osCloudHostSecurityOperations, error) {
	return newOSCloudHostSecurityOperations(workspaceRoot, osCloudHostSecurityProcessRunner{})
}

func newOSCloudHostSecurityOperations(workspaceRoot string, runner cloudHostSecurityProcessRunner) (*osCloudHostSecurityOperations, error) {
	root, err := cloudNodeOwnerWorkspaceRoot(workspaceRoot, runner, "local Cloud host security")
	if err != nil {
		return nil, err
	}
	return &osCloudHostSecurityOperations{
		workspaceRoot: root,
		runner:        runner,
		now:           func() time.Time { return time.Now().UTC() },
	}, nil
}

// cloudNodeOwnerWorkspaceRoot is the shared admission for every OS-backed
// Cloud node owner that runs host processes: the plain workspace directory
// plus a bounded process runner, or nothing.
func cloudNodeOwnerWorkspaceRoot(workspaceRoot string, runner cloudHostSecurityProcessRunner, owner string) (string, error) {
	root, err := ownerWorkspaceRoot(workspaceRoot, owner)
	if err != nil {
		return "", err
	}
	if runner == nil {
		return "", errors.New(owner + " requires a bounded process runner")
	}
	return root, nil
}

// ownerWorkspaceRoot admits the workspace directory every OS-backed owner is
// scoped to: absolute, existing, a plain directory, never a symlink.
func ownerWorkspaceRoot(workspaceRoot, owner string) (string, error) {
	absolute, err := filepath.Abs(workspaceRoot)
	if err != nil || strings.TrimSpace(workspaceRoot) == "" {
		return "", errors.New(owner + " requires an absolute workspace root")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New(owner + " requires an existing plain workspace directory")
	}
	return filepath.Clean(absolute), nil
}

func (o *osCloudHostSecurityOperations) ApplyFirewall(ctx context.Context, policy CloudFirewallPolicy) (CloudHostSecurityApplyObservation, error) {
	return o.enforceFirewall(ctx, policy, cloudHostSecurityApplyOperation, "applied")
}

func (o *osCloudHostSecurityOperations) ReconcileFirewall(ctx context.Context, policy CloudFirewallPolicy) (CloudHostSecurityApplyObservation, error) {
	return o.enforceFirewall(ctx, policy, cloudHostSecurityReconcileOperation, "reconciled")
}

// enforceFirewall installs the owned ruleset and then reads the live table
// back. The observation is derived from what the host reports, never from the
// requested policy alone, so a silently rejected ruleset cannot be reported as
// applied.
func (o *osCloudHostSecurityOperations) enforceFirewall(ctx context.Context, policy CloudFirewallPolicy, operation, status string) (CloudHostSecurityApplyObservation, error) {
	if err := cloudHostSecurityContext(ctx); err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	if err := o.ready(); err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	if err := validCloudFirewallPolicy(policy); err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.ensureManagedTooling(ctx, "nft"); err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	ruleset := renderCloudHostFirewallRuleset(policy)
	rulesetPath, err := o.persist("firewall", "ruleset.nft", []byte(ruleset), 0o600)
	if err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	if _, err := o.runner.Run(ctx, cloudHostSecurityNFTApplyArgs(rulesetPath)); err != nil {
		return CloudHostSecurityApplyObservation{}, fmt.Errorf("install the owned Cloud host ruleset: %w", err)
	}
	observed, err := o.observeFirewall(ctx)
	if err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	if observed != renderCloudHostFirewallTable(policy) {
		return CloudHostSecurityApplyObservation{}, errors.New("the live Cloud host ruleset does not match the policy that was just installed")
	}
	return CloudHostSecurityApplyObservation{
		Operation: operation, PolicyDigest: policy.PolicyDigest, RequestDigest: policy.RequestDigest,
		StackID: policy.StackID, SiteRef: policy.SiteRef, NodeRef: policy.NodeRef,
		ExecutionChannelRef: policy.ExecutionChannelRef, EvaluatedAt: policy.EvaluatedAt,
		ObservedAt: o.observedAt(), StateDigest: policy.StateDigest, Status: status,
	}, nil
}

func (o *osCloudHostSecurityOperations) ApplyHardening(ctx context.Context, policy CloudHardeningPolicy) (CloudHostSecurityApplyObservation, error) {
	if err := cloudHostSecurityContext(ctx); err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	if err := o.ready(); err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	if err := validCloudHardeningPolicy(policy); err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.ensureManagedTooling(ctx, "fail2ban-server", "unattended-upgrade"); err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	if err := o.writeSSHHardening(ctx, policy); err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	if err := o.writeAutomaticSecurityUpdates(ctx, policy); err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	if err := o.enableBruteForceProtection(ctx, policy); err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	effective, err := o.observeSSHD(ctx)
	if err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	if err := effective.satisfies(policy); err != nil {
		return CloudHostSecurityApplyObservation{}, err
	}
	return CloudHostSecurityApplyObservation{
		Operation: cloudHostSecurityHardenOperation, PolicyDigest: policy.PolicyDigest, RequestDigest: policy.RequestDigest,
		StackID: policy.StackID, SiteRef: policy.SiteRef, NodeRef: policy.NodeRef,
		ExecutionChannelRef: policy.ExecutionChannelRef, EvaluatedAt: policy.EvaluatedAt,
		ObservedAt: o.observedAt(), StateDigest: policy.StateDigest, Status: "applied",
	}, nil
}

// VerifyHostSecurity re-observes the host after both mutations. It reports the
// expectation's own values only for the fields it has just proven; anything it
// cannot prove is an error rather than a passing status.
func (o *osCloudHostSecurityOperations) VerifyHostSecurity(ctx context.Context, expectation CloudHostSecurityVerifyExpectation) (CloudHostSecurityVerifyObservation, error) {
	if err := cloudHostSecurityContext(ctx); err != nil {
		return CloudHostSecurityVerifyObservation{}, err
	}
	if err := o.ready(); err != nil {
		return CloudHostSecurityVerifyObservation{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	observed, err := o.observeFirewall(ctx)
	if err != nil {
		return CloudHostSecurityVerifyObservation{}, err
	}
	expected := renderCloudHostFirewallTable(cloudFirewallPolicyFromExpectation(expectation))
	if observed != expected {
		return CloudHostSecurityVerifyObservation{}, errors.New("the live Cloud host ruleset is not the enforced policy")
	}
	effective, err := o.observeSSHD(ctx)
	if err != nil {
		return CloudHostSecurityVerifyObservation{}, err
	}
	if err := effective.satisfies(cloudHardeningPolicyFromExpectation(expectation)); err != nil {
		return CloudHostSecurityVerifyObservation{}, err
	}
	if err := o.verifyManagedUnits(ctx, expectation); err != nil {
		return CloudHostSecurityVerifyObservation{}, err
	}
	return CloudHostSecurityVerifyObservation{
		PolicyDigest: expectation.PolicyDigest, RequestDigest: expectation.RequestDigest,
		StackID: expectation.StackID, SiteRef: expectation.SiteRef, NodeRef: expectation.NodeRef,
		ExecutionChannelRef: expectation.ExecutionChannelRef, EvaluatedAt: expectation.EvaluatedAt,
		ObservedAt: o.observedAt(), Status: "ready",
		FirewallStatus: "enforced", FirewallStateDigest: expectation.FirewallStateDigest,
		NetworkMode: expectation.NetworkMode, TransportSubnet: expectation.TransportSubnet, IPv6: expectation.IPv6,
		BaseRuleset: expectation.BaseRuleset, PublicEdgeChain: expectation.PublicEdgeChain,
		DefaultIngress: expectation.DefaultIngress, DeclaredServicesOnly: expectation.DeclaredServicesOnly,
		BaseIngressRuleRefs:        append([]string(nil), expectation.BaseIngressRuleRefs...),
		UnauthorizedExceptionCount: 0,
		HardeningStatus:            "enforced", HardeningStateDigest: expectation.HardeningStateDigest,
		HardeningProfile: expectation.HardeningProfile, TLSMinVersion: expectation.TLSMinVersion,
		SSHKeyOnly: expectation.SSHKeyOnly, SSHRootLogin: expectation.SSHRootLogin,
		BruteForceProtection: expectation.BruteForceProtection, AutomaticSecurityUpdates: expectation.AutomaticSecurityUpdates,
	}, nil
}

// CommitEvidence persists the executor's evidence record under owner custody
// and returns the digest of exactly the bytes it stored.
func (o *osCloudHostSecurityOperations) CommitEvidence(ctx context.Context, evidence CloudHostSecurityEvidence) (CloudHostSecurityEvidenceReceipt, error) {
	if err := cloudHostSecurityContext(ctx); err != nil {
		return CloudHostSecurityEvidenceReceipt{}, err
	}
	if err := o.ready(); err != nil {
		return CloudHostSecurityEvidenceReceipt{}, err
	}
	if !validCoreHostBootstrapDigest(evidence.RequestDigest) || !validCoreHostBootstrapDigest(evidence.PolicyDigest) ||
		!validCoreHostBootstrapDigest(evidence.ArtifactDigest) || strings.TrimSpace(evidence.SchemaVersion) == "" {
		return CloudHostSecurityEvidenceReceipt{}, errors.New("Cloud host-security evidence is not a bound executor record")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	raw, err := json.Marshal(evidence)
	if err != nil {
		return CloudHostSecurityEvidenceReceipt{}, fmt.Errorf("marshal Cloud host-security evidence: %w", err)
	}
	sum := sha256.Sum256(raw)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if _, err := o.persist(cloudHostSecurityEvidenceRoot, hex.EncodeToString(sum[:])+".json", raw, 0o600); err != nil {
		return CloudHostSecurityEvidenceReceipt{}, err
	}
	return CloudHostSecurityEvidenceReceipt{EvidenceDigest: digest, CommittedAt: o.observedAt()}, nil
}

func (o *osCloudHostSecurityOperations) ready() error {
	if o == nil || o.workspaceRoot == "" || o.runner == nil || o.now == nil {
		return errors.New("local Cloud host security is not initialized")
	}
	return nil
}

func (o *osCloudHostSecurityOperations) observedAt() string {
	return o.now().UTC().Format(time.RFC3339Nano)
}

// persist writes owner-visible state under the workspace custody root. It
// never follows a symlink into or out of the workspace.
func (o *osCloudHostSecurityOperations) persist(scope, name string, content []byte, mode os.FileMode) (string, error) {
	if filepath.Base(name) != name || filepath.Base(scope) != scope {
		return "", errors.New("Cloud host-security state name is outside the custody contract")
	}
	directory := filepath.Join(o.workspaceRoot, ".stackkit", "cloud-host-security", scope)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", fmt.Errorf("create Cloud host-security custody directory: %w", err)
	}
	path := filepath.Join(directory, name)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Cloud host-security state path is a symlink")
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return "", fmt.Errorf("write Cloud host-security state: %w", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return "", fmt.Errorf("seal Cloud host-security state: %w", err)
	}
	return path, nil
}

func cloudHostSecurityContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("local Cloud host security requires a context")
	}
	return ctx.Err()
}
