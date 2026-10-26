package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localbackupruntime"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/spf13/cobra"
)

func configureApplicationTransferCommands() {
	export := &cobra.Command{Use: "transfer-export", Short: "Export selected native applications to one receiving node", Args: machineAwareNoArgs, RunE: runApplicationTransferExport}
	export.Flags().StringArray("workload", nil, "Exact workload closure to transfer; repeat for a use case")
	export.Flags().String("use-case", "", "Transfer every currently selected member of this catalog use case")
	export.Flags().String("destination", "", "Receiving node admission JSON")
	export.Flags().String("destination-owner", "", "Independently admitted receiving Owner")
	export.Flags().String("destination-fingerprint", "", "Independently pinned full receiving signing-key SHA-256")
	export.Flags().String("target", "", "New encrypted export directory")
	export.Flags().String("operation-id", "", "Resumable source snapshot operation")
	export.Flags().String("mode", "copy", "copy resumes source; migrate holds selected source until authenticated destination outcome")
	export.Flags().String("expected-plan-hash", "", "Exact source Plan hash")
	export.Flags().Bool("json", false, "Output transfer receipt")
	importCmd := &cobra.Command{Use: "transfer-import", Short: "Import an authenticated selected application transfer using destination authority", Args: machineAwareNoArgs, RunE: runApplicationTransferImport}
	for _, f := range []string{"archive", "archive-sha256", "identity-file", "source-receipt", "source-owner", "source-fingerprint", "transfer-id", "expected-plan-hash"} {
		importCmd.Flags().String(f, "", "Required exact transfer admission value")
	}
	importCmd.Flags().Bool("owner-approve", false, "Approve selected destination application replacement")
	importCmd.Flags().Bool("recover", false, "Recover the original signed transfer activation journal")
	importCmd.Flags().Bool("json", false, "Output destination activation receipt")
	importCmd.Flags().Int64("max-bytes", 512<<30, "Maximum decompressed bytes")
	prepare := &cobra.Command{Use: "transfer-prepare", Short: "Adopt transferred application credentials before the receiving application's first Apply", Args: machineAwareNoArgs, RunE: runApplicationTransferPrepare}
	for _, f := range []string{"archive", "archive-sha256", "identity-file", "source-receipt", "source-owner", "source-fingerprint", "transfer-id", "expected-plan-hash"} {
		prepare.Flags().String(f, "", "Required exact transfer admission value")
	}
	prepare.Flags().Bool("owner-approve", false, "Approve selected new application credential adoption")
	prepare.Flags().Int64("max-bytes", 512<<30, "Maximum decompressed bytes")
	backupCmd.AddCommand(export, importCmd, prepare)
	recoverCmd := &cobra.Command{Use: "transfer-recover", Short: "Recover the original typed transfer activation through its existing journal", Args: machineAwareNoArgs, RunE: runApplicationTransferImport}
	for _, f := range []string{"archive", "archive-sha256", "identity-file", "source-receipt", "source-owner", "source-fingerprint", "transfer-id", "expected-plan-hash"} {
		recoverCmd.Flags().String(f, "", "Exact original transfer admission")
	}
	recoverCmd.Flags().Bool("recover", true, "Recover original transfer activation")
	_ = recoverCmd.Flags().MarkHidden("recover")
	recoverCmd.Flags().Bool("owner-approve", false, "Authorize original selected activation recovery")
	recoverCmd.Flags().Bool("json", false, "Output recovery outcome")
	recoverCmd.Flags().Int64("max-bytes", 512<<30, "Maximum decompressed archive bytes")
	backupCmd.AddCommand(recoverCmd)
	configureApplicationTransferControlCommands()
}

