package backuplifecycle

import (
	"context"
	"errors"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"reflect"
	"time"
)

// VerifySnapshotAppGroup authenticates the actual existing operation file and
// exact group, not an opaque caller digest. Native adapters can use this under
// the existing node/lifecycle lease; it creates no new credential or journal.
func VerifySnapshotAppGroup(workspace string, g AppGroupControl, completed bool) error {
	s := &Service{workspaceRoot: workspace}
	op, err := s.loadOperation(operationDirectory + "/" + operationKey(g.Binding.JournalOperationID) + ".json")
	if err != nil {
		return err
	}
	if op.Quiescence == nil || op.Quiescence.ControlVersion != 2 || op.Input.OwnerRef != g.Binding.OwnerRef || op.Input.OperationID != g.Binding.JournalOperationID || completed && op.State != "completed" && op.State != "failed" {
		return errors.New("native group lacks exact signed snapshot authority")
	}
	for _, held := range op.Quiescence.AppGroups {
		if reflect.DeepEqual(held, g) {
			return nil
		}
	}
	return errors.New("native group differs from signed snapshot journal")
}
func validateSnapshotGroupClosures(op snapshotOperation) error {
	if len(op.AppGroupClosures) == 0 {
		return nil
	}
	if (op.State != "completed" && op.State != "failed") || op.Quiescence == nil || op.Quiescence.ControlVersion != 2 {
		return errors.New("native group closure lacks completed snapshot authority")
	}
	selected := map[string]bool{}
	for _, g := range op.Quiescence.AppGroups {
		selected[g.Binding.GroupID] = true
	}
	seen := map[string]bool{}
	for _, id := range op.AppGroupClosures {
		if !selected[id] || seen[id] {
			return errors.New("snapshot group closure set changed")
		}
		seen[id] = true
	}
	return nil
}
func (s *Service) closeCompletedSnapshotGroups(ctx context.Context, path string, op snapshotOperation) error {
	if op.Quiescence == nil || len(op.Quiescence.AppGroups) == 0 || len(op.AppGroupClosures) == len(op.Quiescence.AppGroups) {
		return nil
	}
	if op.State != "completed" || op.Anchor == nil {
		return errors.New("snapshot groups close only after signed completion")
	}
	closer, ok := s.runtime.(interface {
		CloseSnapshotAppGroupHolds(context.Context, SnapshotAnchor) error
	})
	if !ok {
		return errors.New("completed snapshot retains native app-group closure obligation")
	}
	if err := closer.CloseSnapshotAppGroupHolds(ctx, *op.Anchor); err != nil {
		return err
	}
	// Immutable anchor retains its G0/G1 evidence. Closure acknowledgment is
	// appended to the SAME signed operation, avoiding anchor replacement.
	op.AppGroupClosures = nil
	for _, g := range op.Quiescence.AppGroups {
		op.AppGroupClosures = append(op.AppGroupClosures, g.Binding.GroupID)
	}
	return s.writeSnapshotOperation(path, op)
}

// ReadSnapshotAppGroupControls is a read-only view of the original authenticated
// operation. It cannot clear a hold, change phases or create a replacement op.
func ReadSnapshotAppGroupControls(workspace, operation string) ([]AppGroupControl, string, bool, error) {
	s := &Service{workspaceRoot: workspace}
	op, err := s.loadOperation(operationDirectory + "/" + operationKey(operation) + ".json")
	if err != nil {
		return nil, "", false, err
	}
	if op.Input.OperationID != operation || op.Quiescence == nil || op.Quiescence.ControlVersion != 2 {
		return nil, "", false, errors.New("original snapshot app-group operation absent")
	}
	return CloneAppGroups(op.Quiescence.AppGroups), op.Quiescence.Phase, op.State == "completed", nil
}

