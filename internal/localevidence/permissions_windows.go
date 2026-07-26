//go:build windows

package localevidence

import (
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

func restrictFileToCurrentUser(path string) error {
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("localevidence: resolve current Windows user: %w", err)
	}
	account := strings.TrimSpace(current.Username)
	if account == "" {
		return fmt.Errorf("localevidence: current Windows user has no account name")
	}
	grant := account + ":(F)"
	output, err := exec.Command("icacls", path, "/inheritance:r", "/grant:r", grant).CombinedOutput() //nolint:gosec // fixed executable and argument vector; no shell
	if err != nil {
		return fmt.Errorf("localevidence: restrict Windows custody ACL: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
