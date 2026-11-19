package lifecyclemutation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// InspectJoin validates a signed child admission without consuming its
// one-use nonce. It is safe for pre-observability admission only.
func InspectJoin(workspace string, request JoinRequest) error {
	_, err := inspectJoin(workspace, request, false)
	return err
}

// InspectUpgradeTargetApplySnapshot exposes the signed pre-target checkpoint
// only to the exact admitted upgrade Apply child. The caller can use its
// original Compose definition to prove ownership of a running old listener
// after target Generate has replaced the active Compose file.
func InspectUpgradeTargetApplySnapshot(workspace string, request JoinRequest) (string, error) {
	if request.Phase != PhaseTargetApplyStarted || request.Command != "apply" {
		return "", errors.New("prior runtime ownership requires target upgrade Apply")
	}
	if err := InspectJoin(workspace, request); err != nil {
		return "", err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return "", err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return "", err
	}
	defer transaction.Close()
	record, _, exists, err := loadRecord(workspace, transaction)
	if err != nil {
		return "", err
	}
	if !exists || record.Kind != KindUpgrade || record.Status != StatusActive ||
		record.OperationID != request.OperationID || record.Phase != request.Phase ||
		record.Checkpoint.ExecutorStateSnapshotID == "" {
		return "", errors.New("prior runtime ownership has no matching signed upgrade checkpoint")
	}
	return record.Checkpoint.ExecutorStateSnapshotID, nil
}

// admitJoin validates and atomically consumes the exact one-use child nonce.
// It returns the held claim lock; the caller releases it after its mutation.
func admitJoin(workspace string, request JoinRequest) (*confinedfs.OutputLock, error) {
	return inspectJoin(workspace, request, true)
}

func inspectJoin(workspace string, request JoinRequest, claim bool) (*confinedfs.OutputLock, error) {
	if strings.TrimSpace(request.OperationID) == "" &&
		strings.TrimSpace(request.Phase) == "" {
		return nil, RequireIdle(workspace)
	}
	if !operationPattern.MatchString(request.OperationID) ||
		strings.TrimSpace(request.Phase) == "" {
		return nil, errors.New("internal lifecycle join requires an exact operation ID and phase")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer transaction.Close()
	record, recordDigest, exists, err := loadRecord(workspace, transaction)
	if err != nil {
		return nil, err
	}
	if !exists || record.Status != StatusActive ||
		record.OperationID != request.OperationID ||
		record.Phase != request.Phase {
		return nil, errors.New("internal lifecycle join differs from the exact current signed operation")
	}
	expectedVersion, expectedCommand := joinAuthority(record)
	if request.BinaryVersion != expectedVersion ||
		request.Command != expectedCommand ||
		record.Join == nil ||
		request.BinaryVersion != record.Join.BinaryVersion ||
		request.Command != record.Join.Command ||
		request.ExecutableSHA256 != record.Join.ExecutableSHA256 ||
		digestNonce(request.Nonce) != record.Join.NonceSHA256 {
		return nil, errors.New("internal lifecycle join binary version or command is not authorized")
	}
	if !claim {
		return nil, nil
	}
	return claimJoin(workspace, root, transaction, record, recordDigest)
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

// JoinClaimCustodyLifetime marks a claim whose child holds the operation's
// claim lock until its mutation returns. An older child released that lock
// right after claiming and wrote no custody, so only this marker lets
// recovery read a free lock as proof that the claimant ended.
const JoinClaimCustodyLifetime = "lifetime-lock"

type joinClaim struct {
	APIVersion       string    `json:"apiVersion"`
	OperationID      string    `json:"operationId"`
	Sequence         uint64    `json:"sequence"`
	Phase            string    `json:"phase"`
	Command          string    `json:"command"`
	NonceSHA256      string    `json:"nonceSha256"`
	ExecutableSHA256 string    `json:"executableSha256"`
	ClaimedAt        time.Time `json:"claimedAt"`
	Custody          string    `json:"custody,omitempty"`
}

func claimJoin(
	workspace string,
	root *confinedfs.Root,
	transaction *confinedfs.Transaction,
	record Record,
	validatedDigest string,
) (held *confinedfs.OutputLock, returnErr error) {
	if root == nil || transaction == nil || record.Join == nil {
		return nil, errors.New("lifecycle mutation join claim authority is incomplete")
	}
	operationRoot := claimsRoot + "/" + record.OperationID
	if err := transaction.MkdirAll(claimsRoot, 0o700); err != nil {
		return nil, err
	}
	if err := transaction.MkdirAll(operationRoot, 0o700); err != nil {
		return nil, err
	}
	for _, relative := range []string{claimsRoot, operationRoot} {
		if err := backupcustody.ProtectPrivatePath(
			filepath.Join(workspace, filepath.FromSlash(relative)), true,
		); err != nil {
			return nil, err
		}
	}
	lock, err := transaction.TryAcquireOutputLock(operationRoot)
	if err != nil {
		return nil, fmt.Errorf("acquire lifecycle join claim lock: %w", err)
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, lock.Release())
		}
	}()
	// Recovery owns this same lock until its replacement phase is signed.
	// Rebind the pre-lock validation before consuming any child authority.
	current, currentDigest, exists, err := loadRecord(workspace, transaction)
	if err != nil {
		return nil, err
	}
	if !exists || current.Status != StatusActive || current.OperationID != record.OperationID ||
		currentDigest != validatedDigest {
		return nil, errors.New("lifecycle mutation join changed before its claim lock was acquired")
	}
	view, err := root.View(".")
	if err != nil {
		return nil, err
	}
	claim := joinClaim{
		APIVersion: APIVersion + "/join-claim", OperationID: record.OperationID,
		Sequence: record.Sequence, Phase: record.Phase,
		Command: record.Join.Command, NonceSHA256: record.Join.NonceSHA256,
		ExecutableSHA256: record.Join.ExecutableSHA256, ClaimedAt: time.Now().UTC(),
		Custody: JoinClaimCustodyLifetime,
	}
	encoded, err := resolvedplan.CanonicalJSON(claim)
	if err != nil {
		return nil, err
	}
	path := joinClaimPath(record)
	if _, err := view.WriteAtomic0600NoReplace(path, encoded); err != nil {
		return nil, errors.New("lifecycle mutation join nonce is already claimed")
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(path)), false,
	); err != nil {
		return nil, err
	}
	return lock, nil
}

