//go:build !unix

package ownappfirewall

import "os"

// The own application firewall is Linux-only; elsewhere no owner is trusted.
func fileOwner(os.FileInfo) (uint32, bool) { return 0, false }
