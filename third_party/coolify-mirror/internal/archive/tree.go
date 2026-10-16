package archive

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// TreeOptions controls AddTree.
type TreeOptions struct {
	// Ctx aborts the walk when cancelled.
	Ctx context.Context
	// Progress is called with the number of file bytes added.
	Progress func(int64)
	// Warn receives non-fatal problems (files that changed while being read, ...).
	Warn func(string)
	// Skip excludes a path (relative to the root, "/"-separated).
	Skip func(rel string) bool
}

// TreeStats summarizes what AddTree stored.
type TreeStats struct {
	Files, Dirs, Links int
	Bytes              int64
}

type inodeKey struct{ dev, ino uint64 }

// AddTree stores the directory root (recursively) under prefix/. Symlinks are
// stored as links (never followed), sockets are skipped.
func (w *Writer) AddTree(prefix, root string, opt TreeOptions) (TreeStats, error) {
	var st TreeStats
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return st, err
	}
	hardlinks := map[inodeKey]string{}
	buf := make([]byte, 1<<20)

	walkErr := filepath.WalkDir(real, func(path string, d fs.DirEntry, err error) error {
		if opt.Ctx != nil && opt.Ctx.Err() != nil {
			return opt.Ctx.Err()
		}
		rel, rerr := filepath.Rel(real, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // vanished while walking
			}
			if opt.Warn != nil {
				opt.Warn(fmt.Sprintf("%s: %v", path, err))
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if rel != "." && opt.Skip != nil && opt.Skip(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		fi, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		mode := fi.Mode()
		if mode&fs.ModeSocket != 0 {
			return nil
		}
		name := prefix + "/"
		if rel != "." {
			name = prefix + "/" + rel
		} else if !fi.IsDir() {
			name = prefix // the root itself is a single file
		}
		link := ""
		if mode&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(fi, link)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		hdr.Name = name
		if fi.IsDir() && !strings.HasSuffix(hdr.Name, "/") {
			hdr.Name += "/"
		}
		hdr.Uname, hdr.Gname = "", ""
		hdr.Format = tar.FormatPAX
		hdr.AccessTime, hdr.ChangeTime = time.Time{}, time.Time{}
		sys, _ := fi.Sys().(*syscall.Stat_t)
		if sys != nil {
			hdr.Uid, hdr.Gid = int(sys.Uid), int(sys.Gid)
		}
		if x := readXattrs(path); len(x) > 0 {
			hdr.PAXRecords = map[string]string{}
			for k, v := range x {
				if keepXattr(k) {
					hdr.PAXRecords["SCHILY.xattr."+k] = v
				}
			}
		}
		if mode.IsRegular() && sys != nil && sys.Nlink > 1 {
			k := inodeKey{uint64(sys.Dev), sys.Ino}
			if first, ok := hardlinks[k]; ok {
				hdr.Typeflag = tar.TypeLink
				hdr.Linkname = first
				hdr.Size = 0
				st.Links++
				return w.tw.WriteHeader(hdr)
			}
			hardlinks[k] = name
		}
		if err := w.tw.WriteHeader(hdr); err != nil {
			return err
		}
		switch {
		case fi.IsDir():
			st.Dirs++
		case mode.IsRegular():
			st.Files++
			n, err := copyFile(w, path, hdr.Size, buf, opt)
			st.Bytes += n
			if err != nil {
				return err
			}
		default:
			st.Links++
		}
		return nil
	})
	return st, walkErr
}

// copyFile writes exactly size bytes of path into the tar stream. Files that
// shrink while being read are padded with zeros (the header is already written).
func copyFile(w *Writer, path string, size int64, buf []byte, opt TreeOptions) (int64, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NOATIME, 0)
	if err == unix.EPERM {
		fd, err = unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	var src io.Reader
	if err != nil {
		if opt.Warn != nil {
			opt.Warn(fmt.Sprintf("%s: cannot open (%v); stored as empty", path, err))
		}
		src = eofReader{}
	} else {
		f := os.NewFile(uintptr(fd), path)
		defer f.Close()
		src = f
	}
	pr := &progressReader{r: io.LimitReader(src, size), fn: opt.Progress, w: w}
	n, err := io.CopyBuffer(w.tw, pr, buf)
	if err != nil {
		return n, fmt.Errorf("%s: %w", path, err)
	}
	if n < size {
		if opt.Warn != nil {
			opt.Warn(fmt.Sprintf("%s changed while it was being saved (%d of %d bytes)", path, n, size))
		}
		pad := size - n
		zero := make([]byte, 64<<10)
		for pad > 0 {
			k := int64(len(zero))
			if pad < k {
				k = pad
			}
			if _, err := w.tw.Write(zero[:k]); err != nil {
				return n, err
			}
			pad -= k
		}
	}
	return n, nil
}

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

func readXattrs(path string) map[string]string {
	size, err := unix.Llistxattr(path, nil)
	if err != nil || size <= 0 {
		return nil
	}
	list := make([]byte, size)
	size, err = unix.Llistxattr(path, list)
	if err != nil || size <= 0 {
		return nil
	}
	out := map[string]string{}
	for _, name := range strings.Split(strings.TrimRight(string(list[:size]), "\x00"), "\x00") {
		if name == "" {
			continue
		}
		vs, err := unix.Lgetxattr(path, name, nil)
		if err != nil || vs < 0 {
			continue
		}
		val := make([]byte, vs)
		vs, err = unix.Lgetxattr(path, name, val)
		if err != nil {
			continue
		}
		out[name] = string(val[:vs])
	}
	return out
}

// TreeSize walks root and returns the total size of regular files and the
// number of entries (used for progress estimates before saving).
func TreeSize(root string, skip func(rel string) bool) (int64, int, error) {
	var total int64
	var count int
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return 0, 0, err
	}
	err = filepath.WalkDir(real, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && path != real {
				return fs.SkipDir
			}
			return nil
		}
		if skip != nil && path != real {
			if rel, e := filepath.Rel(real, path); e == nil && skip(filepath.ToSlash(rel)) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		count++
		if d.Type().IsRegular() {
			if info, e := d.Info(); e == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, count, err
}
