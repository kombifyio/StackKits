package lifecyclemutation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
)

func validateRecord(record Record) error {
	if record.APIVersion != APIVersion ||
		(record.Kind != KindUpgrade && record.Kind != KindRestoreActivation) ||
		!operationPattern.MatchString(record.OperationID) ||
		!canonicalDigest(record.WorkspaceHash) ||
		strings.TrimSpace(record.OwnerRef) == "" ||
		record.Sequence == 0 || record.UpdatedAt.IsZero() ||
		(record.Status != StatusActive &&
			record.Status != StatusSucceeded &&
			record.Status != StatusRecovered) {
		return errors.New("lifecycle mutation journal is incomplete")
	}
	if record.Kind == KindUpgrade {
		if !canonicalDigest(record.Checkpoint.ExecutorStateSnapshotID) ||
			!canonicalDigest(record.Checkpoint.KopiaAnchorID) ||
			!validReleaseAuthority(record.Target) ||
			!validReleaseAuthority(record.Prior) ||
			record.RestoreActivation != nil {
			return errors.New("lifecycle mutation journal is incomplete")
		}
		if record.UpgradeDataActivation != nil {
			if err := validateUpgradeDataActivationState(record); err != nil {
				return err
			}
		} else if upgradeDataActivationPhase(record.Phase) &&
			record.Phase != PhaseRollbackSucceeded {
			return errors.New("upgrade data activation phase lacks its authority")
		}
	} else if err := validateRestoreActivationState(record); err != nil {
		return err
	}
	if record.PreviousRecordDigest != "" &&
		!canonicalDigest(record.PreviousRecordDigest) {
		return errors.New(
			"lifecycle mutation previous-record digest is invalid",
		)
	}
	if !knownPhase(record.Kind, record.Phase) {
		return errors.New("lifecycle mutation journal phase is invalid")
	}
	switch record.Status {
	case StatusSucceeded:
		if record.Phase != PhaseCommitSucceeded {
			return errors.New(
				"successful lifecycle mutation has a non-committed phase",
			)
		}
	case StatusRecovered:
		// A prepared upgrade released before any target phase is the only
		// recovered record that never reached a rollback phase.
		if record.Phase != PhaseRollbackSucceeded &&
			!(record.Kind == KindUpgrade && record.Phase == PhasePrepared) {
			return errors.New(
				"recovered lifecycle mutation has a non-rollback phase",
			)
		}
	}
	if record.Kind == KindRestoreActivation && record.Join != nil {
		return errors.New(
			"restore activation journal cannot grant child join authority",
		)
	}
	if record.Kind == KindUpgrade && joinPhase(record.Phase) {
		if record.Status != StatusActive || record.Join == nil ||
			(record.Join.Command != "generate" &&
				record.Join.Command != "apply" &&
				record.Join.Command != "verify") ||
			strings.TrimSpace(record.Join.BinaryVersion) == "" ||
			!canonicalDigest(record.Join.ExecutableSHA256) ||
			!canonicalDigest(record.Join.NonceSHA256) {
			return errors.New(
				"lifecycle mutation join phase lacks exact one-use authority",
			)
		}
	} else if record.Join != nil {
		return errors.New(
			"lifecycle mutation join authority exists outside a join phase",
		)
	}
	return nil
}

