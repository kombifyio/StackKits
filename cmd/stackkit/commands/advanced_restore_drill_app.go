package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const (
	restoreDrillAppReceiptSchema     = "stackkit.application-recovery-receipt/v1"
	restoreDrillPhaseRecoverIsolated = "recover-isolated-app"
)

var restoreDrillWorkloadPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

type applicationRecoveryReceipt struct {
	SchemaVersion      string                          `json:"schemaVersion"`
	DrillID            string                          `json:"drillId"`
	Status             string                          `json:"status"`
	FailureCode        string                          `json:"failureCode,omitempty"`
	Error              string                          `json:"error,omitempty"`
	ReceiptPath        string                          `json:"receiptPath"`
	WorkloadRef        string                          `json:"workloadRef"`
	ModuleRef          string                          `json:"moduleRef"`
	AppRelease         string                          `json:"appRelease"`
	PostgresImage      string                          `json:"postgresImage"`
	DependencyVersions map[string]string               `json:"dependencyVersions"`
	Scope              applicationRecoveryScope        `json:"scope"`
	Versions           applicationRecoveryVersions     `json:"versions"`
	EvidenceAge        applicationRecoveryEvidenceAge  `json:"evidenceAge"`
	Isolation          applicationRecoveryIsolation    `json:"isolation"`
	Readiness          applicationRecoveryReadiness    `json:"readiness"`
	DataReadback       applicationRecoveryDataReadback `json:"dataReadback"`
	Unknowns           []string                        `json:"unknowns"`
}

type applicationRecoveryScope struct {
	SiteRef              string   `json:"siteRef"`
	NodeRef              string   `json:"nodeRef"`
	LinuxProfile         string   `json:"linuxProfile"`
	LiveProject          string   `json:"liveProject"`
	CloneProject         string   `json:"cloneProject"`
	BackedUpVolumes      []string `json:"backedUpVolumes"`
	StagedVolumes        []string `json:"stagedVolumes"`
	RestoredVolumes      []string `json:"restoredVolumes"`
	VerifiedDependencies []string `json:"verifiedDependencies"`
	ConfigFilesVerified  []string `json:"configFilesVerified"`
	SecretSlotsVerified  []string `json:"secretSlotsVerified"`
}

type applicationRecoveryVersions struct {
	StackKitVersion      string `json:"stackkitVersion"`
	PlanHash             string `json:"planHash"`
	ManifestHash         string `json:"manifestHash"`
	PolicyArtifactDigest string `json:"policyArtifactDigest"`
	KopiaHelperImage     string `json:"kopiaHelperImage"`
}

type applicationRecoveryEvidenceAge struct {
	SnapshotAnchorID   string    `json:"snapshotAnchorId"`
	SnapshotCreatedAt  time.Time `json:"snapshotCreatedAt"`
	SnapshotAgeSeconds int64     `json:"snapshotAgeSeconds"`
	RestoreResultID    string    `json:"restoreResultId"`
	RestoreCompletedAt time.Time `json:"restoreCompletedAt"`
	DrillStartedAt     time.Time `json:"drillStartedAt"`
	DrillCompletedAt   time.Time `json:"drillCompletedAt"`
	DrillDurationMS    int64     `json:"drillDurationMs"`
}

type applicationRecoveryIsolation struct {
	CloneProject                    string `json:"cloneProject"`
	LiveProjectUntouched            bool   `json:"liveProjectUntouched"`
	LiveVolumesUnmounted            bool   `json:"liveVolumesUnmounted"`
	InternalNetworksOnly            bool   `json:"internalNetworksOnly"`
	MasqueradeDisabled              bool   `json:"masqueradeDisabled"`
	RoutingLabelsStripped           bool   `json:"routingLabelsStripped"`
	PublishedHostPortsOnlyLoopback  bool   `json:"publishedHostPortsOnlyLoopback"`
	OutboundNotificationsProhibited bool   `json:"outboundNotificationsProhibited"`
	OutboundNotificationsDetected   bool   `json:"outboundNotificationsDetected"`
	CloneResourcesCleanedUp         bool   `json:"cloneResourcesCleanedUp"`
}

