//go:build linux

package identity

import (
	"net"
	"syscall"
)

// homeSignerPeerUID reads the peer's uid from SO_PEERCRED.
func homeSignerPeerUID(conn net.Conn) (int, bool) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return -1, false
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return -1, false
	}
	var credential *syscall.Ucred
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credential, credentialErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || credentialErr != nil || credential == nil {
		return -1, false
	}
	return int(credential.Uid), true
}
