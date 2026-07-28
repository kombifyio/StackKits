package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"github.com/spf13/cobra"
)

const (
	driftReportSchemaVersion     = "stackkit.drift-report/v1"
	operationDenialSchemaVersion = "stackkit.operation-denial/v1"
)

var (
	driftDetectJSON            bool
	driftReconcileMode         string
	driftReconcileJSON         bool
	driftReconcileOwnerApprove bool
	driftAdvancedCapability    string
	driftAdvancedCandidate     string
	driftAdvancedChangeSet     string
	driftAdvancedChangeSetSHA  string
	observeArchitectureV2Drift = observeCurrentArchitectureV2Drift
)

type driftReport struct {
	SchemaVersion    string         `json:"schemaVersion"`
	Mode             string         `json:"mode"`
	GenerationTarget string         `json:"generationTarget"`
	HasDrift         bool           `json:"hasDrift"`
	PlanHash         string         `json:"planHash"`
	OwnerRef         string         `json:"ownerRef"`
	OwnerBindingHash string         `json:"ownerBindingHash"`
	Runtime          driftRuntime   `json:"runtime"`
	Subjects         []driftSubject `json:"subjects"`
}

type driftRuntime struct {
	ProjectRef   string `json:"projectRef"`
	Status       string `json:"status"`
	ServiceCount int    `json:"serviceCount"`
	ProbeCount   int    `json:"probeCount"`
}

type driftSubject struct {
	Subject string `json:"subject"`
	Status  string `json:"status"`
	Code    string `json:"code,omitempty"`
}

type architectureV2DriftObservation struct {
	Verification architectureV2VerifyReport
	Differences  []architectureV2DriftDifference
}

type architectureV2DriftDifference struct {
	Subject    string
	Code       string
	ProjectRef string
}

type architectureV2DriftCarrier interface {
	error
	DriftSubject() string
	DriftCode() string
	DriftProjectRef() string
}

type architectureV2DriftDetectedError struct {
	Verification architectureV2VerifyReport
	Differences  []architectureV2DriftDifference
}

func (err *architectureV2DriftDetectedError) Error() string {
	return "verified Architecture v2 state differs from the desired state"
}

type driftOperationDenial struct {
	SchemaVersion string `json:"schemaVersion"`
	Operation     string `json:"operation"`
	Mode          string `json:"mode"`
	ReasonCode    string `json:"reasonCode"`
	Message       string `json:"message"`
}

type driftReconcileDeniedError struct {
	denial driftOperationDenial
}

func (err *driftReconcileDeniedError) Error() string {
	return err.denial.Message
}

type standardDriftReconcileResult struct {
	SchemaVersion string                    `json:"schemaVersion"`
	Mode          string                    `json:"mode"`
	Operation     string                    `json:"operation"`
	Changed       bool                      `json:"changed"`
	Before        driftReport               `json:"before"`
	Checkpoint    *publicUpgradeCheckpoint  `json:"checkpoint,omitempty"`
	Transaction   *publicUpgradeTransaction `json:"transaction,omitempty"`
}

var driftCmd = &cobra.Command{
	Use:   "drift",
	Short: "Observe and reconcile local StackKit drift",
	Annotations: map[string]string{
		noDeployObservabilityAnnotation: "true",
	},
}

var driftDetectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Read the authoritative local state and report drift",
	Long: `Read and verify the canonical ResolvedPlan, generated artifacts,
owner-signed Apply evidence, local Owner binding, and live Basement Compose
runtime without refreshing state or writing lifecycle data.`,
	Args: cobra.NoArgs,
	RunE: runDriftDetect,
}

var driftReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Reconcile drift through the governed local lifecycle",
	Long: `Standard mode requires explicit local Owner approval, verifies the
current local authority, creates a mandatory Kopia and executor-state rollback
checkpoint, then runs generate, apply, and verify through the exclusive
lifecycle journal. A failed target phase automatically restores, reapplies, and
verifies the captured prior executor state. Advanced mode remains fail-closed
until an offline-verified capability is supplied.`,
	Args: cobra.NoArgs,
	RunE: runDriftReconcile,
}

