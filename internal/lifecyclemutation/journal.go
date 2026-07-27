// Package lifecyclemutation owns the one local cross-process mutation
// authority shared by StackSpec authoring, generation, Apply, drift reconcile,
// and upgrade recovery.
package lifecyclemutation

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	APIVersion     = "stackkit.lifecycle-mutation/v1"
	journalRoot    = ".stackkit/lifecycle-mutations"
	activePath     = journalRoot + "/active.json"
	operationsRoot = journalRoot + "/operations"
	claimsRoot     = journalRoot + "/join-claims"

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

type ReleaseAuthority struct {
	Version          string `json:"version"`
	ArchiveSHA256    string `json:"archiveSha256"`
	ExecutableSHA256 string `json:"executableSha256"`
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
	Authority        RestoreActivationAuthority `json:"authority"`
	RollbackPrepared []string                   `json:"rollbackPrepared"`
	Activated        []string                   `json:"activated"`
	InFlight         *RestoreActivationStep     `json:"inFlight,omitempty"`
}

type Record struct {
	APIVersion           string                                        `json:"apiVersion"`
	OperationID          string                                        `json:"operationId"`
	Kind                 string                                        `json:"kind"`
	WorkspaceHash        string                                        `json:"workspaceHash"`
	OwnerRef             string                                        `json:"ownerRef"`
	Status               string                                        `json:"status"`
	Phase                string                                        `json:"phase"`
	Sequence             uint64                                        `json:"sequence"`
	PreviousRecordDigest string                                        `json:"previousRecordDigest,omitempty"`
	Checkpoint           CheckpointAuthority                           `json:"checkpoint"`
	Target               ReleaseAuthority                              `json:"target"`
	Prior                ReleaseAuthority                              `json:"prior"`
	RestoreActivation    *RestoreActivationState                       `json:"restoreActivation,omitempty"`
	Join                 *JoinAuthority                                `json:"join,omitempty"`
	UpdatedAt            time.Time                                     `json:"updatedAt"`
	Signature            localevidence.OwnerLifecycleMutationSignature `json:"signature"`
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
}

func BeginUpgrade(workspace string, request BeginRequest) (*Session, error) {
	return BeginUpgradePrepared(workspace, func() (BeginRequest, error) {
		return request, nil
	})
}

// BeginUpgradePrepared holds the shared lifecycle lock while the caller
// prepares the exact checkpoint/release authority and persists the first
// signed journal record without a checkpoint-to-Begin race.
func BeginUpgradePrepared(
	workspace string,
	prepare func() (BeginRequest, error),
) (*Session, error) {
	if prepare == nil {
		return nil, errors.New("lifecycle mutation preparation callback is required")
	}
	session, err := openLocked(workspace)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*Session, error) {
		return nil, errors.Join(cause, session.Close())
	}
	current, _, exists, err := loadRecord(workspace, session.transaction)
	if err != nil {
		return fail(err)
	}
	if exists && current.Status == StatusActive {
		return fail(fmt.Errorf(
			"lifecycle mutation %s is active at phase %s; recover it explicitly",
			current.OperationID, current.Phase,
		))
	}
	request, err := prepare()
	if err != nil {
		return fail(err)
	}
	if !operationPattern.MatchString(request.OperationID) {
		return fail(errors.New("lifecycle mutation operation ID is invalid"))
	}
	operationRoot := operationsRoot + "/" + request.OperationID
	used, _, err := session.transaction.Exists(operationRoot)
	if err != nil {
		return fail(err)
	}
	if used {
		return fail(errors.New("lifecycle mutation operation ID is already used"))
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return fail(fmt.Errorf("load lifecycle mutation Owner: %w", err))
	}
	record := Record{
		APIVersion: APIVersion, OperationID: request.OperationID,
		Kind: KindUpgrade, WorkspaceHash: workspaceHash(workspace),
		OwnerRef: request.OwnerRef, Status: StatusActive, Phase: PhasePrepared,
		Sequence: 1, Checkpoint: request.Checkpoint, Target: request.Target,
		Prior: request.Prior, UpdatedAt: time.Now().UTC(),
	}
	if record.OwnerRef != owner.OwnerRef {
		return fail(errors.New("lifecycle mutation Owner differs from local custody"))
	}
	if err := validateRecord(record); err != nil {
		return fail(err)
	}
	digest, err := persistRecord(
		workspace, session.transaction, session.view, &record, "",
	)
	if err != nil {
		return fail(err)
	}
	session.record = record
	session.recordDigest = digest
	return session, nil
}

