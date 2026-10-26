//go:build linux

package opentofu

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"golang.org/x/sys/unix"
)

const actionLeaseSocketEnv = "STACKKIT_ACTION_LEASE_SOCKET"

type actionLeaseRequest struct {
	Workspace  string `json:"workspace"`
	Project    string `json:"project"`
	Capability string `json:"capability"`
}

type actionLeaseServer struct {
	directory string
	listener  *net.UnixListener
	done      chan struct{}
	mu        sync.Mutex
	closing   bool
	pending   *net.UnixConn
}

func requireActionLeasePlatform() error { return nil }

// newActionLeaseServer transfers only a duplicate of the parent's held lease.
// Close must finish BEFORE the caller closes the borrow or releases the lock.
// Once a helper is admitted, cancellation cannot shorten that borrow.
func newActionLeaseServer(lease *confinedfs.OutputLockLease, workspace, project, capability string) (*actionLeaseServer, error) {
	if len(capability) != 64 {
		return nil, errors.New("action lease requires a process capability")
	}
	directory, err := os.MkdirTemp("/tmp", "stackkit-lease-")
	if err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: filepath.Join(directory, "lease.sock"), Net: "unixpacket"})
	if err != nil {
		os.RemoveAll(directory)
		return nil, err
	}
	if err := os.Chmod(listener.Addr().String(), 0o600); err != nil {
		listener.Close()
		os.RemoveAll(directory)
		return nil, err
	}
	server := &actionLeaseServer{directory: directory, listener: listener, done: make(chan struct{})}
	go server.serve(lease, actionLeaseRequest{workspace, project, capability})
	return server, nil
}

func (s *actionLeaseServer) Endpoint() string { return s.listener.Addr().String() }

func (s *actionLeaseServer) Close() {
	s.mu.Lock()
	s.closing = true
	s.listener.Close()
	if s.pending != nil {
		s.pending.Close()
	}
	s.mu.Unlock()
	<-s.done
	os.RemoveAll(s.directory)
}

func (s *actionLeaseServer) serve(lease *confinedfs.OutputLockLease, expected actionLeaseRequest) {
	defer close(s.done)
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			conn.Close()
			return
		}
		s.pending = conn
		s.mu.Unlock()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		payload, ancillary := make([]byte, 4096), make([]byte, 256)
		n, oobn, flags, _, err := conn.ReadMsgUnix(payload, ancillary)
		closeActionLeaseRights(ancillary[:oobn])
		var request actionLeaseRequest
		decoder := json.NewDecoder(bytes.NewReader(payload[:n]))
		decoder.DisallowUnknownFields()
		valid := err == nil && flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) == 0 && oobn == 0 &&
			decoder.Decode(&request) == nil && decoder.Decode(new(any)) == io.EOF &&
			request.Workspace == expected.Workspace && request.Project == expected.Project &&
			subtle.ConstantTimeCompare([]byte(request.Capability), []byte(expected.Capability)) == 1
		s.mu.Lock()
		s.pending = nil
		if !valid || s.closing {
			s.mu.Unlock()
			conn.Close()
			continue
		}
		file, err := lease.Duplicate()
		if err != nil {
			s.mu.Unlock()
			conn.Close()
			continue
		}
		_, _, err = conn.WriteMsgUnix([]byte("lease-v1"), unix.UnixRights(int(file.Fd())), nil)
		file.Close()
		if err != nil {
			s.mu.Unlock()
			conn.Close()
			continue
		}
		// There is exactly one admitted helper. Its socket EOF means its descriptor
		// has closed, or the kernel has closed both on process death. A parent death
		// skips LOCK_UN and the transferred descriptor keeps the OS flock alive.
		s.listener.Close()
		conn.SetDeadline(time.Time{})
		s.mu.Unlock()
		io.Copy(io.Discard, conn)
		conn.Close()
		return
	}
}

type actionLease struct {
	file *os.File
	conn *net.UnixConn
}

func (l *actionLease) Close() {
	// Descriptor first: EOF must never allow the parent to unlock a live helper.
	l.file.Close()
	l.conn.Close()
}

func receiveActionLease(workspace, project, capability string) (*actionLease, error) {
	endpoint := os.Getenv(actionLeaseSocketEnv)
	if endpoint == "" || len(capability) != 64 {
		return nil, errors.New("action requires its live parent project lease")
	}
	conn, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: endpoint, Net: "unixpacket"})
	if err != nil {
		return nil, errors.New("action parent project lease is unavailable")
	}
	keep := false
	defer func() {
		if !keep {
			conn.Close()
		}
	}()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	request, err := json.Marshal(actionLeaseRequest{workspace, project, capability})
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(request); err != nil {
		return nil, err
	}
	payload, ancillary := make([]byte, 32), make([]byte, 256)
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	var n, oobn, flags int
	var receiveErr error
	err = raw.Read(func(fd uintptr) bool {
		n, oobn, flags, _, receiveErr = unix.Recvmsg(int(fd), payload, ancillary, unix.MSG_CMSG_CLOEXEC)
		return receiveErr != unix.EAGAIN && receiveErr != unix.EWOULDBLOCK
	})
	fds := actionLeaseRights(ancillary[:oobn])
	received := false
	defer func() {
		if !received {
			for _, fd := range fds {
				unix.Close(fd)
			}
		}
	}()
	if err != nil || receiveErr != nil || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || string(payload[:n]) != "lease-v1" || len(fds) != 1 {
		return nil, errors.New("action parent refused the exact project lease")
	}
	file := os.NewFile(uintptr(fds[0]), "parent-project-lease")
	received = true
	defer func() {
		if !keep {
			file.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("action parent supplied an invalid project lease")
	}
	conn.SetDeadline(time.Time{})
	keep, received = true, true
	return &actionLease{file, conn}, nil
}

func actionLeaseRights(raw []byte) []int {
	messages, err := unix.ParseSocketControlMessage(raw)
	if err != nil {
		return nil
	}
	var fds []int
	for _, message := range messages {
		rights, err := unix.ParseUnixRights(&message)
		if err == nil {
			fds = append(fds, rights...)
		}
	}
	return fds
}
func closeActionLeaseRights(raw []byte) {
	for _, fd := range actionLeaseRights(raw) {
		unix.Close(fd)
	}
}
