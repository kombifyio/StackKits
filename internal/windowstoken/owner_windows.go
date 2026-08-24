//go:build windows

// Package windowstoken exposes the two process-token principals Windows uses
// for local custody: TokenUser receives the private DACL grant, while
// TokenOwner is stamped as the owner of newly created objects.
package windowstoken

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CurrentUserSID returns the account represented by the current process token.
func CurrentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return nil, errors.New("resolve current Windows token user")
	}
	return user.User.Sid, nil
}

type tokenOwner struct {
	SID *windows.SID
}

// CurrentOwnerSID returns the principal Windows assigns as owner to objects
// created by the current process token.
func CurrentOwnerSID() (*windows.SID, error) {
	token := windows.GetCurrentProcessToken()
	var size uint32
	err := windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &size)
	if err != windows.ERROR_INSUFFICIENT_BUFFER || size == 0 {
		return nil, errors.New("resolve current Windows token owner")
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenOwner, &buffer[0], size, &size); err != nil {
		return nil, errors.New("resolve current Windows token owner")
	}
	owner := (*tokenOwner)(unsafe.Pointer(&buffer[0]))
	if owner.SID == nil {
		return nil, errors.New("resolve current Windows token owner")
	}
	return owner.SID, nil
}