func newNativeV2ApplicationBackupService(authority nativeV2BackupAuthority, workloads []string, exportWhileQuiesced func(context.Context) error, holdID ...func() string) (nativeV2BackupService, error) {
	closure, err := restoreactivation.ApplicationTransferProjection(authority.WorkspaceRoot, authority.Plan, authority.AppliedAuthority.Manifest, workloads, authority.Policy)
	if err != nil {
		return nil, err
	}
	persistent, err := restoreactivation.ApplicationTransferPersistentWorkloads(authority.Policy, workloads)
	if err != nil {
		return nil, err
	}
	custody, err := nativeBackupApplicationCustody(authority, closure)
	if err != nil {
		return nil, err
	}
	bindings, err := nativeBackupApplicationBindings(context.Background(), authority)
	if err != nil {
		return nil, err
	}
	runtime, err := localbackupruntime.NewApplicationSnapshotRuntime(authority.WorkspaceRoot, authority.Policy, custody, persistent, bindings...)
	if err != nil {
		return nil, err
	}
	if err = runtime.BindApplicationTransferClosure(closure, custody); err != nil {
		return nil, err
	}
	if exportWhileQuiesced != nil {
		if err = runtime.BindApplicationTransferExport(exportWhileQuiesced); err != nil {
			return nil, err
		}
	}
	if len(holdID) > 1 {
		return nil, errors.New("ambiguous application transfer hold")
	}
	if len(holdID) == 1 {
		if err = runtime.BindApplicationTransferHold(holdID[0]); err != nil {
			return nil, err
		}
	}
	return backuplifecycle.NewCreator().Create(authority.WorkspaceRoot, runtime)
}

