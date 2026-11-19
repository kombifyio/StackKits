package lifecyclemutation

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"golang.org/x/mod/semver"
)

func (session *Session) Transition(expected, next string) error {
	return session.transition(expected, next, nil, nil)
}

func (session *Session) TransitionRestoreActivation(
	expectedPhase, nextPhase string,
	progress RestoreActivationProgress,
) error {
	if session == nil || session.record.Kind != KindRestoreActivation ||
		session.record.RestoreActivation == nil {
		return errors.New("active restore activation session is required")
	}
	progress.RollbackPrepared = append(
		[]string{}, progress.RollbackPrepared...,
	)
	progress.Activated = append([]string{}, progress.Activated...)
	if progress.InFlight != nil {
		inFlight := *progress.InFlight
		progress.InFlight = &inFlight
	}
	if err := validateRestoreActivationProgressTransition(
		session.record.Phase, nextPhase,
		*session.record.RestoreActivation, progress,
	); err != nil {
		return err
	}
	nextState := &RestoreActivationState{
		AppGroupClosures: append([]RestoreAppGroupClosure(nil), session.record.RestoreActivation.AppGroupClosures...),
		Controls:         append([]backuplifecycle.RuntimeControl(nil), session.record.RestoreActivation.Controls...),
		AppGroups:        backuplifecycle.CloneAppGroups(session.record.RestoreActivation.AppGroups),
		Authority:        session.record.RestoreActivation.Authority,
		RollbackPrepared: progress.RollbackPrepared,
		Activated:        progress.Activated, InFlight: progress.InFlight,
	}
	nextState.Authority.Volumes = append(
		[]string{}, nextState.Authority.Volumes...,
	)
	return session.transition(expectedPhase, nextPhase, nil, nextState)
}

func (session *Session) BeginJoin(
	expected, next, command, binaryVersion, executableSHA256 string,
) (string, error) {
	if !joinPhase(next) ||
		(command != "generate" && command != "apply" && command != "verify") ||
		strings.TrimSpace(binaryVersion) == "" ||
		!canonicalDigest(executableSHA256) {
		return "", errors.New("lifecycle mutation join authority is invalid")
	}
	expectedRelease := session.record.Target
	if strings.HasPrefix(next, "rollback-") {
		expectedRelease = session.record.Prior
	}
	if binaryVersion != expectedRelease.Version ||
		executableSHA256 != expectedRelease.ExecutableSHA256 {
		return "", errors.New(
			"lifecycle mutation join differs from signed release executor",
		)
	}
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", fmt.Errorf("create lifecycle mutation join nonce: %w", err)
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	join := &JoinAuthority{
		Command: command, BinaryVersion: binaryVersion,
		ExecutableSHA256: executableSHA256,
		NonceSHA256:      digestNonce(nonce),
	}
	if err := session.transition(expected, next, join, nil); err != nil {
		return "", err
	}
	// The new signed phase invalidates the abandoned child's nonce. Only then
	// hand off the recovery claim lock so this authorized child can claim it.
	if session.claimLock != nil {
		if err := session.claimLock.Release(); err != nil {
			return "", err
		}
		session.claimLock = nil
		session.claimantGone = false
	}
	return nonce, nil
}

// ForwardRecoveryPhase reports the phases after target Apply was admitted
// from which an owner may complete the target release forward instead of
// rolling it back.
func ForwardRecoveryPhase(phase string) bool {
	switch phase {
	case PhaseTargetApplyStarted, PhaseTargetApplySucceeded,
		PhaseTargetVerifyStarted, PhaseTargetVerifySucceeded,
		PhaseCommitStarted:
		return true
	default:
		return false
	}
}

// BeginForwardRecoveryJoin re-admits the exact signed target Apply of an
// upgrade interrupted after target Apply was admitted. Rerunning the target
// Apply is the documented resume of a killed Apply, and it never starts the
// prior runtime on data the target may already have changed. It requires
// explicit recovery custody: the shared lifecycle lock and the join-claim
// lock, with no live claimant of the current phase. The new signed phase
// invalidates the abandoned child's nonce before the claim lock is handed to
// the re-admitted child.
func (session *Session) BeginForwardRecoveryJoin(
	expected, binaryVersion, executableSHA256 string,
) (string, error) {
	if session == nil || session.transaction == nil || session.claimLock == nil ||
		session.record.Kind != KindUpgrade || session.record.Status != StatusActive ||
		session.record.UpgradeDataActivation != nil ||
		session.record.Phase != expected || !ForwardRecoveryPhase(expected) {
		return "", errors.New("forward recovery requires explicit recovery custody of an upgrade after target Apply admission")
	}
	session.forwardRecovery = true
	defer func() { session.forwardRecovery = false }()
	return session.BeginJoin(
		expected, PhaseTargetApplyStarted, "apply", binaryVersion, executableSHA256,
	)
}

