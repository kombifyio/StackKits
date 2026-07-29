package runtimeexecutorlocal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

const (
	cloudPublicEdgeApplyOperation     = "apply-public-edge"
	cloudPublicEdgeReconcileOperation = "remove-obsolete-public-edge"
	cloudPublicEdgeVerifyOperation    = "verify-public-edge"
	cloudPublicEdgeCustodyRoot        = "cloud-public-edge"
)

// osCloudPublicEdgeOperations owns the delegated public-edge chain inside the
// Cloud host-security table of exactly one node. The host-security owner
// creates that chain empty and default-deny; this owner fills it with exactly
// the compiler-declared routes and nothing else. DNS, certificates, secrets,
// and provider resources stay out of its capability, matching the contract.
type osCloudPublicEdgeOperations struct {
	workspaceRoot string
	runner        cloudHostSecurityProcessRunner
	mu            sync.Mutex
	now           func() time.Time
}

// NewOSCloudPublicEdgeOperations explicitly selects the local operating system
// as the closed Cloud public-edge capability owner. Product composition must
// opt in; constructing an executor grants nothing.
func NewOSCloudPublicEdgeOperations(workspaceRoot string) (*osCloudPublicEdgeOperations, error) {
	return newOSCloudPublicEdgeOperations(workspaceRoot, osCloudPublicEdgeProcessRunner{})
}

func newOSCloudPublicEdgeOperations(workspaceRoot string, runner cloudHostSecurityProcessRunner) (*osCloudPublicEdgeOperations, error) {
	root, err := cloudNodeOwnerWorkspaceRoot(workspaceRoot, runner, "local Cloud public edge")
	if err != nil {
		return nil, err
	}
	return &osCloudPublicEdgeOperations{
		workspaceRoot: root,
		runner:        runner,
		now:           func() time.Time { return time.Now().UTC() },
	}, nil
}

// cloudPublicEdgeCustodyState is what Apply proved and persisted. Reconcile
// and Verify receive only the expectation refs, so the exact rule set they
// must hold the host to comes from this owner-custody record, never from the
// live host itself.
type cloudPublicEdgeCustodyState struct {
	StateDigest     string   `json:"stateDigest"`
	PolicyDigest    string   `json:"policyDigest"`
	StackID         string   `json:"stackId"`
	SiteRef         string   `json:"siteRef"`
	NodeRef         string   `json:"nodeRef"`
	RouteRefs       []string `json:"routeRefs"`
	BackendPoolRefs []string `json:"backendPoolRefs"`
	HealthGateRefs  []string `json:"healthGateRefs"`
	ChainRules      []string `json:"chainRules"`
}

func (o *osCloudPublicEdgeOperations) ApplyPublicEdge(ctx context.Context, policy CloudPublicEdgeApplyPolicy) (CloudPublicEdgeObservation, error) {
	if err := cloudHostSecurityContext(ctx); err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	if err := o.ready(); err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	if err := validCloudPublicEdgePolicy(policy); err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	rules, err := renderCloudPublicEdgeRules(policy.Routes)
	if err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.installChain(ctx, rules); err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	if err := o.requireLiveChain(ctx, rules); err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	defaultClosed, err := o.observeParentDefaultClosed(ctx)
	if err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	routeRefs, backendRefs, healthRefs := cloudPublicEdgeRefs(policy.Routes)
	state := cloudPublicEdgeCustodyState{
		StateDigest: policy.StateDigest, PolicyDigest: policy.PolicyDigest, StackID: policy.StackID,
		SiteRef: policy.SiteRef, NodeRef: policy.NodeRef,
		RouteRefs: routeRefs, BackendPoolRefs: backendRefs, HealthGateRefs: healthRefs, ChainRules: rules,
	}
	if err := o.persistState(state); err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	return o.observation(cloudPublicEdgeApplyOperation, "applied", cloudPublicEdgeExpectationFromPolicy(policy), state, defaultClosed, 0), nil
}

