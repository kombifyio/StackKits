//go:build linux

package nativehost

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/docker"
	"golang.org/x/sys/unix"
)

// Mount sources are daemon-host paths. Never interpret them in a different
// mount namespace/chroot, or treat an inaccessible path as a disjoint writer.
func requireRuntipiDaemonFilesystem(ctx context.Context) error {
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", "/var/run/docker.sock")
	if err != nil {
		return errors.New("cannot verify local daemon filesystem namespace")
	}
	defer conn.Close()
	socket, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("daemon peer identity unavailable")
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return err
	}
	// SO_PEERCRED may identify systemd, which created an inherited listening
	// socket. SCM_CREDENTIALS identifies the process that actually answers.
	var optionErr error
	if err = raw.Control(func(fd uintptr) { optionErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PASSCRED, 1) }); err != nil {
		return err
	}
	if optionErr != nil {
		return errors.New("daemon response credentials unavailable")
	}
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	cancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer cancel()
	if _, err = io.WriteString(conn, "GET /_ping HTTP/1.1\r\nHost: docker\r\nConnection: close\r\n\r\n"); err != nil {
		return errors.New("daemon namespace ping unconfirmed")
	}
	first := make([]byte, 1)
	control := make([]byte, unix.CmsgSpace(unix.SizeofUcred))
	n, oobn, flags, _, err := socket.ReadMsgUnix(first, control)
	if err != nil || n != 1 || flags&unix.MSG_CTRUNC != 0 {
		return errors.New("daemon response process unavailable")
	}
	messages, err := unix.ParseSocketControlMessage(control[:oobn])
	if err != nil || len(messages) != 1 {
		return errors.New("daemon response identity ambiguous")
	}
	peer, err := unix.ParseUnixCredentials(&messages[0])
	if err != nil || peer.Pid < 1 || peer.Uid != 0 {
		return errors.New("rootful fixed daemon response identity unverifiable")
	}
	response, err := http.ReadResponse(bufio.NewReader(io.LimitReader(io.MultiReader(bytes.NewReader(first), conn), 8192)), nil)
	if err != nil {
		return errors.New("daemon namespace ping invalid")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 3))
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "OK" {
		return errors.New("daemon namespace ping not acknowledged")
	}
	executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", peer.Pid))
	if err != nil || filepath.Base(executable) != "dockerd" {
		return errors.New("local socket responder is not an identifiable Docker daemon")
	}
	for _, paths := range [][2]string{{"/proc/self/ns/mnt", fmt.Sprintf("/proc/%d/ns/mnt", peer.Pid)}, {"/proc/self/ns/pid", fmt.Sprintf("/proc/%d/ns/pid", peer.Pid)}, {"/", fmt.Sprintf("/proc/%d/root", peer.Pid)}} {
		own, e := os.Stat(paths[0])
		if e != nil {
			return e
		}
		daemon, e := os.Stat(paths[1])
		if e != nil {
			return e
		}
		if !os.SameFile(own, daemon) {
			return errors.New("daemon mount/PID namespace or root differs from observer")
		}
	}
	return nil
}

type runtipiHostMount struct{ device, root, target, filesystem string }
type runtipiPhysicalSource struct {
	device, path string
	info         os.FileInfo
	resolved     string
	original     string
}
type runtipiWriterScope struct {
	processes  []runtipiMountProcess
	mountBytes []byte
	mounts     []runtipiHostMount
	selected   []runtipiPhysicalSource
	checked    []runtipiPhysicalSource
}

