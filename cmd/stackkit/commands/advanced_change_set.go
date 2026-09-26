package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/advancedchangeset"
	"github.com/kombifyio/stackkits/internal/advancedtrust"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

func admitAdvancedChangeSetCreate(
	_ context.Context,
	workspace, capabilityPath, candidatePath string,
	now time.Time,
) (advancedChangeSetAdmission, error) {
	return admitAdvancedChangeSetOperation(
		workspace, capabilityPath, candidatePath, now,
		advancedcapability.OperationTerramateChangeSetCreate,
	)
}

func admitAdvancedChangeSetOperation(
	workspace, capabilityPath, candidatePath string,
	now time.Time,
	operation string,
) (advancedChangeSetAdmission, error) {
	candidateRaw, err := readAdvancedRegular(candidatePath, maxAdvancedCandidateBytes, "candidate StackSpec")
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	capabilityRaw, err := readAdvancedRegular(capabilityPath, maxAdvancedTrustBundleBytes, "Advanced capability")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return advancedChangeSetAdmission{}, &advancedcapability.Denial{
				Code: advancedcapability.ReasonCapabilityRequired, Field: "capability", Detail: "file is required",
			}
		}
		return advancedChangeSetAdmission{}, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return advancedChangeSetAdmission{}, &advancedcapability.Denial{
			Code: advancedcapability.ReasonCapabilityScopeMismatch, Field: "ownerRef", Detail: "verified local Owner custody is required",
		}
	}
	trust, err := advancedtrust.Load(workspace)
	if err != nil {
		return advancedChangeSetAdmission{}, &advancedcapability.Denial{
			Code: advancedcapability.ReasonTrustBundleUnavailable, Field: "trustBundle", Detail: "verified Owner-approved local trust is required",
		}
	}
	baselineRaw, sourceVersion, handled, err := classifyArchitectureV2ExecutionSpec(workspace, specFile)
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	if !handled || !sourceVersion.IsV2() {
		return advancedChangeSetAdmission{}, errors.New("advanced lifecycle operation requires a canonical Architecture v2 baseline StackSpec")
	}
	inventory, err := readArchitectureV2Inventory(workspace, "")
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	// Baseline and candidate resolve one after the other through one embedded
	// authority; both carry the same Stack ID, so each resolution is sealed in
	// its own authority scope and neither supersedes the other.
	advancedChangeSetPrepareEvent("resolve-baseline")
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	// Baseline and candidate resolve from the Inventory the baseline was
	// generated from, so the persisted plan and the change set compare like
	// with like.
	inventory, err = inventoryForPersistedGeneration(service, workspace, baselineRaw, inventory)
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	baselineCurrent, err := service.ResolveCurrentScoped(
		architecturev2.ResolveInput{StackSpec: baselineRaw, Inventory: inventory}, advancedBaselineAuthorityScope,
	)
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	baseline, err := baselineCurrent.Result()
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	advancedChangeSetPrepareEvent("resolve-candidate")
	candidateCurrent, err := service.ResolveCurrentScoped(
		architecturev2.ResolveInput{StackSpec: candidateRaw, Inventory: inventory}, advancedCandidateAuthorityScope,
	)
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	candidate, err := candidateCurrent.Result()
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	stackID, outputRoot, target, err := advancedPlanIdentity(candidate)
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	grant, err := advancedcapability.Verify(capabilityRaw, advancedcapability.Request{
		Now: now, TrustBundle: ptrTrustBundle(trust.TrustBundle()),
		StackID: stackID, OwnerRef: owner.OwnerRef,
		Operation: operation,
	})
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	baselineStackID, baselineOutputRoot, _, err := advancedPlanIdentity(baseline)
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	if target != advancedchangeset.GenerationTargetTerramate {
		return advancedChangeSetAdmission{}, &advancedcapability.Denial{
			Code: advancedcapability.ReasonAdvancedChangeSetInvalid, Field: "generation.target", Detail: "must be terramate",
		}
	}
	if stackID != baselineStackID || outputRoot != baselineOutputRoot {
		return advancedChangeSetAdmission{}, &advancedcapability.Denial{
			Code: advancedcapability.ReasonAdvancedChangeSetInvalid, Field: "candidate", Detail: "stackId and outputRoot must match the baseline",
		}
	}
	baselinePlan, err := service.VerifyCanonicalPlan(baseline.CanonicalPlan)
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	planPath, manifestPath, receiptPath := baselinePlan.MetadataPaths(workspace)
	persisted, err := service.ReadCanonicalPlan(planPath)
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	manifest, err := generationartifact.ReadManifest(manifestPath)
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	receipt, err := generationartifact.ReadReceipt(receiptPath)
	if err != nil {
		return advancedChangeSetAdmission{}, err
	}
	if err := generationartifact.VerifyExecution(generationartifact.ExecutionGateInput{
		CurrentCanonical: baseline.CanonicalPlan, Plan: persisted,
		Phase:    generationartifact.ExecutionPhaseGeneration,
		Versions: advancedComponentVersions(), Root: workspace,
		Manifest: manifest, Receipt: receipt,
	}); err != nil {
		return advancedChangeSetAdmission{}, err
	}
	return advancedChangeSetAdmission{
		workspace: workspace, service: service,
		baselineCurrent: baselineCurrent, candidateCurrent: candidateCurrent,
		baselinePlanHash: baseline.PlanHash, candidate: candidate, grant: grant,
		capabilityRaw: bytes.Clone(capabilityRaw), candidateRaw: bytes.Clone(candidateRaw),
		owner: owner, trustSHA256: trust.BundleSHA256,
	}, nil
}