type applicationRecoveryReadiness struct {
	DatabaseReady    bool                                        `json:"databaseReady"`
	DatabaseName     string                                      `json:"databaseName"`
	PublicTableCount int64                                       `json:"publicTableCount"`
	EntryHTTPStatus  int                                         `json:"entryHttpStatus"`
	EntryHTTPReady   bool                                        `json:"entryHttpReady"`
	DependencyChecks []restoreactivation.IsolatedDependencyCheck `json:"dependencyChecks"`
}

type applicationRecoveryDataReadback struct {
	Method  string                                         `json:"method"`
	Volumes []restoreactivation.IsolatedVolumeVerification `json:"volumes"`
}

func validateRestoreDrillWorkloadFlag(raw string) (string, error) {
	workload := strings.TrimSpace(raw)
	if workload == "" {
		return "", nil
	}
	if !restoreDrillWorkloadPattern.MatchString(workload) {
		return "", errors.New("--workload must be a lowercase workload identifier")
	}
	return workload, nil
}

func admitRestoreDrillWorkloadTarget(
	authority nativeV2BackupAuthority,
	workloadRef string,
) (localbackuppolicy.ApplicationRuntime, nativehost.SelectedPaaSWorkloadDeployment, error) {
	if runtime.GOOS != "linux" {
		return localbackuppolicy.ApplicationRuntime{}, nativehost.SelectedPaaSWorkloadDeployment{}, fmt.Errorf("isolated application recovery requires Linux (current OS: %s)", runtime.GOOS)
	}
	runtimes := authority.Policy.SourceProjection().ApplicationRuntimes
	var selected []localbackuppolicy.ApplicationRuntime
	for _, rt := range runtimes {
		if !rt.Retained && rt.WorkloadRef == workloadRef {
			selected = append(selected, rt)
		}
	}
	if len(selected) == 0 {
		return localbackuppolicy.ApplicationRuntime{}, nativehost.SelectedPaaSWorkloadDeployment{}, fmt.Errorf("workload %q is not present in the live backup authority", workloadRef)
	}
	deployments, err := nativeBackupApplicationDeployments(authority, selected)
	if err != nil {
		return localbackuppolicy.ApplicationRuntime{}, nativehost.SelectedPaaSWorkloadDeployment{}, fmt.Errorf("resolve workload deployment %q: %w", workloadRef, err)
	}
	deployment, ok := deployments[workloadRef]
	if !ok {
		return localbackuppolicy.ApplicationRuntime{}, nativehost.SelectedPaaSWorkloadDeployment{}, fmt.Errorf("workload deployment %q is unavailable", workloadRef)
	}
	return selected[0], deployment, nil
}

