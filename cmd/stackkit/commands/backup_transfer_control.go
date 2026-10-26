package commands

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/advancedtrust"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localbackupruntime"
	"github.com/kombifyio/stackkits/internal/localevidence"

	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

type applicationTransferDestinationAdmission struct {
	APIVersion  string                                         `json:"apiVersion"`
	Destination backuplifecycle.ApplicationTransferDestination `json:"destination"`
	Signature   localevidence.ApplicationTransferSignature     `json:"signature"`
}

func readApplicationTransferControlSource(cmd *cobra.Command) (backuplifecycle.ApplicationTransferExportReceipt, backuplifecycle.ApplicationTransfer, error) {
	values := map[string]string{}
	for _, name := range []string{"source-receipt", "source-owner", "source-fingerprint", "transfer-id", "archive-sha256"} {
		values[name], _ = cmd.Flags().GetString(name)
		if values[name] == "" {
			return backuplifecycle.ApplicationTransferExportReceipt{}, backuplifecycle.ApplicationTransfer{}, errors.New("transfer abort requires all exact source admission flags")
		}
	}
	raw, err := os.ReadFile(values["source-receipt"])
	if err != nil {
		return backuplifecycle.ApplicationTransferExportReceipt{}, backuplifecycle.ApplicationTransfer{}, err
	}
	var receipt backuplifecycle.ApplicationTransferExportReceipt
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return receipt, backuplifecycle.ApplicationTransfer{}, err
	}
	t, err := backuplifecycle.VerifyApplicationTransferExportReceipt(receipt, values["source-owner"], values["source-fingerprint"], values["transfer-id"], values["archive-sha256"])
	return receipt, t, err
}