// Authority scopes of the two resolutions of one Advanced admission. They
// isolate freshness state only and never enter a plan, wire or output.
const (
	advancedBaselineAuthorityScope  = "stackkit.advanced.baseline"
	advancedCandidateAuthorityScope = "stackkit.advanced.candidate"
)

// advancedChangeSetPreparePhase prefixes the rollout events emitted before
// each heavy phase of an Advanced admission, so a stuck or killed run shows
// the phase it was in.
const advancedChangeSetPreparePhase = advancedChangeSetRolloutPrefix + "prepare."

func advancedChangeSetPrepareEvent(step string) {
	rolloutEvent(advancedChangeSetPreparePhase+step, "started", "advanced change-set "+step+" started", nil)
}

func createAdvancedChangeSet(ctx context.Context, admitted advancedChangeSetAdmission, now time.Time) (advancedChangeSetResult, error) {
	baselineRender, candidateRender, err := renderAdvancedAdmission(ctx, admitted)
	if err != nil {
		return advancedChangeSetResult{}, err
	}
	advancedChangeSetPrepareEvent("diff")
	capabilityDigest := sha256.Sum256(admitted.capabilityRaw)
	expiresAt := now.Add(advancedchangeset.MaxLifetime)
	if admitted.grant.ExpiresAt.Before(expiresAt) {
		expiresAt = admitted.grant.ExpiresAt
	}
	sign := func(unsigned []byte) (advancedchangeset.OwnerSignature, error) {
		signature, signErr := localevidence.SignOwnerAdvancedChangeSet(admitted.workspace, unsigned)
		return advancedchangeset.OwnerSignature(signature), signErr
	}
	verify := func(unsigned []byte, signature advancedchangeset.OwnerSignature) error {
		return localevidence.VerifyOwnerAdvancedChangeSet(
			admitted.workspace, unsigned, localevidence.OwnerAdvancedChangeSetSignature(signature),
		)
	}
	record, err := advancedchangeset.Create(advancedchangeset.CreateRequest{
		Baseline: baselineRender, Candidate: candidateRender,
		CapabilityID:     admitted.grant.CapabilityID,
		CapabilitySHA256: "sha256:" + hex.EncodeToString(capabilityDigest[:]),
		KeyID:            admitted.grant.KeyID, StackID: admitted.grant.StackID, OwnerRef: admitted.grant.OwnerRef,
		UIManagerRef: admitted.grant.UIManagerRef, RILRef: admitted.grant.RILRef,
		BaselinePlanHash: admitted.baselinePlanHash, CandidatePlanHash: admitted.candidate.PlanHash,
		LocalSiteRef: admitted.owner.Binding.SiteRef, LocalNodeRef: admitted.owner.Binding.NodeRef,
		CreatedAt: now, ExpiresAt: expiresAt, CapabilityExpiresAt: admitted.grant.ExpiresAt,
		Sign: sign, VerifyOwnerSignature: verify,
		// Runtime drift leaves the candidate equal to the baseline; the
		// reconcile of such drift runs through an empty change set, which only
		// a capability that also allows drift.reconcile.advanced may create.
		AllowEmpty: slices.Contains(admitted.grant.AllowedOperations, advancedcapability.OperationDriftReconcileAdvanced),
	})
	if err != nil {
		return advancedChangeSetResult{}, err
	}
	path, err := (advancedchangeset.Store{WorkspaceRoot: admitted.workspace}).Publish(record, advancedchangeset.VerificationRequest{
		Now: now, CapabilityID: admitted.grant.CapabilityID,
		CapabilitySHA256: "sha256:" + hex.EncodeToString(capabilityDigest[:]),
		KeyID:            admitted.grant.KeyID, StackID: admitted.grant.StackID, OwnerRef: admitted.grant.OwnerRef,
		UIManagerRef: admitted.grant.UIManagerRef, RILRef: admitted.grant.RILRef,
		BaselinePlanHash: admitted.baselinePlanHash, CandidatePlanHash: admitted.candidate.PlanHash,
		CapabilityExpiresAt: admitted.grant.ExpiresAt, VerifyOwnerSignature: verify,
	})
	if err != nil {
		return advancedChangeSetResult{}, err
	}
	return advancedChangeSetResult{
		SchemaVersion: advancedchangeset.SchemaVersion, ChangeSetID: record.ChangeSetID,
		Path: path, PlanHash: admitted.candidate.PlanHash, Changes: record.Changes,
		AffectedStacks: record.AffectedStacks, TerramateHostManifestSHA256: record.TerramateHostManifestSHA256,
	}, nil
}