func knownPhase(kind, phase string) bool {
	if kind == KindRestoreActivation {
		for _, candidate := range []string{
			PhasePrepared, PhaseQuiesceStarted, PhaseQuiesced,
			PhaseRollbackCopyStarted, PhaseRollbackCopySucceeded,
			PhaseRollbackReady,
			PhaseActivationCopyStarted, PhaseActivationCopySucceeded,
			PhaseActivationSucceeded,
			PhaseRuntimeStartStarted, PhaseRuntimeStartSucceeded,
			PhaseVerifyStarted, PhaseVerifySucceeded,
			PhaseCommitStarted, PhaseCommitSucceeded,
			PhaseRollbackStarted,
			PhaseRollbackVolumeStarted, PhaseRollbackVolumeSucceeded,
			PhaseRollbackRuntimeStarted, PhaseRollbackRuntimeSucceeded,
			PhaseRollbackActivationVerifyStarted,
			PhaseRollbackActivationVerifyDone,
			PhaseRollbackSucceeded,
		} {
			if phase == candidate {
				return true
			}
		}
		return false
	}
	for _, candidate := range []string{
		PhasePrepared,
		PhaseTargetGenerateStarted, PhaseTargetGenerateSucceeded,
		PhaseTargetApplyStarted, PhaseTargetApplySucceeded,
		PhaseTargetVerifyStarted, PhaseTargetVerifySucceeded,
		PhaseCommitStarted, PhaseCommitSucceeded,
		PhaseRollbackStarted,
		PhaseRollbackGenerateStarted, PhaseRollbackGenerateDone,
		PhaseRollbackApplyStarted, PhaseRollbackApplyDone,
		PhaseRollbackVerifyStarted, PhaseRollbackVerifyDone,
		PhaseRollbackSucceeded,
		PhaseRollbackDataQuiesceStarted, PhaseRollbackDataQuiesced,
		PhaseRollbackDataCopyStarted, PhaseRollbackDataCopySucceeded,
		PhaseRollbackDataActivationSucceeded,
		PhaseRollbackPriorRuntimeStartStarted,
		PhaseRollbackPriorRuntimeStartSucceeded,
		PhaseRollbackPriorRuntimeVerifyStarted,
		PhaseRollbackPriorRuntimeVerifySucceeded,
	} {
		if phase == candidate {
			return true
		}
	}
	return false
}

func allowedTransition(kind, current, next string) bool {
	if kind == KindRestoreActivation {
		return allowedRestoreActivationTransition(current, next)
	}
	if next == PhaseRollbackDataQuiesceStarted {
		return upgradeDataActivationStartPhase(current)
	}
	allowed := map[string][]string{
		PhasePrepared:                {PhaseTargetGenerateStarted, PhaseRollbackStarted},
		PhaseTargetGenerateStarted:   {PhaseTargetGenerateSucceeded, PhaseRollbackStarted},
		PhaseTargetGenerateSucceeded: {PhaseTargetApplyStarted, PhaseRollbackStarted},
		PhaseTargetApplyStarted:      {PhaseTargetApplySucceeded, PhaseRollbackStarted},
		PhaseTargetApplySucceeded:    {PhaseTargetVerifyStarted, PhaseRollbackStarted},
		PhaseTargetVerifyStarted:     {PhaseTargetVerifySucceeded, PhaseRollbackStarted},
		PhaseTargetVerifySucceeded:   {PhaseCommitStarted, PhaseRollbackStarted},
		PhaseCommitStarted:           {PhaseCommitSucceeded, PhaseRollbackStarted},
		PhaseRollbackStarted:         {PhaseRollbackGenerateStarted},
		PhaseRollbackGenerateStarted: {PhaseRollbackGenerateDone, PhaseRollbackStarted},
		PhaseRollbackGenerateDone:    {PhaseRollbackApplyStarted, PhaseRollbackStarted},
		PhaseRollbackApplyStarted:    {PhaseRollbackApplyDone},
		PhaseRollbackApplyDone:       {PhaseRollbackVerifyStarted},
		PhaseRollbackVerifyStarted:   {PhaseRollbackVerifyDone},
		PhaseRollbackVerifyDone:      {PhaseRollbackSucceeded},
		PhaseRollbackDataQuiesceStarted: {
			PhaseRollbackDataQuiesced,
		},
		PhaseRollbackDataQuiesced: {
			PhaseRollbackDataCopyStarted,
		},
		PhaseRollbackDataCopyStarted: {
			PhaseRollbackDataCopySucceeded,
		},
		PhaseRollbackDataCopySucceeded: {
			PhaseRollbackDataCopyStarted,
			PhaseRollbackDataActivationSucceeded,
		},
		PhaseRollbackDataActivationSucceeded: {
			PhaseRollbackPriorRuntimeStartStarted,
		},
		PhaseRollbackPriorRuntimeStartStarted: {
			PhaseRollbackPriorRuntimeStartSucceeded,
		},
		PhaseRollbackPriorRuntimeStartSucceeded: {
			PhaseRollbackPriorRuntimeVerifyStarted,
		},
		PhaseRollbackPriorRuntimeVerifyStarted: {
			PhaseRollbackPriorRuntimeVerifySucceeded,
		},
		PhaseRollbackPriorRuntimeVerifySucceeded: {PhaseRollbackSucceeded},
	}
	for _, candidate := range allowed[current] {
		if candidate == next {
			return true
		}
	}
	return false
}