func runApplicationTransferAbort(cmd *cobra.Command, _ []string) error {
	approved, _ := cmd.Flags().GetBool("owner-approve")
	if !approved {
		return errors.New("receiving transfer abort requires --owner-approve")
	}
	_, t, err := readApplicationTransferControlSource(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), backupLongOperationTimeout)
	defer cancel()
	if err = verifyApplicationTransferIssuedAdmission(getWorkDir(), t, false); err != nil {
		return err
	}
	operation := "transfer-abort." + strings.TrimPrefix(t.ID, "sha256:")
	join, err := currentLifecycleMutationJoin("backup transfer-abort")
	if err != nil {
		return err
	}
	var outcome backuplifecycle.ApplicationTransferOutcome
	err = lifecyclemutation.WithSnapshotMutationContext(ctx, getWorkDir(), join, operation, func(lease context.Context) error {
		if _, exists, e := readStoredApplicationTransferOutcome(getWorkDir(), t, "activated"); e != nil {
			return e
		} else if exists {
			return errors.New("activated destination cannot be aborted: preserve accepted target writes; source remains retired/held")
		}
		importID, e := restoreactivation.TransferOperationID(t.ID)
		if e != nil {
			return e
		}
		record, e := lifecyclemutation.ReadRestoreActivationRecord(getWorkDir(), importID)
		if e == nil && (record.Status == lifecyclemutation.StatusSucceeded || record.Phase == lifecyclemutation.PhaseCommitSucceeded) {
			return errors.New("committed destination activation cannot be aborted")
		}
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if prior, exists, e := readStoredApplicationTransferOutcome(getWorkDir(), t, "stopped"); e != nil {
			return e
		} else if exists {
			outcome = prior
			return nil
		}
		// Resume the exact CAS terminalization after a crash without requiring a
		// stale pre-retirement generation to match the already retired spec.
		if planned, e := backuplifecycle.ReadApplicationTransferRetirementCandidate(getWorkDir(), operation, t.ID); e == nil && planned != "" {
			specPath, _, _, e := config.NewLoader(getWorkDir()).ResolveStackSpecPathForRead(specFile)
			if e != nil {
				return e
			}
			raw, e := os.ReadFile(specPath)
			if e != nil {
				return e
			}
			author, e := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
			if e != nil {
				return e
			}
			current, e := author.ValidateStackSpec(raw)
			if e != nil {
				return e
			}
			if current.SpecHash == planned {
				journal, e := backuplifecycle.NewCreator().OpenTransferJournal(getWorkDir())
				if e != nil {
					return e
				}
				if e = journal.CompleteApplicationTransferAbortRetirement(operation, t.ID, planned); e != nil {
					return e
				}
				outcome, e = pendingApplicationTransferStoppedOutcome(getWorkDir(), t)
				if e != nil {
					return e
				}
				return persistApplicationTransferOutcome(getWorkDir(), outcome)
			}
		}
		generated, e := inspectNativeV2GeneratedAuthority(lease, getWorkDir(), specFile)
		if e != nil {
			return e
		}
		expected, _ := cmd.Flags().GetString("expected-plan-hash")
		if expected != t.Destination.PlanHash {
			return errors.New("abort requires exact receiving Plan")
		}
		if e = generated.Plan.RequireExpectedPlanHash(expected); e != nil {
			return e
		}
		d := t.Destination
		var plan struct {
			StackID string `json:"stackId"`
		}
		if e = json.Unmarshal(generated.Plan.Canonical(), &plan); e != nil {
			return e
		}
		if d.OwnerRef != generated.OwnerRef || d.StackID != plan.StackID || d.SiteRef != generated.Owner.Binding.SiteRef || d.NodeRef != generated.Owner.Binding.NodeRef {
			return errors.New("abort differs from receiving owner/node")
		}
		root, e := confinedfs.Open(getWorkDir())
		if e != nil {
			return e
		}
		defer root.Close()
		tx, e := root.BeginTransaction()
		if e != nil {
			return e
		}
		defer tx.Close()
		_, requirement, e := nativeV2BackupPolicyRequirement(generated.Plan, generated.Owner.Binding.SiteRef, generated.Owner.Binding.NodeRef)
		if e != nil {
			return e
		}
		_, _, policy, e := readNativeV2BackupPolicyForArtifact(tx, generated.Manifest, requirement.ID)
		if e != nil {
			return e
		}
		workloads := make([]string, len(t.Runtimes))
		for i, r := range t.Runtimes {
			workloads[i] = r.WorkloadRef
		}
		closure, e := restoreactivation.ApplicationTransferProjection(generated.WorkspaceRoot, generated.Plan, generated.Manifest, workloads, policy)
		if e != nil {
			return e
		}
		if e = restoreactivation.RequireApplicationTransferConfiguration(t, generated.Plan, workloads); e != nil {
			return e
		}
		if _, e = backuplifecycle.MatchApplicationTransfer(t, policy, closure); e != nil {
			return e
		}
		if absent := restoreactivation.RequireApplicationTransferTargetAbsent(lease, getWorkDir(), policy, workloads, closure); absent != nil {
			if e = verifyApplicationTransferPreparation(getWorkDir(), t, func() string { value, _ := cmd.Flags().GetString("source-fingerprint"); return value }()); e != nil { // compare exact source fingerprint from original pinned provenance below
				return errors.New("applied target abort requires original successful transfer preparation")
			}
			current, e := inspectNativeV2BackupAuthorityForRequest(lease, getWorkDir(), specFile)
			if e != nil {
				return e
			}
			backup, e := newNativeV2ApplicationBackupService(current, workloads, nil, func() string { return t.ID })
			if e != nil {
				return e
			}
			persistent, e := restoreactivation.ApplicationTransferPersistentWorkloads(policy, workloads)
			if e != nil {
				return e
			}
			_, e = backup.Run(lease, backuplifecycle.RunInput{OwnerRef: current.OwnerRef, AuthorityRef: current.AuthorityRef, Lineage: current.Lineage, PolicyArtifact: current.PolicyArtifact, OperationID: operation, ProtectRecovery: true, Workloads: persistent})
			if !errors.Is(e, backuplifecycle.ErrApplicationTransferHeld) {
				if e == nil {
					return errors.New("target abort did not persist exact stopped hold")
				}
				return e
			}
			stopped, e := pendingApplicationTransferStoppedOutcome(getWorkDir(), t)
			outcome = stopped
			if e != nil {
				return e
			}
			encoded, e := json.Marshal(stopped)
			if e != nil {
				return e
			}
			sum := sha256.Sum256(encoded)
			journal, e := backuplifecycle.NewCreator().OpenTransferJournal(getWorkDir())
			if e != nil {
				return e
			}
			if e = journal.RecordApplicationTransferOutcome(operation, t.ID, "sha256:"+hex.EncodeToString(sum[:]), "stopped"); e != nil {
				return e
			}
			if e = retireAbortedApplicationTransferTarget(lease, cmd, operation, t, workloads, generated.Plan.Binding().SpecHash, journal); e != nil {
				return e
			}
		}
		if outcome.TransferID == "" {
			outcome, e = pendingApplicationTransferStoppedOutcome(getWorkDir(), t)
			if e != nil {
				return e
			}
		}
		return persistApplicationTransferOutcome(getWorkDir(), outcome)
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(outcome)
}

func retireAbortedApplicationTransferTarget(ctx context.Context, cmd *cobra.Command, operation string, t backuplifecycle.ApplicationTransfer, workloads []string, expectedSpec string, journal *backuplifecycle.Service) error {
	authority, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return err
	}
	specPath, _, _, err := config.NewLoader(getWorkDir()).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	current, err := authority.ValidateStackSpec(raw)
	if err != nil {
		return err
	}
	if current.SpecHash != expectedSpec {
		return errors.New("target desired intent changed before abort retirement")
	}
	var intent struct {
		Install struct {
			Mode string `json:"mode"`
		}
	}
	if err = json.Unmarshal(current.CanonicalStackSpec, &intent); err != nil {
		return err
	}
	inventoryPath, _ := cmd.Flags().GetString("inventory")
	inventory, err := readArchitectureV2Inventory(getWorkDir(), inventoryPath)
	if err != nil {
		return err
	}
	resolved, err := authority.Resolve(architecturev2.ResolveInput{StackSpec: current.CanonicalStackSpec, Inventory: inventory})
	if err != nil {
		return err
	}
	plan, err := authority.VerifyCanonicalPlan(resolved.CanonicalPlan)
	if err != nil {
		return err
	}
	ordered, err := authority.OrderWorkloadRemovals(plan, workloads)
	if err != nil {
		return err
	}
	candidate, err := authority.MaterializeWorkloadRemovals(current.CanonicalStackSpec, ordered)
	if err != nil {
		return err
	}
	if _, err = authority.Resolve(architecturev2.ResolveInput{StackSpec: candidate.CanonicalStackSpec, Inventory: inventory}); err != nil {
		return err
	}
	if err = journal.PrepareApplicationTransferRetirement(operation, t.ID, candidate.SpecHash, intent.Install.Mode == "advanced"); err != nil {
		return err
	}
	if intent.Install.Mode == "advanced" {
		stopped, e := pendingApplicationTransferStoppedOutcome(getWorkDir(), t)
		if e != nil {
			return e
		}
		raw, e := json.Marshal(stopped)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(raw)
		scope := advancedcapability.TransferRetirement{BaselineSpecHash: current.SpecHash, CandidateSpecHash: candidate.SpecHash, OperationID: operation, OutcomeDigest: "sha256:" + hex.EncodeToString(sum[:]), Role: "destination", TransferID: t.ID}
		if e = admitApplicationTransferRetirement(cmd, candidate.CanonicalStackSpec, scope, plan); e != nil {
			return e
		}
	}
	persisted, err := stackspecintent.PersistWithSnapshotLease(ctx, operation, stackspecintent.Request{WorkspaceRoot: getWorkDir(), SpecPath: specPath, Candidate: candidate.CanonicalStackSpec, ExpectedSpecHash: current.SpecHash, BuildVersion: version, Authority: authority})
	if err != nil {
		return err
	}
	return journal.CompleteApplicationTransferAbortRetirement(operation, t.ID, persisted.SpecHash)
}

