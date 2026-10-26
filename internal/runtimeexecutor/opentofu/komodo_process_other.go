//go:build !linux

package opentofu

import (
	"context"
	"errors"
	"os/exec"
)

func requireKomodoProcessPlatform() error        { return errors.New("Komodo process custody requires Linux") }
func configureKomodoOwnerCancellation(*exec.Cmd) {}
func runKomodoOwnedProcess(context.Context, komodoProcessRequest) (int, error) {
	return 1, requireKomodoProcessPlatform()
}
