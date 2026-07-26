package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
)

const (
	maxArchitectureV2ApplyResults     = 256
	ownerApplyResultReceiptAPIVersion = "stackkit.apply-result-receipt/v1"
	ownerApplyResultReceiptKind       = "OwnerSignedApplyResultReceipt"
	architectureV2ApplyEvidenceRoot   = ".stackkit/evidence/apply"
)

type ownerApplyResultReceipt struct {
	APIVersion string                                  `json:"apiVersion"`
	Kind       string                                  `json:"kind"`
	ResultHash string                                  `json:"resultHash"`
	Signature  localevidence.OwnerApplyResultSignature `json:"signature"`
}

func newOwnerApplyResultReceipt(workspaceRoot string, result architecturev2.VerifiedApplyResult) (ownerApplyResultReceipt, []byte, error) {
	canonicalResult, err := result.Canonical()
	if err != nil {
		return ownerApplyResultReceipt{}, nil, err
	}
	return newOwnerApplyResultReceiptForCanonical(workspaceRoot, canonicalResult, result.ResultHash())
}

func newOwnerApplyResultReceiptForCanonical(workspaceRoot string, canonicalResult []byte, resultHash string) (ownerApplyResultReceipt, []byte, error) {
	signature, err := localevidence.SignOwnerApplyResult(workspaceRoot, canonicalResult)
	if err != nil {
		return ownerApplyResultReceipt{}, nil, fmt.Errorf("sign canonical Architecture v2 Apply result: %w", err)
	}
	receipt := ownerApplyResultReceipt{
		APIVersion: ownerApplyResultReceiptAPIVersion, Kind: ownerApplyResultReceiptKind,
		ResultHash: resultHash, Signature: signature,
	}
	canonicalReceipt, err := resolvedplan.CanonicalJSON(receipt)
	if err != nil {
		return ownerApplyResultReceipt{}, nil, err
	}
	return receipt, canonicalReceipt, nil
}

func verifyOwnerApplyResultReceipt(workspaceRoot string, canonicalResult, canonicalReceipt []byte, resultHash string) error {
	var receipt ownerApplyResultReceipt
	decoder := json.NewDecoder(bytes.NewReader(canonicalReceipt))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return fmt.Errorf("decode owner-signed Apply result receipt: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("owner-signed Apply result receipt contains multiple JSON values")
	}
	want, err := resolvedplan.CanonicalJSON(receipt)
	if err != nil || !bytes.Equal(want, canonicalReceipt) ||
		receipt.APIVersion != ownerApplyResultReceiptAPIVersion ||
		receipt.Kind != ownerApplyResultReceiptKind || receipt.ResultHash != resultHash {
		return errors.New("owner-signed Apply result receipt is malformed or not canonical")
	}
	if err := localevidence.VerifyOwnerApplyResult(workspaceRoot, canonicalResult, receipt.Signature); err != nil {
		return fmt.Errorf("verify owner-signed Apply result receipt: %w", err)
	}
	return nil
}

type architectureV2VerifyReport struct {
	SchemaVersion string                              `json:"schemaVersion"`
	Offline       bool                                `json:"offline"`
	PlanHash      string                              `json:"planHash"`
	Apply         architecturev2.ApplyResultSummary   `json:"apply"`
	Owner         architectureV2OwnerVerifySummary    `json:"owner"`
	Runtime       *architectureV2RuntimeVerifySummary `json:"runtime,omitempty"`
	Releases      []releaseindex.Receipt              `json:"releases"`
}

type architectureV2OwnerVerifySummary struct {
	OwnerRef           string `json:"ownerRef"`
	KeyID              string `json:"keyId"`
	PocketIDSubject    string `json:"pocketIdSubject"`
	OwnerBindingDigest string `json:"ownerBindingDigest"`
}

type architectureV2RuntimeVerifySummary struct {
	ProjectRef   string `json:"projectRef"`
	Status       string `json:"status"`
	ServiceCount int    `json:"serviceCount"`
	ProbeCount   int    `json:"probeCount"`
}

