package archive

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Extractor writes archive entries below Root, restoring numeric ownership,
// modes (incl. setuid/setgid/sticky), timestamps, xattrs, symlinks, hardlinks,
// device nodes and fifos. It never writes outside Root.
type Extractor struct {
	Root     string
	Progress func(int64)
	Warn     func(string)

	dirs     []dirMeta
	symlinks map[string]bool
	buf      []byte
}

type dirMeta struct {
	path string
	hdr  tar.Header
}

// NewExtractor prepares extraction into root (created if missing).
func NewExtractor(root string) (*Extractor, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Extractor{Root: root, symlinks: map[string]bool{}, buf: make([]byte, 1<<20)}, nil
}

func (x *Extractor) warn(format string, a ...any) {
	if x.Warn != nil {
		x.Warn(fmt.Sprintf(format, a...))
	}
}

// safeJoin maps an archive-relative path to a path below Root.
func (x *Extractor) safeJoin(rel string) (string, error) {
	rel = strings.TrimPrefix(rel, "/")
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe path %q in backup", rel)
		}
	}
	clean := path.Clean("/" + rel)
	if clean == "/" {
		return x.Root, nil
	}
	// Refuse to write through a symlink created by this extraction.
	for p := path.Dir(clean); p != "/" && p != "."; p = path.Dir(p) {
		if x.symlinks[p] {
			return "", fmt.Errorf("path %q goes through a symlink", rel)
		}
	}
	return filepath.Join(x.Root, filepath.FromSlash(clean)), nil
}

