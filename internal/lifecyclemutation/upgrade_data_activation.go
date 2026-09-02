package lifecyclemutation

import (
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	// Upgrade data activation phases are deliberately outside the old
	// generate/apply/verify join phases. A prior executor cannot be admitted
	// while the current controller is copying data back into live volumes.
	PhaseRollbackDataQuiesceStarted          = "rollback-data-quiesce-started"
	PhaseRollbackDataQuiesced                = "rollback-data-quiesced"
	PhaseRollbackDataCopyStarted             = "rollback-data-copy-started"
	PhaseRollbackDataCopySucceeded           = "rollback-data-copy-succeeded"
	PhaseRollbackDataActivationSucceeded     = "rollback-data-activation-succeeded"
	PhaseRollbackPriorRuntimeStartStarted    = "rollback-prior-runtime-start-started"
	PhaseRollbackPriorRuntimeStartSucceeded  = "rollback-prior-runtime-start-succeeded"
	PhaseRollbackPriorRuntimeVerifyStarted   = "rollback-prior-runtime-verify-started"
	PhaseRollbackPriorRuntimeVerifySucceeded = "rollback-prior-runtime-verify-succeeded"
)

// UpgradeDataActivationAuthority is the immutable, owner-signed binding for
// post-Apply prior-data activation. The lifecycle record already carries the
// operation owner and release/checkpoint authority, so they are intentionally
// not duplicated here.
type UpgradeDataActivationAuthority struct {
	SourceGraphDigest    string   `json:"sourceGraphDigest"`
	TargetTopologyDigest string   `json:"targetTopologyDigest"`
	TargetCustodyDigest  string   `json:"targetCustodyDigest"`
	RestoreResultID      string   `json:"restoreResultId"`
	ManagedVolumeSetHash string   `json:"managedVolumeSetHash"`
	LiveVolumes          []string `json:"liveVolumes"`
}

// UpgradeDataActivationStep identifies the one volume whose copy may have
// started before a process crash. Retrying that step is deliberately allowed.
type UpgradeDataActivationStep struct {
	Volume string `json:"volume"`
}

// UpgradeDataActivationProgress is the caller's next signed progress value.
// CompletedPrefix is the exact ordered prefix whose copies completed;
// InFlight is persisted before a copy starts and remains until its completion
// transition is committed.
type UpgradeDataActivationProgress struct {
	CompletedPrefix []string                   `json:"completedPrefix"`
	InFlight        *UpgradeDataActivationStep `json:"inFlight,omitempty"`
}

// UpgradeDataActivationState is stored additively on a KindUpgrade record
// only once post-Apply data recovery actually begins. Keeping it nil before
// then preserves the old record shape for older controllers.
type UpgradeDataActivationState struct {
	Authority       UpgradeDataActivationAuthority `json:"authority"`
	CompletedPrefix []string                       `json:"completedPrefix"`
	InFlight        *UpgradeDataActivationStep     `json:"inFlight,omitempty"`
}

// BeginUpgradeDataActivation records the source/target custody binding and
// enters the quiescence phase under the already-held KindUpgrade lock.
func (session *Session) BeginUpgradeDataActivation(
	authority UpgradeDataActivationAuthority,
) error {
	if session == nil || session.transaction == nil ||
		session.record.Kind != KindUpgrade ||
		session.record.Status != StatusActive ||
		!upgradeDataActivationStartPhase(session.record.Phase) ||
		session.record.UpgradeDataActivation != nil {
		return errors.New("active upgrade session is required before data activation")
	}
	state := UpgradeDataActivationState{
		Authority:       cloneUpgradeDataActivationAuthority(authority),
		CompletedPrefix: []string{},
	}
	if err := validateUpgradeDataActivationStateForPhase(
		PhaseRollbackDataQuiesceStarted, state,
	); err != nil {
		return err
	}
	return session.transitionWithUpgradeData(
		session.record.Phase, PhaseRollbackDataQuiesceStarted,
		nil, nil, &state,
	)
}

func upgradeDataActivationStartPhase(phase string) bool {
	switch phase {
	case PhaseTargetApplyStarted, PhaseTargetApplySucceeded,
		PhaseTargetVerifyStarted, PhaseTargetVerifySucceeded, PhaseCommitStarted:
		return true
	default:
		return false
	}
}