func runApplicationTransferResume(cmd *cobra.Command, _ []string) error {
	approved, _ := cmd.Flags().GetBool("owner-approve")
	sourcePath, _ := cmd.Flags().GetString("source-receipt")
	outcomePath, _ := cmd.Flags().GetString("target-outcome")
	fingerprint, _ := cmd.Flags().GetString("target-fingerprint")
	if !approved || sourcePath == "" || outcomePath == "" || fingerprint == "" {
		return errors.New("source resume requires source receipt, independently pinned signed stopped-target outcome and --owner-approve")
	}
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	var receipt backuplifecycle.ApplicationTransferExportReceipt
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return err
	}
	if receipt.Export.Manifest.ApplicationTransfer == nil {
		return errors.New("no source transfer")
	}
	key, err := localevidence.LoadOwnerKey(getWorkDir())
	if err != nil {
		return err
	}
	t, err := backuplifecycle.VerifyApplicationTransferExportReceipt(receipt, key.OwnerRef, localevidence.ApplicationTransferFingerprint(key.Public()), receipt.Export.Manifest.ApplicationTransfer.ID, receipt.Export.ArchiveSHA256)
	if err != nil {
		return err
	}
	raw, err = os.ReadFile(outcomePath)
	if err != nil {
		return err
	}
	var outcome backuplifecycle.ApplicationTransferOutcome
	if err = json.Unmarshal(raw, &outcome); err != nil {
		return err
	}
	if err = backuplifecycle.VerifyApplicationTransferOutcome(outcome, t, fingerprint); err != nil {
		return err
	}
	if outcome.State != "stopped" {
		return errors.New("source cannot resume while receiving operation may be active or unknown")
	}
	workloads, err := backuplifecycle.ReadApplicationTransferHold(getWorkDir(), receipt.SourceOperationID, t.ID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), backupLongOperationTimeout)
	defer cancel()
	current, err := inspectNativeV2BackupAuthorityForRequest(ctx, getWorkDir(), specFile)
	if err != nil {
		return err
	}
	if current.Lineage != t.Lineage {
		return errors.New("source lineage changed while transfer was held")
	}
	closure, err := restoreactivation.ApplicationTransferProjection(current.WorkspaceRoot, current.Plan, current.AppliedAuthority.Manifest, workloads, current.Policy)
	if err != nil {
		return err
	}
	custody, err := nativeBackupApplicationCustody(current, closure)
	if err != nil {
		return err
	}
	bindings, err := nativeBackupApplicationBindings(ctx, current)
	if err != nil {
		return err
	}
	persistent, err := restoreactivation.ApplicationTransferPersistentWorkloads(current.Policy, workloads)
	if err != nil {
		return err
	}
	runtime, err := localbackupruntime.NewApplicationSnapshotRuntime(getWorkDir(), current.Policy, custody, persistent, bindings...)
	if err != nil {
		return err
	}
	if err = runtime.BindApplicationTransferClosure(closure, custody); err != nil {
		return err
	}
	if err = runtime.BindApplicationTransferResume(t.ID); err != nil {
		return err
	}
	backup, err := backuplifecycle.NewCreator().Create(getWorkDir(), runtime)
	if err != nil {
		return err
	}
	canonical, err := json.Marshal(outcome)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(canonical)
	join, err := currentLifecycleMutationJoin("backup transfer-resume")
	if err != nil {
		return err
	}
	err = lifecyclemutation.WithSnapshotMutationContext(ctx, getWorkDir(), join, receipt.SourceOperationID, func(read context.Context) error {
		if e := backup.RecordApplicationTransferOutcome(receipt.SourceOperationID, t.ID, "sha256:"+hex.EncodeToString(sum[:]), "stopped"); e != nil {
			return e
		}
		_, e := backup.Run(read, backuplifecycle.RunInput{OwnerRef: current.OwnerRef, AuthorityRef: current.AuthorityRef, Lineage: current.Lineage, PolicyArtifact: current.PolicyArtifact, OperationID: receipt.SourceOperationID, ProtectRecovery: true, Workloads: persistent})
		return e
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"transferId": t.ID, "sourceState": "resumed-after-verified-target-stop"})
}