func verifyArchitectureV2LocalState(
	ctx context.Context,
	workspaceRoot string,
	plan generationartifact.VerifiedPlan,
	manifest generationartifact.ArtifactManifest,
	offline bool,
) (architectureV2OwnerVerifySummary, *architectureV2RuntimeVerifySummary, error) {
	owner, err := localevidence.LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return architectureV2OwnerVerifySummary{}, nil, fmt.Errorf("verify local owner custody: %w", err)
	}
	if _, err := localevidence.LoadBasementRuntimeCustody(workspaceRoot); err != nil {
		return architectureV2OwnerVerifySummary{}, nil, fmt.Errorf("verify local Basement runtime custody: %w", err)
	}
	binding, err := localevidence.LoadOwnerRuntimeBinding(workspaceRoot)
	if err != nil {
		return architectureV2OwnerVerifySummary{}, nil, fmt.Errorf("verify PocketID/step-ca owner binding: %w", err)
	}
	ownerSummary := architectureV2OwnerVerifySummary{
		OwnerRef: binding.OwnerRef, KeyID: owner.KeyID,
		PocketIDSubject:    binding.PocketIDSubject,
		OwnerBindingDigest: localevidence.OwnerRuntimeBindingDigest(binding),
	}
	if ownerSummary.OwnerRef != owner.OwnerRef {
		return architectureV2OwnerVerifySummary{}, nil, errors.New("PocketID owner binding differs from local owner custody")
	}
	if offline {
		return ownerSummary, nil, nil
	}
	observation, err := verifyBasementCoreWorkspace(ctx, workspaceRoot, plan, manifest, owner.Binding)
	if err != nil {
		return architectureV2OwnerVerifySummary{}, nil, fmt.Errorf("verify live Basement core: %w", err)
	}
	if observation.OwnerRef != ownerSummary.OwnerRef ||
		observation.PocketIDSubject != ownerSummary.PocketIDSubject ||
		observation.OwnerBindingDigest != ownerSummary.OwnerBindingDigest {
		return architectureV2OwnerVerifySummary{}, nil, errors.New("live Basement owner observation differs from signed local binding")
	}
	return ownerSummary, &architectureV2RuntimeVerifySummary{
		ProjectRef: observation.ProjectRef, Status: observation.Status,
		ServiceCount: len(observation.Services), ProbeCount: len(observation.Probes),
	}, nil
}

