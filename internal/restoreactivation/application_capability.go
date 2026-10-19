package restoreactivation

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
)

// Closed reasons for a workload-scoped restore the selected delivery cannot
// serve. Each refuses before staging verification, safety snapshot, journal or
// manager control, so no live volume is touched.
const (
	// The workload's selected adapter row does not declare backupRestore: its
	// data is outside the node's signed Kopia source. Shared with the backup gate.
	ReasonApplicationRestoreAdapterUnsupported = actionableerror.ReasonSelectedAdapterBackupRestoreUnavailable
	// The workload restores with the node (standalone Compose); only a
	// platform-managed workload (Coolify, Komodo) activates on its own.
	ReasonApplicationRestoreScopeUnsupported = "application_restore_scope_unsupported"
	// The Plan selects no such application workload.
	ReasonApplicationRestoreWorkloadUnselected = "application_restore_workload_unselected"
	// The restore staged none of the workload's volumes under its coverage.
	ReasonApplicationRestoreWorkloadNotStaged = "application_restore_workload_not_staged"
)

// ApplicationRestoreUnsupportedError names why `backup restore activate
// --workload` refused. It carries identifiers only, never data or credentials.
type ApplicationRestoreUnsupportedError struct {
	WorkloadRef string
	AdapterRef  string
	Reason      string
}

func (e *ApplicationRestoreUnsupportedError) Error() string {
	if e.AdapterRef == "" {
		return fmt.Sprintf("restoreactivation: workload %q has no workload-scoped restore (%s)", e.WorkloadRef, e.Reason)
	}
	return fmt.Sprintf("restoreactivation: workload %q delivered by %q has no workload-scoped restore (%s)", e.WorkloadRef, e.AdapterRef, e.Reason)
}

func (e *ApplicationRestoreUnsupportedError) ActionableError() actionableerror.Contract {
	contract, _ := actionableerror.Diagnostic(e.Reason)
	return contract
}

// An adapter without backup/restore is a denial, like the backup gate; a
// node-scoped or unknown selection is a failed request.
func (e *ApplicationRestoreUnsupportedError) ActionableStatus() string {
	if e.Reason == ReasonApplicationRestoreAdapterUnsupported {
		return "denied"
	}
	return "failed"
}

// stagedWorkloadVolumes keeps the selected workload's volumes that the restore
// staged under its backup coverage, as the node-wide authority does: an
// uncovered class (the Photos library under content coverage) stays live. A
// whole-root snapshot names no sources and stages every selected volume.
func stagedWorkloadVolumes(workload string, selected []Volume, result backuplifecycle.RestoreResult) ([]Volume, error) {
	sources := result.Request.SnapshotRequest.Sources
	if len(sources) == 0 {
		return selected, nil
	}
	staged := make(map[string]bool, len(sources))
	for _, source := range sources {
		staged[source.VolumeName] = true
	}
	kept := make([]Volume, 0, len(selected))
	for _, volume := range selected {
		if staged[volume.LiveName] {
			kept = append(kept, volume)
		}
	}
	if len(kept) == 0 {
		return nil, &ApplicationRestoreUnsupportedError{WorkloadRef: workload, Reason: ReasonApplicationRestoreWorkloadNotStaged}
	}
	return kept, nil
}

// workloadNotStaged lets a recovery that searches every platform workload pass
// over one the restore never staged; an explicit selection still refuses.
func workloadNotStaged(err error) bool {
	var refusal *ApplicationRestoreUnsupportedError
	return errors.As(err, &refusal) && refusal.Reason == ReasonApplicationRestoreWorkloadNotStaged
}

// requireApplicationRestoreCapability reads the compiler-projected delivery of
// the selected workload: the same row that admits it to the backup source.
func requireApplicationRestoreCapability(plan map[string]any, workload string) error {
	lifecycles, err := array(plan, "applicationLifecycles")
	if err != nil {
		return errors.New("restoreactivation: Application lifecycle authority is absent")
	}
	for _, raw := range lifecycles {
		lifecycle, ok := raw.(map[string]any)
		if !ok || text(lifecycle, "workloadRef") != workload {
			continue
		}
		delivery, err := object(lifecycle, "delivery")
		if err != nil {
			return errors.New("restoreactivation: selected Application lifecycle has no delivery")
		}
		adapterRef := text(delivery, "adapterRef")
		capabilities, err := object(delivery, "capabilities")
		if err != nil || capabilities["backupRestore"] != true {
			return &ApplicationRestoreUnsupportedError{WorkloadRef: workload, AdapterRef: adapterRef, Reason: ReasonApplicationRestoreAdapterUnsupported}
		}
		if adapterRef != "coolify" && adapterRef != "komodo" {
			return &ApplicationRestoreUnsupportedError{WorkloadRef: workload, AdapterRef: adapterRef, Reason: ReasonApplicationRestoreScopeUnsupported}
		}
		return nil
	}
	return &ApplicationRestoreUnsupportedError{WorkloadRef: workload, Reason: ReasonApplicationRestoreWorkloadUnselected}
}

// StandaloneComposeWorkloadHoldsNoBackupData reports whether the verified plan
// delivers the application workload through standalone Compose without any
// backup-enabled storage. Such a workload (Remote's Guacamole, the ntfy live
// relay) contributes nothing to the node's signed backup source, so the backup
// gate admits it exactly as restore skips it. Any other or unreadable shape
// returns false and stays denied.
func StandaloneComposeWorkloadHoldsNoBackupData(canonicalPlan []byte, workloadRef string) (bool, error) {
	var plan map[string]any
	if err := json.Unmarshal(canonicalPlan, &plan); err != nil {
		return false, fmt.Errorf("restoreactivation: decode verified plan for backup coverage: %w", err)
	}
	workloads, err := array(plan, "workloads")
	if err != nil {
		return false, nil
	}
	for _, raw := range workloads {
		workload, ok := raw.(map[string]any)
		if !ok || text(workload, "kind") != "application" || text(workload, "id") != workloadRef {
			continue
		}
		alternative, err := object(workload, "alternative")
		if err != nil {
			return false, nil
		}
		runtimeContract, err := object(alternative, "runtime")
		if err != nil || text(runtimeContract, "delivery") != "application-adapter" {
			return false, nil
		}
		adapter, err := object(runtimeContract, "adapter")
		if err != nil || text(adapter, "id") != "standalone-compose" {
			return false, nil
		}
		return !holdsBackupEnabledStorage(plan, alternative), nil
	}
	return false, nil
}