func readAdvancedRegular(name string, limit int64, label string) ([]byte, error) {
	file, err := os.Open(filepath.Clean(name))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", label)
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", label, limit)
	}
	return raw, nil
}

func advancedPlanIdentity(result architecturev2.Result) (stackID, outputRoot, target string, err error) {
	stackID, _ = result.Plan["stackId"].(string)
	generation, _ := result.Plan["generation"].(map[string]any)
	outputRoot, _ = generation["outputRoot"].(string)
	target, _ = generation["target"].(string)
	if strings.TrimSpace(stackID) == "" || strings.TrimSpace(outputRoot) == "" || strings.TrimSpace(target) == "" {
		err = errors.New("resolved Advanced plan lacks stackId, generation.outputRoot, or generation.target")
	}
	return
}

func advancedComponentVersions() generationartifact.ComponentVersions {
	component := architectureV2ComponentVersion(version)
	return generationartifact.ComponentVersions{CLI: component, Generator: component, Runtime: component}
}

func ptrTrustBundle(bundle advancedcapability.TrustBundle) *advancedcapability.TrustBundle {
	return &bundle
}

func writeAdvancedChangeSetDenial(cmd *cobra.Command, err error) error {
	reason, ok := advancedcapability.Reason(err)
	if !ok {
		if changesetReason, typed := advancedchangeset.Reason(err); typed {
			reason = advancedcapability.ReasonCode(changesetReason)
			ok = true
		}
	}
	if !ok {
		return err
	}
	denial := driftOperationDenial{
		SchemaVersion: operationDenialSchemaVersion,
		Operation:     "terramate.change-set.create", Mode: "advanced",
		ReasonCode: string(reason), Message: err.Error(),
	}
	if advancedChangeSetJSON {
		if writeErr := writeCommandResultStatus(cmd, cmd.CommandPath(), "denied", denial); writeErr != nil {
			return writeErr
		}
	} else {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Denied: %s\n", denial.Message)
	}
	return err
}