func verifyBasementCoreWorkspace(
	ctx context.Context,
	workspaceRoot string,
	plan generationartifact.VerifiedPlan,
	manifest generationartifact.ArtifactManifest,
	localBinding localevidence.LocalBinding,
) (runtimeexecutorlocal.BasementCoreVerifyObservation, error) {
	if ctx == nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("Basement workspace verification requires a context")
	}
	requirements := plan.ApplyRequirements()
	var targets []generationartifact.ApplyRuntimeRequirement
	for _, candidate := range requirements.RuntimeInstances {
		if candidate.OwnerKind == "module" &&
			candidate.OwnerRef == "stackkits-basement-core-runtime" &&
			candidate.ProviderRef == "stackkits-basement-core" &&
			candidate.ModuleRef == "stackkits-basement-core-runtime" &&
			candidate.UnitRef == "compose" && candidate.WorkloadRef == "basement-core" {
			targets = append(targets, candidate)
		}
	}
	if len(targets) != 1 {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("verified plan requires exactly one Basement core runtime")
	}
	target := targets[0]
	if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || len(target.ArtifactRefs) != 1 ||
		target.RuntimeKind != "container" || target.RuntimeDelivery != "stackkit" ||
		target.RuntimeEngine != "docker" {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("verified Basement runtime is not the closed single-node Compose contract")
	}
	channelRef, err := verifiedLocalBasementExecutionChannel(requirements, target, localBinding)
	if err != nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, err
	}
	artifactID := target.ArtifactRefs[0]
	var (
		artifactRequirement generationartifact.ApplyArtifactRequirement
		rendered            generationartifact.RenderedArtifact
		requirementCount    int
		renderedCount       int
	)
	for _, candidate := range requirements.Artifacts {
		if candidate.ID == artifactID {
			artifactRequirement = candidate
			requirementCount++
		}
	}
	for _, candidate := range manifest.Artifacts {
		if candidate.ID == artifactID {
			rendered = candidate
			renderedCount++
		}
	}
	if requirementCount != 1 || renderedCount != 1 ||
		artifactRequirement.Kind != "compose" || artifactRequirement.Format != "yaml" ||
		artifactRequirement.ExecutionClass != generationartifact.ApplyExecutionClassExecutable ||
		rendered.Kind != artifactRequirement.Kind || rendered.Format != artifactRequirement.Format ||
		rendered.Mode != artifactRequirement.Mode {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("verified generation lacks the exact executable Basement Compose artifact")
	}
	artifactPath := filepath.Join(workspaceRoot, filepath.FromSlash(rendered.Path))
	info, err := os.Lstat(artifactPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 256<<10 {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("Basement Compose artifact is not a bounded plain file")
	}
	definition, err := os.ReadFile(artifactPath) //nolint:gosec // exact manifest path was already verified by the generation gate
	if err != nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, fmt.Errorf("read Basement Compose artifact: %w", err)
	}
	digest := sha256.Sum256(definition)
	if "sha256:"+hex.EncodeToString(digest[:]) != rendered.SHA256 ||
		!bytes.Equal(definition, architecturev2renderer.ExpectedBasementCoreComposeArtifact()) {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("Basement Compose artifact differs from the CUE-owned standard")
	}
	serviceContracts := architecturev2renderer.BasementCoreServiceContracts()
	services := make([]runtimeexecutorlocal.BasementCoreServiceExpectation, len(serviceContracts))
	for index, contract := range serviceContracts {
		services[index] = runtimeexecutorlocal.BasementCoreServiceExpectation(contract)
	}
	health, err := verifiedBasementCoreHealth(requirements, target)
	if err != nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, err
	}
	project := runtimeexecutorlocal.BasementCoreProject{
		ProjectRef: target.InstanceRef, SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0],
		ExecutionChannelRef: channelRef, ArtifactID: rendered.ID, ArtifactDigest: rendered.SHA256,
		Definition: definition, Services: services, Health: health,
	}
	operations, err := runtimeexecutorlocal.NewOSBasementCoreOperations(workspaceRoot)
	if err != nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, err
	}
	observation, err := operations.VerifyProject(ctx, project)
	if err != nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, err
	}
	if observation.ProjectRef != project.ProjectRef || observation.ArtifactDigest != project.ArtifactDigest ||
		observation.Status != "ready" || len(observation.Services) != len(project.Services) ||
		len(observation.Probes) != len(project.Health) {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("live Basement observation does not prove the exact ready project")
	}
	return observation, nil
}

func verifiedLocalBasementExecutionChannel(
	requirements generationartifact.ApplyRequirements,
	target generationartifact.ApplyRuntimeRequirement,
	localBinding localevidence.LocalBinding,
) (string, error) {
	if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		localBinding.SiteRef != target.SiteRefs[0] || localBinding.NodeRef != target.NodeRefs[0] ||
		strings.TrimSpace(localBinding.ChannelRef) == "" {
		return "", errors.New("local owner custody does not bind the exact Basement Site/node/channel")
	}
	matches := 0
	for _, host := range requirements.Hosts {
		if host.SiteRef != target.SiteRefs[0] || host.NodeRef != target.NodeRefs[0] {
			continue
		}
		matches++
		if host.External {
			return "", errors.New("verified Basement host is external and cannot use local owner custody")
		}
		if host.ExecutionChannelRef != "" && host.ExecutionChannelRef != localBinding.ChannelRef {
			return "", errors.New("verified Basement host conflicts with the local owner execution channel")
		}
	}
	if matches != 1 {
		return "", errors.New("verified plan has no unique local Basement host")
	}
	return localBinding.ChannelRef, nil
}