func runApplicationTransferExport(cmd *cobra.Command, _ []string) error {
	workloads, _ := cmd.Flags().GetStringArray("workload")
	destinationPath, _ := cmd.Flags().GetString("destination")
	target, _ := cmd.Flags().GetString("target")
	expected, _ := cmd.Flags().GetString("expected-plan-hash")
	useCase, _ := cmd.Flags().GetString("use-case")
	mode, _ := cmd.Flags().GetString("mode")
	if mode != "copy" && mode != "migrate" {
		return errors.New("--mode must be copy or migrate")
	}
	if destinationPath == "" || target == "" || expected == "" || (len(workloads) == 0 && useCase == "") || (len(workloads) > 0 && useCase != "") {
		return errors.New("transfer export requires --workload, --destination, --target and --expected-plan-hash")
	}
	raw, err := os.ReadFile(destinationPath)
	if err != nil {
		return err
	}
	var destinationAdmission applicationTransferDestinationAdmission
	if err = json.Unmarshal(raw, &destinationAdmission); err != nil {
		return err
	}
	destinationOwner, _ := cmd.Flags().GetString("destination-owner")
	destinationFingerprint, _ := cmd.Flags().GetString("destination-fingerprint")
	if err = verifyApplicationTransferDestinationAdmission(destinationAdmission, destinationOwner, destinationFingerprint, false); err != nil {
		return err
	}
	destination := destinationAdmission.Destination
	recipient, err := age.ParseX25519Recipient(destination.Recipient)
	if err != nil {
		return err
	}
	operation, _ := cmd.Flags().GetString("operation-id")
	operation, err = normalizeNativeV2BackupOperationID(nativeV2BackupRun, operation)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), backupLongOperationTimeout)
	defer cancel()
	authority, err := inspectNativeV2BackupAuthorityForRequest(ctx, getWorkDir(), specFile)
	if err != nil {
		return err
	}
	if err = authority.Plan.RequireExpectedPlanHash(expected); err != nil {
		return err
	}
	if useCase != "" {
		catalog, e := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
		if e != nil {
			return e
		}
		workloads, e = catalog.SelectedUseCaseWorkloads(authority.Plan, useCase)
		if e != nil {
			return e
		}
	}
	closure, err := restoreactivation.ApplicationTransferProjection(authority.WorkspaceRoot, authority.Plan, authority.AppliedAuthority.Manifest, workloads, authority.Policy)
	if err != nil {
		return err
	}
	persistent, err := restoreactivation.ApplicationTransferPersistentWorkloads(authority.Policy, workloads)
	if err != nil {
		return err
	}
	if _, _, err = backuplifecycle.SelectApplicationTransfer(authority.Policy, workloads, closure); err != nil {
		return err
	}
	if receipt, found, e := recoverApplicationTransferSourceReceipt(authority, operation, target, mode, destination, workloads); e != nil {
		return e
	} else if found {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(receipt)
	}
	portable, err := restoreactivation.ApplicationTransferConfiguration(authority.Plan, workloads)
	if err != nil {
		return err
	}
	var exported backuplifecycle.EmergencyExportResult
	execute := func(ctx context.Context) error {
		return withArchitectureV2OutputLock(getWorkDir(), authority.OutputRoot, func(_ *confinedfs.Transaction, _ *confinedfs.OutputLock) error {
			current, err := inspectNativeV2BackupAuthorityForRequest(ctx, getWorkDir(), specFile)
			if err != nil {
				return err
			}
			if !sameNativeV2BackupAuthority(authority, current) {
				return errors.New("source authority changed before selected transfer")
			}
			bindings, err := restoreactivation.ApplicationTransferSecretBindings(authority.WorkspaceRoot, authority.Plan, authority.AppliedAuthority.Manifest, workloads)
			if err != nil {
				return err
			}
			materials, err := restoreactivation.ReadApplicationTransferSecrets(authority.WorkspaceRoot, bindings)
			if err != nil {
				return err
			}
			defer restoreactivation.ClearApplicationTransferSecrets(materials)
			exportCallback := func(snapshotContext context.Context) error {
				var exportErr error
				exported, exportErr = backuplifecycle.ExportApplicationTransfer(snapshotContext, backuplifecycle.ApplicationTransferExportInput{Mode: mode, PortableConfiguration: portable, WorkspaceRoot: authority.WorkspaceRoot, OwnerRef: authority.OwnerRef, Lineage: authority.Lineage, Policy: authority.Policy, PolicyDigest: authority.PolicyDigest, Workloads: workloads, RuntimeClosure: closure, SecretMaterials: materials, Target: target, Recipients: []age.Recipient{recipient}, Destination: destination, Quiesce: func(context.Context) (func() error, error) { return func() error { return nil }, nil }})
				return exportErr
			}
			holds := []func() string{}
			if mode == "migrate" {
				holds = append(holds, func() string {
					if exported.Manifest.ApplicationTransfer == nil {
						return ""
					}
					return exported.Manifest.ApplicationTransfer.ID
				})
			}
			service, err := newNativeV2ApplicationBackupService(authority, workloads, exportCallback, holds...)
			if err != nil {
				return err
			}
			_, err = service.Run(ctx, backuplifecycle.RunInput{OwnerRef: authority.OwnerRef, AuthorityRef: authority.AuthorityRef, Lineage: authority.Lineage, PolicyArtifact: authority.PolicyArtifact, OperationID: operation, ProtectRecovery: true, Workloads: persistent})
			if errors.Is(err, backuplifecycle.ErrApplicationTransferHeld) && mode == "migrate" && exported.Manifest.ApplicationTransfer != nil {
				return nil
			}
			return err
		})
	}
	join, err := currentLifecycleMutationJoin("backup transfer-export")
	if err != nil {
		return err
	}
	if err = lifecyclemutation.WithSnapshotMutationContext(ctx, getWorkDir(), join, operation, execute); err != nil {
		return err
	}
	if exported.Manifest.ApplicationTransfer == nil {
		return errors.New("snapshot completed without a new transfer archive; inspect prior export or use a new operation")
	}
	receipt, err := newApplicationTransferExportReceipt(authority.WorkspaceRoot, exported, operation, mode)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(receipt)
}