func runtipiPathContains(parent, child string) bool {
	return parent == child || parent == "/" || strings.HasPrefix(child, parent+"/")
}
func readRuntipiMountInfo() ([]byte, error) { return readRuntipiMountFile("/proc/self/mountinfo") }
func readRuntipiMountFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 4<<20 {
		return nil, errors.New("host mount topology exceeds observation bound")
	}
	return raw, nil
}
func runtipiMountPath(encoded string) (string, error) {
	decoded := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(encoded)
	if strings.ContainsRune(decoded, 0) || !filepath.IsAbs(decoded) || filepath.Clean(decoded) != decoded {
		return "", errors.New("ambiguous host mount path")
	}
	// Only the four escapes specified by Linux mountinfo are admitted.
	stripped := strings.NewReplacer(`\040`, "", `\011`, "", `\012`, "", `\134`, "").Replace(encoded)
	if strings.Contains(stripped, `\`) {
		return "", errors.New("unknown host mount path escape")
	}
	return decoded, nil
}
func newRuntipiWriterScope(ctx context.Context, volumes []backuplifecycle.AppGroupVolume) (*runtipiWriterScope, error) {
	if err := requireRuntipiDaemonFilesystem(ctx); err != nil {
		return nil, err
	}
	raw, err := readRuntipiMountInfo()
	if err != nil {
		return nil, err
	}
	s := &runtipiWriterScope{mountBytes: raw}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		separator := -1
		for i, v := range f {
			if v == "-" {
				separator = i
				break
			}
		}
		if len(f) < 10 || separator < 6 || separator+3 >= len(f) {
			return nil, errors.New("invalid host mount topology")
		}
		root, e := runtipiMountPath(f[3])
		if e != nil {
			return nil, e
		}
		target, e := runtipiMountPath(f[4])
		if e != nil {
			return nil, e
		}
		s.mounts = append(s.mounts, runtipiHostMount{device: f[2], root: root, target: target, filesystem: f[separator+1]})
	}
	for _, v := range volumes {
		refs, e := s.subtree(v.Mountpoint)
		if e != nil {
			return nil, e
		}
		if !refs[0].info.IsDir() {
			return nil, errors.New("selected volume source is not a directory")
		}
		s.selected = append(s.selected, refs...)
	}
	if err = s.verify(ctx, nil); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *runtipiWriterScope) source(path string) (runtipiPhysicalSource, error) {
	var out runtipiPhysicalSource
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return out, errors.New("writable source is not an exact host path")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return out, errors.New("writable source alias cannot be resolved")
	}
	resolved = filepath.Clean(resolved)
	info, err := os.Stat(resolved)
	if err != nil {
		return out, errors.New("writable source identity cannot be inspected")
	}
	best := -1
	var selected runtipiHostMount
	for _, m := range s.mounts {
		if !runtipiPathContains(m.target, resolved) {
			continue
		}
		if len(m.target) > best {
			best = len(m.target)
			selected = m
		} else if len(m.target) == best && (selected.device != m.device || selected.root != m.root || selected.filesystem != m.filesystem) {
			return out, errors.New("overmounted source identity is ambiguous")
		}
	}
	if best < 0 {
		return out, errors.New("writable source has no host mount identity")
	}
	// Network, overlay/FUSE and unknown filesystem aliases cannot be proved
	// disjoint with local mountinfo alone; qualification must refuse them.
	switch selected.filesystem {
	case "ext4", "xfs", "btrfs", "tmpfs":
	default:
		return out, errors.New("writable source filesystem alias is not qualified")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return out, errors.New("writable source inode identity unavailable")
	}
	device := strconv.FormatUint(uint64(unix.Major(uint64(stat.Dev))), 10) + ":" + strconv.FormatUint(uint64(unix.Minor(uint64(stat.Dev))), 10)
	if device != selected.device {
		return out, errors.New("writable source changed mount during inspection")
	}
	relative, err := filepath.Rel(selected.target, resolved)
	if err != nil {
		return out, err
	}
	out = runtipiPhysicalSource{device: device, path: filepath.Join(selected.root, relative), info: info, resolved: resolved, original: path}
	s.checked = append(s.checked, out)
	return out, nil
}
func (s *runtipiWriterScope) subtree(path string) ([]runtipiPhysicalSource, error) {
	first, err := s.source(path)
	if err != nil {
		return nil, err
	}
	refs := []runtipiPhysicalSource{first}
	if first.info.IsDir() {
		for _, mount := range s.mounts {
			if mount.target == first.resolved || !runtipiPathContains(first.resolved, mount.target) {
				continue
			}
			child, e := s.source(mount.target)
			if e != nil {
				return nil, e
			}
			refs = append(refs, child)
		}
	}
	return refs, nil
}
func (s *runtipiWriterScope) overlaps(source string) (bool, error) {
	refs, err := s.subtree(source)
	if err != nil {
		return false, err
	}
	for _, writer := range refs {
		for _, selected := range s.selected {
			if os.SameFile(writer.info, selected.info) {
				return true, nil
			}
			if writer.device != selected.device {
				continue
			}
			if runtipiPathContains(writer.path, selected.path) || runtipiPathContains(selected.path, writer.path) {
				return true, nil
			}
			// A writable file bind can be a hardlink of a file inside a selected
			// directory. Without traversing data, same-filesystem disjointness is unknown.
			if !writer.info.IsDir() {
				return false, errors.New("writable file bind may alias selected volume through hardlinks")
			}
		}
	}
	return false, nil
}
func (s *runtipiWriterScope) verify(ctx context.Context, client *docker.ExecHandleClient) error {
	if err := requireRuntipiDaemonFilesystem(ctx); err != nil {
		return err
	}
	for _, p := range s.processes {
		pid, err := client.InspectMountProcess(ctx, p.container)
		if err != nil || pid != p.pid {
			return errors.New("container mount process changed during observation")
		}
		actual, err := os.Stat(fmt.Sprintf("/proc/%d/ns/mnt", p.pid))
		if err != nil || !os.SameFile(actual, p.namespace) {
			return errors.New("writer mount process namespace changed")
		}
		raw, err := readRuntipiMountFile(fmt.Sprintf("/proc/%d/mountinfo", p.pid))
		if err != nil || !bytes.Equal(raw, p.mounts) {
			return errors.New("writer kernel mount topology changed during observation")
		}
	}

	raw, err := readRuntipiMountInfo()
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, s.mountBytes) {
		return errors.New("host mount topology changed during writer observation")
	}
	for _, held := range s.checked {
		resolved, err := filepath.EvalSymlinks(held.original)
		if err != nil || filepath.Clean(resolved) != held.resolved {
			return errors.New("writable source alias changed during observation")
		}
		actual, err := os.Stat(held.resolved)
		if err != nil || !os.SameFile(held.info, actual) {
			return errors.New("writable source inode changed during observation")
		}
	}
	return nil
}

type runtipiMountProcess struct {
	container string
	pid       int
	mounts    []byte
	namespace os.FileInfo
}

// Check the actual kernel mount as well as Docker's host source spelling.
// Renamed/replaced sources cannot be mistaken for a disjoint current pathname.
func (s *runtipiWriterScope) overlapsMount(ctx context.Context, client *docker.ExecHandleClient, container, source, destination string, writable bool) (bool, error) {
	pid, err := client.InspectMountProcess(ctx, container)
	if err != nil {
		return false, err
	}
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return false, errors.New("container mount destination is ambiguous")
	}
	namespace, err := os.Stat(fmt.Sprintf("/proc/%d/ns/mnt", pid))
	if err != nil {
		return false, errors.New("container kernel mount namespace unavailable")
	}
	raw, err := readRuntipiMountFile(fmt.Sprintf("/proc/%d/mountinfo", pid))
	if err != nil {
		return false, err
	}
	host, err := s.source(source)
	if err != nil {
		return false, err
	}
	actual, err := os.Stat(filepath.Join(fmt.Sprintf("/proc/%d/root", pid), strings.TrimPrefix(destination, "/")))
	if err != nil || !os.SameFile(host.info, actual) {
		return false, errors.New("Docker source differs from actual mounted kernel inode")
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		separator := -1
		for i, v := range fields {
			if v == "-" {
				separator = i
				break
			}
		}
		if len(fields) < 10 || separator < 6 || separator+3 >= len(fields) {
			return false, errors.New("container mount topology invalid")
		}
		target, e := runtipiMountPath(fields[4])
		if e != nil {
			return false, e
		}
		if !runtipiPathContains(destination, target) {
			continue
		}
		root, e := runtipiMountPath(fields[3])
		if e != nil {
			return false, e
		}
		if target == destination {
			if found || fields[2] != host.device || root != host.path {
				return false, errors.New("kernel bind root differs from host physical source")
			}
			found = true
		}
		switch fields[separator+1] {
		case "ext4", "xfs", "btrfs", "tmpfs":
		default:
			return false, errors.New("recursive container bind alias is unverifiable")
		}
		// Docker's read-only parent flag does not prove its recursive child
		// mounts are read-only. Inspect each actual kernel mount below it.
		options := strings.Split(fields[5], ",")
		modeKnown, kernelWritable := false, false
		for _, option := range options {
			if option == "rw" || option == "ro" {
				if modeKnown {
					return false, errors.New("kernel bind write mode is ambiguous")
				}
				modeKnown, kernelWritable = true, option == "rw"
			}
		}
		if !modeKnown || target == destination && writable != kernelWritable {
			return false, errors.New("kernel bind write mode differs from observation")
		}
		if !kernelWritable {
			continue
		}
		for _, selected := range s.selected {
			if fields[2] != selected.device {
				continue
			}
			if runtipiPathContains(root, selected.path) || runtipiPathContains(selected.path, root) {
				return true, nil
			}
			entry, e := os.Stat(filepath.Join(fmt.Sprintf("/proc/%d/root", pid), strings.TrimPrefix(target, "/")))
			if e != nil || !entry.IsDir() {
				return false, errors.New("writable recursive file bind may alias selected data")
			}
		}
	}
	if !found {
		return false, errors.New("actual kernel bind mount root missing")
	}
	s.processes = append(s.processes, runtipiMountProcess{container: container, pid: pid, mounts: raw, namespace: namespace})
	if writable {
		return s.overlaps(source)
	}
	return false, nil
}