func (o *osCloudPublicEdgeOperations) RemoveObsoletePublicEdge(ctx context.Context, expectation CloudPublicEdgeExpectation) (CloudPublicEdgeObservation, error) {
	return o.reconcile(ctx, expectation, cloudPublicEdgeReconcileOperation, "reconciled")
}

func (o *osCloudPublicEdgeOperations) VerifyPublicEdge(ctx context.Context, expectation CloudPublicEdgeExpectation) (CloudPublicEdgeObservation, error) {
	return o.reconcile(ctx, expectation, cloudPublicEdgeVerifyOperation, "ready")
}

// reconcile serves both remove-obsolete and verify: load the custody state the
// expectation names, hold the live chain to exactly that rule set, and prove
// the parent table still fails closed. Remove-obsolete reinstalls the exact
// set (dropping anything foreign); verify only observes and refuses drift.
func (o *osCloudPublicEdgeOperations) reconcile(ctx context.Context, expectation CloudPublicEdgeExpectation, operation, status string) (CloudPublicEdgeObservation, error) {
	if err := cloudHostSecurityContext(ctx); err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	if err := o.ready(); err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	state, err := o.loadState(expectation.StateDigest)
	if err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	if state.PolicyDigest != expectation.PolicyDigest || state.StackID != expectation.StackID ||
		state.SiteRef != expectation.SiteRef || state.NodeRef != expectation.NodeRef ||
		!slices.Equal(state.RouteRefs, expectation.RouteRefs) ||
		!slices.Equal(state.BackendPoolRefs, expectation.BackendPoolRefs) ||
		!slices.Equal(state.HealthGateRefs, expectation.HealthGateRefs) {
		return CloudPublicEdgeObservation{}, errors.New("Cloud public-edge custody state does not match the expectation")
	}
	if operation == cloudPublicEdgeReconcileOperation {
		if err := o.installChain(ctx, state.ChainRules); err != nil {
			return CloudPublicEdgeObservation{}, err
		}
	}
	if err := o.requireLiveChain(ctx, state.ChainRules); err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	defaultClosed, err := o.observeParentDefaultClosed(ctx)
	if err != nil {
		return CloudPublicEdgeObservation{}, err
	}
	if operation == cloudPublicEdgeVerifyOperation && !defaultClosed {
		return CloudPublicEdgeObservation{}, errors.New("the parent Cloud host ruleset no longer fails closed")
	}
	return o.observation(operation, status, expectation, state, defaultClosed, 0), nil
}

