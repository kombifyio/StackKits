package identity

import (
	"log/slog"
	"net"
)

// homeSignerListener admits a connection to the signer socket only from the
// core server's account (or root): the kernel's peer credentials of the Unix
// socket, never anything the caller says. Every other peer is closed before a
// byte is read.
type homeSignerListener struct {
	net.Listener
	peerUID int
}

func (l *homeSignerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, ok := homeSignerPeerUID(conn)
		if ok && (uid == l.peerUID || uid == 0) {
			return conn, nil
		}
		slog.Warn("identity signer refused a peer", "uid", uid, "known", ok)
		_ = conn.Close()
	}
}