func executeIsolatedApplicationRecoveryPhase(
	ctx context.Context,
	workspace string,
	authority nativeV2BackupAuthority,
	anchor backuplifecycle.SnapshotAnchor,
	restored backuplifecycle.RestoreResult,
	stagingPath, workloadRef string,
	report *restoreDrillReport,
) error {
	phaseStarted := time.Now().UTC()
	receiptRelPath := filepath.ToSlash(filepath.Join(".stackkit", "backups", "restore-drills", report.DrillID+".json"))
	drillSum := sha256.Sum256([]byte(report.DrillID))
	cloneProject := "stackkit-drill-" + hex.EncodeToString(drillSum[:6])

	receipt := &applicationRecoveryReceipt{
		SchemaVersion:      restoreDrillAppReceiptSchema,
		DrillID:            report.DrillID,
		Status:             restoreDrillStatusFailed,
		ReceiptPath:        receiptRelPath,
		WorkloadRef:        workloadRef,
		DependencyVersions: map[string]string{},
		Scope: applicationRecoveryScope{
			LinuxProfile:         "linux/standalone-compose",
			CloneProject:         cloneProject,
			BackedUpVolumes:      []string{},
			StagedVolumes:        []string{},
			RestoredVolumes:      []string{},
			VerifiedDependencies: []string{},
			ConfigFilesVerified:  []string{},
			SecretSlotsVerified:  []string{},
		},
		Versions: applicationRecoveryVersions{
			StackKitVersion:      version,
			PolicyArtifactDigest: authority.PolicyDigest,
		},
		EvidenceAge: applicationRecoveryEvidenceAge{
			SnapshotAnchorID:   anchor.ID,
			SnapshotCreatedAt:  anchor.Snapshot.CreatedAt,
			SnapshotAgeSeconds: max(int64(phaseStarted.Sub(anchor.Snapshot.CreatedAt).Seconds()), 0),
			RestoreResultID:    restored.ID,
			RestoreCompletedAt: restored.Receipt.CompletedAt,
			DrillStartedAt:     report.StartedAt,
		},
		Isolation: applicationRecoveryIsolation{
			CloneProject:                    cloneProject,
			OutboundNotificationsProhibited: true,
		},
		Readiness: applicationRecoveryReadiness{
			DependencyChecks: []restoreactivation.IsolatedDependencyCheck{},
		},
		DataReadback: applicationRecoveryDataReadback{
			Method:  "volume-file-tree-sha256/v1",
			Volumes: []restoreactivation.IsolatedVolumeVerification{},
		},
		Unknowns: []string{},
	}

	failWith := func(code string, err error) error {
		receipt.Status = restoreDrillStatusFailed
		receipt.FailureCode = code
		receipt.Error = err.Error()
		receipt.EvidenceAge.DrillCompletedAt = time.Now().UTC()
		receipt.EvidenceAge.DrillDurationMS = max(receipt.EvidenceAge.DrillCompletedAt.Sub(phaseStarted).Milliseconds(), 0)
		_ = persistApplicationRecoveryReceipt(workspace, report.DrillID, *receipt)
		report.ApplicationRecovery = receipt
		report.addCheck("isolated-app-recovery", err)
		return err
	}

	_, deployment, err := admitRestoreDrillWorkloadTarget(authority, workloadRef)
	if err != nil {
		return failWith("unsupported_workload", err)
	}

	plan, manifest, err := readNativeV2RestorePlanManifest(ctx, workspace, authority.OutputRoot)
	if err != nil {
		return failWith("missing_config", fmt.Errorf("read verified plan/manifest: %w", err))
	}
	graph, err := restoreactivation.DeriveRuntimeRecoveryGraph(workspace, plan, manifest, report.DrillID)
	if err != nil {
		return failWith("missing_dependency", fmt.Errorf("derive recovery graph: %w", err))
	}
	receipt.Versions.PlanHash = graph.PlanHash
	receipt.Versions.ManifestHash = graph.ManifestHash
	receipt.Versions.KopiaHelperImage = graph.KopiaHelperImage

	beforeNotify, err := snapshotOwnerNotificationReceipts(workspace)
	if err != nil {
		return failWith("prohibited_outbound_notification", fmt.Errorf("snapshot owner-notifications outbox: %w", err))
	}

	cloneDir := filepath.Join(workspace, ".stackkit", "backups", "restore-drills", report.DrillID, "clone")
	defer func() { _ = os.RemoveAll(cloneDir) }()

	prep, err := nativehost.PrepareIsolatedCloneCompose(ctx, workspace, cloneProject, cloneDir, report.DrillID, deployment)
	if err != nil {
		var prepErr *nativehost.ClonePreparationError
		if errors.As(err, &prepErr) {
			return failWith(prepErr.Code, err)
		}
		return failWith("missing_config", err)
	}

	receipt.ModuleRef = prep.ModuleRef
	receipt.AppRelease = prep.Release
	receipt.PostgresImage = prep.DatabaseImage
	receipt.Scope.SiteRef = prep.SiteRef
	receipt.Scope.NodeRef = prep.NodeRef
	receipt.Scope.LiveProject = prep.LiveProject
	receipt.Scope.ConfigFilesVerified = append([]string(nil), prep.ConfigFilesVerified...)
	receipt.Scope.SecretSlotsVerified = append([]string(nil), prep.SecretSlotsVerified...)
	receipt.Unknowns = append([]string(nil), prep.Unknowns...)

	for _, comp := range prep.Components {
		receipt.DependencyVersions[comp.ID] = comp.ImageRef + "@" + comp.ImageDigest
		receipt.Scope.VerifiedDependencies = append(receipt.Scope.VerifiedDependencies, comp.ID)
	}
	sort.Strings(receipt.Scope.VerifiedDependencies)

	var reqVols []restoreactivation.IsolatedVolumeInput
	for _, v := range prep.Volumes {
		reqVols = append(reqVols, restoreactivation.IsolatedVolumeInput{
			ComponentRef: v.ComponentRef,
			LogicalName:  v.LogicalName,
			LiveName:     v.LiveName,
			CloneName:    v.CloneName,
			Backup:       v.Backup,
		})
		if v.Backup {
			receipt.Scope.BackedUpVolumes = append(receipt.Scope.BackedUpVolumes, v.LiveName)
			receipt.Scope.StagedVolumes = append(receipt.Scope.StagedVolumes, v.LiveName)
		}
	}
	reqComps := make([]restoreactivation.IsolatedComponentInput, 0, len(prep.Components))
	for _, c := range prep.Components {
		reqComps = append(reqComps, restoreactivation.IsolatedComponentInput{
			ID:            c.ID,
			Role:          c.Role,
			Lifecycle:     c.Lifecycle,
			HealthFailure: c.HealthFailure,
			ImageRef:      c.ImageRef,
			ImageDigest:   c.ImageDigest,
		})
	}

	// Cross-check policy backed-up volumes against clone volumes so missing volume declarations fail closed.
	policyVols := map[string]bool{}
	for _, av := range authority.Policy.SourceProjection().ApplicationVolumes {
		if av.WorkloadRef == workloadRef && av.Backup {
			policyVols[av.VolumeName] = true
		}
	}
	for liveVol := range policyVols {
		found := false
		for _, bv := range receipt.Scope.BackedUpVolumes {
			if bv == liveVol {
				found = true
				break
			}
		}
		if !found {
			return failWith("missing_staged_data", fmt.Errorf("policy backup volume %q is missing from workload recovery graph", liveVol))
		}
	}
	if len(receipt.Scope.BackedUpVolumes) == 0 {
		return failWith("missing_staged_data", fmt.Errorf("workload %q declares no backed-up persistent volumes", workloadRef))
	}

	execResult, execErr := restoreactivation.ExecuteIsolatedApplicationRecovery(ctx, workspace, restoreactivation.IsolatedApplicationRecoveryRequest{
		DrillID:                 report.DrillID,
		WorkloadRef:             workloadRef,
		LiveProject:             prep.LiveProject,
		CloneProject:            prep.CloneProject,
		ComposePath:             prep.ComposePath,
		EnvironmentPath:         prep.EnvironmentPath,
		StagingVolume:           graph.StagingVolume,
		StagingPath:             stagingPath,
		KopiaHelperImage:        graph.KopiaHelperImage,
		EntryComponent:          prep.EntryComponent,
		EntryHealthPath:         prep.EntryHealthPath,
		EntryHealthPort:         prep.EntryHealthPort,
		DatabaseComponent:       prep.DatabaseComponent,
		DatabaseName:            prep.DatabaseName,
		DatabaseUser:            prep.DatabaseUser,
		HasRestoreActivationEnv: prep.HasRestoreActivationEnv,
		InternalNetworks:        prep.InternalNetworks,
		HealthNetwork:           prep.HealthNetwork,
		Components:              reqComps,
		Volumes:                 reqVols,
	})

	// Always copy cleanup/isolation observations we already gathered before failing.
	receipt.Isolation.LiveVolumesUnmounted = execResult.Isolation.LiveVolumesUnmounted
	receipt.Isolation.InternalNetworksOnly = execResult.Isolation.InternalNetworksOnly
	receipt.Isolation.MasqueradeDisabled = execResult.Isolation.MasqueradeDisabled
	receipt.Isolation.RoutingLabelsStripped = execResult.Isolation.RoutingLabelsStripped
	receipt.Isolation.PublishedHostPortsOnlyLoopback = execResult.Isolation.PublishedHostPortsOnlyLoopback
	receipt.Isolation.CloneResourcesCleanedUp = execResult.Isolation.CloneResourcesCleanedUp

	afterNotify, notifyErr := snapshotOwnerNotificationReceipts(workspace)
	if notifyErr != nil {
		return failWith("prohibited_outbound_notification", fmt.Errorf("verify owner-notifications outbox: %w", notifyErr))
	}
	if !equalSnapshotMaps(beforeNotify, afterNotify) {
		receipt.Isolation.OutboundNotificationsDetected = true
		return failWith("prohibited_outbound_notification", errors.New("outbound owner notifications were emitted during the isolated recovery drill"))
	}

	if execErr != nil {
		code := "app_not_ready"
		var recErr *restoreactivation.IsolatedRecoveryError
		if errors.As(execErr, &recErr) {
			code = recErr.Code
		}
		return failWith(code, execErr)
	}

	receipt.Isolation.LiveProjectUntouched = true
	receipt.Readiness = applicationRecoveryReadiness{
		DatabaseReady:    execResult.DatabaseReady,
		DatabaseName:     execResult.DatabaseName,
		PublicTableCount: execResult.PublicTableCount,
		EntryHTTPStatus:  execResult.EntryHTTPStatus,
		EntryHTTPReady:   execResult.EntryHTTPReady,
		DependencyChecks: execResult.DependencyChecks,
	}
	receipt.DataReadback.Volumes = execResult.VolumeVerification
	for _, vv := range execResult.VolumeVerification {
		receipt.Scope.RestoredVolumes = append(receipt.Scope.RestoredVolumes, vv.CloneName)
	}
	sort.Strings(receipt.Scope.RestoredVolumes)

	receipt.Status = restoreDrillStatusSucceeded
	receipt.EvidenceAge.DrillCompletedAt = time.Now().UTC()
	receipt.EvidenceAge.DrillDurationMS = max(receipt.EvidenceAge.DrillCompletedAt.Sub(phaseStarted).Milliseconds(), 0)
	if err := persistApplicationRecoveryReceipt(workspace, report.DrillID, *receipt); err != nil {
		return failWith("missing_config", fmt.Errorf("persist application recovery receipt: %w", err))
	}
	report.ApplicationRecovery = receipt
	report.addCheck("isolated-app-recovery", nil)
	return nil
}

func snapshotOwnerNotificationReceipts(workspace string) (map[string]string, error) {
	dir := filepath.Join(workspace, ".stackkit", "runs", "owner-notifications")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	snapshot := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil, statErr
		}
		snapshot[entry.Name()] = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
	}
	return snapshot, nil
}

func equalSnapshotMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func persistApplicationRecoveryReceipt(workspace, drillID string, receipt applicationRecoveryReceipt) error {
	drillsDir := filepath.Join(workspace, ".stackkit", "backups", "restore-drills")
	if err := os.MkdirAll(drillsDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(drillsDir, 0o700); err != nil {
		return err
	}
	root, err := confinedfs.Open(drillsDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	res, err := view.WriteAtomic0600(drillID+".json", payload)
	if err != nil {
		return err
	}
	if !res.Installed || !res.FileSynced {
		return errors.New("application recovery receipt persistence did not confirm durable sync")
	}
	return nil
}