// CommitEvidence persists the executor's evidence record under owner custody
// and returns the digest of exactly the stored bytes.
func (o *osCloudPublicEdgeOperations) CommitEvidence(ctx context.Context, evidence CloudPublicEdgeEvidence) (CloudPublicEdgeEvidenceReceipt, error) {
	if err := cloudHostSecurityContext(ctx); err != nil {
		return CloudPublicEdgeEvidenceReceipt{}, err
	}
	if err := o.ready(); err != nil {
		return CloudPublicEdgeEvidenceReceipt{}, err
	}
	if !validCoreHostBootstrapDigest(evidence.RequestDigest) || !validCoreHostBootstrapDigest(evidence.PolicyDigest) ||
		!validCoreHostBootstrapDigest(evidence.ArtifactDigest) || !validCoreHostBootstrapDigest(evidence.StateDigest) ||
		strings.TrimSpace(evidence.SchemaVersion) == "" {
		return CloudPublicEdgeEvidenceReceipt{}, errors.New("Cloud public-edge evidence is not a bound executor record")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	raw, err := json.Marshal(evidence)
	if err != nil {
		return CloudPublicEdgeEvidenceReceipt{}, fmt.Errorf("marshal Cloud public-edge evidence: %w", err)
	}
	sum := sha256.Sum256(raw)
	if err := o.persist("evidence", hex.EncodeToString(sum[:])+".json", raw); err != nil {
		return CloudPublicEdgeEvidenceReceipt{}, err
	}
	return CloudPublicEdgeEvidenceReceipt{
		EvidenceDigest: "sha256:" + hex.EncodeToString(sum[:]),
		CommittedAt:    o.now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (o *osCloudPublicEdgeOperations) observation(operation, status string, expectation CloudPublicEdgeExpectation, state cloudPublicEdgeCustodyState, defaultClosed bool, unauthorized int) CloudPublicEdgeObservation {
	return CloudPublicEdgeObservation{
		Operation: operation, PolicyDigest: expectation.PolicyDigest, RequestDigest: expectation.RequestDigest,
		ArtifactDigest: expectation.ArtifactDigest, StateDigest: expectation.StateDigest,
		StackID: expectation.StackID, SiteRef: expectation.SiteRef, NodeRef: expectation.NodeRef,
		ExecutionChannelRef: expectation.ExecutionChannelRef, EvaluatedAt: expectation.EvaluatedAt,
		ObservedAt:       o.now().UTC().Format(time.RFC3339Nano),
		ParentRulesetRef: expectation.ParentRulesetRef, DelegatedChainRef: expectation.DelegatedChainRef,
		Status:    status,
		RouteRefs: append([]string(nil), state.RouteRefs...), BackendPoolRefs: append([]string(nil), state.BackendPoolRefs...),
		HealthGateRefs: append([]string(nil), state.HealthGateRefs...),
		DefaultClosed:  defaultClosed, UnauthorizedRoutes: unauthorized,
	}
}

// cloudPublicEdgeExpectationFromPolicy mirrors the executor's own derivation
// so the apply observation echoes the exact bound identity.
func cloudPublicEdgeExpectationFromPolicy(policy CloudPublicEdgeApplyPolicy) CloudPublicEdgeExpectation {
	routeRefs, backendRefs, healthRefs := cloudPublicEdgeRefs(policy.Routes)
	return CloudPublicEdgeExpectation{
		PolicyDigest: policy.PolicyDigest, RequestDigest: policy.RequestDigest, ArtifactDigest: policy.ArtifactDigest,
		StateDigest: policy.StateDigest, EvaluatedAt: policy.EvaluatedAt, StackID: policy.StackID,
		SiteRef: policy.SiteRef, NodeRef: policy.NodeRef, ExecutionChannelRef: policy.ExecutionChannelRef,
		ParentRulesetRef: policy.ParentRulesetRef, DelegatedChainRef: policy.DelegatedChainRef,
		RouteRefs: routeRefs, BackendPoolRefs: backendRefs, HealthGateRefs: healthRefs,
	}
}

func validCloudPublicEdgePolicy(policy CloudPublicEdgeApplyPolicy) error {
	if !validCoreHostBootstrapDigest(policy.PolicyDigest) || !validCoreHostBootstrapDigest(policy.RequestDigest) ||
		!validCoreHostBootstrapDigest(policy.ArtifactDigest) || !validCoreHostBootstrapDigest(policy.StateDigest) {
		return errors.New("Cloud public-edge policy is not a bound executor request")
	}
	if strings.TrimSpace(policy.StackID) == "" || strings.TrimSpace(policy.SiteRef) == "" ||
		strings.TrimSpace(policy.NodeRef) == "" || strings.TrimSpace(policy.ExecutionChannelRef) == "" ||
		strings.TrimSpace(policy.EvaluatedAt) == "" ||
		strings.TrimSpace(policy.ParentRulesetRef) == "" || strings.TrimSpace(policy.DelegatedChainRef) == "" {
		return errors.New("Cloud public-edge policy is missing its exact node binding")
	}
	return nil
}

// renderCloudPublicEdgeRules turns the compiler-declared routes into the
// closed nftables rule vocabulary: one TCP port accept per public route,
// deduplicated and sorted. Anything the vocabulary cannot express is refused
// rather than approximated.
func renderCloudPublicEdgeRules(routes []architecturev2renderer.CloudPublicEdgeRoute) ([]string, error) {
	ports := make(map[int]struct{}, len(routes))
	for _, route := range routes {
		if route.Port <= 0 || route.Port > 65535 {
			return nil, fmt.Errorf("public-edge route %q declares port %d outside the closed contract", route.ID, route.Port)
		}
		ports[route.Port] = struct{}{}
	}
	sorted := make([]int, 0, len(ports))
	for port := range ports {
		sorted = append(sorted, port)
	}
	sort.Ints(sorted)
	rules := make([]string, 0, len(sorted))
	for _, port := range sorted {
		rules = append(rules, "tcp dport "+strconv.Itoa(port)+" accept")
	}
	return rules, nil
}

// installChain flushes the delegated chain and reinstalls exactly the given
// rules as one nft transaction. The flush fails if the host-security owner
// has not created the parent table and chain, which is the correct order
// dependency rather than something to paper over.
func (o *osCloudPublicEdgeOperations) installChain(ctx context.Context, rules []string) error {
	lines := []string{"flush chain inet " + cloudHostSecurityTable + " " + cloudHostSecurityEdgeChain}
	for _, rule := range rules {
		lines = append(lines, "add rule inet "+cloudHostSecurityTable+" "+cloudHostSecurityEdgeChain+" "+rule)
	}
	path := filepath.Join(o.workspaceRoot, ".stackkit", cloudPublicEdgeCustodyRoot, "chain", "edge-rules.nft")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create Cloud public-edge custody directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Cloud public-edge chain path is a symlink")
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return fmt.Errorf("write Cloud public-edge chain rules: %w", err)
	}
	if _, err := o.runner.Run(ctx, cloudPublicEdgeNFTApplyArgs(path)); err != nil {
		return fmt.Errorf("install the delegated Cloud public-edge chain: %w", err)
	}
	return nil
}

// requireLiveChain lists the delegated chain and refuses anything other than
// exactly the expected rules, so the returned observation only ever describes
// what the host verifiably holds.
func (o *osCloudPublicEdgeOperations) requireLiveChain(ctx context.Context, rules []string) error {
	raw, err := o.runner.Run(ctx, cloudPublicEdgeNFTListChainArgs())
	if err != nil {
		return fmt.Errorf("list the delegated Cloud public-edge chain: %w", err)
	}
	want := "table inet " + cloudHostSecurityTable + " {\n\tchain " + cloudHostSecurityEdgeChain + " {\n"
	for _, rule := range rules {
		want += "\t\t" + rule + "\n"
	}
	want += "\t}\n}\n"
	if normalizeCloudHostRuleset(string(raw)) != want {
		return errors.New("the live delegated Cloud public-edge chain does not hold exactly the declared routes")
	}
	return nil
}

// observeParentDefaultClosed proves the base chain still drops undeclared
// ingress. The edge owner only reads the parent table; mutating it belongs to
// the host-security owner.
func (o *osCloudPublicEdgeOperations) observeParentDefaultClosed(ctx context.Context) (bool, error) {
	raw, err := o.runner.Run(ctx, cloudPublicEdgeNFTListTableArgs())
	if err != nil {
		return false, fmt.Errorf("observe the parent Cloud host ruleset: %w", err)
	}
	listing := normalizeCloudHostRuleset(string(raw))
	if !strings.Contains(listing, "chain "+cloudHostSecurityBaseChain+" {") {
		return false, errors.New("the parent Cloud host ruleset is missing its base chain")
	}
	return strings.Contains(listing, "type filter hook input priority filter; policy drop;"), nil
}

func (o *osCloudPublicEdgeOperations) persistState(state cloudPublicEdgeCustodyState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal Cloud public-edge custody state: %w", err)
	}
	name, err := cloudPublicEdgeStateFilename(state.StateDigest)
	if err != nil {
		return err
	}
	return o.persist("state", name, raw)
}

func (o *osCloudPublicEdgeOperations) loadState(stateDigest string) (cloudPublicEdgeCustodyState, error) {
	name, err := cloudPublicEdgeStateFilename(stateDigest)
	if err != nil {
		return cloudPublicEdgeCustodyState{}, err
	}
	path := filepath.Join(o.workspaceRoot, ".stackkit", cloudPublicEdgeCustodyRoot, "state", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		return cloudPublicEdgeCustodyState{}, errors.New("Cloud public-edge custody has no applied state for the expectation; apply must precede reconcile and verify")
	}
	var state cloudPublicEdgeCustodyState
	if err := json.Unmarshal(raw, &state); err != nil || state.StateDigest != stateDigest {
		return cloudPublicEdgeCustodyState{}, errors.New("Cloud public-edge custody state is corrupt")
	}
	return state, nil
}

func cloudPublicEdgeStateFilename(stateDigest string) (string, error) {
	if !validCoreHostBootstrapDigest(stateDigest) {
		return "", errors.New("Cloud public-edge state digest is not a bound identity")
	}
	return strings.TrimPrefix(stateDigest, "sha256:") + ".json", nil
}

func (o *osCloudPublicEdgeOperations) persist(scope, name string, content []byte) error {
	if filepath.Base(name) != name || filepath.Base(scope) != scope {
		return errors.New("Cloud public-edge custody name is outside the contract")
	}
	directory := filepath.Join(o.workspaceRoot, ".stackkit", cloudPublicEdgeCustodyRoot, scope)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create Cloud public-edge custody directory: %w", err)
	}
	path := filepath.Join(directory, name)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Cloud public-edge custody path is a symlink")
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write Cloud public-edge custody state: %w", err)
	}
	return nil
}

