//go:build !linux

package confinedfs

import (
	"errors"
	"os"
)

// Duplicate is intentionally Linux-only until descriptor transfer is qualified.
func (l *OutputLockLease) Duplicate() (*os.File, error) {
	return nil, errors.New("output lock descriptor transfer requires Linux")
}