// BeginForwardRetargetJoin admits a target Generate for forward completion of
// an upgrade whose history admitted target Apply. With a newer verified release
// it supersedes the signed target: the owner approves that when the signed
// target itself cannot finish the operation, for example because its own Apply
// is defective. With the signed target itself it regenerates after a failed
// retarget. The checkpoint and prior release stay the recovery authority. Only
// the signed or a strictly newer release from the verified workspace release
// cache is accepted, so live data the interrupted Apply may have changed is
// never opened by an older runtime. Operations whose signed target is the
// running executable (change sets, coordinated rollbacks) are refused.
// Custody requirements equal BeginForwardRecoveryJoin's.
func (session *Session) BeginForwardRetargetJoin(
	expected string,
	target ReleaseAuthority,
) (string, error) {
	if session == nil || session.transaction == nil || session.claimLock == nil ||
		session.record.Kind != KindUpgrade || session.record.Status != StatusActive ||
		session.record.UpgradeDataActivation != nil || session.record.Phase != expected {
		return "", errors.New("forward recovery requires explicit recovery custody of an upgrade after target Apply admission")
	}
	if admitted, err := session.ForwardRecoveryAdmitted(); err != nil || !admitted {
		return "", errors.Join(errors.New("forward recovery requires an upgrade whose signed history admitted target Apply"), err)
	}
	signedTarget := session.record.Target
	order := semver.Compare("v"+target.Version, "v"+signedTarget.Version)
	if signedTarget.Authority != "" || !semver.IsValid("v"+signedTarget.Version) ||
		target.Authority != "" || !validReleaseAuthority(target) ||
		order < 0 || (order == 0 && target != signedTarget) {
		return "", errors.New("a forward retarget requires the signed or a newer verified release from the workspace release cache")
	}
	session.record.Target = target
	session.forwardRecovery, session.forwardRetarget = true, true
	defer func() { session.forwardRecovery, session.forwardRetarget = false, false }()
	nonce, err := session.BeginJoin(
		expected, PhaseTargetGenerateStarted, "generate", target.Version, target.ExecutableSHA256,
	)
	if err != nil && session.record.Phase == expected {
		session.record.Target = signedTarget
	}
	return nonce, err
}

// forwardRecoveryTransition admits only BeginForwardRecoveryJoin's re-entry
// into target Apply, or BeginForwardRetargetJoin's into target Generate,
// while it holds explicit recovery custody. Both verified forward admission
// before setting forwardRecovery.
func (session *Session) forwardRecoveryTransition(expected, next string, join *JoinAuthority) bool {
	if !session.forwardRecovery || session.claimLock == nil ||
		session.record.Kind != KindUpgrade || session.record.UpgradeDataActivation != nil || join == nil {
		return false
	}
	if session.forwardRetarget {
		return (ForwardRecoveryPhase(expected) || expected == PhaseTargetGenerateStarted || expected == PhaseTargetGenerateSucceeded) &&
			next == PhaseTargetGenerateStarted && join.Command == "generate"
	}
	return ForwardRecoveryPhase(expected) && next == PhaseTargetApplyStarted && join.Command == "apply"
}

func (session *Session) transition(
	expected, next string,
	join *JoinAuthority,
	restoreActivation *RestoreActivationState,
) error {
	return session.transitionWithUpgradeData(
		expected, next, join, restoreActivation, nil,
	)
}

