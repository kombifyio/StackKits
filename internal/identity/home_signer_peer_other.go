//go:build !linux

package identity

import "net"

// homeSignerPeerUID knows no peer outside Linux, so the signer admits none:
// the Basement core runs on Linux only.
func homeSignerPeerUID(net.Conn) (int, bool) {
	return -1, false
}