// BeginRestoreActivationPrepared holds the shared lifecycle lock while the
// caller prepares the immutable restore and safety-snapshot authority.
func BeginRestoreActivationPrepared(
	workspace string,
	prepare func() (RestoreActivationBeginRequest, error),
) (*Session, error) {
	if prepare == nil {
		return nil, errors.New("restore activation preparation callback is required")
	}
	session, err := openLocked(workspace)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*Session, error) {
		return nil, errors.Join(cause, session.Close())
	}
	current, _, exists, err := loadRecord(workspace, session.transaction)
	if err != nil {
		return fail(err)
	}
	if exists && current.Status == StatusActive {
		return fail(fmt.Errorf(
			"lifecycle mutation %s is active at phase %s; recover it explicitly",
			current.OperationID, current.Phase,
		))
	}
	request, err := prepare()
	if err != nil {
		return fail(err)
	}
	if !operationPattern.MatchString(request.OperationID) {
		return fail(errors.New("lifecycle mutation operation ID is invalid"))
	}
	operationRoot := operationsRoot + "/" + request.OperationID
	used, _, err := session.transaction.Exists(operationRoot)
	if err != nil {
		return fail(err)
	}
	if used {
		return fail(errors.New("lifecycle mutation operation ID is already used"))
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return fail(fmt.Errorf("load lifecycle mutation Owner: %w", err))
	}
	authority := RestoreActivationAuthority{
		OperationID: request.OperationID, OwnerRef: request.OwnerRef,
		RestoreResultID:  request.RestoreResultID,
		SafetySnapshotID: request.SafetySnapshotID,
		PlanHash:         request.PlanHash, ManifestHash: request.ManifestHash,
		ApplyResultHash:      request.ApplyResultHash,
		ManagedVolumeSetHash: request.ManagedVolumeSetHash,
		Volumes:              append([]string{}, request.Volumes...),
	}
	record := Record{
		APIVersion: APIVersion, OperationID: request.OperationID,
		Kind: KindRestoreActivation, WorkspaceHash: workspaceHash(workspace),
		OwnerRef: request.OwnerRef, Status: StatusActive, Phase: PhasePrepared,
		Sequence: 1, UpdatedAt: time.Now().UTC(),
		RestoreActivation: &RestoreActivationState{
			Authority: authority, RollbackPrepared: []string{},
			Activated: []string{},
		},
	}
	if record.OwnerRef != owner.OwnerRef {
		return fail(errors.New("lifecycle mutation Owner differs from local custody"))
	}
	if err := validateRecord(record); err != nil {
		return fail(err)
	}
	digest, err := persistRecord(
		workspace, session.transaction, session.view, &record, "",
	)
	if err != nil {
		return fail(err)
	}
	session.record = record
	session.recordDigest = digest
	return session, nil
}

// OpenRecovery acquires the shared lock and accepts only the exact active,
// signed operation. It never advances or resumes the target path.
func OpenRecovery(workspace, operationID string) (*Session, Record, error) {
	session, err := openLocked(workspace)
	if err != nil {
		return nil, Record{}, err
	}
	fail := func(cause error) (*Session, Record, error) {
		return nil, Record{}, errors.Join(cause, session.Close())
	}
	record, digest, exists, err := loadRecord(workspace, session.transaction)
	if err != nil || !exists || record.OperationID != operationID {
		record, digest, err = recoverLatestRecord(
			workspace, operationID, session.transaction, session.view,
		)
		if err != nil {
			return fail(err)
		}
		exists = true
	}
	if !exists || record.OperationID != operationID {
		return fail(errors.New("exact lifecycle mutation is required for explicit recovery"))
	}
	session.record = record
	session.recordDigest = digest
	if err := session.acquireRecoveryClaimLock(); err != nil {
		return fail(err)
	}
	return session, session.Record(), nil
}

