//go:build windows

package backupcustody

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows custody permits exactly one principal: the current process-token
// user. SYSTEM and Administrators receive no explicit ACE; their ability to
// take ownership is an operating-system administration boundary, not custody
// access granted by StackKits.
func restrictPathToCurrentUser(path string, directory bool) error {
	currentUser, err := currentProcessUserSID()
	if err != nil {
		return err
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if directory {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(currentUser),
		},
	}}, nil)
	if err != nil {
		return errors.New("backupcustody: construct owner-only Windows custody ACL")
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return errors.New("backupcustody: restrict Windows custody ACL")
	}
	return requirePrivatePath(path, directory)
}

func requirePrivatePath(path string, directory bool) error {
	currentUser, err := currentProcessUserSID()
	if err != nil {
		return err
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("backupcustody: inspect Windows custody ACL for %q: %w", path, err)
	}
	if descriptor == nil {
		return errors.New("backupcustody: inspect Windows custody ACL: empty security descriptor")
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.Equals(currentUser) {
		return errors.New("backupcustody: Windows custody owner is not the current user")
	}
	control, _, err := descriptor.Control()
	if err != nil ||
		control&windows.SE_DACL_PRESENT == 0 ||
		control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("backupcustody: Windows custody DACL is absent or inherited")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("backupcustody: Windows custody ACL is not owner-only")
	}
	expectedACECount := uint16(1)
	if directory {
		// Windows canonicalizes one full-control (OI)(CI) grant into one
		// effective ACE plus one inherit-only ACE for child objects.
		expectedACECount = 2
	}
	if dacl.AceCount != expectedACECount {
		return errors.New("backupcustody: Windows custody ACL is not owner-only")
	}
	const fileAllAccess windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
	effectiveACE := false
	inheritOnlyACE := false
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil || ace == nil {
			return errors.New("backupcustody: inspect Windows custody ACE")
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			return errors.New("backupcustody: Windows custody ACL is not owner-only")
		}
		aceUser := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !currentUser.Equals(aceUser) {
			return errors.New("backupcustody: Windows custody ACL grants another principal")
		}
		if ace.Mask != windows.GENERIC_ALL && ace.Mask != fileAllAccess {
			return errors.New("backupcustody: Windows custody owner lacks full access")
		}
		switch ace.Header.AceFlags {
		case uint8(windows.NO_INHERITANCE):
			effectiveACE = true
		case uint8(windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE | windows.INHERIT_ONLY_ACE):
			inheritOnlyACE = true
		default:
			return errors.New("backupcustody: Windows custody ACL inheritance is not private")
		}
	}
	if !effectiveACE || (directory != inheritOnlyACE) {
		return errors.New("backupcustody: Windows custody ACL inheritance is not private")
	}
	return nil
}

// ProtectPrivatePath applies and verifies the current-user-only custody ACL.
// It is also used by other local Owner-signed lifecycle state.
func ProtectPrivatePath(path string, directory bool) error {
	return restrictPathToCurrentUser(path, directory)
}

// RequirePrivatePath verifies the current-user-only custody ACL.
func RequirePrivatePath(path string, directory bool) error {
	return requirePrivatePath(path, directory)
}

func currentProcessUserSID() (*windows.SID, error) {
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || currentUser == nil || currentUser.User.Sid == nil {
		return nil, errors.New("backupcustody: resolve current Windows token user")
	}
	return currentUser.User.Sid, nil
}
