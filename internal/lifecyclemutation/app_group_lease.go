package lifecyclemutation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

type appGroupLeaseKey struct{}

func (c *appGroupLease) operationID() string { c.mu.Lock(); defer c.mu.Unlock(); return c.operation }

type appGroupLease struct {
	session   *Session
	operation string
	mu        sync.Mutex
}

// WithSnapshotMutationContext propagates the actual existing OS lease. It
// replaces, rather than nests inside, WithSnapshotMutation at group call sites.
// Joined legacy children retain their old admission but get no fabricated lease.
func WithSnapshotMutationContext(ctx context.Context, workspace string, join JoinRequest, operation string, execute func(context.Context) error) error {
	if ctx == nil || execute == nil || operation != "" && !backuplifecycle.ValidateSnapshotOperationID(operation) {
		return errors.New("snapshot lease requires exact portable operation/context")
	}
	if _, ok := ctx.Value(appGroupLeaseKey{}).(*appGroupLease); ok {
		if err := RequireAppGroupLease(ctx, workspace, operation); err != nil {
			return err
		}
		return execute(ctx)
	}
	if strings.TrimSpace(join.OperationID) != "" || strings.TrimSpace(join.Phase) != "" {
		return withIdleMutation(workspace, join, operation, func() error { return execute(ctx) })
	}
	s, err := openLocked(workspace)
	if err != nil {
		return err
	}
	defer s.Close()
	record, _, exists, err := loadRecord(s.workspace, s.transaction)
	if err != nil {
		return err
	}
	if exists && recordBlocksMutation(record) {
		return errors.New("snapshot cannot bypass active lifecycle operation")
	}
	if operation != "" {
		if err = backuplifecycle.RequireSettledRuntimeControls(s.workspace, operation); err != nil {
			return err
		}
	}
	return execute(context.WithValue(ctx, appGroupLeaseKey{}, &appGroupLease{session: s, operation: operation}))
}

// WithAppGroupContext binds this live restore Session without minting a token
// or acquiring another lock. Every consumer re-borrows/verifies the OS lease.
func (s *Session) WithAppGroupContext(ctx context.Context) (context.Context, error) {
	if s == nil || ctx == nil || s.record.Kind != KindRestoreActivation || s.record.OperationID == "" || s.lock == nil || s.transaction == nil {
		return nil, errors.New("live restore Session required")
	}
	bound := context.WithValue(ctx, appGroupLeaseKey{}, &appGroupLease{session: s, operation: s.record.OperationID})
	if err := RequireAppGroupLease(bound, s.workspace, s.record.OperationID); err != nil {
		return nil, err
	}
	return bound, nil
}
func RequireAppGroupLease(ctx context.Context, workspace, operation string) error {
	return WithAppGroupCustodyTransaction(ctx, workspace, operation, func(*confinedfs.Transaction, confinedfs.View) error { return nil })
}

// WithAppGroupCustodyTransaction authorizes native generation CAS at the same
// owning lifecycle lock. Borrow pins both exact transaction and held OS lock
// through callback completion, including bounded detached cleanup. This is no
// ambient/global lock lookup and a released context can never reacquire it.
func WithAppGroupCustodyTransaction(ctx context.Context, workspace, operation string, execute func(*confinedfs.Transaction, confinedfs.View) error) error {
	if ctx == nil || execute == nil {
		return errors.New("group custody lease/context missing")
	}
	c, ok := ctx.Value(appGroupLeaseKey{}).(*appGroupLease)
	if !ok || c.session == nil || operation == "" || c.operationID() != operation {
		return errors.New("group custody lacks exact owning OS lease")
	}
	s := c.session
	absolute, err := filepath.Abs(workspace)
	if err != nil || filepath.Clean(absolute) != s.workspace || s.lock == nil || s.transaction == nil {
		return errors.New("group custody lease workspace changed")
	}
	borrow, err := s.lock.Borrow(s.transaction, journalRoot)
	if err != nil {
		return err
	}
	defer borrow.Close()
	if err = borrow.Verify(s.transaction, journalRoot); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return execute(s.transaction, s.view)
}

// BindAppGroupSnapshotChild delegates the held preparation lease to one named
// child snapshot. Its own signed backup journal remains its actuation authority.
func BindAppGroupSnapshotChild(ctx context.Context, workspace, parent, child string) (context.Context, error) {
	if !backuplifecycle.ValidateSnapshotOperationID(child) || child == parent {
		return nil, errors.New("explicit distinct safety child operation required")
	}
	if err := RequireAppGroupLease(ctx, workspace, parent); err != nil {
		return nil, err
	}
	c := ctx.Value(appGroupLeaseKey{}).(*appGroupLease)
	if c.session.record.OperationID != "" || c.session.record.Kind != "" {
		return nil, errors.New("snapshot child only during held restore preparation")
	}
	return context.WithValue(ctx, appGroupLeaseKey{}, &appGroupLease{session: c.session, operation: child}), nil
}

func WithIdleMutationContext(ctx context.Context, workspace string, join JoinRequest, execute func(context.Context) error) error {
	if execute == nil || ctx == nil {
		return errors.New("idle lease callback/context required")
	}
	if join.OperationID != "" || join.Phase != "" {
		return errors.New("qualification cannot run as joined upgrade child")
	}
	s, err := openLocked(workspace)
	if err != nil {
		return err
	}
	defer s.Close()
	record, _, exists, err := loadRecord(workspace, s.transaction)
	if err != nil {
		return err
	}
	if exists && recordBlocksMutation(record) {
		return errors.New("qualification cannot change custody during active lifecycle")
	}
	if err = backuplifecycle.RequireSettledRuntimeControls(workspace, ""); err != nil {
		return err
	}
	return execute(context.WithValue(ctx, appGroupLeaseKey{}, &appGroupLease{session: s, operation: "runtipi-qualification"}))
}

func BindAppGroupScheduledSnapshotContext(ctx context.Context, workspace, operation string) (context.Context, error) {
	if ctx == nil || !backuplifecycle.ValidateSnapshotOperationID(operation) {
		return nil, errors.New("actual authorized scheduled operation required")
	}
	c, ok := ctx.Value(appGroupLeaseKey{}).(*appGroupLease)
	if !ok || c == nil {
		return nil, errors.New("scheduled snapshot requires held unbound lease")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operation != "" && c.operation != operation {
		return nil, errors.New("scheduled lease already bound to different operation")
	}
	// Verify held OS lease before the one assignment; no schedule policy is
	// created here. The caller owns the existing signed schedule authorization.
	s := c.session
	absolute, err := filepath.Abs(workspace)
	if err != nil || s == nil || absolute != s.workspace || s.lock == nil {
		return nil, errors.New("scheduled lease workspace changed")
	}
	borrow, err := s.lock.Borrow(s.transaction, journalRoot)
	if err != nil {
		return nil, err
	}
	defer borrow.Close()
	if err = borrow.Verify(s.transaction, journalRoot); err != nil {
		return nil, err
	}
	if err = backuplifecycle.RequireSettledRuntimeControls(workspace, operation); err != nil {
		return nil, err
	}
	c.operation = operation
	return ctx, nil
}
