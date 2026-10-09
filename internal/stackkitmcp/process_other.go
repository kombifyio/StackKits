//go:build !linux

package stackkitmcp

import "os/exec"

// Other platforms retain their existing direct-child cancellation behavior.
func configureMCPCLIProcess(_ *exec.Cmd) {}
