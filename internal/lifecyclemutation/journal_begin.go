package lifecyclemutation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

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
	if exists && recordBlocksMutation(current) {
		return fail(fmt.Errorf(
			"lifecycle mutation %s is active at phase %s; recover it explicitly%s",
			current.OperationID, current.Phase, recoveryHint(current),
		))
	}
	if err := backuplifecycle.RequireSettledRuntimeControls(workspace, ""); err != nil {
		return fail(err)
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
	onAbort ...func() error,
) (*Session, error) {
	if prepare == nil {
		return nil, errors.New("restore activation preparation callback is required")
	}
	return beginRestoreActivationPreparedContext(context.Background(), workspace, "", func(context.Context) (RestoreActivationBeginRequest, error) { return prepare() }, onAbort...)
}
func BeginRestoreActivationPreparedContext(ctx context.Context, workspace, operation string, prepare func(context.Context) (RestoreActivationBeginRequest, error), onAbort ...func() error) (*Session, error) {
	return beginRestoreActivationPreparedContext(ctx, workspace, operation, prepare, onAbort...)
}
func beginRestoreActivationPreparedContext(ctx context.Context, workspace, operation string, prepare func(context.Context) (RestoreActivationBeginRequest, error), onAbort ...func() error) (*Session, error) {
	if prepare == nil {
		return nil, errors.New("restore activation preparation callback is required")
	}
	if len(onAbort) > 1 {
		return nil, errors.New("restore activation preparation accepts one abort cleanup")
	}
	session, err := openLocked(workspace)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*Session, error) {
		// Preparation may have acquired an existing application hold. Every
		// failure, including journal admission/persistence after preparation,
		// releases it while this lifecycle lock is still held.
		if len(onAbort) == 1 && onAbort[0] != nil {
			cause = errors.Join(cause, onAbort[0]())
		}
		return nil, errors.Join(cause, session.Close())
	}
	current, _, exists, err := loadRecord(workspace, session.transaction)
	if err != nil {
		return fail(err)
	}
	if exists && recordBlocksMutation(current) {
		return fail(fmt.Errorf(
			"lifecycle mutation %s is active at phase %s; recover it explicitly%s",
			current.OperationID, current.Phase, recoveryHint(current),
		))
	}
	if err := backuplifecycle.RequireSettledRuntimeControls(workspace, ""); err != nil {
		return fail(err)
	}
	preparation := ctx
	if operation != "" {
		if !operationPattern.MatchString(operation) {
			return fail(errors.New("invalid parent preparation operation"))
		}
		preparation = context.WithValue(ctx, appGroupLeaseKey{}, &appGroupLease{session: session, operation: operation})
	}
	request, err := prepare(preparation)
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

func OpenUpgradeRecovery(workspace, operationID string) (*Session, Record, error) {
	return openRecoveryForKind(workspace, operationID, KindUpgrade)
}

func OpenRestoreActivationRecovery(
	workspace, operationID string,
) (*Session, Record, error) {
	return openRecoveryForKind(workspace, operationID, KindRestoreActivation)
}

func openRecoveryForKind(workspace, operationID, kind string) (*Session, Record, error) {
	session, record, err := OpenRecovery(workspace, operationID)
	if err != nil {
		return nil, Record{}, err
	}
	if record.Kind != kind {
		return nil, Record{}, errors.Join(
			fmt.Errorf("exact %s operation is required for explicit recovery", kind),
			session.Close(),
		)
	}
	return session, record, nil
}

func RequireIdle(workspace string) error {
	if err := backuplifecycle.RequireSettledRuntimeControls(workspace, ""); err != nil {
		return err
	}
	record, active, err := ActiveRecord(workspace)
	if err != nil {
		return err
	}
	if active {
		return fmt.Errorf(
			"lifecycle mutation %s is active at phase %s; ordinary mutation is denied%s",
			record.OperationID, record.Phase, recoveryHint(record),
		)
	}
	return nil
}

// ActiveRecord returns the verified record of the active lifecycle mutation
// or terminal restore with pending native closes, without taking the lock.
// It only inspects: acting on the mutation
// still needs OpenRecovery, which revalidates the record under the lock.
func ActiveRecord(workspace string) (Record, bool, error) {
	if _, err := os.Lstat(
		filepath.Join(workspace, filepath.FromSlash(journalRoot)),
	); os.IsNotExist(err) {
		return Record{}, false, nil
	} else if err != nil {
		return Record{}, false, fmt.Errorf("inspect lifecycle mutation root: %w", err)
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return Record{}, false, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return Record{}, false, err
	}
	defer transaction.Close()
	record, _, exists, err := loadRecord(workspace, transaction)
	if err != nil {
		return Record{}, false, err
	}
	if !exists || !recordBlocksMutation(record) {
		return Record{}, false, nil
	}
	return record, true, nil
}

// WithIdleMutation holds the same lock as an upgrade Session across the exact
// ordinary mutation boundary. A joined upgrade child is admitted from the
// signed parent journal and deliberately does not reacquire the parent's lock.
func WithIdleMutation(
	workspace string,
	join JoinRequest,
	execute func() error,
) error {
	return withIdleMutation(workspace, join, "", execute)
}

// WithSnapshotMutation admits recovery of only the original authenticated
// snapshot operation. Its service still revalidates exact Plan/Owner input;
// a changed operation or Plan cannot discard an older control obligation.
func WithSnapshotMutation(workspace string, join JoinRequest, operation string, execute func() error) error {
	return withIdleMutation(workspace, join, operation, execute)
}

func withIdleMutation(workspace string, join JoinRequest, snapshot string, execute func() error) error {
	if execute == nil {
		return errors.New("lifecycle mutation callback is required")
	}
	if strings.TrimSpace(join.OperationID) != "" ||
		strings.TrimSpace(join.Phase) != "" {
		if err := backuplifecycle.RequireSettledRuntimeControls(workspace, snapshot); err != nil {
			return err
		}
		claim, err := admitJoin(workspace, join)
		if err != nil {
			return err
		}
		// The child keeps its claim lock until its mutation returns. The
		// kernel also releases it when the child dies, so explicit recovery
		// that acquires it proves this claimant gone.
		return errors.Join(execute(), claim.Release())
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
	if exists && recordBlocksMutation(record) {
		return fmt.Errorf(
			"lifecycle mutation %s is active at phase %s; ordinary mutation is denied%s",
			record.OperationID, record.Phase, recoveryHint(record),
		)
	}
	if err := backuplifecycle.RequireSettledRuntimeControls(workspace, snapshot); err != nil {
		return err
	}
	return execute()
}

// recoveryHint names the owner command that settles an active operation.
func recoveryHint(record Record) string {
	if record.Kind != KindUpgrade || record.Status != StatusActive {
		return ""
	}
	return fmt.Sprintf("; settle it with stackkit upgrade --recover %s", record.OperationID)
}