// TransitionUpgradeDataActivation persists one exact, resumable phase under
// the same signed KindUpgrade journal. It never creates a second journal or a
// child-executor join authority.
func (session *Session) TransitionUpgradeDataActivation(
	expectedPhase, nextPhase string,
	progress UpgradeDataActivationProgress,
) error {
	if session == nil || session.transaction == nil ||
		session.record.Kind != KindUpgrade ||
		session.record.UpgradeDataActivation == nil {
		return errors.New("active upgrade data activation state is required")
	}
	current := *session.record.UpgradeDataActivation
	if err := validateUpgradeDataActivationProgressTransition(
		expectedPhase, nextPhase, current, progress,
	); err != nil {
		return err
	}
	next := &UpgradeDataActivationState{
		Authority:       cloneUpgradeDataActivationAuthority(current.Authority),
		CompletedPrefix: cloneUpgradeDataActivationStrings(progress.CompletedPrefix),
	}
	if progress.InFlight != nil {
		inFlight := *progress.InFlight
		next.InFlight = &inFlight
	}
	return session.transitionWithUpgradeData(
		expectedPhase, nextPhase, nil, nil, next,
	)
}

func cloneUpgradeDataActivationAuthority(
	authority UpgradeDataActivationAuthority,
) UpgradeDataActivationAuthority {
	authority.LiveVolumes = append([]string(nil), authority.LiveVolumes...)
	return authority
}

func cloneUpgradeDataActivationState(
	state UpgradeDataActivationState,
) UpgradeDataActivationState {
	state.Authority = cloneUpgradeDataActivationAuthority(state.Authority)
	state.CompletedPrefix = cloneUpgradeDataActivationStrings(state.CompletedPrefix)
	if state.InFlight != nil {
		inFlight := *state.InFlight
		state.InFlight = &inFlight
	}
	return state
}

func cloneUpgradeDataActivationStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func validateUpgradeDataActivationAuthority(
	authority UpgradeDataActivationAuthority,
) error {
	for _, value := range []string{
		authority.SourceGraphDigest,
		authority.TargetTopologyDigest,
		authority.TargetCustodyDigest,
		authority.RestoreResultID,
		authority.ManagedVolumeSetHash,
	} {
		if !canonicalDigest(value) {
			return errors.New("upgrade data activation authority digest is invalid")
		}
	}
	if len(authority.LiveVolumes) == 0 {
		return errors.New("upgrade data activation authority has no live volumes")
	}
	seen := make(map[string]struct{}, len(authority.LiveVolumes))
	previous := ""
	for index, volume := range authority.LiveVolumes {
		if !managedVolumePattern.MatchString(volume) ||
			(index > 0 && volume <= previous) {
			return errors.New("upgrade data activation live volumes are not a sorted portable set")
		}
		if _, duplicate := seen[volume]; duplicate {
			return fmt.Errorf("upgrade data activation live volume %q is duplicated", volume)
		}
		seen[volume] = struct{}{}
		previous = volume
	}
	digest, err := resolvedplan.CanonicalSHA256(authority.LiveVolumes)
	if err != nil || digest != authority.ManagedVolumeSetHash {
		return errors.New("upgrade data activation volume set differs from its digest")
	}
	return nil
}

func upgradeDataActivationPhase(phase string) bool {
	switch phase {
	case PhaseRollbackDataQuiesceStarted,
		PhaseRollbackDataQuiesced,
		PhaseRollbackDataCopyStarted,
		PhaseRollbackDataCopySucceeded,
		PhaseRollbackDataActivationSucceeded,
		PhaseRollbackPriorRuntimeStartStarted,
		PhaseRollbackPriorRuntimeStartSucceeded,
		PhaseRollbackPriorRuntimeVerifyStarted,
		PhaseRollbackPriorRuntimeVerifySucceeded,
		PhaseRollbackSucceeded:
		return true
	default:
		return false
	}
}

func validateUpgradeDataActivationStateForPhase(
	phase string,
	state UpgradeDataActivationState,
) error {
	if err := validateUpgradeDataActivationAuthority(state.Authority); err != nil {
		return err
	}
	if state.CompletedPrefix == nil ||
		!orderedPrefix(state.CompletedPrefix, state.Authority.LiveVolumes) {
		return errors.New("upgrade data activation completed prefix is invalid")
	}
	if state.InFlight != nil {
		if len(state.CompletedPrefix) >= len(state.Authority.LiveVolumes) ||
			state.InFlight.Volume != state.Authority.LiveVolumes[len(state.CompletedPrefix)] {
			return errors.New("upgrade data activation in-flight volume is not the next live volume")
		}
	}
	switch phase {
	case PhaseRollbackDataQuiesceStarted, PhaseRollbackDataQuiesced:
		if len(state.CompletedPrefix) != 0 || state.InFlight != nil {
			return errors.New("upgrade data activation progressed before quiescence")
		}
	case PhaseRollbackDataCopyStarted:
		if state.InFlight == nil {
			return errors.New("upgrade data activation copy lacks an in-flight volume")
		}
	case PhaseRollbackDataCopySucceeded:
		if len(state.CompletedPrefix) == 0 || state.InFlight != nil {
			return errors.New("upgrade data activation copy completion is invalid")
		}
	case PhaseRollbackDataActivationSucceeded,
		PhaseRollbackPriorRuntimeStartStarted,
		PhaseRollbackPriorRuntimeStartSucceeded,
		PhaseRollbackPriorRuntimeVerifyStarted,
		PhaseRollbackPriorRuntimeVerifySucceeded,
		PhaseRollbackSucceeded:
		if len(state.CompletedPrefix) != len(state.Authority.LiveVolumes) ||
			state.InFlight != nil {
			return errors.New("upgrade data activation is incomplete before runtime use")
		}
	default:
		return errors.New("upgrade data activation phase is invalid")
	}
	return nil
}