func init() {
	driftDetectCmd.Flags().BoolVar(&driftDetectJSON, "json", false, "Emit stackkit.command-result/v1 JSON")
	driftReconcileCmd.Flags().StringVar(&driftReconcileMode, "mode", "standard", "Reconcile mode: standard or advanced")
	driftReconcileCmd.Flags().BoolVar(&driftReconcileOwnerApprove, "owner-approve", false, "Record explicit local Owner approval for standard reconciliation")
	driftReconcileCmd.Flags().BoolVar(&driftReconcileJSON, "json", false, "Emit stackkit.command-result/v1 JSON")
	driftReconcileCmd.Flags().StringVar(&driftAdvancedCapability, "capability", "", "Advanced: canonical offline capability file")
	driftReconcileCmd.Flags().StringVar(&driftAdvancedCandidate, "candidate-spec", "", "Advanced: exact Terramate candidate StackSpec")
	driftReconcileCmd.Flags().StringVar(&driftAdvancedChangeSet, "change-set", "", "Advanced: exact Owner-signed change-set ID")
	driftReconcileCmd.Flags().StringVar(&driftAdvancedChangeSetSHA, "expect-sha256", "", "Advanced: exact stored change-set byte digest")
	driftCmd.AddCommand(driftDetectCmd)
	driftCmd.AddCommand(driftReconcileCmd)
	rootCmd.AddCommand(driftCmd)
}

func runDriftDetect(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	observation, err := observeArchitectureV2Drift(ctx, getWorkDir(), specFile)
	if err != nil {
		return fmt.Errorf("detect Architecture v2 drift: %w", err)
	}
	report, err := newDriftReport(observation)
	if err != nil {
		return err
	}
	if driftDetectJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), report)
	}
	return printDriftReport(cmd.OutOrStdout(), report)
}

func observeCurrentArchitectureV2Drift(
	ctx context.Context,
	workspaceRoot string,
	specPath string,
) (architectureV2DriftObservation, error) {
	options := architectureV2ExecutionCLIOptions{context: ctx, driftObservation: true}
	var report architectureV2VerifyReport
	options.verifySink = func(observation architectureV2VerifyReport) error {
		report = observation
		return nil
	}
	handled, err := newArchitectureV2ExecutionGate().preflight(
		workspaceRoot,
		specPath,
		architectureV2Verify,
		options,
	)
	if err != nil {
		var detected *architectureV2DriftDetectedError
		if errors.As(err, &detected) {
			return architectureV2DriftObservation{
				Verification: detected.Verification,
				Differences:  append([]architectureV2DriftDifference(nil), detected.Differences...),
			}, nil
		}
		return architectureV2DriftObservation{}, err
	}
	if !handled {
		return architectureV2DriftObservation{}, errors.New("drift detection requires a canonical Architecture v2 StackSpec")
	}
	return architectureV2DriftObservation{Verification: report}, nil
}