func runApplicationTransferImport(cmd *cobra.Command, _ []string) error {
	if err := requireApplicationTransferDestinationMode(getWorkDir()); err != nil {
		return err
	}
	recovering, _ := cmd.Flags().GetBool("recover")
	values := map[string]string{}
	for _, f := range []string{"archive", "archive-sha256", "identity-file", "source-receipt", "source-owner", "source-fingerprint", "transfer-id", "expected-plan-hash"} {
		values[f], _ = cmd.Flags().GetString(f)
		if values[f] == "" && !(recovering && (f == "archive" || f == "identity-file")) {
			return fmt.Errorf("--%s is required", f)
		}
	}
	approved, _ := cmd.Flags().GetBool("owner-approve")
	if !approved {
		return errors.New("transfer import requires --owner-approve")
	}
	operation, err := restoreactivation.TransferOperationID(values["transfer-id"])
	if err != nil {
		return err
	}
	admittedReceipt, err := readAdmittedApplicationTransferReceipt(values, !recovering)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), backupLongOperationTimeout)
	defer cancel()
	authority, err := inspectNativeV2BackupAuthorityForRequest(ctx, getWorkDir(), specFile)
	if err != nil {
		return err
	}
	if err = authority.Plan.RequireExpectedPlanHash(values["expected-plan-hash"]); err != nil {
		return err
	}
	admittedTransfer := *admittedReceipt.Export.Manifest.ApplicationTransfer
	if err = verifyApplicationTransferIssuedAdmission(authority.WorkspaceRoot, admittedTransfer, !recovering); err != nil {
		return err
	}
	if _, exists, e := readStoredApplicationTransferOutcome(authority.WorkspaceRoot, admittedTransfer, "stopped"); e != nil {
		return e
	} else if exists {
		return errors.New("receiving transfer was aborted/stopped; replay is denied")
	}
	if completed, exists, e := readStoredApplicationTransferOutcome(authority.WorkspaceRoot, admittedTransfer, "activated"); e != nil {
		return e
	} else if exists {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(completed)
	}
	var staged backuplifecycle.EmergencyRestoreResult
	var identities []age.Identity
	if recovering {
		original, e := lifecyclemutation.ReadRestoreActivationRecord(authority.WorkspaceRoot, operation)
		if e != nil {
			return e
		}
		if original.RestoreActivation.Authority.RestoreResultID != values["transfer-id"] || original.RestoreActivation.Authority.PlanHash != authority.Plan.Binding().PlanHash {
			return errors.New("recovery journal differs from original admitted transfer")
		}
		// Its signed journal was opened only after authenticated staging and
		// safety validation. Recovery rebinds that consumed transfer; it does
		// not manufacture a repository snapshot or a normal RestoreResult.
		staged = backuplifecycle.EmergencyRestoreResult{ContentVerified: true, Manifest: admittedReceipt.Export.Manifest, Target: filepath.Join(authority.WorkspaceRoot, ".stackkit", "backups", operation)}
	} else {
		file, err := os.Open(values["archive"])
		if err != nil {
			return err
		}
		digest := sha256.New()
		_, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if err = errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		if hex.EncodeToString(digest.Sum(nil)) != strings.TrimPrefix(values["archive-sha256"], "sha256:") {
			return errors.New("archive ciphertext differs from admitted digest")
		}
		identities, err = backuplifecycle.ReadEmergencyIdentities(values["identity-file"])
		if err != nil {
			return err
		}
		extraction, cleanup, e := newApplicationTransferExtraction(authority.WorkspaceRoot, operation)
		if e != nil {
			return e
		}
		defer cleanup()
		maxBytes, _ := cmd.Flags().GetInt64("max-bytes")
		staged, err = backuplifecycle.RestoreEmergency(ctx, backuplifecycle.EmergencyRestoreInput{Archive: values["archive"], Identities: identities, Target: extraction, MaxBytes: maxBytes, MemoryFile: fmt.Sprintf("sources/%04d/application-secrets.json", *admittedTransfer.CredentialSourceIndex)})
		if err != nil {
			return err
		}
	}
	defer func() {
		for _, data := range staged.PrivateFiles {
			clear(data)
		}
	}()
	transfer, err := backuplifecycle.VerifyApplicationTransfer(staged.Manifest, values["source-owner"], values["source-fingerprint"], values["transfer-id"])
	if err != nil {
		return err
	}
	if err = verifyApplicationTransferPreparation(authority.WorkspaceRoot, transfer, values["source-fingerprint"]); err != nil {
		return err
	}
	matchedRecipient := false
	for _, identity := range identities {
		if x, ok := identity.(*age.X25519Identity); ok && x.Recipient().String() == transfer.Destination.Recipient {
			matchedRecipient = true
		}
	}
	if !matchedRecipient && !recovering {
		return errors.New("archive identity differs from exact receiving recipient")
	}
	workloads := make([]string, len(transfer.Runtimes))
	for i, r := range transfer.Runtimes {
		workloads[i] = r.WorkloadRef
	}
	plan, manifest, err := readNativeV2RestorePlanManifest(ctx, authority.WorkspaceRoot, authority.OutputRoot)
	if err != nil {
		return err
	}
	catalog, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return err
	}
	runtimeOrder, err := catalog.OrderWorkloadRemovals(plan, workloads)
	if err != nil {
		return err
	}
	slices.Reverse(runtimeOrder)
	admission := restoreactivation.TransferAdmission{Staged: staged, SourceOwner: values["source-owner"], SourceFingerprint: values["source-fingerprint"], TransferID: transfer.ID, RuntimeStartOrder: runtimeOrder, DestinationPolicy: authority.Policy}
	selected, err := restoreactivation.DeriveTransferAuthority(authority.WorkspaceRoot, plan, manifest, authority.Lineage, operation, admission)
	if err != nil {
		return err
	}

	runtime, err := restoreactivation.NewDockerRuntime(authority.WorkspaceRoot)
	if err != nil {
		return err
	}
	service, err := restoreactivation.NewService(runtime, func(_ context.Context, journal lifecyclemutation.RestoreActivationAuthority) (restoreactivation.Authority, error) {
		if e := cleanupApplicationTransferExtractions(authority.WorkspaceRoot, operation, ""); e != nil {
			return restoreactivation.Authority{}, e
		}
		if journal.OperationID != operation || journal.RestoreResultID != transfer.ID {
			return restoreactivation.Authority{}, errors.New("recovery resolver refuses a different transfer journal")
		}
		return restoreactivation.DeriveTransferAuthority(authority.WorkspaceRoot, plan, manifest, authority.Lineage, operation, admission)
	})
	if err != nil {
		return err
	}
	persistent, err := restoreactivation.ApplicationTransferPersistentWorkloads(authority.Policy, workloads)
	if err != nil {
		return err
	}
	backup, err := newNativeV2ApplicationBackupService(authority, workloads, nil)
	if err != nil {
		return err
	}
	activationInput := restoreactivation.ActivateInput{Transfer: &admission, WorkspaceRoot: authority.WorkspaceRoot, OperationID: operation, ExpectedPlanHash: values["expected-plan-hash"], OwnerApproved: true, Plan: plan, Manifest: manifest, CurrentLineage: authority.Lineage,
		PrepareMutation: func(read context.Context) (func() error, error) {
			if e := requireApplicationTransferDestinationMode(authority.WorkspaceRoot); e != nil {
				return nil, e
			}
			if _, stopped, e := readStoredApplicationTransferOutcome(authority.WorkspaceRoot, transfer, "stopped"); e != nil {
				return nil, e
			} else if stopped {
				return nil, errors.New("transfer was durably aborted before activation")
			}
			if _, stopped, e := readStoredApplicationTransferOutcome(authority.WorkspaceRoot, transfer, "stopped", true); e != nil {
				return nil, e
			} else if stopped {
				return nil, errors.New("transfer abort is pending; activation is forbidden")
			}
			if e := cleanupApplicationTransferExtractions(authority.WorkspaceRoot, operation, staged.Target); e != nil {
				return nil, e
			}
			if e := restoreactivation.StageApplicationTransfer(read, authority.WorkspaceRoot, selected, admission); e != nil {
				return nil, e
			}
			return func() error { return nil }, nil
		},
		RevalidateCurrentAuthority: func(read context.Context) (generationartifact.VerifiedPlan, backuplifecycle.AuthorityLineage, error) {
			current, e := inspectNativeV2BackupAuthorityForRequest(read, authority.WorkspaceRoot, specFile)
			if e != nil {
				return generationartifact.VerifiedPlan{}, backuplifecycle.AuthorityLineage{}, e
			}
			if !sameNativeV2BackupAuthority(authority, current) {
				return generationartifact.VerifiedPlan{}, backuplifecycle.AuthorityLineage{}, errors.New("destination authority changed")
			}
			return current.Plan, current.Lineage, nil
		},
		CreateSafetySnapshot: func(read context.Context, child string) (backuplifecycle.SnapshotAnchor, error) {
			return backup.Run(read, backuplifecycle.RunInput{OwnerRef: authority.OwnerRef, AuthorityRef: authority.AuthorityRef, Lineage: authority.Lineage, PolicyArtifact: authority.PolicyArtifact, OperationID: child, ProtectRecovery: true, Workloads: persistent})
		},
		VerifyLive: func(read context.Context) (restoreactivation.LiveVerification, error) {
			closure, e := restoreactivation.ApplicationTransferProjection(authority.WorkspaceRoot, plan, manifest, workloads, authority.Policy)
			if e != nil {
				return restoreactivation.LiveVerification{}, e
			}
			if e = verifyNativeV2SelectedApplications(read, authority, closure); e != nil {
				return restoreactivation.LiveVerification{}, e
			}
			return verifyNativeV2CurrentRuntime(read, authority, false)
		},
		FinalizeResult: func(context.Context, restoreactivation.Result, error) error { return nil },
	}
	var result restoreactivation.Result
	if recovering {
		result, err = service.Recover(ctx, restoreactivation.RecoverInput{WorkspaceRoot: authority.WorkspaceRoot, OperationID: operation, ExpectedPlanHash: values["expected-plan-hash"], OwnerApproved: true, VerifyLive: activationInput.VerifyLive, FinalizeResult: activationInput.FinalizeResult})
	} else {
		result, err = service.Activate(ctx, activationInput)
	}
	if err != nil {
		return err
	}
	if result.Status != "activated" {
		return errors.New("requested transfer was rolled back to prior target state; source remains held until signed stopped-target recovery")
	}
	if result.Verification.DataReadback == nil || len(result.Verification.DataReadback.Volumes) != len(selected.Volumes) {
		return errors.New("destination activation lacks exact application data readback")
	}
	readback, err := json.Marshal(result.Verification.DataReadback)
	if err != nil {
		return err
	}
	readbackSum := sha256.Sum256(readback)
	outcome, err := backuplifecycle.SignApplicationTransferOutcome(authority.WorkspaceRoot, backuplifecycle.ApplicationTransferOutcome{TransferID: transfer.ID, Destination: transfer.Destination, State: "activated", ActivationID: result.OperationID, ReadbackDigest: "sha256:" + hex.EncodeToString(readbackSum[:])})
	if err != nil {
		return err
	}
	if err = persistApplicationTransferOutcome(authority.WorkspaceRoot, outcome); err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(outcome)
}

