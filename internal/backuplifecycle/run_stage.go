package backuplifecycle

import "errors"

// Closed steps of one snapshot run. A failed `stackkit backup run` names the
// step in its reason code (backup_run_failed_in_<step>) so evidence that
// keeps only closed tokens still says where the run stopped. The step is a
// fixed identifier, never cause text, IDs or credentials.
const (
	RunStageRuntime          = "runtime-ready"
	RunStageConfiguration    = "configuration"
	RunStageOperationJournal = "operation-journal"
	RunStageRetention        = "retention"
	RunStageRepository       = "repository-status"
	RunStageSnapshot         = "snapshot"
	RunStageSnapshotReceipt  = "snapshot-receipt"
	RunStageComplete         = "complete"
	RunStageAppGroups        = "app-groups"
	RunStageQuiescePrepare   = "quiesce-prepare"
	RunStageQuiesceStop      = "quiesce-stop"
	RunStageSnapshotCreate   = "snapshot-create"
	RunStageSnapshotFinalize = "snapshot-finalize"
	RunStageKopiaSettle      = "kopia-settle"
	RunStageQuiesceResume    = "quiesce-resume"
)

// RunStageError carries the closed step at which a snapshot run failed. Its
// text and unwrap chain are the cause's own, so existing classification and
// sentinel checks see the same error.
type RunStageError struct {
	Stage string
	Err   error
}

func (e *RunStageError) Error() string { return e.Err.Error() }

func (e *RunStageError) Unwrap() error { return e.Err }

// WithRunStage names stage on err. A step already named further down keeps
// precedence because it is the more exact location.
func WithRunStage(stage string, err error) error {
	if err == nil {
		return nil
	}
	var named *RunStageError
	if errors.As(err, &named) {
		return err
	}
	return &RunStageError{Stage: stage, Err: err}
}
