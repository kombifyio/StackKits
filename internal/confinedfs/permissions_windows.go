//go:build windows

package confinedfs

import "os"

// Windows mode bits do not prove an ACL equivalent to POSIX 0600. The caller
// receives an explicit unsupported signal instead of a synthetic proof.
func verifyMode0600(_ os.FileInfo) (bool, error) { return false, nil }