func newApplicationTransferExportReceipt(workspace string, exported backuplifecycle.EmergencyExportResult, operation, mode string) (backuplifecycle.ApplicationTransferExportReceipt, error) {
	receipt, err := backuplifecycle.SignApplicationTransferExportReceipt(workspace, exported, operation, mode)
	if err != nil {
		return receipt, err
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	return receipt, writeApplicationTransferFile(filepath.Join(filepath.Dir(exported.Archive), "application-transfer-receipt.json"), encoded)
}

func readAdmittedApplicationTransferReceipt(values map[string]string, requireFresh ...bool) (backuplifecycle.ApplicationTransferExportReceipt, error) {
	raw, err := os.ReadFile(values["source-receipt"])
	if err != nil {
		return backuplifecycle.ApplicationTransferExportReceipt{}, err
	}
	var receipt backuplifecycle.ApplicationTransferExportReceipt
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return receipt, err
	}
	t, err := backuplifecycle.VerifyApplicationTransferExportReceipt(receipt, values["source-owner"], values["source-fingerprint"], values["transfer-id"], values["archive-sha256"])
	if err != nil {
		return receipt, err
	}
	if (len(requireFresh) == 0 || requireFresh[0]) && !t.Destination.ExpiresAt.After(time.Now()) {
		return receipt, errors.New("transfer destination admission expired")
	}
	return receipt, nil
}