func allowedRestoreActivationTransition(current, next string) bool {
	allowed := map[string][]string{
		PhasePrepared:              {PhaseQuiesceStarted, PhaseRollbackStarted},
		PhaseQuiesceStarted:        {PhaseQuiesced, PhaseRollbackStarted},
		PhaseQuiesced:              {PhaseRollbackCopyStarted, PhaseRollbackStarted},
		PhaseRollbackCopyStarted:   {PhaseRollbackCopySucceeded, PhaseRollbackStarted},
		PhaseRollbackCopySucceeded: {PhaseRollbackCopyStarted, PhaseRollbackReady, PhaseRollbackStarted},
		PhaseRollbackReady:         {PhaseActivationCopyStarted, PhaseRollbackStarted},
		PhaseActivationCopyStarted: {PhaseActivationCopySucceeded, PhaseRollbackStarted},
		PhaseActivationCopySucceeded: {
			PhaseActivationCopyStarted, PhaseActivationSucceeded,
			PhaseRollbackStarted,
		},
		PhaseActivationSucceeded:   {PhaseRuntimeStartStarted, PhaseRollbackStarted},
		PhaseRuntimeStartStarted:   {PhaseRuntimeStartSucceeded, PhaseRollbackStarted},
		PhaseRuntimeStartSucceeded: {PhaseVerifyStarted, PhaseRollbackStarted},
		PhaseVerifyStarted:         {PhaseVerifySucceeded, PhaseRollbackStarted},
		PhaseVerifySucceeded:       {PhaseCommitStarted, PhaseRollbackStarted},
		PhaseCommitStarted:         {PhaseCommitSucceeded, PhaseRollbackStarted},
		PhaseRollbackStarted: {
			PhaseRollbackVolumeStarted, PhaseRollbackRuntimeStarted,
		},
		PhaseRollbackVolumeStarted: {PhaseRollbackVolumeSucceeded},
		PhaseRollbackVolumeSucceeded: {
			PhaseRollbackVolumeStarted, PhaseRollbackRuntimeStarted,
		},
		PhaseRollbackRuntimeStarted: {
			PhaseRollbackRuntimeSucceeded,
		},
		PhaseRollbackRuntimeSucceeded: {
			PhaseRollbackActivationVerifyStarted,
		},
		PhaseRollbackActivationVerifyStarted: {
			PhaseRollbackActivationVerifyDone,
		},
		PhaseRollbackActivationVerifyDone: {PhaseRollbackSucceeded},
	}
	for _, candidate := range allowed[current] {
		if candidate == next {
			return true
		}
	}
	return false
}

func validateRestoreActivationState(record Record) error {
	if err := validateRestoreAppGroupClosures(record); err != nil {
		return err
	}
	state := record.RestoreActivation
	if state == nil ||
		record.UpgradeDataActivation != nil ||
		record.Checkpoint != (CheckpointAuthority{}) ||
		record.Target != (ReleaseAuthority{}) ||
		record.Prior != (ReleaseAuthority{}) {
		return errors.New("restore activation journal authority is incomplete")
	}
	if err := backuplifecycle.ValidateRuntimeControls(state.Controls); err != nil {
		return err
	}
	authority := state.Authority
	if authority.OperationID != record.OperationID ||
		authority.OwnerRef != record.OwnerRef ||
		strings.TrimSpace(authority.RestoreResultID) == "" ||
		!canonicalDigest(authority.SafetySnapshotID) ||
		!canonicalDigest(authority.PlanHash) ||
		!canonicalDigest(authority.ManifestHash) ||
		!canonicalDigest(authority.ApplyResultHash) ||
		!canonicalDigest(authority.ManagedVolumeSetHash) ||
		len(authority.Volumes) == 0 ||
		state.RollbackPrepared == nil || state.Activated == nil {
		return errors.New("restore activation journal authority is incomplete")
	}
	seen := make(map[string]struct{}, len(authority.Volumes))
	for _, volume := range authority.Volumes {
		if !managedVolumePattern.MatchString(volume) {
			return errors.New("restore activation managed volume is invalid")
		}
		if _, duplicate := seen[volume]; duplicate {
			return errors.New("restore activation managed volumes are duplicated")
		}
		seen[volume] = struct{}{}
	}
	if !orderedPrefix(state.RollbackPrepared, authority.Volumes) ||
		!orderedPrefix(state.Activated, authority.Volumes) ||
		(len(state.Activated) > 0 &&
			len(state.RollbackPrepared) != len(authority.Volumes)) {
		return errors.New("restore activation progress is not an ordered volume prefix")
	}
	return validateRestoreActivationPhaseProgress(record.Phase, *state)
}