func VerifySnapshotAppGroupAuthority(workspace string, g AppGroupControl, lineage AuthorityLineage, completed bool) error {
	if err := VerifySnapshotAppGroup(workspace, g, completed); err != nil {
		return err
	}
	op, err := (&Service{workspaceRoot: workspace}).loadOperation(operationDirectory + "/" + operationKey(g.Binding.JournalOperationID) + ".json")
	if err != nil {
		return err
	}
	digest, err := localbackuppolicy.SourceDigest(op.Input.Policy.SourceProjection())
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(op.Input.Lineage, lineage) || !reflect.DeepEqual(g.Binding.AuthorityLineage, lineage) || g.Binding.PolicySourceDigest != digest {
		return errors.New("snapshot group policy source/full lineage differs from signed operation")
	}
	return nil
}

// Only the runtime's signed restored journal settles a failed snapshot. An
// uncertain Kopia helper or native claim cannot reach this path. No anchor is
// minted, and replay of this operation remains a negative result forever.
func (s *Service) settleFailedSnapshot(ctx context.Context, path string, input operationInput) error {
	op, err := s.loadOperation(path)
	if err != nil {
		return err
	}
	if op.State != "pending" || !operationInputsEqual(op.Input, input) || op.Quiescence == nil || op.Quiescence.ControlVersion != 2 {
		return nil
	}
	if err = validateRestoredSnapshotQuiescence(*op.Quiescence); err != nil {
		return err
	}
	op.State = "failed"
	op.Failure = "SNAPSHOT_FAILED_AFTER_SETTLEMENT"
	if err = s.writeSnapshotOperation(path, op); err != nil {
		return err
	}
	return s.closeFailedSnapshotGroups(ctx, path, op)
}
func (s *Service) closeFailedSnapshotGroups(ctx context.Context, path string, op snapshotOperation) error {
	if op.State != "failed" || op.Quiescence == nil || validateRestoredSnapshotQuiescence(*op.Quiescence) != nil {
		return errors.New("failed snapshot retains unresolved control")
	}
	if len(op.AppGroupClosures) == len(op.Quiescence.AppGroups) {
		return nil
	}
	closer, ok := s.runtime.(interface {
		CloseFailedSnapshotAppGroupHolds(context.Context, []AppGroupControl) error
	})
	if !ok {
		return errors.New("negative native group closure unavailable")
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	if err := closer.CloseFailedSnapshotAppGroupHolds(cleanup, CloneAppGroups(op.Quiescence.AppGroups)); err != nil {
		return err
	}
	op.AppGroupClosures = nil
	for _, g := range op.Quiescence.AppGroups {
		op.AppGroupClosures = append(op.AppGroupClosures, g.Binding.GroupID)
	}
	return s.writeSnapshotOperation(path, op)
}

// ReadSnapshotAppGroupEvidence authenticates the actual policy source and v1
// control selection alongside the group for real-product journey assertions.
type SnapshotAppGroupEvidence struct {
	Groups             []AppGroupControl
	Controls           []RuntimeControl
	Phase              string
	State              string
	PolicySourceDigest string
	NativeHoldsClosed  bool
}

func ReadSnapshotAppGroupEvidence(workspace, operation string) (SnapshotAppGroupEvidence, error) {
	var evidence SnapshotAppGroupEvidence
	op, err := (&Service{workspaceRoot: workspace}).loadOperation(operationDirectory + "/" + operationKey(operation) + ".json")
	if err != nil {
		return evidence, err
	}
	if op.Input.OperationID != operation || op.Quiescence == nil || op.Quiescence.ControlVersion != 2 {
		return evidence, errors.New("original signed group snapshot absent")
	}
	digest, err := localbackuppolicy.SourceDigest(op.Input.Policy.SourceProjection())
	if err != nil {
		return evidence, err
	}
	for _, g := range op.Quiescence.AppGroups {
		if g.Binding.PolicySourceDigest != digest || !reflect.DeepEqual(g.Binding.AuthorityLineage, op.Input.Lineage) {
			return evidence, errors.New("signed group differs from actual journal policy/lineage")
		}
	}
	return SnapshotAppGroupEvidence{Groups: CloneAppGroups(op.Quiescence.AppGroups), Controls: append([]RuntimeControl(nil), op.Quiescence.Controls...), Phase: op.Quiescence.Phase, State: op.State, PolicySourceDigest: digest, NativeHoldsClosed: len(op.AppGroupClosures) == len(op.Quiescence.AppGroups)}, nil
}