func persistApplicationTransferOutcome(workspace string, outcome backuplifecycle.ApplicationTransferOutcome, pending ...bool) error {
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
	dir := ".stackkit/backups/transfer-outcomes"
	if err = tx.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	encoded, err := json.Marshal(outcome)
	if err != nil {
		return err
	}
	state := outcome.State
	if len(pending) > 1 {
		return errors.New("one outcome persistence purpose required")
	}
	if len(pending) == 1 && pending[0] {
		state = "pending-" + state
	}
	if err = tx.Close(); err != nil {
		return err
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600NoReplace(dir+"/"+strings.TrimPrefix(outcome.TransferID, "sha256:")+"-"+state+".json", encoded)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("transfer outcome was not durably installed")
	}
	return nil
}

func readStoredApplicationTransferOutcome(workspace string, t backuplifecycle.ApplicationTransfer, state string, pending ...bool) (backuplifecycle.ApplicationTransferOutcome, bool, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return backuplifecycle.ApplicationTransferOutcome{}, false, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return backuplifecycle.ApplicationTransferOutcome{}, false, err
	}
	defer tx.Close()
	filenameState := state
	if len(pending) > 1 {
		return backuplifecycle.ApplicationTransferOutcome{}, false, errors.New("one outcome persistence purpose required")
	}
	if len(pending) == 1 && pending[0] {
		filenameState = "pending-" + state
	}
	raw, _, err := tx.ReadStable(".stackkit/backups/transfer-outcomes/" + strings.TrimPrefix(t.ID, "sha256:") + "-" + filenameState + ".json")
	if errors.Is(err, os.ErrNotExist) {
		return backuplifecycle.ApplicationTransferOutcome{}, false, nil
	}
	if err != nil {
		return backuplifecycle.ApplicationTransferOutcome{}, false, err
	}
	var outcome backuplifecycle.ApplicationTransferOutcome
	if err = json.Unmarshal(raw, &outcome); err != nil {
		return outcome, false, err
	}
	key, err := localevidence.LoadOwnerKey(workspace)
	if err != nil {
		return outcome, false, err
	}
	err = backuplifecycle.VerifyApplicationTransferOutcome(outcome, t, localevidence.ApplicationTransferFingerprint(key.Public()))
	if err == nil && outcome.State != state {
		err = errors.New("transfer outcome phase differs from persisted state")
	}
	return outcome, err == nil, err
}

