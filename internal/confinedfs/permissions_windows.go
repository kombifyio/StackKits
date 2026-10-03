//go:build windows

package confinedfs

import "os"

// Windows mode bits do not prove an ACL equivalent to the POSIX mode. The caller
// receives an explicit unsupported signal instead of a synthetic proof.
func verifyMode(os.FileInfo, os.FileMode) (bool, error) { return false, nil }