func validateUpgradeDataActivationState(record Record) error {
	if record.UpgradeDataActivation == nil || !upgradeDataActivationPhase(record.Phase) {
		return errors.New("upgrade data activation state is not bound to its phase")
	}
	return validateUpgradeDataActivationStateForPhase(
		record.Phase, *record.UpgradeDataActivation,
	)
}

func validateUpgradeDataActivationProgressTransition(
	currentPhase, nextPhase string,
	current UpgradeDataActivationState,
	next UpgradeDataActivationProgress,
) error {
	candidate := UpgradeDataActivationState{
		Authority:       current.Authority,
		CompletedPrefix: cloneUpgradeDataActivationStrings(next.CompletedPrefix),
	}
	if next.InFlight != nil {
		inFlight := *next.InFlight
		candidate.InFlight = &inFlight
	}
	if err := validateUpgradeDataActivationStateForPhase(nextPhase, candidate); err != nil {
		return err
	}
	currentProgress := UpgradeDataActivationProgress{
		CompletedPrefix: current.CompletedPrefix,
		InFlight:        current.InFlight,
	}
	equalProgress := func() bool {
		if !equalStrings(currentProgress.CompletedPrefix, next.CompletedPrefix) {
			return false
		}
		if currentProgress.InFlight == nil || next.InFlight == nil {
			return currentProgress.InFlight == nil && next.InFlight == nil
		}
		return currentProgress.InFlight.Volume == next.InFlight.Volume
	}
	switch nextPhase {
	case PhaseRollbackDataQuiesced:
		if currentPhase != PhaseRollbackDataQuiesceStarted || !equalProgress() {
			return errors.New("upgrade data activation quiescence transition changed progress")
		}
	case PhaseRollbackDataCopyStarted:
		if (currentPhase != PhaseRollbackDataQuiesced &&
			currentPhase != PhaseRollbackDataCopySucceeded) ||
			!equalStrings(current.CompletedPrefix, next.CompletedPrefix) ||
			current.InFlight != nil {
			return errors.New("upgrade data activation copy start changed progress")
		}
	case PhaseRollbackDataCopySucceeded:
		if currentPhase != PhaseRollbackDataCopyStarted ||
			current.InFlight == nil || next.InFlight != nil ||
			!appendedExact(current.CompletedPrefix, next.CompletedPrefix, current.InFlight.Volume) {
			return errors.New("upgrade data activation copy completion is not exact")
		}
	case PhaseRollbackDataActivationSucceeded:
		if currentPhase != PhaseRollbackDataCopySucceeded || !equalProgress() {
			return errors.New("upgrade data activation completion changed progress")
		}
	case PhaseRollbackPriorRuntimeStartStarted,
		PhaseRollbackPriorRuntimeStartSucceeded,
		PhaseRollbackPriorRuntimeVerifyStarted,
		PhaseRollbackPriorRuntimeVerifySucceeded,
		PhaseRollbackSucceeded:
		if !equalProgress() {
			return errors.New("upgrade data activation runtime transition changed progress")
		}
		previous := map[string]string{
			PhaseRollbackPriorRuntimeStartStarted:    PhaseRollbackDataActivationSucceeded,
			PhaseRollbackPriorRuntimeStartSucceeded:  PhaseRollbackPriorRuntimeStartStarted,
			PhaseRollbackPriorRuntimeVerifyStarted:   PhaseRollbackPriorRuntimeStartSucceeded,
			PhaseRollbackPriorRuntimeVerifySucceeded: PhaseRollbackPriorRuntimeVerifyStarted,
			PhaseRollbackSucceeded:                   PhaseRollbackPriorRuntimeVerifySucceeded,
		}[nextPhase]
		if currentPhase != previous {
			return errors.New("upgrade data activation runtime phase order is invalid")
		}
	default:
		return errors.New("upgrade data activation transition is invalid")
	}
	return nil
}