func verifiedBasementCoreHealth(
	requirements generationartifact.ApplyRequirements,
	target generationartifact.ApplyRuntimeRequirement,
) ([]runtimeexecutorlocal.BasementCoreHealthExpectation, error) {
	type expectedHealth struct {
		sourceRef, kind, targetKind, targetRef, path string
		port                                         int
		statuses                                     []int
	}
	expected := []expectedHealth{
		{sourceRef: "basement-core-provider-contract", kind: "contract", targetKind: "provider", targetRef: target.ProviderRef},
		{sourceRef: "basement-hub-http", kind: "http", targetKind: "module", targetRef: target.ModuleRef, path: "/healthz", port: 80, statuses: []int{200}},
		{sourceRef: "basement-router-http", kind: "http", targetKind: "module", targetRef: target.ModuleRef, path: "/ping", port: 8080, statuses: []int{200}},
		{sourceRef: "coolify-http", kind: "http", targetKind: "module", targetRef: target.ModuleRef, path: "/", port: 8000, statuses: []int{200, 302}},
		{sourceRef: "pocketid-http", kind: "http", targetKind: "module", targetRef: target.ModuleRef, path: "/", port: 1411, statuses: []int{200, 302}},
		{sourceRef: "step-ca-tcp", kind: "tcp", targetKind: "module", targetRef: target.ModuleRef, port: 9000},
		{sourceRef: "tinyauth-http", kind: "http", targetKind: "module", targetRef: target.ModuleRef, path: "/", port: 4000, statuses: []int{200, 302}},
	}
	bySource := make(map[string]generationartifact.ApplyHealthRequirement, len(expected))
	for _, item := range requirements.HealthRequirements {
		for _, want := range expected {
			if item.SourceRef != want.sourceRef || item.Kind != want.kind ||
				item.TargetKind != want.targetKind || item.TargetRef != want.targetRef {
				continue
			}
			if _, duplicate := bySource[item.SourceRef]; duplicate {
				return nil, errors.New("verified Basement health requirements contain a duplicate source")
			}
			bySource[item.SourceRef] = item
		}
	}
	health := make([]runtimeexecutorlocal.BasementCoreHealthExpectation, 0, len(expected))
	for _, want := range expected {
		item, ok := bySource[want.sourceRef]
		if !ok {
			return nil, errors.New("verified Basement runtime lacks one of its exact seven postconditions")
		}
		expectation := runtimeexecutorlocal.BasementCoreHealthExpectation{
			RequirementID: item.ID, SourceRef: item.SourceRef, Kind: item.Kind,
			Port: want.port, Path: want.path,
			ExpectedStatuses: append([]int(nil), want.statuses...),
		}
		health = append(health, expectation)
	}
	return health, nil
}

func printArchitectureV2VerifyReport(w io.Writer, report architectureV2VerifyReport) error {
	if _, err := fmt.Fprintf(w, "Architecture v2 verify: success\nPlan: %s\nApply result: %s\nApply evidence: %s\nOwner: %s (%s)\n",
		report.PlanHash, report.Apply.ResultHash, report.Apply.EvidenceBundleHash,
		report.Owner.OwnerRef, report.Owner.PocketIDSubject); err != nil {
		return err
	}
	for _, receipt := range report.Releases {
		if _, err := fmt.Fprintf(w, "Release: %s %s (%s, %s/%s)\n",
			receipt.Kit, receipt.Version, receipt.Channel, receipt.Platform.OS, receipt.Platform.Arch); err != nil {
			return err
		}
	}
	if report.Offline {
		_, err := fmt.Fprintln(w, "Runtime probes: skipped (offline)")
		return err
	}
	if report.Runtime != nil {
		_, err := fmt.Fprintf(w, "Runtime: %s (%d services, %d probes)\n",
			report.Runtime.Status, report.Runtime.ServiceCount, report.Runtime.ProbeCount)
		return err
	}
	return nil
}