func OpenRestoreActivationRecovery(
	workspace, operationID string,
) (*Session, Record, error) {
	session, record, err := OpenRecovery(workspace, operationID)
	if err != nil {
		return nil, Record{}, err
	}
	if record.Kind != KindRestoreActivation {
		return nil, Record{}, errors.Join(
			errors.New("exact restore activation is required for explicit recovery"),
			session.Close(),
		)
	}
	return session, record, nil
}

func RequireIdle(workspace string) error {
	if _, err := os.Lstat(
		filepath.Join(workspace, filepath.FromSlash(journalRoot)),
	); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect lifecycle mutation root: %w", err)
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer transaction.Close()
	record, _, exists, err := loadRecord(workspace, transaction)
	if err != nil {
		return err
	}
	if exists && record.Status == StatusActive {
		return fmt.Errorf(
			"lifecycle mutation %s is active at phase %s; ordinary mutation is denied",
			record.OperationID, record.Phase,
		)
	}
	return nil
}

// WithIdleMutation holds the same lock as an upgrade Session across the exact
// ordinary mutation boundary. A joined upgrade child is admitted from the
// signed parent journal and deliberately does not reacquire the parent's lock.
func WithIdleMutation(
	workspace string,
	join JoinRequest,
	execute func() error,
) error {
	if execute == nil {
		return errors.New("lifecycle mutation callback is required")
	}
	if strings.TrimSpace(join.OperationID) != "" ||
		strings.TrimSpace(join.Phase) != "" {
		if err := AdmitJoin(workspace, join); err != nil {
			return err
		}
		return execute()
	}
	session, err := openLocked(workspace)
	if err != nil {
		return err
	}
	defer session.Close()
	record, _, exists, err := loadRecord(workspace, session.transaction)
	if err != nil {
		return err
	}
	if exists && record.Status == StatusActive {
		return fmt.Errorf(
			"lifecycle mutation %s is active at phase %s; ordinary mutation is denied",
			record.OperationID, record.Phase,
		)
	}
	return execute()
}

// InspectJoin validates a signed child admission without consuming its
// one-use nonce. It is safe for pre-observability admission only.
func InspectJoin(workspace string, request JoinRequest) error {
	return inspectJoin(workspace, request, false)
}

// AdmitJoin validates and atomically consumes the exact one-use child nonce.
func AdmitJoin(workspace string, request JoinRequest) error {
	return inspectJoin(workspace, request, true)
}