func (session *Session) transitionWithUpgradeData(
	expected, next string,
	join *JoinAuthority,
	restoreActivation *RestoreActivationState,
	upgradeDataActivation *UpgradeDataActivationState,
) error {
	if (expected == PhaseRollbackGenerateStarted || expected == PhaseRollbackGenerateDone) && next == PhaseRollbackStarted {
		if err := session.RequirePreApplyUpgradeRollback(); err != nil {
			return err
		}
		if session.claimLock == nil {
			return errors.New("rollback Generate restart requires exclusive unclaimed recovery custody")
		}
	}
	if session == nil || session.transaction == nil ||
		session.record.Status != StatusActive ||
		session.record.Phase != expected ||
		!(allowedTransition(session.record.Kind, expected, next) || session.forwardRecoveryTransition(expected, next, join) || (validRestoreControlTransition(session.record, expected, next, restoreActivation) || validRestoreAppGroupTransition(session.record, expected, next, restoreActivation))) {
		return errors.New("lifecycle mutation transition is not authorized")
	}
	if session.record.Kind == KindRestoreActivation {
		if join != nil || restoreActivation == nil || upgradeDataActivation != nil {
			return errors.New("restore activation transition state is required")
		}
	} else if restoreActivation != nil ||
		(upgradeDataActivation != nil && session.record.Kind != KindUpgrade) {
		return errors.New("restore activation state exists on an upgrade transition")
	}
	if session.record.UpgradeDataActivation != nil &&
		upgradeDataActivation == nil {
		return errors.New("upgrade data activation state must use its transition API")
	}
	if session.record.Kind == KindUpgrade && upgradeDataActivation == nil &&
		upgradeDataActivationPhase(next) && next != PhaseRollbackSucceeded {
		return errors.New("upgrade data activation phase requires its transition API")
	}
	if upgradeDataActivation != nil && join != nil {
		return errors.New("upgrade data activation cannot grant a child join authority")
	}
	current, digest, exists, err := loadRecord(session.workspace, session.transaction)
	if err != nil {
		return err
	}
	if !exists || current.OperationID != session.record.OperationID ||
		current.Sequence != session.record.Sequence ||
		current.Phase != expected || digest != session.recordDigest {
		return errors.New("lifecycle mutation journal changed before transition")
	}
	previous := session.record
	session.record.Phase = next
	session.record.Join = join
	if restoreActivation != nil {
		session.record.RestoreActivation = restoreActivation
	}
	if upgradeDataActivation != nil {
		state := cloneUpgradeDataActivationState(*upgradeDataActivation)
		session.record.UpgradeDataActivation = &state
	}
	session.record.Sequence++
	session.record.UpdatedAt = time.Now().UTC()
	nextDigest, err := persistRecord(
		session.workspace, session.transaction, session.view, &session.record,
		session.recordDigest,
	)
	if nextDigest != "" {
		session.recordDigest = nextDigest
	} else if err != nil {
		session.record = previous
	}
	return err
}

func (session *Session) Complete(status string) error {
	if session == nil || session.transaction == nil {
		return errors.New("active lifecycle mutation session is required")
	}
	if status != StatusSucceeded && status != StatusRecovered {
		return errors.New("lifecycle mutation terminal status is invalid")
	}
	if status == StatusSucceeded && session.record.Phase != PhaseCommitSucceeded {
		return errors.New("successful lifecycle mutation requires committed phase")
	}
	if status == StatusRecovered && session.record.Phase != PhaseRollbackSucceeded &&
		!(session.releasePrepared && session.record.Phase == PhasePrepared) {
		return errors.New("recovered lifecycle mutation requires verified rollback phase")
	}
	if session.record.Status == status {
		current, digest, exists, err := loadRecord(session.workspace, session.transaction)
		if err != nil || !exists || current.OperationID != session.record.OperationID || digest != session.recordDigest {
			return errors.Join(errors.New("terminal lifecycle mutation journal changed before completion retry"), err)
		}
		return nil
	}
	if session.record.Status != StatusActive {
		return errors.New("active lifecycle mutation session is required")
	}
	current, digest, exists, err := loadRecord(
		session.workspace, session.transaction,
	)
	if err != nil {
		return err
	}
	if !exists || current.OperationID != session.record.OperationID ||
		current.Sequence != session.record.Sequence ||
		current.Phase != session.record.Phase ||
		digest != session.recordDigest {
		return errors.New("lifecycle mutation journal changed before completion")
	}
	previous := session.record
	session.record.Status = status
	session.record.Join = nil
	session.record.Sequence++
	session.record.UpdatedAt = time.Now().UTC()
	nextDigest, err := persistRecord(
		session.workspace, session.transaction, session.view, &session.record,
		session.recordDigest,
	)
	if nextDigest != "" {
		session.recordDigest = nextDigest
	} else if err != nil {
		session.record = previous
	}
	return err
}

// ReconcileForRecovery repairs a stale active pointer from the immutable
// signed history while the caller still holds the lifecycle lock. A failed
// transition can therefore roll back in the same process when its history
// record was installed before the pointer write reported an error. Root and
// identity failures remain fail-closed in recoverLatestRecord.
func (session *Session) ReconcileForRecovery() error {
	if session == nil || session.transaction == nil || session.record.OperationID == "" {
		return errors.New("lifecycle mutation recovery reconciliation requires an active session")
	}
	record, digest, err := recoverLatestRecord(
		session.workspace, session.record.OperationID, session.transaction, session.view,
	)
	if err != nil {
		return err
	}
	if record.OperationID != session.record.OperationID {
		return errors.New("lifecycle mutation recovery reconciled a different operation")
	}
	session.record, session.recordDigest = record, digest
	return nil
}