func validateRestoreActivationPhaseProgress(
	phase string,
	state RestoreActivationState,
) error {
	if err := validateRestoreAppGroupsForPhase(phase, state.AppGroups); err != nil {
		return err
	}
	if err := validateRestoreControlsForPhase(phase, state.Controls); err != nil {
		return err
	}
	volumeCount := len(state.Authority.Volumes)
	rollbackCount := len(state.RollbackPrepared)
	activatedCount := len(state.Activated)
	inFlight := ""
	if state.InFlight != nil {
		inFlight = state.InFlight.Volume
	}
	requireNoInFlight := func() error {
		if state.InFlight != nil {
			return errors.New("restore activation phase has unexpected in-flight volume")
		}
		return nil
	}
	switch phase {
	case PhasePrepared, PhaseQuiesceStarted, PhaseQuiesced:
		if rollbackCount != 0 || activatedCount != 0 {
			return errors.New("restore activation progressed before rollback preparation")
		}
		return requireNoInFlight()
	case PhaseRollbackCopyStarted:
		if rollbackCount >= volumeCount || activatedCount != 0 ||
			inFlight != state.Authority.Volumes[rollbackCount] {
			return errors.New("restore activation rollback copy is not the next volume")
		}
	case PhaseRollbackCopySucceeded:
		if rollbackCount == 0 || activatedCount != 0 {
			return errors.New("restore activation rollback copy progress is invalid")
		}
		return requireNoInFlight()
	case PhaseRollbackReady:
		if rollbackCount != volumeCount || activatedCount != 0 {
			return errors.New("restore activation rollback set is incomplete")
		}
		return requireNoInFlight()
	case PhaseActivationCopyStarted:
		if rollbackCount != volumeCount || activatedCount >= volumeCount ||
			inFlight != state.Authority.Volumes[activatedCount] {
			return errors.New("restore activation copy is not the next volume")
		}
	case PhaseActivationCopySucceeded:
		if rollbackCount != volumeCount || activatedCount == 0 {
			return errors.New("restore activation copy progress is invalid")
		}
		return requireNoInFlight()
	case PhaseActivationSucceeded, PhaseRuntimeStartStarted,
		PhaseRuntimeStartSucceeded, PhaseVerifyStarted, PhaseVerifySucceeded,
		PhaseCommitStarted, PhaseCommitSucceeded:
		if rollbackCount != volumeCount || activatedCount != volumeCount {
			return errors.New("restore activation volume set is incomplete")
		}
		return requireNoInFlight()
	case PhaseRollbackStarted:
		return requireNoInFlight()
	case PhaseRollbackVolumeStarted:
		if activatedCount == 0 ||
			inFlight != state.Activated[activatedCount-1] {
			return errors.New("restore activation rollback is not the next active volume")
		}
	case PhaseRollbackVolumeSucceeded:
		return requireNoInFlight()
	case PhaseRollbackRuntimeStarted, PhaseRollbackRuntimeSucceeded,
		PhaseRollbackActivationVerifyStarted,
		PhaseRollbackActivationVerifyDone, PhaseRollbackSucceeded:
		if activatedCount != 0 {
			return errors.New("restore activation rollback still has active volumes")
		}
		return requireNoInFlight()
	default:
		return errors.New("lifecycle mutation journal phase is invalid")
	}
	return nil
}

