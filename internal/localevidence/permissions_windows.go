//go:build windows

package localevidence

import (
	"errors"
	"fmt"
	"os/exec"
	"os/user"
	"strings"
	"unsafe"

	"github.com/kombifyio/stackkits/internal/windowstoken"
	"golang.org/x/sys/windows"
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

func requireFilePrivateToCurrentUser(path string) error {
	currentUser, err := windowstoken.CurrentUserSID()
	if err != nil {
		return fmt.Errorf("localevidence: %w", err)
	}
	currentOwner, err := windowstoken.CurrentOwnerSID()
	if err != nil {
		return fmt.Errorf("localevidence: %w", err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || descriptor == nil {
		return errors.New("localevidence: inspect Windows custody ACL")
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.Equals(currentOwner) {
		return errors.New("localevidence: Windows custody owner differs from the process token owner")
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PRESENT == 0 || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("localevidence: Windows custody DACL is absent or inherited")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 1 {
		return errors.New("localevidence: Windows custody ACL is not owner-only")
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil || ace == nil ||
		ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
		ace.Header.AceFlags != uint8(windows.NO_INHERITANCE) {
		return errors.New("localevidence: Windows custody ACL is not owner-only")
	}
	aceUser := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !currentUser.Equals(aceUser) {
		return errors.New("localevidence: Windows custody ACL grants another principal")
	}
	const fileAllAccess windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
	if ace.Mask != windows.GENERIC_ALL && ace.Mask != fileAllAccess {
		return errors.New("localevidence: Windows custody owner lacks full access")
	}
	return nil
}