func configureApplicationTransferControlCommands() {
	target := &cobra.Command{Use: "transfer-target", Short: "Issue a signed receiving-node admission for an existing private age identity", Args: machineAwareNoArgs, RunE: runApplicationTransferTarget}
	target.Flags().String("identity-file", "", "Existing private receiving age identity")
	target.Flags().String("expected-plan-hash", "", "Exact receiving candidate Plan hash")
	finish := &cobra.Command{Use: "transfer-finish", Short: "Retire selected source intent after signed destination activation and retain all source data", Args: machineAwareNoArgs, RunE: runApplicationTransferFinish}
	for _, f := range []string{"source-receipt", "target-outcome", "target-fingerprint", "candidate-output", "inventory"} {
		finish.Flags().String(f, "", "Exact existing transfer/source retirement input")
	}
	finish.Flags().String("capability", "", "Techstack capability for the exact Advanced held transfer retirement")
	finish.Flags().Bool("owner-approve", false, "Approve source intent retirement while preserving data")
	backupCmd.AddCommand(target, finish)
	abort := &cobra.Command{Use: "transfer-abort", Short: "Hold the receiving application stopped and issue signed abort evidence", Args: machineAwareNoArgs, RunE: runApplicationTransferAbort}
	for _, f := range []string{"source-receipt", "source-owner", "source-fingerprint", "transfer-id", "archive-sha256", "expected-plan-hash"} {
		abort.Flags().String(f, "", "Exact admitted transfer/target authority")
	}
	abort.Flags().String("candidate-output", "", "New candidate path for Advanced target abort retirement")
	abort.Flags().String("inventory", "", "Existing local inventory for selected target intent retirement")
	abort.Flags().String("capability", "", "Techstack capability for the exact Advanced held target retirement")
	abort.Flags().Bool("owner-approve", false, "Stop and hold only the receiving application closure")
	resume := &cobra.Command{Use: "transfer-resume", Short: "Resume held source only after independently verified stopped-destination evidence", Args: machineAwareNoArgs, RunE: runApplicationTransferResume}
	for _, f := range []string{"source-receipt", "target-outcome", "target-fingerprint"} {
		resume.Flags().String(f, "", "Exact original transfer and stopped-target proof")
	}
	resume.Flags().Bool("owner-approve", false, "Resume the retained original application after verified target stop")
	backupCmd.AddCommand(abort, resume)
}

