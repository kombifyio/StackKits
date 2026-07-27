//go:build windows

package runtimeexecutorlocal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
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

func requirePrivateBasementRuntimeFile(path string, info os.FileInfo) error {
	if info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Basement runtime file is not a plain regular file")
	}
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || currentUser == nil || currentUser.User.Sid == nil {
		return errors.New("resolve current Windows token user")
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil || descriptor == nil {
		return errors.New("inspect Windows Basement runtime ACL")
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.Equals(currentUser.User.Sid) {
		return errors.New("Windows Basement runtime owner is not the current user")
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PRESENT == 0 || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("Windows Basement runtime DACL is absent or inherited")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 1 {
		return errors.New("Windows Basement runtime ACL is not owner-only")
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil || ace == nil ||
		ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
		ace.Header.AceFlags != uint8(windows.NO_INHERITANCE) {
		return errors.New("Windows Basement runtime ACL is not an explicit owner-only grant")
	}
	aceUser := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !currentUser.User.Sid.Equals(aceUser) {
		return errors.New("Windows Basement runtime ACL grants another principal")
	}
	const fileAllAccess windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
	if ace.Mask != windows.GENERIC_ALL && ace.Mask != fileAllAccess {
		return errors.New("Windows Basement runtime owner lacks full access")
	}
	return nil
}