func validateRestoreActivationProgressTransition(
	currentPhase, nextPhase string,
	current RestoreActivationState,
	next RestoreActivationProgress,
) error {
	candidate := RestoreActivationState{
		Controls:         append([]backuplifecycle.RuntimeControl(nil), current.Controls...),
		Authority:        current.Authority,
		RollbackPrepared: next.RollbackPrepared,
		Activated:        next.Activated, InFlight: next.InFlight,
	}
	if err := validateRestoreActivationPhaseProgress(nextPhase, candidate); err != nil {
		return err
	}
	currentProgress := RestoreActivationProgress{
		RollbackPrepared: current.RollbackPrepared,
		Activated:        current.Activated, InFlight: current.InFlight,
	}
	unchanged := func() bool {
		return equalRestoreActivationProgress(currentProgress, next)
	}
	switch nextPhase {
	case PhaseRollbackCopyStarted:
		if !equalStrings(current.RollbackPrepared, next.RollbackPrepared) ||
			!equalStrings(current.Activated, next.Activated) ||
			current.InFlight != nil {
			return errors.New("restore activation rollback-copy start changed committed progress")
		}
	case PhaseRollbackCopySucceeded:
		if currentPhase != PhaseRollbackCopyStarted ||
			current.InFlight == nil || next.InFlight != nil ||
			!equalStrings(current.Activated, next.Activated) ||
			!appendedExact(
				current.RollbackPrepared, next.RollbackPrepared,
				current.InFlight.Volume,
			) {
			return errors.New("restore activation rollback-copy completion is not exact")
		}
	case PhaseActivationCopyStarted:
		if !equalStrings(current.RollbackPrepared, next.RollbackPrepared) ||
			!equalStrings(current.Activated, next.Activated) ||
			current.InFlight != nil {
			return errors.New("restore activation copy start changed committed progress")
		}
	case PhaseActivationCopySucceeded:
		if currentPhase != PhaseActivationCopyStarted ||
			current.InFlight == nil || next.InFlight != nil ||
			!equalStrings(current.RollbackPrepared, next.RollbackPrepared) ||
			!appendedExact(
				current.Activated, next.Activated,
				current.InFlight.Volume,
			) {
			return errors.New("restore activation copy completion is not exact")
		}
	case PhaseRollbackStarted:
		if !equalStrings(current.RollbackPrepared, next.RollbackPrepared) ||
			next.InFlight != nil {
			return errors.New("restore activation rollback start changed committed progress")
		}
		if currentPhase == PhaseActivationCopyStarted {
			if current.InFlight == nil ||
				!appendedExact(
					current.Activated, next.Activated,
					current.InFlight.Volume,
				) {
				return errors.New(
					"restore activation rollback did not conservatively mark the in-flight volume active",
				)
			}
		} else if !equalStrings(current.Activated, next.Activated) {
			return errors.New("restore activation rollback start changed committed progress")
		}
	case PhaseRollbackVolumeStarted:
		if !equalStrings(current.RollbackPrepared, next.RollbackPrepared) ||
			!equalStrings(current.Activated, next.Activated) ||
			current.InFlight != nil {
			return errors.New("restore activation volume rollback start changed committed progress")
		}
	case PhaseRollbackVolumeSucceeded:
		if currentPhase != PhaseRollbackVolumeStarted ||
			current.InFlight == nil || next.InFlight != nil ||
			!equalStrings(current.RollbackPrepared, next.RollbackPrepared) ||
			!removedExactTail(
				current.Activated, next.Activated,
				current.InFlight.Volume,
			) {
			return errors.New("restore activation volume rollback completion is not exact")
		}
	default:
		if !unchanged() {
			return errors.New("restore activation transition changed committed progress")
		}
	}
	return nil
}

func orderedPrefix(prefix, values []string) bool {
	if len(prefix) > len(values) {
		return false
	}
	for index := range prefix {
		if prefix[index] != values[index] {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	return len(left) == len(right) && orderedPrefix(left, right)
}

func equalRestoreActivationProgress(
	left, right RestoreActivationProgress,
) bool {
	if !equalStrings(left.RollbackPrepared, right.RollbackPrepared) ||
		!equalStrings(left.Activated, right.Activated) {
		return false
	}
	if left.InFlight == nil || right.InFlight == nil {
		return left.InFlight == nil && right.InFlight == nil
	}
	return left.InFlight.Volume == right.InFlight.Volume
}

func appendedExact(before, after []string, value string) bool {
	return len(after) == len(before)+1 &&
		orderedPrefix(before, after) && after[len(before)] == value
}

func removedExactTail(before, after []string, value string) bool {
	return len(before) == len(after)+1 &&
		orderedPrefix(after, before) && before[len(after)] == value
}

func canonicalDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && value == strings.ToLower(value)
}

func workspaceHash(workspace string) string {
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		absolute = filepath.Clean(workspace)
	}
	absolute = filepath.Clean(absolute)
	if runtime.GOOS == "windows" {
		absolute = strings.ToLower(absolute)
	}
	sum := sha256.Sum256([]byte(absolute))
	return "sha256:" + hex.EncodeToString(sum[:])
}