// requireClaimantGone proves that the child which claimed the current join
// can no longer mutate. The caller holds the claim lock. A lifetime-lock
// claim is proven by that lock alone; an older child's claim needs the
// kernel process table (requireLegacyClaimantGone). Anything else keeps the
// refusal.
func (session *Session) requireClaimantGone(claimPath string) error {
	if err := backupcustody.RequirePrivatePath(
		filepath.Join(session.workspace, filepath.FromSlash(claimPath)), false,
	); err != nil {
		return fmt.Errorf("verify private join claim: %w", err)
	}
	raw, info, err := session.transaction.ReadStable(claimPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || len(raw) > maxRecordBytes {
		return errors.New("join claim is not a bounded regular file")
	}
	var claim joinClaim
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claim); err != nil {
		return fmt.Errorf("decode join claim: %w", err)
	}
	record := session.record
	if claim.APIVersion != APIVersion+"/join-claim" ||
		claim.OperationID != record.OperationID ||
		claim.Sequence != record.Sequence || claim.Phase != record.Phase ||
		record.Join == nil || claim.Command != record.Join.Command ||
		claim.NonceSHA256 != record.Join.NonceSHA256 ||
		claim.ExecutableSHA256 != record.Join.ExecutableSHA256 {
		return errors.New("join claim differs from the signed current phase")
	}
	switch claim.Custody {
	case JoinClaimCustodyLifetime:
		return nil
	case "":
		return requireLegacyClaimantGone(record.OperationID)
	default:
		return fmt.Errorf("join claim custody %q is unknown", claim.Custody)
	}
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
			"acquire exclusive explicit-recovery join-claim lock (a claimed child may still run): %w", err,
		)
	}
	session.claimLock = lock
	if session.record.Kind == KindUpgrade && joinPhase(session.record.Phase) {
		claimPath := joinClaimPath(session.record)
		exists, _, inspectErr := session.transaction.Exists(claimPath)
		if inspectErr != nil {
			return inspectErr
		}
		if exists {
			if err := session.requireClaimantGone(claimPath); err != nil {
				return fmt.Errorf(
					"explicit lifecycle recovery refuses a claimed child phase: %w", err,
				)
			}
			session.claimantGone = true
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
func CurrentExecutableSHA256() (string, error) { return localevidence.CurrentExecutableSHA256() }

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