// Rebuild the receipt after archive installation or a crash after source hold.
// The signed existing journal, manifest and actual ciphertext must all agree.
func recoverApplicationTransferSourceReceipt(authority nativeV2BackupAuthority, operation, target, mode string, destination backuplifecycle.ApplicationTransferDestination, workloads []string) (backuplifecycle.ApplicationTransferExportReceipt, bool, error) {
	var zero backuplifecycle.ApplicationTransferExportReceipt
	raw, err := os.ReadFile(filepath.Join(target, backuplifecycle.EmergencyManifestName))
	if errors.Is(err, os.ErrNotExist) {
		return zero, false, nil
	}
	if err != nil {
		return zero, false, err
	}
	var exported backuplifecycle.EmergencyExportResult
	if err = json.Unmarshal(raw, &exported); err != nil {
		return zero, false, err
	}
	if exported.Manifest.ApplicationTransfer == nil {
		return zero, false, errors.New("existing export is not this application transfer")
	}
	key, err := localevidence.LoadOwnerKey(authority.WorkspaceRoot)
	if err != nil {
		return zero, false, err
	}
	t, err := backuplifecycle.VerifyApplicationTransfer(exported.Manifest, authority.OwnerRef, localevidence.ApplicationTransferFingerprint(key.Public()), exported.Manifest.ApplicationTransfer.ID)
	if err != nil {
		return zero, false, err
	}
	refs := make([]string, len(t.Runtimes))
	for i, r := range t.Runtimes {
		refs[i] = r.WorkloadRef
	}
	slices.Sort(refs)
	want := slices.Clone(workloads)
	slices.Sort(want)
	if t.Lineage != authority.Lineage || t.Mode != mode || t.Destination != destination || !slices.Equal(refs, want) {
		return zero, false, errors.New("existing transfer archive differs from requested source/destination/scope")
	}
	f, err := os.Open(filepath.Join(target, backuplifecycle.EmergencyArchive))
	if err != nil {
		return zero, false, err
	}
	sum := sha256.New()
	_, e := io.Copy(sum, f)
	err = errors.Join(e, f.Close())
	if err != nil {
		return zero, false, err
	}
	if hex.EncodeToString(sum.Sum(nil)) != exported.ArchiveSHA256 {
		return zero, false, errors.New("existing transfer ciphertext changed")
	}
	inspection, err := backuplifecycle.InspectSnapshotOperation(authority.WorkspaceRoot, operation)
	if err != nil {
		return zero, false, err
	}
	if !inspection.Found || inspection.Lineage != t.Lineage {
		return zero, false, errors.New("existing transfer lacks original authenticated operation")
	}
	if mode == "migrate" {
		held, e := backuplifecycle.ReadApplicationTransferHold(authority.WorkspaceRoot, operation, t.ID)
		if errors.Is(e, backuplifecycle.ErrApplicationTransferHoldNotRecorded) {
			// The existing snapshot operation must settle/resume first; an
			// installed archive alone never proves a source hold.
			return zero, false, nil
		}
		if e != nil {
			return zero, false, e
		}
		slices.Sort(held)
		if !slices.Equal(held, refs) {
			return zero, false, errors.New("source hold differs from existing transfer closure")
		}
	} else if inspection.State != "completed" || !inspection.SnapshotAnchorVerified {
		return zero, false, errors.New("copy source has not completed resume")
	}
	receiptPath := filepath.Join(target, "application-transfer-receipt.json")
	raw, err = os.ReadFile(receiptPath)
	if err == nil {
		var receipt backuplifecycle.ApplicationTransferExportReceipt
		if err = json.Unmarshal(raw, &receipt); err != nil {
			return zero, false, err
		}
		_, err = backuplifecycle.VerifyApplicationTransferExportReceipt(receipt, authority.OwnerRef, localevidence.ApplicationTransferFingerprint(key.Public()), t.ID, exported.ArchiveSHA256)
		return receipt, err == nil, err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return zero, false, err
	}
	exported.Archive = filepath.Join(target, backuplifecycle.EmergencyArchive)
	receipt, err := newApplicationTransferExportReceipt(authority.WorkspaceRoot, exported, operation, mode)
	return receipt, err == nil, err
}