// Entry extracts one entry. rel is the path relative to the extraction root;
// for hardlinks, linkRel is the relative path of the link target.
func (x *Extractor) Entry(rel string, hdr *tar.Header, r io.Reader, linkRel string) error {
	target, err := x.safeJoin(rel)
	if err != nil {
		return err
	}
	if target != x.Root {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := os.Mkdir(target, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if fi, err := os.Lstat(target); err == nil && !fi.IsDir() {
			if err := os.RemoveAll(target); err != nil {
				return err
			}
			if err := os.Mkdir(target, 0o700); err != nil {
				return err
			}
		}
		x.dirs = append(x.dirs, dirMeta{path: target, hdr: *hdr})
		return nil

	case tar.TypeReg, tar.TypeRegA:
		if fi, err := os.Lstat(target); err == nil && (fi.IsDir() || fi.Mode()&os.ModeSymlink != 0) {
			if err := os.RemoveAll(target); err != nil {
				return err
			}
		}
		fd, err := unix.Open(target, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err != nil {
			return fmt.Errorf("create %s: %w", target, err)
		}
		f := os.NewFile(uintptr(fd), target)
		_, err = io.CopyBuffer(f, &progressReader{r: r, fn: x.Progress}, x.buf)
		if err == nil {
			err = x.applyFd(f, hdr)
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return x.setTimes(target, hdr)

	case tar.TypeSymlink:
		_ = os.RemoveAll(target)
		if err := os.Symlink(hdr.Linkname, target); err != nil {
			return err
		}
		x.symlinks[path.Clean("/"+strings.TrimPrefix(rel, "/"))] = true
		if err := os.Lchown(target, hdr.Uid, hdr.Gid); err != nil {
			x.warn("chown %s: %v", target, err)
		}
		return x.setTimes(target, hdr)

	case tar.TypeLink:
		src, err := x.safeJoin(linkRel)
		if err != nil {
			return err
		}
		_ = os.RemoveAll(target)
		return os.Link(src, target)

	case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		_ = os.RemoveAll(target)
		mode := uint32(hdr.Mode & 0o7777)
		switch hdr.Typeflag {
		case tar.TypeChar:
			mode |= unix.S_IFCHR
		case tar.TypeBlock:
			mode |= unix.S_IFBLK
		default:
			mode |= unix.S_IFIFO
		}
		dev := unix.Mkdev(uint32(hdr.Devmajor), uint32(hdr.Devminor))
		if err := unix.Mknod(target, mode, int(dev)); err != nil {
			x.warn("mknod %s: %v", target, err)
			return nil
		}
		_ = os.Lchown(target, hdr.Uid, hdr.Gid)
		_ = os.Chmod(target, os.FileMode(hdr.Mode&0o777))
		return x.setTimes(target, hdr)

	case tar.TypeXGlobalHeader, tar.TypeXHeader:
		return nil
	default:
		x.warn("skipped unsupported entry %s (type %c)", rel, hdr.Typeflag)
		return nil
	}
}

func (x *Extractor) applyFd(f *os.File, hdr *tar.Header) error {
	if err := f.Chown(hdr.Uid, hdr.Gid); err != nil {
		x.warn("chown %s: %v", f.Name(), err)
	}
	// chmod after chown: chown clears setuid/setgid bits.
	if err := unix.Fchmod(int(f.Fd()), fileMode(hdr)); err != nil {
		return err
	}
	for k, v := range hdr.PAXRecords {
		if name, ok := strings.CutPrefix(k, "SCHILY.xattr."); ok && keepXattr(name) {
			if err := unix.Fsetxattr(int(f.Fd()), name, []byte(v), 0); err != nil {
				xattrFailed(name, f.Name(), err)
			}
		}
	}
	return nil
}

// keepXattr skips SELinux labels (host specific; Docker relabels volumes itself).
func keepXattr(name string) bool {
	return name != "security.selinux"
}

// xattrFailed only logs: filesystems without xattr support are common and
// the data itself is restored correctly.
func xattrFailed(name, path string, err error) {
	if xattrLog != nil {
		xattrLog(fmt.Sprintf("xattr %s on %s: %v", name, path, err))
	}
}

// xattrLog receives non-critical extended attribute errors (debug log).
var xattrLog func(string)

// SetDebugLog routes non-critical extraction messages to a debug log.
func SetDebugLog(f func(string)) { xattrLog = f }

func fileMode(hdr *tar.Header) uint32 {
	m := uint32(hdr.Mode & 0o777)
	if hdr.Mode&0o4000 != 0 {
		m |= unix.S_ISUID
	}
	if hdr.Mode&0o2000 != 0 {
		m |= unix.S_ISGID
	}
	if hdr.Mode&0o1000 != 0 {
		m |= unix.S_ISVTX
	}
	return m
}

func (x *Extractor) setTimes(p string, hdr *tar.Header) error {
	mt := hdr.ModTime
	if mt.IsZero() {
		mt = time.Now()
	}
	ts := []unix.Timespec{unix.NsecToTimespec(mt.UnixNano()), unix.NsecToTimespec(mt.UnixNano())}
	if err := unix.UtimesNanoAt(unix.AT_FDCWD, p, ts, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		x.warn("times %s: %v", p, err)
	}
	return nil
}

// Finish applies directory ownership, modes, xattrs and times (deepest first,
// so writing children does not change a parent's mtime afterwards).
func (x *Extractor) Finish() error {
	sort.SliceStable(x.dirs, func(i, j int) bool {
		return strings.Count(x.dirs[i].path, string(os.PathSeparator)) > strings.Count(x.dirs[j].path, string(os.PathSeparator))
	})
	for _, d := range x.dirs {
		if err := os.Lchown(d.path, d.hdr.Uid, d.hdr.Gid); err != nil {
			x.warn("chown %s: %v", d.path, err)
		}
		if err := unix.Chmod(d.path, fileMode(&d.hdr)); err != nil {
			x.warn("chmod %s: %v", d.path, err)
		}
		for k, v := range d.hdr.PAXRecords {
			if name, ok := strings.CutPrefix(k, "SCHILY.xattr."); ok && keepXattr(name) {
				if err := unix.Lsetxattr(d.path, name, []byte(v), 0); err != nil {
					xattrFailed(name, d.path, err)
				}
			}
		}
		_ = x.setTimes(d.path, &d.hdr)
	}
	x.dirs = nil
	return nil
}