func (o *osCloudPublicEdgeOperations) ready() error {
	if o == nil || o.workspaceRoot == "" || o.runner == nil || o.now == nil {
		return errors.New("local Cloud public edge is not initialized")
	}
	return nil
}

func cloudPublicEdgeNFTApplyArgs(path string) []string {
	return []string{"nft", "-f", path}
}

func cloudPublicEdgeNFTListChainArgs() []string {
	return []string{"nft", "list", "chain", "inet", cloudHostSecurityTable, cloudHostSecurityEdgeChain}
}

func cloudPublicEdgeNFTListTableArgs() []string {
	return []string{"nft", "list", "table", "inet", cloudHostSecurityTable}
}

type osCloudPublicEdgeProcessRunner struct{}

// Run re-validates the argv against the closed public-edge contract before
// exec. The edge owner can only load its own rendered chain file and list the
// owned table; no other process shape exists.
func (osCloudPublicEdgeProcessRunner) Run(ctx context.Context, args []string) ([]byte, error) {
	if err := validCloudPublicEdgeArgs(args); err != nil {
		return nil, err
	}
	executable, err := exec.LookPath(args[0])
	if err != nil {
		return nil, fmt.Errorf("required %s tooling is not installed", args[0])
	}
	command := exec.CommandContext(ctx, executable, args[1:]...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	output := &cloudHostSecurityBoundedBuffer{remaining: cloudHostSecurityProcessOutputLimit}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("bounded %s process failed", args[0])
	}
	if output.exceeded {
		return nil, errors.New("Cloud public-edge process output exceeded the bound")
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func validCloudPublicEdgeArgs(args []string) error {
	switch {
	case len(args) == 3 && args[0] == "nft" && args[1] == "-f" &&
		filepath.Clean(args[2]) == args[2] && filepath.Base(args[2]) == "edge-rules.nft":
		return nil
	case slices.Equal(args, cloudPublicEdgeNFTListChainArgs()),
		slices.Equal(args, cloudPublicEdgeNFTListTableArgs()):
		return nil
	}
	return errors.New("process is outside the closed Cloud public-edge contract")
}

var _ CloudPublicEdgeOperations = (*osCloudPublicEdgeOperations)(nil)
