//go:build windows

package runtimeexecutorlocal

import (
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

func restrictBasementRuntimeFile(path string) error {
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("resolve current Windows user: %w", err)
	}
	account := strings.TrimSpace(current.Username)
	if account == "" {
		return fmt.Errorf("current Windows user has no account name")
	}
	output, err := exec.Command("icacls", path, "/inheritance:r", "/grant:r", account+":(F)").CombinedOutput() //nolint:gosec // fixed executable and argument vector
	if err != nil {
		return fmt.Errorf("restrict Windows Basement runtime ACL: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