func inspectJoin(workspace string, request JoinRequest, claim bool) error {
	if strings.TrimSpace(request.OperationID) == "" &&
		strings.TrimSpace(request.Phase) == "" {
		return RequireIdle(workspace)
	}
	if !operationPattern.MatchString(request.OperationID) ||
		strings.TrimSpace(request.Phase) == "" {
		return errors.New("internal lifecycle join requires an exact operation ID and phase")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer transaction.Close()
	record, _, exists, err := loadRecord(workspace, transaction)
	if err != nil {
		return err
	}
	if !exists || record.Status != StatusActive ||
		record.OperationID != request.OperationID ||
		record.Phase != request.Phase {
		return errors.New("internal lifecycle join differs from the exact current signed operation")
	}
	expectedVersion, expectedCommand := joinAuthority(record)
	if request.BinaryVersion != expectedVersion ||
		request.Command != expectedCommand ||
		record.Join == nil ||
		request.BinaryVersion != record.Join.BinaryVersion ||
		request.Command != record.Join.Command ||
		request.ExecutableSHA256 != record.Join.ExecutableSHA256 ||
		digestNonce(request.Nonce) != record.Join.NonceSHA256 {
		return errors.New("internal lifecycle join binary version or command is not authorized")
	}
	if !claim {
		return nil
	}
	return claimJoin(workspace, root, transaction, record)
}

func joinAuthority(record Record) (string, string) {
	switch record.Phase {
	case PhaseTargetGenerateStarted:
		return record.Target.Version, "generate"
	case PhaseTargetApplyStarted:
		return record.Target.Version, "apply"
	case PhaseTargetVerifyStarted:
		return record.Target.Version, "verify"
	case PhaseRollbackGenerateStarted:
		return record.Prior.Version, "generate"
	case PhaseRollbackApplyStarted:
		return record.Prior.Version, "apply"
	case PhaseRollbackVerifyStarted:
		return record.Prior.Version, "verify"
	default:
		return "", ""
	}
}

type joinClaim struct {
	APIVersion       string    `json:"apiVersion"`
	OperationID      string    `json:"operationId"`
	Sequence         uint64    `json:"sequence"`
	Phase            string    `json:"phase"`
	Command          string    `json:"command"`
	NonceSHA256      string    `json:"nonceSha256"`
	ExecutableSHA256 string    `json:"executableSha256"`
	ClaimedAt        time.Time `json:"claimedAt"`
}

func claimJoin(
	workspace string,
	root *confinedfs.Root,
	transaction *confinedfs.Transaction,
	record Record,
) (returnErr error) {
	if root == nil || transaction == nil || record.Join == nil {
		return errors.New("lifecycle mutation join claim authority is incomplete")
	}
	operationRoot := claimsRoot + "/" + record.OperationID
	if err := transaction.MkdirAll(claimsRoot, 0o700); err != nil {
		return err
	}
	if err := transaction.MkdirAll(operationRoot, 0o700); err != nil {
		return err
	}
	for _, relative := range []string{claimsRoot, operationRoot} {
		if err := backupcustody.ProtectPrivatePath(
			filepath.Join(workspace, filepath.FromSlash(relative)), true,
		); err != nil {
			return err
		}
	}
	lock, err := transaction.TryAcquireOutputLock(operationRoot)
	if err != nil {
		return fmt.Errorf("acquire lifecycle join claim lock: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, lock.Release()) }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	claim := joinClaim{
		APIVersion: APIVersion + "/join-claim", OperationID: record.OperationID,
		Sequence: record.Sequence, Phase: record.Phase,
		Command: record.Join.Command, NonceSHA256: record.Join.NonceSHA256,
		ExecutableSHA256: record.Join.ExecutableSHA256, ClaimedAt: time.Now().UTC(),
	}
	encoded, err := resolvedplan.CanonicalJSON(claim)
	if err != nil {
		return err
	}
	path := joinClaimPath(record)
	if _, err := view.WriteAtomic0600NoReplace(path, encoded); err != nil {
		return errors.New("lifecycle mutation join nonce is already claimed")
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(path)), false,
	); err != nil {
		return err
	}
	return nil
}

func (session *Session) acquireRecoveryClaimLock() error {
	if session == nil || session.transaction == nil ||
		session.record.OperationID == "" {
		return errors.New("explicit recovery claim-lock authority is incomplete")
	}
	operationRoot := claimsRoot + "/" + session.record.OperationID
	if err := session.transaction.MkdirAll(claimsRoot, 0o700); err != nil {
		return err
	}
	if err := session.transaction.MkdirAll(operationRoot, 0o700); err != nil {
		return err
	}
	for _, relative := range []string{claimsRoot, operationRoot} {
		if err := backupcustody.ProtectPrivatePath(
			filepath.Join(session.workspace, filepath.FromSlash(relative)), true,
		); err != nil {
			return err
		}
	}
	lock, err := session.transaction.TryAcquireOutputLock(operationRoot)
	if err != nil {
		return fmt.Errorf(
			"acquire exclusive explicit-recovery join-claim lock: %w", err,
		)
	}
	session.claimLock = lock
	if joinPhase(session.record.Phase) {
		claimPath := joinClaimPath(session.record)
		exists, _, inspectErr := session.transaction.Exists(claimPath)
		if inspectErr != nil {
			return inspectErr
		}
		if exists {
			return errors.New(
				"explicit lifecycle recovery refuses a claimed child phase",
			)
		}
	}
	return nil
}

func joinClaimPath(record Record) string {
	return fmt.Sprintf(
		"%s/%s/%020d-%s.json",
		claimsRoot, record.OperationID, record.Sequence,
		strings.TrimPrefix(record.Join.NonceSHA256, "sha256:"),
	)
}

func digestNonce(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// CurrentExecutableSHA256 hashes the bytes of the currently executing binary;
// hidden flags cannot supply or substitute this authority.
func CurrentExecutableSHA256() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve current lifecycle executable: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read current lifecycle executable: %w", err)
	}
	if len(raw) == 0 || len(raw) > 512<<20 {
		return "", errors.New("current lifecycle executable bytes are invalid")
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func joinPhase(phase string) bool {
	switch phase {
	case PhaseTargetGenerateStarted, PhaseTargetApplyStarted,
		PhaseTargetVerifyStarted, PhaseRollbackGenerateStarted,
		PhaseRollbackApplyStarted, PhaseRollbackVerifyStarted:
		return true
	default:
		return false
	}
}

func (session *Session) Record() Record {
	if session == nil {
		return Record{}
	}
	record := session.record
	if session.record.RestoreActivation != nil {
		state := *session.record.RestoreActivation
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
	return record
}

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
	return nonce, nil
}

func (session *Session) transition(
	expected, next string,
	join *JoinAuthority,
	restoreActivation *RestoreActivationState,
) error {
	if session == nil || session.transaction == nil ||
		session.record.Status != StatusActive ||
		session.record.Phase != expected ||
		!allowedTransition(session.record.Kind, expected, next) {
		return errors.New("lifecycle mutation transition is not authorized")
	}
	if session.record.Kind == KindRestoreActivation {
		if join != nil || restoreActivation == nil {
			return errors.New("restore activation transition state is required")
		}
	} else if restoreActivation != nil {
		return errors.New("restore activation state exists on an upgrade transition")
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
	session.record.Phase = next
	session.record.Join = join
	if restoreActivation != nil {
		session.record.RestoreActivation = restoreActivation
	}
	session.record.Sequence++
	session.record.UpdatedAt = time.Now().UTC()
	nextDigest, err := persistRecord(
		session.workspace, session.transaction, session.view, &session.record,
		session.recordDigest,
	)
	if err == nil {
		session.recordDigest = nextDigest
	}
	return err
}

func (session *Session) Complete(status string) error {
	if session == nil || session.transaction == nil ||
		session.record.Status != StatusActive {
		return errors.New("active lifecycle mutation session is required")
	}
	if status != StatusSucceeded && status != StatusRecovered {
		return errors.New("lifecycle mutation terminal status is invalid")
	}
	if status == StatusSucceeded && session.record.Phase != PhaseCommitSucceeded {
		return errors.New("successful lifecycle mutation requires committed phase")
	}
	if status == StatusRecovered && session.record.Phase != PhaseRollbackSucceeded {
		return errors.New("recovered lifecycle mutation requires verified rollback phase")
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
	session.record.Status = status
	session.record.Join = nil
	session.record.Sequence++
	session.record.UpdatedAt = time.Now().UTC()
	nextDigest, err := persistRecord(
		session.workspace, session.transaction, session.view, &session.record,
		session.recordDigest,
	)
	if err == nil {
		session.recordDigest = nextDigest
	}
	return err
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

func loadRecord(
	workspace string,
	transaction *confinedfs.Transaction,
) (Record, string, bool, error) {
	exists, info, err := transaction.Exists(activePath)
	if err != nil {
		return Record{}, "", false, err
	}
	if !exists {
		active, err := activeHistoryHeads(workspace, transaction)
		if err != nil {
			return Record{}, "", false, err
		}
		if len(active) != 0 {
			return Record{}, "", false, errors.New(
				"lifecycle mutation has active orphan history without a current pointer",
			)
		}
		return Record{}, "", false, nil
	}
	if !info.Mode().IsRegular() {
		return Record{}, "", false, errors.New("lifecycle mutation journal is not a private regular file")
	}
	if err := backupcustody.RequirePrivatePath(
		filepath.Join(workspace, filepath.FromSlash(activePath)), false,
	); err != nil {
		return Record{}, "", false, fmt.Errorf(
			"verify private lifecycle mutation journal: %w", err,
		)
	}
	raw, _, err := transaction.ReadStable(activePath)
	if err != nil {
		return Record{}, "", false, err
	}
	record, digest, err := decodeRecord(workspace, raw)
	if err != nil {
		return Record{}, "", false, err
	}
	latest, latestDigest, err := latestHistoryRecord(
		workspace, transaction, record.OperationID,
	)
	if err != nil {
		return Record{}, "", false, err
	}
	if latest.Sequence != record.Sequence || latestDigest != digest {
		return Record{}, "", false, errors.New(
			"lifecycle mutation active pointer is stale or replayed",
		)
	}
	active, err := activeHistoryHeads(workspace, transaction)
	if err != nil {
		return Record{}, "", false, err
	}
	switch {
	case record.Status == StatusActive &&
		(len(active) != 1 || active[record.OperationID] != digest):
		return Record{}, "", false, errors.New(
			"lifecycle mutation has divergent active operation histories",
		)
	case record.Status != StatusActive && len(active) != 0:
		return Record{}, "", false, errors.New(
			"lifecycle mutation terminal pointer hides an active orphan history",
		)
	}
	return record, digest, true, nil
}

func decodeRecord(workspace string, raw []byte) (Record, string, error) {
	var record Record
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Record{}, "", fmt.Errorf("decode lifecycle mutation journal: %w", err)
	}
	if err := validateRecord(record); err != nil {
		return Record{}, "", err
	}
	canonical, err := signingBytes(record)
	if err != nil {
		return Record{}, "", err
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(
		workspace, canonical, record.Signature,
	); err != nil {
		return Record{}, "", fmt.Errorf(
			"verify lifecycle mutation Owner signature: %w", err,
		)
	}
	if record.Signature.OwnerRef != record.OwnerRef ||
		record.WorkspaceHash != workspaceHash(workspace) {
		return Record{}, "", errors.New(
			"lifecycle mutation journal differs from Owner or workspace",
		)
	}
	expected, err := resolvedplan.CanonicalJSON(record)
	if err != nil || !bytes.Equal(raw, expected) {
		return Record{}, "", errors.New(
			"lifecycle mutation journal is not canonical",
		)
	}
	sum := sha256.Sum256(raw)
	return record, "sha256:" + hex.EncodeToString(sum[:]), nil
}

type historyRecord struct {
	record Record
	digest string
}

func latestHistoryRecord(
	workspace string,
	transaction *confinedfs.Transaction,
	operationID string,
) (Record, string, error) {
	operationRoot := operationsRoot + "/" + operationID
	exists, info, err := transaction.Exists(operationRoot)
	if err != nil || !exists {
		if err != nil {
			return Record{}, "", err
		}
		return Record{}, "", errors.New(
			"lifecycle mutation immutable history is missing",
		)
	}
	if !info.IsDir() {
		return Record{}, "", errors.New(
			"lifecycle mutation immutable history is not a directory",
		)
	}
	entries, err := transaction.Walk(operationRoot)
	if err != nil {
		return Record{}, "", err
	}
	if len(entries) > 257 {
		return Record{}, "", errors.New(
			"lifecycle mutation history exceeds the bounded phase limit",
		)
	}
	history := make([]historyRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.Info.IsDir() {
			continue
		}
		base := filepath.Base(filepath.FromSlash(entry.Path))
		if strings.HasPrefix(base, ".stackkit-tmp-") {
			if !entry.Info.Mode().IsRegular() || entry.Info.Size() > 1<<20 {
				return Record{}, "", errors.New(
					"lifecycle mutation atomic temporary artifact is invalid",
				)
			}
			if err := backupcustody.RequirePrivatePath(
				filepath.Join(workspace, filepath.FromSlash(entry.Path)), false,
			); err != nil {
				return Record{}, "", fmt.Errorf(
					"verify lifecycle mutation atomic temporary artifact: %w",
					err,
				)
			}
			continue
		}
		raw, _, err := transaction.ReadStable(entry.Path)
		if err != nil {
			return Record{}, "", err
		}
		if err := backupcustody.RequirePrivatePath(
			filepath.Join(workspace, filepath.FromSlash(entry.Path)), false,
		); err != nil {
			return Record{}, "", err
		}
		record, digest, err := decodeRecord(workspace, raw)
		if err != nil {
			return Record{}, "", err
		}
		if record.OperationID != operationID {
			return Record{}, "", errors.New(
				"lifecycle mutation history contains a substituted operation",
			)
		}
		expectedName := fmt.Sprintf(
			"%020d-%s.json",
			record.Sequence, strings.TrimPrefix(digest, "sha256:"),
		)
		if filepath.Base(filepath.FromSlash(entry.Path)) != expectedName {
			return Record{}, "", errors.New(
				"lifecycle mutation history filename differs from signed record",
			)
		}
		history = append(history, historyRecord{record: record, digest: digest})
	}
	sort.Slice(history, func(i, j int) bool {
		if history[i].record.Sequence == history[j].record.Sequence {
			return history[i].digest < history[j].digest
		}
		return history[i].record.Sequence < history[j].record.Sequence
	})
	if len(history) == 0 || history[0].record.Sequence != 1 ||
		history[0].record.PreviousRecordDigest != "" {
		return Record{}, "", errors.New(
			"lifecycle mutation history does not start at the signed origin",
		)
	}
	for index := 1; index < len(history); index++ {
		previous := history[index-1]
		current := history[index]
		if current.record.Sequence != previous.record.Sequence+1 ||
			current.record.PreviousRecordDigest != previous.digest {
			return Record{}, "", errors.New(
				"lifecycle mutation history is divergent or non-linear",
			)
		}
	}
	latest := history[len(history)-1]
	return latest.record, latest.digest, nil
}

func recoverLatestRecord(
	workspace, operationID string,
	transaction *confinedfs.Transaction,
	view confinedfs.View,
) (Record, string, error) {
	if !operationPattern.MatchString(operationID) {
		return Record{}, "", errors.New(
			"explicit lifecycle recovery operation ID is invalid",
		)
	}
	latest, latestDigest, err := latestHistoryRecord(
		workspace, transaction, operationID,
	)
	if err != nil {
		return Record{}, "", err
	}
	activeHeads, err := activeHistoryHeads(workspace, transaction)
	if err != nil {
		return Record{}, "", err
	}
	if latest.Status == StatusActive {
		if len(activeHeads) != 1 ||
			activeHeads[operationID] != latestDigest {
			return Record{}, "", errors.New(
				"explicit lifecycle recovery requires one unique requested active history",
			)
		}
	} else if len(activeHeads) != 0 {
		return Record{}, "", errors.New(
			"explicit lifecycle recovery terminal history is hidden by another active operation",
		)
	}
	exists, info, err := transaction.Exists(activePath)
	if err != nil {
		return Record{}, "", err
	}
	if exists && !info.Mode().IsRegular() {
		return Record{}, "", errors.New(
			"explicit lifecycle recovery requires a private regular active pointer",
		)
	}
	if exists {
		raw, _, readErr := transaction.ReadStable(activePath)
		if readErr != nil {
			return Record{}, "", readErr
		}
		active, activeDigest, decodeErr := decodeRecord(workspace, raw)
		if decodeErr != nil {
			return Record{}, "", errors.New(
				"explicit lifecycle recovery refuses a tampered active pointer",
			)
		}
		if active.Status == StatusActive &&
			active.OperationID != operationID {
			return Record{}, "", errors.New(
				"explicit lifecycle recovery cannot replace another active operation",
			)
		}
		if active.OperationID == operationID &&
			activeDigest == latestDigest {
			return latest, latestDigest, nil
		}
	}
	latestRaw, err := resolvedplan.CanonicalJSON(latest)
	if err != nil {
		return Record{}, "", err
	}
	if _, err := view.WriteAtomic0600(activePath, latestRaw); err != nil {
		return Record{}, "", fmt.Errorf(
			"repair stale lifecycle pointer during explicit recovery: %w", err,
		)
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(activePath)), false,
	); err != nil {
		return Record{}, "", err
	}
	return latest, latestDigest, nil
}

func activeHistoryHeads(
	workspace string,
	transaction *confinedfs.Transaction,
) (map[string]string, error) {
	entries, err := transaction.Walk(operationsRoot)
	if err != nil {
		return nil, err
	}
	operationIDs := map[string]struct{}{}
	prefix := operationsRoot + "/"
	for _, entry := range entries {
		if entry.Path == operationsRoot {
			continue
		}
		remainder := strings.TrimPrefix(entry.Path, prefix)
		operationID := strings.SplitN(remainder, "/", 2)[0]
		if operationID != "" {
			operationIDs[operationID] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(operationIDs))
	for operationID := range operationIDs {
		ordered = append(ordered, operationID)
	}
	sort.Strings(ordered)
	active := make(map[string]string)
	for _, operationID := range ordered {
		record, digest, err := latestHistoryRecord(
			workspace, transaction, operationID,
		)
		if err != nil {
			return nil, err
		}
		if record.Status == StatusActive {
			active[operationID] = digest
		}
	}
	if len(active) > 1 {
		return nil, errors.New(
			"lifecycle mutation history contains multiple active operations",
		)
	}
	return active, nil
}

func persistRecord(
	workspace string,
	transaction *confinedfs.Transaction,
	view confinedfs.View,
	record *Record,
	previousDigest string,
) (string, error) {
	if record == nil {
		return "", errors.New("lifecycle mutation record is required")
	}
	record.PreviousRecordDigest = previousDigest
	record.Signature = localevidence.OwnerLifecycleMutationSignature{}
	canonical, err := signingBytes(*record)
	if err != nil {
		return "", err
	}
	record.Signature, err = localevidence.SignOwnerLifecycleMutation(workspace, canonical)
	if err != nil {
		return "", err
	}
	encoded, err := resolvedplan.CanonicalJSON(*record)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	operationRoot := operationsRoot + "/" + record.OperationID
	if err := transaction.MkdirAll(operationRoot, 0o700); err != nil {
		return "", err
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(operationRoot)), true,
	); err != nil {
		return "", err
	}
	historyPath := fmt.Sprintf(
		"%s/%020d-%s.json",
		operationRoot, record.Sequence, strings.TrimPrefix(digest, "sha256:"),
	)
	if _, err := view.WriteAtomic0600NoReplace(historyPath, encoded); err != nil {
		return "", fmt.Errorf("commit immutable lifecycle mutation phase: %w", err)
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(historyPath)), false,
	); err != nil {
		return "", err
	}
	if _, err := view.WriteAtomic0600(activePath, encoded); err != nil {
		return "", err
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(activePath)), false,
	); err != nil {
		return "", err
	}
	loaded, loadedDigest, exists, err := loadRecord(workspace, transaction)
	if err != nil || !exists || loaded.OperationID != record.OperationID ||
		loaded.Sequence != record.Sequence || loaded.Phase != record.Phase ||
		loaded.Status != record.Status || loadedDigest != digest {
		if err != nil {
			return "", err
		}
		return "", errors.New("re-read lifecycle mutation journal differs after commit")
	}
	return digest, nil
}

func signingBytes(record Record) ([]byte, error) {
	record.Signature = localevidence.OwnerLifecycleMutationSignature{}
	return resolvedplan.CanonicalJSON(record)
}

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
			strings.TrimSpace(record.Target.Version) == "" ||
			!canonicalDigest(record.Target.ArchiveSHA256) ||
			!canonicalDigest(record.Target.ExecutableSHA256) ||
			strings.TrimSpace(record.Prior.Version) == "" ||
			!canonicalDigest(record.Prior.ArchiveSHA256) ||
			!canonicalDigest(record.Prior.ExecutableSHA256) ||
			record.RestoreActivation != nil {
			return errors.New("lifecycle mutation journal is incomplete")
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
		if record.Phase != PhaseRollbackSucceeded {
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
		PhaseRollbackGenerateStarted: {PhaseRollbackGenerateDone},
		PhaseRollbackGenerateDone:    {PhaseRollbackApplyStarted},
		PhaseRollbackApplyStarted:    {PhaseRollbackApplyDone},
		PhaseRollbackApplyDone:       {PhaseRollbackVerifyStarted},
		PhaseRollbackVerifyStarted:   {PhaseRollbackVerifyDone},
		PhaseRollbackVerifyDone:      {PhaseRollbackSucceeded},
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
	state := record.RestoreActivation
	if state == nil ||
		record.Checkpoint != (CheckpointAuthority{}) ||
		record.Target != (ReleaseAuthority{}) ||
		record.Prior != (ReleaseAuthority{}) {
		return errors.New("restore activation journal authority is incomplete")
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
