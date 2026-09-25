package commands

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/advancedtrust"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

// The Advanced restore drill proves that a backup anchor can be restored
// without touching live application data: it verifies the running runtime,
// creates or selects a snapshot anchor, stages a restore of that anchor,
// verifies the staged tree, removes the staging area, and re-verifies the
// live runtime. It never activates the staged data.

const (
	restoreDrillReportSchema   = "stackkit.restore-drill-report/v1"
	restoreDrillCommandName    = "advanced restore-drill run"
	restoreDrillRolloutPrefix  = "advanced.restore-drill."
	restoreDrillCleanupTimeout = 5 * time.Minute

	restoreDrillPhaseVerifyRuntime       = "verify-runtime"
	restoreDrillPhaseBackup              = "backup"
	restoreDrillPhaseSelectAnchor        = "select-anchor"
	restoreDrillPhaseStageRestore        = "stage-restore"
	restoreDrillPhaseVerifyStaged        = "verify-staged-restore"
	restoreDrillPhaseCleanupStaging      = "cleanup-staging"
	restoreDrillPhaseVerifyLiveUnchanged = "verify-live-unchanged"

	restoreDrillStatusSucceeded = "succeeded"
	restoreDrillStatusFailed    = "failed"
	restoreDrillStatusSkipped   = "skipped"
)

var restoreDrillIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{7,99}$`)

var (
	advancedRestoreDrillCapability   string
	advancedRestoreDrillAnchor       string
	advancedRestoreDrillOperationID  string
	advancedRestoreDrillOwnerApprove bool
	advancedRestoreDrillJSON         bool
)

var advancedRestoreDrillCmd = &cobra.Command{
	Use:   "restore-drill",
	Short: "Run capability-gated native restore drills",
}

var advancedRestoreDrillRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Stage and verify a restore of a backup anchor without activation",
	Long: `Run a native v2 restore drill as an Advanced operation.