func runApplicationTransferTarget(cmd *cobra.Command, _ []string) error {
	if err := requireApplicationTransferDestinationMode(getWorkDir()); err != nil {
		return err
	}
	identityFile, _ := cmd.Flags().GetString("identity-file")
	expected, _ := cmd.Flags().GetString("expected-plan-hash")
	if identityFile == "" || expected == "" {
		return errors.New("receiving admission requires --identity-file and --expected-plan-hash")
	}
	identities, err := backuplifecycle.ReadEmergencyIdentities(identityFile)
	if err != nil {
		return err
	}
	if len(identities) != 1 {
		return errors.New("receiving admission requires exactly one private X25519 identity")
	}
	identity, ok := identities[0].(*age.X25519Identity)
	if !ok {
		return errors.New("receiving admission requires X25519 identity")
	}
	current, err := inspectNativeV2GeneratedAuthority(cmd.Context(), getWorkDir(), specFile)
	if err != nil {
		return err
	}
	if err = current.Plan.RequireExpectedPlanHash(expected); err != nil {
		return err
	}
	var plan struct {
		StackID string `json:"stackId"`
	}
	if err = json.Unmarshal(current.Plan.Canonical(), &plan); err != nil {
		return err
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	key, err := localevidence.LoadOwnerKey(current.WorkspaceRoot)
	if err != nil {
		return err
	}
	admission := applicationTransferDestinationAdmission{APIVersion: "stackkit.application-transfer-destination/v1", Destination: backuplifecycle.ApplicationTransferDestination{OwnerRef: current.OwnerRef, SigningFingerprint: localevidence.ApplicationTransferFingerprint(key.Public()), StackID: plan.StackID, SiteRef: current.Owner.Binding.SiteRef, NodeRef: current.Owner.Binding.NodeRef, PlanHash: expected, Recipient: identity.Recipient().String(), Nonce: hex.EncodeToString(nonce[:]), ExpiresAt: time.Now().UTC().Add(12 * time.Hour)}}
	canonical, err := json.Marshal(admission)
	if err != nil {
		return err
	}
	admission.Signature, err = localevidence.SignApplicationTransfer(current.WorkspaceRoot, canonical)
	if err != nil {
		return err
	}
	if err = persistApplicationTransferAdmission(current.WorkspaceRoot, admission); err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(admission)
}

func verifyApplicationTransferDestinationAdmission(admission applicationTransferDestinationAdmission, expectedOwner, fingerprint string, fresh ...bool) error {
	if admission.APIVersion != "stackkit.application-transfer-destination/v1" || admission.Destination.OwnerRef != expectedOwner || admission.Destination.SigningFingerprint != fingerprint || len(admission.Destination.Nonce) != 64 || ((len(fresh) == 0 || fresh[0]) && !admission.Destination.ExpiresAt.After(time.Now())) || admission.Destination.ExpiresAt.After(time.Now().Add(24*time.Hour)) {
		return errors.New("receiving admission is invalid, expired or differs from pinned destination")
	}
	signature := admission.Signature
	admission.Signature = localevidence.ApplicationTransferSignature{}
	canonical, err := json.Marshal(admission)
	if err != nil {
		return err
	}
	return localevidence.VerifyApplicationTransfer(canonical, signature, expectedOwner, fingerprint)
}

func runApplicationTransferFinish(cmd *cobra.Command, _ []string) error {
	sourcePath, _ := cmd.Flags().GetString("source-receipt")
	outcomePath, _ := cmd.Flags().GetString("target-outcome")
	targetFingerprint, _ := cmd.Flags().GetString("target-fingerprint")
	approved, _ := cmd.Flags().GetBool("owner-approve")
	if sourcePath == "" || outcomePath == "" || targetFingerprint == "" || !approved {
		return errors.New("transfer finish requires source receipt, target outcome, independently pinned target fingerprint and --owner-approve")
	}
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	var source backuplifecycle.ApplicationTransferExportReceipt
	if err = json.Unmarshal(raw, &source); err != nil {
		return err
	}
	if source.Export.Manifest.ApplicationTransfer == nil {
		return errors.New("source receipt contains no transfer")
	}
	key, err := localevidence.LoadOwnerKey(getWorkDir())
	if err != nil {
		return err
	}
	t, err := backuplifecycle.VerifyApplicationTransferExportReceipt(source, key.OwnerRef, localevidence.ApplicationTransferFingerprint(key.Public()), source.Export.Manifest.ApplicationTransfer.ID, source.Export.ArchiveSHA256)
	if err != nil {
		return err
	}
	if t.Mode != "migrate" {
		return errors.New("copy exports do not authorize source retirement")
	}
	raw, err = os.ReadFile(outcomePath)
	if err != nil {
		return err
	}
	var outcome backuplifecycle.ApplicationTransferOutcome
	if err = json.Unmarshal(raw, &outcome); err != nil {
		return err
	}
	if err = backuplifecycle.VerifyApplicationTransferOutcome(outcome, t, targetFingerprint); err != nil {
		return err
	}
	if outcome.State != "activated" {
		return errors.New("source retirement requires verified activated destination")
	}
	canonical, err := json.Marshal(outcome)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(canonical)
	outcomeDigest := "sha256:" + hex.EncodeToString(sum[:])
	workloads, err := backuplifecycle.ReadApplicationTransferRetirementHold(getWorkDir(), source.SourceOperationID, t.ID)
	if err != nil {
		return err
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return err
	}
	specPath, _, _, err := config.NewLoader(getWorkDir()).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return err
	}
	specBytes, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	current, err := service.ValidateStackSpec(specBytes)
	if err != nil {
		return err
	}
	if current.SpecHash != t.Lineage.Binding.SpecHash {
		planned, e := backuplifecycle.ReadApplicationTransferRetirementCandidate(getWorkDir(), source.SourceOperationID, t.ID)
		if e != nil || planned != current.SpecHash {
			return errors.New("held source intent differs from original transfer and planned retirement")
		}
		join, e := currentLifecycleMutationJoin("backup transfer-finish")
		if e != nil {
			return e
		}
		e = lifecyclemutation.WithSnapshotMutationContext(cmd.Context(), getWorkDir(), join, source.SourceOperationID, func(context.Context) error {
			journal, e := backuplifecycle.NewCreator().OpenTransferJournal(getWorkDir())
			if e != nil {
				return e
			}
			return journal.CompleteApplicationTransferRetirement(source.SourceOperationID, t.ID, current.SpecHash)
		})
		if e != nil {
			return e
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"transferId": t.ID, "sourceState": "retired-stopped-data-retained", "retiredSpecHash": current.SpecHash})
	}
	var intent struct {
		Install struct {
			Mode string `json:"mode"`
		} `json:"install"`
	}
	if err = json.Unmarshal(current.CanonicalStackSpec, &intent); err != nil {
		return err
	}
	inventoryPath, _ := cmd.Flags().GetString("inventory")
	inventory, err := readArchitectureV2Inventory(getWorkDir(), inventoryPath)
	if err != nil {
		return err
	}
	resolved, err := service.Resolve(architecturev2.ResolveInput{StackSpec: current.CanonicalStackSpec, Inventory: inventory})
	if err != nil {
		return err
	}
	plan, err := service.VerifyCanonicalPlan(resolved.CanonicalPlan)
	if err != nil {
		return err
	}
	workloads, err = service.OrderWorkloadRemovals(plan, workloads)
	if err != nil {
		return err
	}
	candidate, err := service.MaterializeWorkloadRemovals(current.CanonicalStackSpec, workloads)
	if err != nil {
		return err
	}
	if _, err = service.Resolve(architecturev2.ResolveInput{StackSpec: candidate.CanonicalStackSpec, Inventory: inventory}); err != nil {
		return err
	}
	join, err := currentLifecycleMutationJoin("backup transfer-finish")
	if err != nil {
		return err
	}
	var retiredHash string
	err = lifecyclemutation.WithSnapshotMutationContext(cmd.Context(), getWorkDir(), join, source.SourceOperationID, func(ctx context.Context) error {
		// The existing snapshot journal owns the transfer's source hold.
		journalService, e := backuplifecycle.NewCreator().OpenTransferJournal(getWorkDir())
		if e != nil {
			return e
		}
		if e := journalService.RecordApplicationTransferOutcome(source.SourceOperationID, t.ID, outcomeDigest, "activated"); e != nil {
			return e
		}
		if e := journalService.PrepareApplicationTransferRetirement(source.SourceOperationID, t.ID, candidate.SpecHash, intent.Install.Mode == "advanced"); e != nil {
			return e
		}
		if intent.Install.Mode == "advanced" {
			scope := advancedcapability.TransferRetirement{BaselineSpecHash: current.SpecHash, CandidateSpecHash: candidate.SpecHash, OperationID: source.SourceOperationID, OutcomeDigest: outcomeDigest, Role: "source", TransferID: t.ID}
			if e := admitApplicationTransferRetirement(cmd, candidate.CanonicalStackSpec, scope, plan); e != nil {
				return e
			}
		}
		persisted, e := stackspecintent.PersistWithSnapshotLease(ctx, source.SourceOperationID, stackspecintent.Request{WorkspaceRoot: getWorkDir(), SpecPath: specPath, Candidate: candidate.CanonicalStackSpec, ExpectedSpecHash: current.SpecHash, BuildVersion: version, Authority: service})
		if e != nil {
			return e
		}
		retiredHash = persisted.SpecHash
		return journalService.CompleteApplicationTransferRetirement(source.SourceOperationID, t.ID, retiredHash)
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"apiVersion": "stackkit.application-transfer-source-finish/v1", "transferId": t.ID, "sourceState": "retired-stopped-data-retained", "retiredSpecHash": retiredHash, "destinationOutcomeDigest": outcomeDigest, "workloads": workloads})
}

