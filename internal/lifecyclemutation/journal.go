// Package lifecyclemutation owns the one local cross-process mutation
// authority shared by StackSpec authoring, generation, Apply, drift reconcile,
// and upgrade recovery.

package lifecyclemutation

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	APIVersion     = "stackkit.lifecycle-mutation/v1"
	journalRoot    = ".stackkit/lifecycle-mutations"
	activePath     = journalRoot + "/active.json"
	operationsRoot = journalRoot + "/operations"
	claimsRoot     = journalRoot + "/join-claims"
	maxRecordBytes = 1 << 20

	StatusActive    = "active"
	StatusSucceeded = "succeeded"
	StatusRecovered = "recovered"

	KindUpgrade           = "upgrade"
	KindRestoreActivation = "restore-activation"

	PhasePrepared                = "prepared"
	PhaseTargetGenerateStarted   = "target-generate-started"
	PhaseTargetGenerateSucceeded = "target-generate-succeeded"
	PhaseTargetApplyStarted      = "target-apply-started"
	PhaseTargetApplySucceeded    = "target-apply-succeeded"
	PhaseTargetVerifyStarted     = "target-verify-started"
	PhaseTargetVerifySucceeded   = "target-verify-succeeded"
	PhaseCommitStarted           = "commit-started"
	PhaseCommitSucceeded         = "commit-succeeded"
	PhaseRollbackStarted         = "rollback-started"
	PhaseRollbackGenerateStarted = "rollback-generate-started"
	PhaseRollbackGenerateDone    = "rollback-generate-succeeded"
	PhaseRollbackApplyStarted    = "rollback-apply-started"
	PhaseRollbackApplyDone       = "rollback-apply-succeeded"
	PhaseRollbackVerifyStarted   = "rollback-verify-started"
	PhaseRollbackVerifyDone      = "rollback-verify-succeeded"
	PhaseRollbackSucceeded       = "rollback-succeeded"

	PhaseQuiesceStarted                  = "quiesce-started"
	PhaseQuiesced                        = "quiesced"
	PhaseRollbackCopyStarted             = "rollback-copy-started"
	PhaseRollbackCopySucceeded           = "rollback-copy-succeeded"
	PhaseRollbackReady                   = "rollback-ready"
	PhaseActivationCopyStarted           = "activation-copy-started"
	PhaseActivationCopySucceeded         = "activation-copy-succeeded"
	PhaseActivationSucceeded             = "activation-succeeded"
	PhaseRuntimeStartStarted             = "runtime-start-started"
	PhaseRuntimeStartSucceeded           = "runtime-start-succeeded"
	PhaseVerifyStarted                   = "verify-started"
	PhaseVerifySucceeded                 = "verify-succeeded"
	PhaseRollbackVolumeStarted           = "rollback-volume-started"
	PhaseRollbackVolumeSucceeded         = "rollback-volume-succeeded"
	PhaseRollbackRuntimeStarted          = "rollback-runtime-started"
	PhaseRollbackRuntimeSucceeded        = "rollback-runtime-succeeded"
	PhaseRollbackActivationVerifyStarted = "rollback-verify-started"
	PhaseRollbackActivationVerifyDone    = "rollback-verify-succeeded"
)

var operationPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{7,127}$`)
var managedVolumePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)

// AuthorityRunningExecutable marks a release whose authority is the
// executable that runs the mutation itself: a same-release target on a host
// without a workspace release cache. It has no archive digest; the executable
// digest is its only identity. An empty Authority is a workspace release
// cache receipt, which always carries its archive digest.
const AuthorityRunningExecutable = "running-executable"

type ReleaseAuthority struct {
	Authority        string `json:"authority,omitempty"`
	Version          string `json:"version"`
	ArchiveSHA256    string `json:"archiveSha256"`
	ExecutableSHA256 string `json:"executableSha256"`
}

func validReleaseAuthority(release ReleaseAuthority) bool {
	if strings.TrimSpace(release.Version) == "" || !canonicalDigest(release.ExecutableSHA256) {
		return false
	}
	switch release.Authority {
	case "":
		return canonicalDigest(release.ArchiveSHA256)
	case AuthorityRunningExecutable:
		return release.ArchiveSHA256 == ""
	default:
		return false
	}
}

type CheckpointAuthority struct {
	ExecutorStateSnapshotID string `json:"executorStateSnapshotId"`
	KopiaAnchorID           string `json:"kopiaAnchorId"`
}

type RestoreActivationAuthority struct {
	OperationID          string   `json:"operationId"`
	OwnerRef             string   `json:"ownerRef"`
	RestoreResultID      string   `json:"restoreResultId"`
	SafetySnapshotID     string   `json:"safetySnapshotId"`
	PlanHash             string   `json:"planHash"`
	ManifestHash         string   `json:"manifestHash"`
	ApplyResultHash      string   `json:"applyResultHash"`
	ManagedVolumeSetHash string   `json:"managedVolumeSetHash"`
	Volumes              []string `json:"volumes"`
}

type RestoreActivationStep struct {
	Volume string `json:"volume"`
}

type RestoreActivationProgress struct {
	RollbackPrepared []string               `json:"rollbackPrepared"`
	Activated        []string               `json:"activated"`
	InFlight         *RestoreActivationStep `json:"inFlight,omitempty"`
}

type RestoreActivationState struct {
	AppGroupClosures []RestoreAppGroupClosure          `json:"appGroupClosures,omitempty"`
	AppGroups        []backuplifecycle.AppGroupControl `json:"appGroups,omitempty"`
	Controls         []backuplifecycle.RuntimeControl  `json:"controls,omitempty"`
	Authority        RestoreActivationAuthority        `json:"authority"`
	RollbackPrepared []string                          `json:"rollbackPrepared"`
	Activated        []string                          `json:"activated"`
	InFlight         *RestoreActivationStep            `json:"inFlight,omitempty"`
}

type Record struct {
	APIVersion            string                                        `json:"apiVersion"`
	OperationID           string                                        `json:"operationId"`
	Kind                  string                                        `json:"kind"`
	WorkspaceHash         string                                        `json:"workspaceHash"`
	OwnerRef              string                                        `json:"ownerRef"`
	Status                string                                        `json:"status"`
	Phase                 string                                        `json:"phase"`
	Sequence              uint64                                        `json:"sequence"`
	PreviousRecordDigest  string                                        `json:"previousRecordDigest,omitempty"`
	Checkpoint            CheckpointAuthority                           `json:"checkpoint"`
	Target                ReleaseAuthority                              `json:"target"`
	Prior                 ReleaseAuthority                              `json:"prior"`
	RestoreActivation     *RestoreActivationState                       `json:"restoreActivation,omitempty"`
	UpgradeDataActivation *UpgradeDataActivationState                   `json:"upgradeDataActivation,omitempty"`
	Join                  *JoinAuthority                                `json:"join,omitempty"`
	UpdatedAt             time.Time                                     `json:"updatedAt"`
	Signature             localevidence.OwnerLifecycleMutationSignature `json:"signature"`
}

type JoinAuthority struct {
	Command          string `json:"command"`
	BinaryVersion    string `json:"binaryVersion"`
	ExecutableSHA256 string `json:"executableSha256"`
	NonceSHA256      string `json:"nonceSha256"`
}

type BeginRequest struct {
	OperationID string
	OwnerRef    string
	Checkpoint  CheckpointAuthority
	Target      ReleaseAuthority
	Prior       ReleaseAuthority
}

type RestoreActivationBeginRequest struct {
	OperationID          string
	OwnerRef             string
	RestoreResultID      string
	SafetySnapshotID     string
	PlanHash             string
	ManifestHash         string
	ApplyResultHash      string
	ManagedVolumeSetHash string
	Volumes              []string
}

type JoinRequest struct {
	OperationID      string
	Phase            string
	BinaryVersion    string
	ExecutableSHA256 string
	Nonce            string
	Command          string
}

type Session struct {
	workspace    string
	root         *confinedfs.Root
	transaction  *confinedfs.Transaction
	lock         *confinedfs.OutputLock
	claimLock    *confinedfs.OutputLock
	view         confinedfs.View
	record       Record
	recordDigest string
	// claimantGone is set by explicit recovery when the current phase's child
	// claimed its join and recovery proved that claimant ended.
	claimantGone bool
	// forwardRecovery admits the one recovery-only re-entry into target Apply
	// for the duration of BeginForwardRecoveryJoin; with forwardRetarget it
	// admits BeginForwardRetargetJoin's re-entry into target Generate instead.
	forwardRecovery bool
	forwardRetarget bool
	// releasePrepared admits the one terminal completion of an upgrade that
	// never left its prepared phase, for the duration of
	// CompletePreparedRelease.
	releasePrepared bool
}

func (session *Session) Record() Record {
	if session == nil {
		return Record{}
	}
	record := session.record
	if session.record.RestoreActivation != nil {
		state := *session.record.RestoreActivation
		state.Controls = append([]backuplifecycle.RuntimeControl(nil), state.Controls...)
		state.AppGroups = backuplifecycle.CloneAppGroups(state.AppGroups)
		state.AppGroupClosures = append([]RestoreAppGroupClosure(nil), state.AppGroupClosures...)
		state.Authority.Volumes = append(
			[]string{}, session.record.RestoreActivation.Authority.Volumes...,
		)
		state.RollbackPrepared = append(
			[]string{}, session.record.RestoreActivation.RollbackPrepared...,
		)
		state.Activated = append(
			[]string{}, session.record.RestoreActivation.Activated...,
		)
		if session.record.RestoreActivation.InFlight != nil {
			inFlight := *session.record.RestoreActivation.InFlight
			state.InFlight = &inFlight
		}
		record.RestoreActivation = &state
	}
	if session.record.UpgradeDataActivation != nil {
		state := *session.record.UpgradeDataActivation
		state.Authority = cloneUpgradeDataActivationAuthority(
			session.record.UpgradeDataActivation.Authority,
		)
		state.CompletedPrefix = append(
			[]string{}, session.record.UpgradeDataActivation.CompletedPrefix...,
		)
		if session.record.UpgradeDataActivation.InFlight != nil {
			inFlight := *session.record.UpgradeDataActivation.InFlight
			state.InFlight = &inFlight
		}
		record.UpgradeDataActivation = &state
	}
	return record
}

func (session *Session) Close() (returnErr error) {
	if session == nil {
		return nil
	}
	if session.claimLock != nil {
		returnErr = errors.Join(returnErr, session.claimLock.Release())
		session.claimLock = nil
	}
	if session.lock != nil {
		returnErr = errors.Join(returnErr, session.lock.Release())
		session.lock = nil
	}
	if session.transaction != nil {
		returnErr = errors.Join(returnErr, session.transaction.Close())
		session.transaction = nil
	}
	if session.root != nil {
		returnErr = errors.Join(returnErr, session.root.Close())
		session.root = nil
	}
	return returnErr
}

func openLocked(workspace string) (*Session, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	transaction, err := root.BeginTransaction()
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	if err := transaction.MkdirAll(journalRoot, 0o700); err != nil {
		_ = transaction.Close()
		_ = root.Close()
		return nil, err
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(journalRoot)), true,
	); err != nil {
		_ = transaction.Close()
		_ = root.Close()
		return nil, err
	}
	if err := transaction.MkdirAll(operationsRoot, 0o700); err != nil {
		_ = transaction.Close()
		_ = root.Close()
		return nil, err
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(operationsRoot)), true,
	); err != nil {
		_ = transaction.Close()
		_ = root.Close()
		return nil, err
	}
	lock, err := transaction.TryAcquireOutputLock(journalRoot)
	if err != nil {
		_ = transaction.Close()
		_ = root.Close()
		return nil, fmt.Errorf("acquire exclusive lifecycle mutation lock: %w", err)
	}
	view, err := root.View(".")
	if err != nil {
		_ = lock.Release()
		_ = transaction.Close()
		_ = root.Close()
		return nil, err
	}
	return &Session{
		workspace: workspace, root: root, transaction: transaction,
		lock: lock, view: view,
	}, nil
}
