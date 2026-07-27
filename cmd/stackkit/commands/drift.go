package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

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
	Short: "Reconcile drift after the required lifecycle authority is available",
	Long: `Fail closed until the complete Owner-approved standard snapshot
transaction or the offline-verified Advanced capability is available. This
command performs no rendering, snapshot, Apply, or runtime side effect.`,
	Args: cobra.NoArgs,
	RunE: runDriftReconcile,
}

func init() {
	driftDetectCmd.Flags().BoolVar(&driftDetectJSON, "json", false, "Emit stackkit.command-result/v1 JSON")
	driftReconcileCmd.Flags().StringVar(&driftReconcileMode, "mode", "standard", "Reconcile mode: standard or advanced")
	driftReconcileCmd.Flags().BoolVar(&driftReconcileJSON, "json", false, "Emit a stackkit.command-result/v1 denial")
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
	denial := driftOperationDenial{
		SchemaVersion: operationDenialSchemaVersion,
		Operation:     "drift-reconcile",
		Mode:          mode,
	}
	switch mode {
	case "standard":
		denial.ReasonCode = "standard_reconcile_transaction_unavailable"
		denial.Message = "standard drift reconcile is denied until the Owner-approved snapshot, Apply, Verify, and rollback transaction is available"
	case "advanced":
		denial.ReasonCode = "advanced_capability_unavailable"
		denial.Message = "advanced drift reconcile is denied until an offline-verified stackkit.advanced-capability/v1 is available"
	default:
		denial.ReasonCode = "invalid_reconcile_mode"
		denial.Message = "drift reconcile mode must be standard or advanced"
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