func readCurrentArchitectureV2ApplyResult(
	workspaceRoot string,
	binding generationartifact.PlanBinding,
	verify func([]byte) (architecturev2.VerifiedApplyResult, error),
) (result architecturev2.VerifiedApplyResult, returnErr error) {
	if verify == nil {
		return architecturev2.VerifiedApplyResult{}, errors.New("Architecture v2 Apply result verifier is required")
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return architecturev2.VerifiedApplyResult{}, fmt.Errorf("open workspace for Apply result verification: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return architecturev2.VerifiedApplyResult{}, fmt.Errorf("begin Apply result verification transaction: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()

	directory := path.Join(architectureV2ApplyEvidenceRoot, "results")
	entries, err := transaction.Walk(directory)
	if err != nil {
		return architecturev2.VerifiedApplyResult{}, fmt.Errorf("read Architecture v2 Apply results: %w", err)
	}
	if len(entries) < 2 || len(entries)-1 > maxArchitectureV2ApplyResults {
		return architecturev2.VerifiedApplyResult{}, fmt.Errorf("Architecture v2 verification requires 1-%d persisted Apply results", maxArchitectureV2ApplyResults)
	}
	var (
		selected   architecturev2.VerifiedApplyResult
		selectedAt time.Time
		found      int
	)
	for _, entry := range entries[1:] {
		if !entry.Info.Mode().IsRegular() || path.Dir(entry.Path) != directory ||
			!strings.HasSuffix(path.Base(entry.Path), ".json") {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("Architecture v2 Apply result directory contains an unsupported entry %q", entry.Path)
		}
		raw, _, err := transaction.ReadStable(entry.Path)
		if err != nil {
			return architecturev2.VerifiedApplyResult{}, err
		}
		var probe struct {
			Binding generationartifact.PlanBinding `json:"binding"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("decode Apply result binding %q: %w", entry.Path, err)
		}
		if probe.Binding != binding {
			continue
		}
		verified, err := verify(raw)
		if err != nil {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("verify current Apply result %q: %w", entry.Path, err)
		}
		wantName := strings.TrimPrefix(verified.ResultHash(), "sha256:") + ".json"
		if path.Base(entry.Path) != wantName {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("Apply result %q is not stored at its content address", entry.Path)
		}
		receiptPath := path.Join(architectureV2ApplyEvidenceRoot, "receipts", wantName)
		canonicalReceipt, receiptInfo, err := transaction.ReadStable(receiptPath)
		if err != nil {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("read owner-signed Apply result receipt %q: %w", receiptPath, err)
		}
		if !receiptInfo.Mode().IsRegular() {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("owner-signed Apply result receipt %q is not a regular file", receiptPath)
		}
		if err := verifyOwnerApplyResultReceipt(workspaceRoot, raw, canonicalReceipt, verified.ResultHash()); err != nil {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("verify Apply result receipt %q: %w", receiptPath, err)
		}
		summary := verified.Summary()
		if summary.AppliedAt.IsZero() {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("Apply result %q has no verified Apply time", entry.Path)
		}
		found++
		if selectedAt.IsZero() || summary.AppliedAt.After(selectedAt) {
			selected, selectedAt = verified, summary.AppliedAt
		} else if summary.AppliedAt.Equal(selectedAt) && verified.ResultHash() != selected.ResultHash() {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("multiple current Apply results share the latest Apply time")
		}
	}
	if found == 0 {
		return architecturev2.VerifiedApplyResult{}, errors.New("no persisted Apply result matches the current ResolvedPlan; run `stackkit apply` first")
	}
	return selected, nil
}