func newDriftReport(observed architectureV2DriftObservation) (driftReport, error) {
	observation := observed.Verification
	if strings.TrimSpace(observation.PlanHash) == "" ||
		strings.TrimSpace(observation.Apply.ResultHash) == "" ||
		strings.TrimSpace(observation.Apply.EvidenceBundleHash) == "" {
		return driftReport{}, errors.New("drift observation lacks verified Plan or Apply evidence")
	}
	if strings.TrimSpace(observation.Owner.OwnerRef) == "" ||
		strings.TrimSpace(observation.Owner.OwnerBindingDigest) == "" {
		return driftReport{}, errors.New("drift observation lacks the verified local Owner binding")
	}
	if len(observed.Differences) == 0 && (observation.Runtime == nil ||
		observation.Runtime.Status != "ready" ||
		strings.TrimSpace(observation.Runtime.ProjectRef) == "" ||
		observation.Runtime.ServiceCount <= 0 ||
		observation.Runtime.ProbeCount <= 0) {
		return driftReport{}, errors.New("drift observation does not prove a ready local runtime")
	}
	report := driftReport{
		SchemaVersion:    driftReportSchemaVersion,
		Mode:             "standard",
		GenerationTarget: "compose",
		HasDrift:         len(observed.Differences) > 0,
		PlanHash:         observation.PlanHash,
		OwnerRef:         observation.Owner.OwnerRef,
		OwnerBindingHash: observation.Owner.OwnerBindingDigest,
		Subjects: []driftSubject{
			{Subject: "resolved-plan", Status: "in-sync"},
			{Subject: "generated-artifacts", Status: "in-sync"},
			{Subject: "apply-evidence", Status: "in-sync"},
			{Subject: "owner-binding", Status: "in-sync"},
			{Subject: "runtime-configuration", Status: "in-sync"},
		},
	}
	if observation.Runtime != nil {
		report.Runtime = driftRuntime{
			ProjectRef: observation.Runtime.ProjectRef, Status: observation.Runtime.Status,
			ServiceCount: observation.Runtime.ServiceCount, ProbeCount: observation.Runtime.ProbeCount,
		}
	}
	bySubject := make(map[string]int, len(report.Subjects))
	for index, subject := range report.Subjects {
		bySubject[subject.Subject] = index
	}
	for _, difference := range observed.Differences {
		index, exists := bySubject[difference.Subject]
		if !exists || strings.TrimSpace(difference.Code) == "" {
			return driftReport{}, errors.New("drift observation contains an unknown or untyped difference")
		}
		report.Subjects[index].Status = "drifted"
		report.Subjects[index].Code = difference.Code
		if report.Runtime.ProjectRef == "" && difference.ProjectRef != "" {
			report.Runtime.ProjectRef = difference.ProjectRef
		}
	}
	if report.HasDrift && report.Runtime.Status == "" {
		report.Runtime.Status = "drifted"
	}
	return report, nil
}

func printDriftReport(output io.Writer, report driftReport) error {
	_, err := fmt.Fprintf(
		output,
		"Drift: %s\nPlan: %s\nRuntime: %s (%d services, %d probes)\n",
		map[bool]string{false: "none", true: "detected"}[report.HasDrift],
		report.PlanHash,
		report.Runtime.ProjectRef,
		report.Runtime.ServiceCount,
		report.Runtime.ProbeCount,
	)
	return err
}

func runDriftReconcile(cmd *cobra.Command, _ []string) error {
	if err := admitLifecycleMutationBeforeObservability(
		getWorkDir(), "drift-reconcile", true,
	); err != nil {
		return err
	}
	mode := strings.ToLower(strings.TrimSpace(driftReconcileMode))
	if mode == "standard" {
		return runStandardDriftReconcile(cmd)
	}
	if mode == "advanced" {
		capabilityPath := strings.TrimSpace(driftAdvancedCapability)
		if capabilityPath == "" {
			denial := driftOperationDenial{
				SchemaVersion: operationDenialSchemaVersion,
				Operation:     "drift-reconcile",
				Mode:          "advanced",
				ReasonCode:    string(advancedcapability.ReasonCapabilityUnavailable),
				Message:       "advanced drift reconciliation requires an offline-verifiable capability",
			}
			if driftReconcileJSON {
				if err := writeCommandResultStatus(cmd, cmd.CommandPath(), "denied", denial); err != nil {
					return err
				}
			} else {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Denied: %s\n", denial.Message)
			}
			return &driftReconcileDeniedError{denial: denial}
		}
		result, err := runAdvancedMutation(cmd, advancedMutationRequest{
			CapabilityPath: capabilityPath,
			CandidatePath:  strings.TrimSpace(driftAdvancedCandidate),
			ChangeSetID:    strings.TrimSpace(driftAdvancedChangeSet),
			ChangeSetSHA:   strings.TrimSpace(driftAdvancedChangeSetSHA),
			Operation:      advancedcapability.OperationDriftReconcileAdvanced,
		})
		if driftReconcileJSON {
			status := "success"
			var data any = result
			if err != nil {
				status = "failed"
				if reason, ok := advancedcapability.Reason(err); ok {
					status = "denied"
					data = driftOperationDenial{
						SchemaVersion: operationDenialSchemaVersion,
						Operation:     "drift-reconcile",
						Mode:          "advanced",
						ReasonCode:    string(reason),
						Message:       err.Error(),
					}
				}
			}
			if writeErr := writeCommandResultStatus(cmd, cmd.CommandPath(), status, data); writeErr != nil {
				return errors.Join(err, writeErr)
			}
		}
		return err
	}
	denial := driftOperationDenial{
		SchemaVersion: operationDenialSchemaVersion,
		Operation:     "drift-reconcile",
		Mode:          mode,
	}
	denial.ReasonCode = "invalid_reconcile_mode"
	denial.Message = "drift reconcile mode must be standard or advanced"
	if driftReconcileJSON {
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), "denied", denial); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Denied: %s\n", denial.Message)
	}
	return &driftReconcileDeniedError{denial: denial}
}

