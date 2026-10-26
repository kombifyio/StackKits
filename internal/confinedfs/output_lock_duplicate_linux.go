//go:build linux

package confinedfs

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Duplicate returns a close-on-exec descriptor for the SAME open file
// description, never another flock authority. The borrower must remain live
// until every recipient closes its descriptor: Release explicitly unlocks it.
// A recipient closes the descriptor without issuing LOCK_UN.
func (l *OutputLockLease) Duplicate() (*os.File, error) {
	if l == nil || l.state == nil {
		return nil, errors.New("live output lock lease required")
	}
	l.state.mu.RLock()
	defer l.state.mu.RUnlock()
	if l.state.closed || l.state.lock == nil || l.state.transaction == nil {
		return nil, errors.New("output lock lease is closed")
	}
	if err := l.state.transaction.VerifyPathIdentity(); err != nil {
		return nil, err
	}
	fd, err := unix.FcntlInt(l.state.lock.file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "borrowed-output-lock"), nil
}