The drill is admitted only with an offline-verified capability that allows
restore.drill, the local Owner-approved issuer trust, and explicit
--owner-approve. It verifies the current runtime, creates a new snapshot
anchor (or validates --anchor in local custody), stages a restore of that
anchor, verifies the staged tree, removes the staging area, and verifies that
the live runtime is unchanged. Staged data is never activated.`,
	Example: `  stackkit advanced restore-drill run --capability capability.json --owner-approve --json`,
	Args:    cobra.NoArgs,
	RunE:    runAdvancedRestoreDrill,
}

func init() {
	flags := advancedRestoreDrillRunCmd.Flags()
	flags.StringVar(&advancedRestoreDrillCapability, "capability", "",
		"Path to a canonical stackkit.advanced-capability/v1 file that allows restore.drill")
	flags.StringVar(&advancedRestoreDrillAnchor, "anchor", "",
		"Existing sha256 snapshot-anchor ID to drill; a new backup anchor is created when omitted")
	flags.StringVar(&advancedRestoreDrillOperationID, "operation-id", "",
		"Stable lowercase drill ID (8-100 characters); generated when omitted")
	flags.BoolVar(&advancedRestoreDrillOwnerApprove, "owner-approve", false,
		"Explicitly approve the drill's backup and staged restore side effects")
	flags.BoolVar(&advancedRestoreDrillJSON, "json", false,
		"Emit stackkit.command-result/v1 JSON")
	advancedRestoreDrillCmd.AddCommand(advancedRestoreDrillRunCmd)
	advancedCmd.AddCommand(advancedRestoreDrillCmd)
}

// restoreDrillReport is the stackkit.restore-drill-report/v1 subject.
type restoreDrillReport struct {
	SchemaVersion    string              `json:"schemaVersion"`
	Mode             string              `json:"mode"`
	Operation        string              `json:"operation"`
	DrillID          string              `json:"drillId"`
	Status           string              `json:"status"`
	CapabilityID     string              `json:"capabilityId"`
	AnchorID         string              `json:"anchorId"`
	AnchorSource     string              `json:"anchorSource"`
	KopiaSnapshotIDs []string            `json:"kopiaSnapshotIds"`
	RestoreResultID  string              `json:"restoreResultId"`
	StagingPath      string              `json:"stagingPath"`
	StagingRemoved   bool                `json:"stagingRemoved"`
	Activated        bool                `json:"activated"`
	Phases           []restoreDrillPhase `json:"phases"`
	Verification     []restoreDrillCheck `json:"verification"`
	StartedAt        time.Time           `json:"startedAt"`
	CompletedAt      time.Time           `json:"completedAt"`
}

type restoreDrillPhase struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMS int64     `json:"durationMs"`
	Error      string    `json:"error,omitempty"`
}

type restoreDrillCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type advancedRestoreDrillScope struct {
	StackID     string
	OwnerRef    string
	TrustSHA256 string
	Trust       advancedcapability.TrustBundle
}

type advancedRestoreDrillAdmission struct {
	grant         advancedcapability.Grant
	capabilityRaw []byte
	scope         advancedRestoreDrillScope
}

// advancedRestoreDrillStaging is one drill's staged-restore area. Verify is
// read-only; Remove deletes only the drill's staged tree.
type advancedRestoreDrillStaging interface {
	Verify(context.Context, backuplifecycle.RestoreResult) ([]restoreDrillCheck, error)
	Remove(context.Context) error
}

type advancedRestoreDrillDependencies struct {
	scope         func(workspace string) (advancedRestoreDrillScope, error)
	verifyRuntime func(context.Context, nativeV2BackupAuthority) (backuplifecycle.RestoreVerification, error)
	loadAnchor    func(workspace, anchorID string) (backuplifecycle.SnapshotAnchor, error)
	staging       func(ctx context.Context, workspace string, authority nativeV2BackupAuthority, drillID, stagingPath string) (advancedRestoreDrillStaging, error)
	now           func() time.Time
}

var advancedRestoreDrillDeps = advancedRestoreDrillDependencies{
	scope:         advancedRestoreDrillScopeFromWorkspace,
	verifyRuntime: verifyRestoreDrillRuntime,
	loadAnchor:    backuplifecycle.LoadSnapshotAnchor,
	staging:       newRestoreDrillStaging,
	now:           time.Now,
}

type restoreDrillFailedError struct{ cause error }

func (err *restoreDrillFailedError) Error() string {
	return "advanced restore drill failed: " + err.cause.Error()
}

func (err *restoreDrillFailedError) Unwrap() error { return err.cause }

func runAdvancedRestoreDrill(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	capabilityPath := strings.TrimSpace(advancedRestoreDrillCapability)
	if capabilityPath == "" {
		// Standard Mode has no capability and is denied like every other
		// Advanced operation.
		return writeRestoreDrillDenial(cmd, "", &advancedcapability.Denial{
			Code:   advancedcapability.ReasonCapabilityUnavailable,
			Field:  "capability",
			Detail: "advanced restore drill requires an offline-verifiable capability",
		})
	}
	if !advancedRestoreDrillOwnerApprove {
		return writeRestoreDrillDenialCode(cmd, "owner_approval_required",
			"advanced restore drill requires explicit --owner-approve")
	}
	anchorID := strings.TrimSpace(advancedRestoreDrillAnchor)
	if anchorID != "" && !nativeV2BackupDigestPattern.MatchString(anchorID) {
		return writeRestoreDrillDenialCode(cmd, "restore_drill_request_invalid",
			"--anchor must be a sha256 snapshot-anchor ID")
	}
	drillID, err := normalizeRestoreDrillID(advancedRestoreDrillOperationID)
	if err != nil {
		return writeRestoreDrillDenialCode(cmd, "restore_drill_request_invalid", err.Error())
	}

	workspace := getWorkDir()
	now := advancedRestoreDrillDeps.now().UTC().Truncate(time.Second)
	capabilityFile := resolvePathFromWorkDir(workspace, capabilityPath)
	// Capability, trust and Owner scope are verified before any backup,
	// staging, lock or journal side effect.
	admission, err := admitAdvancedRestoreDrill(workspace, capabilityFile, now)
	if err != nil {
		return writeRestoreDrillDenial(cmd, drillID, err)
	}

	report := restoreDrillReport{
		SchemaVersion: restoreDrillReportSchema, Mode: "advanced",
		Operation: advancedcapability.OperationRestoreDrill, DrillID: drillID,
		Status: restoreDrillStatusFailed, CapabilityID: admission.grant.CapabilityID,
		KopiaSnapshotIDs: []string{}, Phases: []restoreDrillPhase{},
		Verification: []restoreDrillCheck{}, StartedAt: time.Now().UTC(),
	}
	drillErr := executeAdvancedRestoreDrill(ctx, workspace, capabilityFile, now, admission, anchorID, &report)
	report.CompletedAt = time.Now().UTC()
	report.Activated = false
	if drillErr == nil {
		report.Status = restoreDrillStatusSucceeded
	} else {
		drillErr = &restoreDrillFailedError{cause: drillErr}
	}
	return writeRestoreDrillReport(cmd, report, drillErr)
}

func executeAdvancedRestoreDrill(
	ctx context.Context,
	workspace, capabilityFile string,
	now time.Time,
	admission advancedRestoreDrillAdmission,
	anchorID string,
	report *restoreDrillReport,
) error {
	initial, err := inspectNativeV2BackupAuthorityForRequest(ctx, workspace, specFile)
	if err != nil {
		return fmt.Errorf("restore drill authority: %w", err)
	}
	if initial.LegacyBeta4 != nil || initial.HistoricalStable != nil {
		return errors.New("restore drill requires a native v2 applied authority")
	}
	return withLifecycleMutation(workspace, restoreDrillCommandName, func() error {
		return withArchitectureV2OutputLock(workspace, initial.OutputRoot, func(*confinedfs.Transaction, *confinedfs.OutputLock) error {
			current, inspectErr := inspectNativeV2BackupAuthorityForRequest(ctx, workspace, specFile)
			if inspectErr != nil {
				return fmt.Errorf("restore drill locked authority: %w", inspectErr)
			}
			if !sameNativeV2BackupAuthority(initial, current) {
				return errors.New("restore drill authority changed while acquiring the output lock")
			}
			revalidated, admitErr := admitAdvancedRestoreDrill(workspace, capabilityFile, now)
			if admitErr != nil {
				return admitErr
			}
			if !equalAdvancedRestoreDrillAdmission(admission, revalidated) {
				return errors.New("advanced restore drill authority changed after admission")
			}
			return runRestoreDrillPhases(ctx, workspace, current, anchorID, report)
		})
	})
}

func runRestoreDrillPhases(
	ctx context.Context,
	workspace string,
	authority nativeV2BackupAuthority,
	anchorID string,
	report *restoreDrillReport,
) error {
	deps := advancedRestoreDrillDeps
	drillID := report.DrillID
	restoreOperationID := drillID + "-restore"
	stagingPath := backuplifecycle.RestoreStagingPath(restoreOperationID)
	report.StagingPath = stagingPath

	anchorPhase := restoreDrillPhaseBackup
	if anchorID != "" {
		anchorPhase = restoreDrillPhaseSelectAnchor
	}
	if err := restoreDrillRunPhase(report, restoreDrillPhaseVerifyRuntime, func() error {
		_, err := deps.verifyRuntime(ctx, authority)
		report.addCheck("live-runtime", err)
		return err
	}); err != nil {
		report.skipRemaining(anchorPhase, restoreDrillPhaseStageRestore,
			restoreDrillPhaseVerifyStaged, restoreDrillPhaseCleanupStaging, restoreDrillPhaseVerifyLiveUnchanged)
		return err
	}

	var anchor backuplifecycle.SnapshotAnchor
	if err := restoreDrillRunPhase(report, anchorPhase, func() error {
		var err error
		anchor, err = restoreDrillAnchor(ctx, workspace, authority, anchorID, drillID)
		return err
	}); err != nil {
		report.skipRemaining(restoreDrillPhaseStageRestore, restoreDrillPhaseVerifyStaged, restoreDrillPhaseCleanupStaging)
		return errors.Join(err, restoreDrillVerifyLive(ctx, workspace, authority, report))
	}
	report.AnchorID = anchor.ID
	report.AnchorSource = map[bool]string{true: "selected", false: "created"}[anchorID != ""]
	report.addSnapshotID(anchor.Snapshot.SnapshotID)

	var (
		staging  advancedRestoreDrillStaging
		restored backuplifecycle.RestoreResult
	)
	stageErr := restoreDrillRunPhase(report, restoreDrillPhaseStageRestore, func() error {
		var err error
		staging, err = deps.staging(ctx, workspace, authority, drillID, stagingPath)
		if err != nil {
			return fmt.Errorf("prepare restore drill staging: %w", err)
		}
		raw, err := continueNativeV2Backup(ctx, nativeV2BackupRestore, authority, nativeV2BackupRequest{
			OperationID: restoreOperationID, SnapshotAnchorID: anchor.ID, OwnerApproved: true,
		})
		if err != nil {
			return err
		}
		result, ok := raw.(backuplifecycle.RestoreResult)
		if !ok {
			return errors.New("native restore runtime returned no restore result")
		}
		if result.SnapshotAnchorID != anchor.ID || result.Receipt.StagingPath != stagingPath {
			return errors.New("staged restore result differs from the drill anchor or staging area")
		}
		restored = result
		report.RestoreResultID = result.ID
		report.addSnapshotID(result.Receipt.SnapshotID)
		return nil
	})

	var verifyErr error
	if stageErr == nil {
		verifyErr = restoreDrillRunPhase(report, restoreDrillPhaseVerifyStaged, func() error {
			checks, err := staging.Verify(ctx, restored)
			report.Verification = append(report.Verification, checks...)
			return err
		})
	} else {
		report.skipRemaining(restoreDrillPhaseVerifyStaged)
	}

	var cleanupErr error
	if staging != nil {
		cleanupErr = restoreDrillRunPhase(report, restoreDrillPhaseCleanupStaging, func() error {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreDrillCleanupTimeout)
			defer cancel()
			if err := staging.Remove(cleanupCtx); err != nil {
				return err
			}
			report.StagingRemoved = true
			return nil
		})
	} else {
		report.skipRemaining(restoreDrillPhaseCleanupStaging)
	}
	return errors.Join(stageErr, verifyErr, cleanupErr, restoreDrillVerifyLive(ctx, workspace, authority, report))
}

func restoreDrillAnchor(
	ctx context.Context,
	workspace string,
	authority nativeV2BackupAuthority,
	anchorID, drillID string,
) (backuplifecycle.SnapshotAnchor, error) {
	if anchorID != "" {
		anchor, err := advancedRestoreDrillDeps.loadAnchor(workspace, anchorID)
		if err != nil {
			return backuplifecycle.SnapshotAnchor{}, fmt.Errorf("load snapshot anchor from local custody: %w", err)
		}
		if anchor.ID != anchorID || anchor.OwnerRef != authority.OwnerRef {
			return backuplifecycle.SnapshotAnchor{}, errors.New("snapshot anchor is not bound to the current local Owner")
		}
		return anchor, nil
	}
	raw, err := continueNativeV2Backup(ctx, nativeV2BackupRun, authority, nativeV2BackupRequest{
		OperationID: drillID + "-backup",
	})
	if err != nil {
		return backuplifecycle.SnapshotAnchor{}, err
	}
	anchor, ok := raw.(backuplifecycle.SnapshotAnchor)
	if !ok || !nativeV2BackupDigestPattern.MatchString(anchor.ID) {
		return backuplifecycle.SnapshotAnchor{}, errors.New("native backup runtime returned no snapshot anchor")
	}
	return anchor, nil
}

// restoreDrillVerifyLive proves that the drill left the live authority and
// runtime exactly as it found them.
func restoreDrillVerifyLive(
	ctx context.Context,
	workspace string,
	authority nativeV2BackupAuthority,
	report *restoreDrillReport,
) error {
	return restoreDrillRunPhase(report, restoreDrillPhaseVerifyLiveUnchanged, func() error {
		current, err := inspectNativeV2BackupAuthorityForRequest(ctx, workspace, specFile)
		if err == nil && !sameNativeV2BackupAuthority(authority, current) {
			err = errors.New("live applied authority changed during the restore drill")
		}
		if err == nil {
			_, err = advancedRestoreDrillDeps.verifyRuntime(ctx, current)
		}
		report.addCheck("live-runtime-unchanged", err)
		return err
	})
}

func restoreDrillRunPhase(report *restoreDrillReport, name string, execute func() error) error {
	phase := restoreDrillRolloutPrefix + name
	attributes := map[string]string{"drillId": report.DrillID}
	rolloutEvent(phase, "started", "restore drill phase started", attributes)
	started := time.Now().UTC()
	err := execute()
	record := restoreDrillPhase{
		Name: name, Status: restoreDrillStatusSucceeded, StartedAt: started,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if err != nil {
		record.Status = restoreDrillStatusFailed
		record.Error = err.Error()
		rolloutFailure(phase, err)
	} else {
		rolloutEvent(phase, "succeeded", "restore drill phase succeeded", attributes)
	}
	report.Phases = append(report.Phases, record)
	return err
}

func (report *restoreDrillReport) skipRemaining(names ...string) {
	for _, name := range names {
		report.Phases = append(report.Phases, restoreDrillPhase{
			Name: name, Status: restoreDrillStatusSkipped, StartedAt: time.Now().UTC(),
		})
		rolloutEvent(restoreDrillRolloutPrefix+name, "skipped", "restore drill phase skipped",
			map[string]string{"drillId": report.DrillID})
	}
}

func (report *restoreDrillReport) addCheck(name string, err error) {
	check := restoreDrillCheck{Name: name, Status: "passed"}
	if err != nil {
		check.Status = "failed"
		check.Detail = err.Error()
	}
	report.Verification = append(report.Verification, check)
}

func (report *restoreDrillReport) addSnapshotID(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	for _, existing := range report.KopiaSnapshotIDs {
		if existing == id {
			return
		}
	}
	report.KopiaSnapshotIDs = append(report.KopiaSnapshotIDs, id)
}

func normalizeRestoreDrillID(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		random := make([]byte, 8)
		if _, err := rand.Read(random); err != nil {
			return "", fmt.Errorf("generate restore drill ID: %w", err)
		}
		requested = "drill-" + strings.ToLower(time.Now().UTC().Format("20060102t150405z")) + "-" + hex.EncodeToString(random)
	}
	if !restoreDrillIDPattern.MatchString(requested) {
		return "", errors.New("--operation-id must be 8-100 lowercase portable characters")
	}
	return requested, nil
}

func admitAdvancedRestoreDrill(
	workspace, capabilityPath string,
	now time.Time,
) (advancedRestoreDrillAdmission, error) {
	capabilityRaw, err := readAdvancedRegular(capabilityPath, maxAdvancedTrustBundleBytes, "Advanced capability")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return advancedRestoreDrillAdmission{}, &advancedcapability.Denial{
				Code: advancedcapability.ReasonCapabilityRequired, Field: "capability", Detail: "file is required",
			}
		}
		return advancedRestoreDrillAdmission{}, err
	}
	scope, err := advancedRestoreDrillDeps.scope(workspace)
	if err != nil {
		return advancedRestoreDrillAdmission{}, err
	}
	trust := scope.Trust
	grant, err := advancedcapability.Verify(capabilityRaw, advancedcapability.Request{
		Now: now, TrustBundle: &trust,
		StackID: scope.StackID, OwnerRef: scope.OwnerRef,
		Operation: advancedcapability.OperationRestoreDrill,
	})
	if err != nil {
		return advancedRestoreDrillAdmission{}, err
	}
	return advancedRestoreDrillAdmission{
		grant: grant, capabilityRaw: bytes.Clone(capabilityRaw), scope: scope,
	}, nil
}

func equalAdvancedRestoreDrillAdmission(left, right advancedRestoreDrillAdmission) bool {
	return left.grant.CapabilityID == right.grant.CapabilityID &&
		left.grant.KeyID == right.grant.KeyID &&
		left.scope.StackID == right.scope.StackID &&
		left.scope.OwnerRef == right.scope.OwnerRef &&
		left.scope.TrustSHA256 == right.scope.TrustSHA256 &&
		bytes.Equal(left.capabilityRaw, right.capabilityRaw)
}

// advancedRestoreDrillScopeFromWorkspace derives the exact local scope a
// restore.drill capability must match: verified Owner custody, the
// Owner-approved issuer trust, and the stackId of the current v2 baseline.
func advancedRestoreDrillScopeFromWorkspace(workspace string) (advancedRestoreDrillScope, error) {
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return advancedRestoreDrillScope{}, &advancedcapability.Denial{
			Code: advancedcapability.ReasonCapabilityScopeMismatch, Field: "ownerRef", Detail: "verified local Owner custody is required",
		}
	}
	trust, err := advancedtrust.Load(workspace)
	if err != nil {
		return advancedRestoreDrillScope{}, &advancedcapability.Denial{
			Code: advancedcapability.ReasonTrustBundleUnavailable, Field: "trustBundle", Detail: "verified Owner-approved local trust is required",
		}
	}
	baselineRaw, sourceVersion, handled, err := classifyArchitectureV2ExecutionSpec(workspace, specFile)
	if err != nil {
		return advancedRestoreDrillScope{}, err
	}
	if !handled || !sourceVersion.IsV2() {
		return advancedRestoreDrillScope{}, errors.New("advanced restore drill requires a canonical Architecture v2 StackSpec")
	}
	inventory, err := readArchitectureV2Inventory(workspace, "")
	if err != nil {
		return advancedRestoreDrillScope{}, err
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return advancedRestoreDrillScope{}, err
	}
	current, err := service.ResolveCurrent(architecturev2.ResolveInput{StackSpec: baselineRaw, Inventory: inventory})
	if err != nil {
		return advancedRestoreDrillScope{}, err
	}
	baseline, err := current.Result()
	if err != nil {
		return advancedRestoreDrillScope{}, err
	}
	stackID, _, _, err := advancedPlanIdentity(baseline)
	if err != nil {
		return advancedRestoreDrillScope{}, err
	}
	return advancedRestoreDrillScope{
		StackID: stackID, OwnerRef: owner.OwnerRef,
		TrustSHA256: trust.BundleSHA256, Trust: trust.TrustBundle(),
	}, nil
}

// verifyRestoreDrillRuntime reuses the native v2 verify path read-only.
func verifyRestoreDrillRuntime(
	ctx context.Context,
	authority nativeV2BackupAuthority,
) (backuplifecycle.RestoreVerification, error) {
	return verifyNativeV2CurrentRuntime(ctx, authority, false)
}

func writeRestoreDrillDenial(cmd *cobra.Command, drillID string, err error) error {
	reason, ok := advancedcapability.Reason(err)
	if !ok {
		now := time.Now().UTC()
		return writeRestoreDrillReport(cmd, restoreDrillReport{
			SchemaVersion: restoreDrillReportSchema, Mode: "advanced",
			Operation: advancedcapability.OperationRestoreDrill, DrillID: drillID,
			Status: restoreDrillStatusFailed, StartedAt: now, CompletedAt: now,
			KopiaSnapshotIDs: []string{}, Phases: []restoreDrillPhase{}, Verification: []restoreDrillCheck{},
		}, err)
	}
	denial := driftOperationDenial{
		SchemaVersion: operationDenialSchemaVersion,
		Operation:     advancedcapability.OperationRestoreDrill, Mode: "advanced",
		ReasonCode: string(reason), Message: err.Error(),
	}
	return emitRestoreDrillDenial(cmd, denial, err)
}

func writeRestoreDrillDenialCode(cmd *cobra.Command, code, message string) error {
	denial := driftOperationDenial{
		SchemaVersion: operationDenialSchemaVersion,
		Operation:     advancedcapability.OperationRestoreDrill, Mode: "advanced",
		ReasonCode: code, Message: message,
	}
	return emitRestoreDrillDenial(cmd, denial, &driftReconcileDeniedError{denial: denial})
}

func emitRestoreDrillDenial(cmd *cobra.Command, denial driftOperationDenial, cause error) error {
	if advancedRestoreDrillJSON {
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), "denied", denial); err != nil {
			return errors.Join(cause, err)
		}
	} else {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Denied: %s\n", denial.Message)
	}
	return cause
}

func writeRestoreDrillReport(cmd *cobra.Command, report restoreDrillReport, drillErr error) error {
	if advancedRestoreDrillJSON {
		status := "success"
		if drillErr != nil {
			status = "failed"
		}
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), status, report); err != nil {
			return errors.Join(drillErr, err)
		}
		return drillErr
	}
	if drillErr != nil {
		return drillErr
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(),
		"Restore drill %s %s\nAnchor: %s (%s)\nStaged restore: %s (removed: %t, activated: false)\n",
		report.DrillID, report.Status, report.AnchorID, report.AnchorSource,
		report.RestoreResultID, report.StagingRemoved,
	)
	return err
}