func runStandardDriftReconcile(cmd *cobra.Command) error {
	result := standardDriftReconcileResult{
		SchemaVersion: "stackkit.standard-drift-reconcile/v1",
		Mode:          "standard",
		Operation:     "drift-reconcile",
	}
	if !driftReconcileOwnerApprove {
		return denyStandardDriftReconcile(
			cmd,
			"owner_approval_required",
			"standard drift reconcile requires explicit --owner-approve",
		)
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	workspace := getWorkDir()
	observation, err := observeArchitectureV2Drift(ctx, workspace, specFile)
	if err != nil {
		return failStandardDriftReconcile(cmd, result, fmt.Errorf("verify current drift authority: %w", err))
	}
	result.Before, err = newDriftReport(observation)
	if err != nil {
		return failStandardDriftReconcile(cmd, result, err)
	}
	if !result.Before.HasDrift {
		if driftReconcileJSON {
			return writeCommandResult(cmd, cmd.CommandPath(), result)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No drift detected; no lifecycle mutation was required.")
		return err
	}

	kit, err := loadWorkspaceKit(workspace)
	if err != nil {
		return failStandardDriftReconcile(cmd, result, fmt.Errorf("load StackKit identity: %w", err))
	}
	receipts, err := verifyWorkspaceReleaseReceipts(cmd, workspace)
	if err != nil {
		return failStandardDriftReconcile(cmd, result, err)
	}
	receipt, err := currentDriftReconcileReceipt(receipts, kit)
	if err != nil {
		return failStandardDriftReconcile(cmd, result, err)
	}
	current, err := newCurrentUpgradeInspection(ctx, workspace, specFile)
	if err != nil {
		return failStandardDriftReconcile(cmd, result, fmt.Errorf("inspect current generation: %w", err))
	}
	inspection := upgradelifecycle.Inspection{
		SchemaVersion: upgradelifecycle.InspectionSchemaVersion,
		Target: upgradelifecycle.Target{
			Kit: receipt.Kit, Version: receipt.Version, Channel: receipt.Channel,
			Platform: receipt.Platform, ArchiveSHA256: receipt.ArchiveSHA256,
		},
		Plan: upgradelifecycle.PlanDiff{
			Changed:         false,
			CurrentPlanHash: current.Binding.PlanHash, TargetPlanHash: current.Binding.PlanHash,
			CurrentManifestHash: current.Manifest.Hash, TargetManifestHash: current.Manifest.Hash,
		},
		Execution: upgradelifecycle.InspectionExecution{
			Mode: "standard-reconcile", GenerateInvoked: false,
			ApplyInvoked: false, SnapshotCreated: false,
		},
	}
	resolution := releaseindex.Resolution{Asset: releaseindex.Asset{
		Kit: receipt.Kit, Version: receipt.Version, Channel: receipt.Channel,
		Platform: receipt.Platform,
		Archive:  releaseindex.Blob{SHA256: receipt.ArchiveSHA256},
	}}

	var checkpoint publicUpgradeCheckpoint
	mutation, err := beginPublicUpgradeMutation(
		workspace,
		func() (lifecyclemutation.BeginRequest, error) {
			prepared, prepareErr := preparePublicUpgradeCheckpoint(ctx, workspace, kit, resolution)
			if prepareErr != nil {
				return lifecyclemutation.BeginRequest{}, fmt.Errorf(
					"create mandatory drift-reconcile rollback checkpoint: %w", prepareErr,
				)
			}
			if prepareErr := prepared.validate(); prepareErr != nil {
				return lifecyclemutation.BeginRequest{}, prepareErr
			}
			snapshot, loadErr := loadPublicUpgradeRecoveryCheckpoint(
				workspace, prepared.ExecutorStateSnapshotID,
			)
			if loadErr != nil {
				return lifecyclemutation.BeginRequest{}, loadErr
			}
			var executableDigest string
			if executableErr := withPublicUpgradeInstalledExecutable(
				ctx, receipt, func(path string) error {
					var digestErr error
					executableDigest, digestErr = executableFileSHA256(path)
					return digestErr
				},
			); executableErr != nil {
				return lifecyclemutation.BeginRequest{}, executableErr
			}
			checkpoint = prepared
			return lifecyclemutation.BeginRequest{
				OperationID: prepared.OperationID,
				OwnerRef:    snapshot.OwnerRef,
				Checkpoint: lifecyclemutation.CheckpointAuthority{
					ExecutorStateSnapshotID: prepared.ExecutorStateSnapshotID,
					KopiaAnchorID:           prepared.KopiaAnchorID,
				},
				Target: lifecyclemutation.ReleaseAuthority{
					Version:          architectureV2ComponentVersion(receipt.Version),
					ArchiveSHA256:    "sha256:" + receipt.ArchiveSHA256,
					ExecutableSHA256: executableDigest,
				},
				Prior: lifecyclemutation.ReleaseAuthority{
					Version:          architectureV2ComponentVersion(snapshot.Release.Version),
					ArchiveSHA256:    snapshot.Release.ArchiveSHA256,
					ExecutableSHA256: snapshot.Executable.Blob.SHA256,
				},
			}, nil
		},
	)
	if err != nil {
		return failStandardDriftReconcile(cmd, result, fmt.Errorf("begin standard reconcile lifecycle: %w", err))
	}
	defer mutation.Close()
	result.Checkpoint = &checkpoint
	transaction, transactionErr := executePublicUpgradeTransaction(
		ctx, workspace, specFile, receipt, inspection, checkpoint, mutation,
	)
	result.Transaction = &transaction
	result.Changed = transaction.Target.ApplyInvoked
	if transactionErr != nil {
		return failStandardDriftReconcile(cmd, result, transactionErr)
	}
	if driftReconcileJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	_, err = fmt.Fprintf(
		cmd.OutOrStdout(),
		"Standard drift reconcile completed and verified.\nRollback checkpoint: %s (Kopia %s)\n",
		checkpoint.ExecutorStateSnapshotID, checkpoint.KopiaAnchorID,
	)
	return err
}

func currentDriftReconcileReceipt(
	receipts []releaseindex.Receipt,
	kit string,
) (releaseindex.Receipt, error) {
	exactVersion, err := releaseindex.ExactTagForBuildVersion(version)
	if err != nil {
		return releaseindex.Receipt{}, fmt.Errorf("bind standard reconcile to exact current release: %w", err)
	}
	platform := currentReleasePlatform()
	var matches []releaseindex.Receipt
	for _, receipt := range receipts {
		if receipt.Kit == kit && receipt.Version == exactVersion && receipt.Platform == platform {
			matches = append(matches, receipt)
		}
	}
	if len(matches) != 1 || strings.TrimSpace(matches[0].InstallDir) == "" {
		return releaseindex.Receipt{}, errors.New(
			"standard reconcile requires exactly one verified installed receipt for the running StackKit release",
		)
	}
	return matches[0], nil
}

func denyStandardDriftReconcile(cmd *cobra.Command, code, message string) error {
	denial := driftOperationDenial{
		SchemaVersion: operationDenialSchemaVersion,
		Operation:     "drift-reconcile", Mode: "standard",
		ReasonCode: code, Message: message,
	}
	if driftReconcileJSON {
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), "denied", denial); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Denied: %s\n", message)
	}
	return &driftReconcileDeniedError{denial: denial}
}

func failStandardDriftReconcile(
	cmd *cobra.Command,
	result standardDriftReconcileResult,
	cause error,
) error {
	if driftReconcileJSON {
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), "failed", result); err != nil {
			return errors.Join(cause, err)
		}
	}
	return cause
}

func writeCommandResultStatus(
	cmd *cobra.Command,
	commandName string,
	status string,
	data any,
) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(commandResult{
		SchemaVersion: commandResultSchemaVersion,
		Command:       commandName,
		Status:        status,
		Data:          data,
	})
}