func persistApplicationTransferAdmission(workspace string, a applicationTransferDestinationAdmission) error {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	dir := ".stackkit/backups/transfer-admissions"
	if err = tx.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return tx.WriteFileExclusive(dir+"/"+a.Destination.Nonce+".json", raw, 0o600)
}

func verifyApplicationTransferIssuedAdmission(workspace string, t backuplifecycle.ApplicationTransfer, fresh bool) error {
	if len(t.Destination.Nonce) != 64 {
		return errors.New("invalid receiving admission nonce")
	}
	if _, err := hex.DecodeString(t.Destination.Nonce); err != nil {
		return err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	raw, _, err := tx.ReadStable(".stackkit/backups/transfer-admissions/" + t.Destination.Nonce + ".json")
	if err != nil {
		return errors.New("destination has not issued this transfer admission")
	}
	var a applicationTransferDestinationAdmission
	if err = json.Unmarshal(raw, &a); err != nil {
		return err
	}
	if a.Destination != t.Destination || a.APIVersion != "stackkit.application-transfer-destination/v1" {
		return errors.New("transfer differs from exact destination-issued admission")
	}
	key, err := localevidence.LoadOwnerKey(workspace)
	if err != nil {
		return err
	}
	fingerprint := localevidence.ApplicationTransferFingerprint(key.Public())
	if fingerprint != a.Destination.SigningFingerprint || key.OwnerRef != a.Destination.OwnerRef {
		return errors.New("destination key/owner changed since issued admission")
	}
	signature := a.Signature
	a.Signature = localevidence.ApplicationTransferSignature{}
	canonical, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if err = localevidence.VerifyApplicationTransfer(canonical, signature, key.OwnerRef, fingerprint); err != nil {
		return err
	}
	if fresh && !a.Destination.ExpiresAt.After(time.Now()) {
		return errors.New("destination-issued admission expired")
	}
	return nil
}

type applicationTransferAdmissionConsumption struct {
	TransferID        string                                         `json:"transferId"`
	SourceOwner       string                                         `json:"sourceOwner"`
	SourceFingerprint string                                         `json:"sourceFingerprint"`
	CiphertextDigest  string                                         `json:"ciphertextDigest"`
	Destination       backuplifecycle.ApplicationTransferDestination `json:"destination"`
	Signature         localevidence.OwnerPolicyStateSignature        `json:"signature"`
}

func consumeApplicationTransferAdmission(workspace string, t backuplifecycle.ApplicationTransfer, sourceFingerprint, ciphertextDigest string) error {
	record := applicationTransferAdmissionConsumption{TransferID: t.ID, SourceOwner: t.SourceOwner, SourceFingerprint: sourceFingerprint, CiphertextDigest: strings.TrimPrefix(ciphertextDigest, "sha256:"), Destination: t.Destination}
	canonical, err := json.Marshal(record)
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	file := ".stackkit/backups/transfer-admissions/" + t.Destination.Nonce + "-consumed.json"
	raw, _, err := tx.ReadStable(file)
	if err == nil {
		var prior applicationTransferAdmissionConsumption
		if err = json.Unmarshal(raw, &prior); err != nil {
			return err
		}
		signature := prior.Signature
		prior.Signature = localevidence.OwnerPolicyStateSignature{}
		signed, e := json.Marshal(prior)
		if e != nil {
			return e
		}
		if e = localevidence.VerifyOwnerPolicyState(workspace, signed, signature); e != nil {
			return e
		}
		if string(signed) != string(canonical) {
			return errors.New("destination-issued nonce was already consumed by a different exact source/ciphertext/transfer")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	record.Signature, err = localevidence.SignOwnerPolicyState(workspace, canonical)
	if err != nil {
		return err
	}
	raw, err = json.Marshal(record)
	if err != nil {
		return err
	}
	return tx.WriteFileExclusive(file, raw, 0o600)
}

// The pending proof is signed once under the abort lease. A crash before the
// selected intent CAS must replay exactly this digest, not a fresh timestamp.
func pendingApplicationTransferStoppedOutcome(workspace string, t backuplifecycle.ApplicationTransfer) (backuplifecycle.ApplicationTransferOutcome, error) {
	if prior, exists, err := readStoredApplicationTransferOutcome(workspace, t, "stopped", true); err != nil {
		return prior, err
	} else if exists {
		return prior, nil
	}
	outcome, err := backuplifecycle.SignApplicationTransferOutcome(workspace, backuplifecycle.ApplicationTransferOutcome{TransferID: t.ID, Destination: t.Destination, State: "stopped"})
	if err != nil {
		return outcome, err
	}
	return outcome, persistApplicationTransferOutcome(workspace, outcome, true)
}

// admitApplicationTransferRetirement runs beneath the original snapshot lease.
// It neither joins a full upgrade nor applies or rolls back the source runtime.
// All expected scope fields were reconstructed from the signed hold/outcome
// and the canonical selected removal candidate in this invocation.
func admitApplicationTransferRetirement(cmd *cobra.Command, candidate []byte, expected advancedcapability.TransferRetirement, plan generationartifact.VerifiedPlan) error {
	if path, _ := cmd.Flags().GetString("candidate-output"); path != "" {
		// Retries may reuse only byte-identical canonical candidate output.
		if existing, e := readAdvancedRegular(path, maxAdvancedCandidateBytes, "transfer retirement candidate"); e == nil {
			if !bytes.Equal(existing, candidate) {
				return errors.New("transfer retirement output already names a different candidate")
			}
		} else if errors.Is(e, os.ErrNotExist) {
			if e = writeApplicationTransferFile(path, candidate); e != nil {
				return e
			}
		} else {
			return e
		}
	}
	owner, err := localevidence.LoadOwnerCustody(getWorkDir())
	if err != nil {
		return err
	}
	trusted, err := advancedtrust.Load(getWorkDir())
	if err != nil {
		return err
	}
	var local struct {
		StackID string `json:"stackId"`
	}
	if err = json.Unmarshal(plan.Canonical(), &local); err != nil {
		return err
	}
	if plan.Binding().SpecHash != expected.BaselineSpecHash {
		return errors.New("transfer retirement plan differs from its canonical baseline")
	}
	trust := trusted.TrustBundle()
	path, _ := cmd.Flags().GetString("capability")
	if path == "" {
		if err = writeCommandResultStatus(cmd, cmd.CommandPath(), "denied", map[string]any{"apiVersion": "stackkit.application-transfer-retirement-request/v1", "reasonCode": string(advancedcapability.ReasonCapabilityRequired), "status": "awaiting-capability", "stackId": local.StackID, "ownerRef": owner.OwnerRef, "transferRetirement": expected}); err != nil {
			return err
		}
		return &advancedcapability.Denial{Code: advancedcapability.ReasonCapabilityRequired, Field: "capability", Detail: "exact Techstack transfer retirement capability is required; selected runtime remains stopped and data retained"}
	}
	raw, err := readAdvancedRegular(resolvePathFromWorkDir(getWorkDir(), path), maxAdvancedTrustBundleBytes, "Advanced capability")
	if err != nil {
		return err
	}
	_, err = advancedcapability.Verify(raw, advancedcapability.Request{Now: time.Now().UTC(), TrustBundle: &trust, StackID: local.StackID, OwnerRef: owner.OwnerRef, Operation: advancedcapability.OperationApplicationTransferRetire, TransferRetirement: &expected})
	if err != nil {
		return err
	}
	journal, err := backuplifecycle.NewCreator().OpenTransferJournal(getWorkDir())
	if err != nil {
		return err
	}
	return journal.RecordApplicationTransferRetirementAuthorization(expected.OperationID, expected.TransferID, raw)
}

// Atomic no-replace publication keeps a crash from exposing a partial signed
// proof or candidate. The existing confined filesystem owns the parent handle.
func writeApplicationTransferFile(output string, data []byte) error {
	target, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600NoReplace(filepath.Base(target), data)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("transfer evidence was not durably installed")
	}
	return root.VerifyPathIdentity()
}

// Advanced receiving activation needs its own exact Techstack grant. Until
// that wire/dispatch step exists, refuse before issuing an admission or
// decrypting/adopting credentials. Aborting a prior admission stays available.
func requireApplicationTransferDestinationMode(workspace string) error {
	specPath, _, _, err := config.NewLoader(workspace).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	author, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return err
	}
	current, err := author.ValidateStackSpec(raw)
	if err != nil {
		return err
	}
	var intent struct {
		Install struct {
			Mode string `json:"mode"`
		} `json:"install"`
	}
	if err = json.Unmarshal(current.CanonicalStackSpec, &intent); err != nil {
		return err
	}
	if intent.Install.Mode == "advanced" {
		return errors.New("Advanced receiving transfer activation requires its exact Techstack capability dispatch; no receiving admission was issued")
	}
	return nil
}