func newApplicationTransferExtraction(workspace, operation string) (string, func(), error) {
	base := filepath.Join(workspace, ".stackkit", "backups")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(base, operation+"-private-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		root, e := confinedfs.Open(base)
		if e != nil {
			return
		}
		defer root.Close()
		tx, e := root.BeginTransaction()
		if e != nil {
			return
		}
		defer tx.Close()
		_ = tx.RemoveTree(filepath.Base(dir))
	}
	return filepath.Join(dir, "extracted"), cleanup, nil
}

// The existing lifecycle lease serializes this reconciliation. Only private
// extraction roots of this exact transfer are eligible; confinedfs rejects links.
func cleanupApplicationTransferExtractions(workspace, operation, keep string) error {
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
	entries, err := tx.WalkExcluding(".stackkit/backups", func(entry string) bool { return strings.Count(filepath.ToSlash(entry), "/") > 2 })
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	keepRoot := filepath.Dir(keep)
	for _, entry := range entries {
		if !entry.Info.IsDir() {
			continue
		}
		name := filepath.Base(entry.Path)
		if filepath.ToSlash(filepath.Dir(entry.Path)) != ".stackkit/backups" || (!strings.HasPrefix(name, operation+"-private-") && !strings.HasPrefix(name, operation+"-prepare-private-")) {
			continue
		}
		absolute := filepath.Join(workspace, filepath.FromSlash(entry.Path))
		if absolute == keepRoot {
			continue
		}
		if err = tx.RemoveTree(entry.Path); err != nil {
			return err
		}
	}
	return nil
}
