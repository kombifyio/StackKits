package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/docker"
	"github.com/mtalavi/coolify-mirror/internal/run"

	"golang.org/x/sys/unix"
)

// Work directories of this tool.
const (
	HomeDir     = "/data/coolify-mirror"
	BackupsDir  = "/data/coolify-mirror/backups"
	IncomingDir = "/data/coolify-mirror/incoming"
	LogsDir     = "/data/coolify-mirror/logs"
	stateFile   = "/data/coolify-mirror/paused.json"
)

// HumanBytes formats a byte count (1.5 GB).
// imageTitle names an image in a progress step: its first name, a long tag
// (a commit) shortened to 12 characters, and how many other names it has.
func imageTitle(refs []string) string {
	if len(refs) == 0 {
		return "Image"
	}
	r := refs[0]
	if i := strings.LastIndex(r, ":"); i > 0 && !strings.Contains(r[i:], "/") && len(r)-i-1 > 12 {
		r = r[:i+13]
	}
	if len(refs) > 1 {
		return fmt.Sprintf("Images %s  +%d more", r, len(refs)-1)
	}
	return "Image  " + r
}

func HumanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

// HumanDuration formats a duration as 1h02m03s / 2m03s / 5s.
func HumanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// FreeSpace returns the free bytes of the filesystem holding path.
func FreeSpace(path string) int64 {
	var st unix.Statfs_t
	for p := path; ; p = filepath.Dir(p) {
		if err := unix.Statfs(p, &st); err == nil {
			return int64(st.Bavail) * int64(st.Bsize)
		}
		if p == "/" || p == "." {
			return -1
		}
	}
}

// SpaceWarning returns a message when the restored data probably does not fit
// on this server ("" when it does, or when free space is unknown).
func SpaceWarning(dockerRoot string, man *Manifest) string {
	var docker, files int64
	for _, v := range man.Volumes {
		docker += v.Size
	}
	for _, im := range man.Images {
		docker += im.Size
	}
	for _, p := range man.Paths {
		files += p.Size
	}
	var msgs []string
	if free := FreeSpace(dockerRoot); free >= 0 && free < docker {
		msgs = append(msgs, fmt.Sprintf("Docker (%s) needs about %s but has %s free", dockerRoot, HumanBytes(docker), HumanBytes(free)))
	}
	if free := FreeSpace("/data"); free >= 0 && free < files {
		msgs = append(msgs, fmt.Sprintf("/data needs about %s but has %s free", HumanBytes(files), HumanBytes(free)))
	}
	return strings.Join(msgs, "; ")
}

// failpoint lets tests force a failure at a named point to exercise rollbacks
// (COOLIFY_MIRROR_FAILPOINT=<name>). It is never set in normal use.
func failpoint(name string) error {
	if os.Getenv("COOLIFY_MIRROR_FAILPOINT") == name {
		return fmt.Errorf("failpoint %s triggered", name)
	}
	return nil
}

// deniedHostPath reports host paths that must never be saved or overwritten.
func deniedHostPath(p string) bool {
	p = filepath.Clean(p)
	if p == "/" || !filepath.IsAbs(p) {
		return true
	}
	protected := []string{"/proc", "/sys", "/dev", "/run", "/var/run", "/boot", "/etc",
		"/var/lib/docker", "/var/lib/containerd", "/tmp", "/bin", "/sbin", "/usr", "/lib", "/lib64",
		"/data/coolify/source", "/data/coolify-mirror", "/root/.ssh"}
	for _, d := range protected {
		// the directory itself, anything inside it, or anything containing it
		// (e.g. a mount of /data would include Coolify's .env and keys).
		if p == d || strings.HasPrefix(p, d+"/") || strings.HasPrefix(d, p+"/") {
			return true
		}
	}
	// ~/.ssh of any user, and the home directories containing them.
	if parts := strings.Split(strings.TrimPrefix(p, "/"), "/"); parts[0] == "home" {
		if len(parts) <= 2 || parts[2] == ".ssh" {
			return true
		}
	}
	return false
}

// isAnonymousVolume reports docker's random 64-hex volume names.
func isAnonymousVolume(name string) bool {
	if len(name) != 64 {
		return false
	}
	for _, c := range name {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

type pausedState struct {
	Mode       string    `json:"mode"` // pause | stop
	Containers []string  `json:"containers"`
	Since      time.Time `json:"since"`
	PID        int       `json:"pid"`
}

func savePaused(mode string, ids []string) {
	_ = os.MkdirAll(HomeDir, 0o700)
	b, _ := json.Marshal(pausedState{Mode: mode, Containers: ids, Since: time.Now(), PID: os.Getpid()})
	_ = os.WriteFile(stateFile, b, 0o600)
}

func clearPaused() { _ = os.Remove(stateFile) }

const lockFile = "/data/coolify-mirror/.lock"

// Lock takes the global lock: only one backup or restore runs at a time on a
// server. It returns the unlock function.
func Lock() (func(), error) {
	if err := os.MkdirAll(HomeDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		b, _ := os.ReadFile(lockFile)
		f.Close()
		return nil, fmt.Errorf("another coolify-mirror backup/restore is running on this server (pid %s); wait for it to finish", strings.TrimSpace(string(b)))
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(fmt.Sprint(os.Getpid())), 0)
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, nil
}

// lockHeld reports whether another process holds the global lock.
func lockHeld() bool {
	f, err := os.OpenFile(lockFile, os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return true
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return false
}

// RecoverPaused resumes containers that a previous, interrupted run left
// paused or stopped. It returns a message when it did something. It never
// touches the containers of a backup that is still running.
func RecoverPaused(ctx context.Context) string {
	b, err := os.ReadFile(stateFile)
	if err != nil {
		return ""
	}
	var st pausedState
	if json.Unmarshal(b, &st) != nil || len(st.Containers) == 0 {
		clearPaused()
		return ""
	}
	// The backup that paused them still runs (it holds the lock until it ends,
	// and the kernel releases the lock if the process dies).
	if lockHeld() {
		return ""
	}
	if st.Mode == "stop" {
		err = docker.Start(ctx, st.Containers...)
	} else {
		err = docker.Unpause(ctx, st.Containers...)
	}
	if err != nil && !strings.Contains(err.Error(), "is not paused") {
		run.Logf("recover paused containers: %v", err)
	}
	clearPaused()
	return fmt.Sprintf("resumed %d container(s) left %sd by an interrupted backup", len(st.Containers), st.Mode)
}

// resume always runs, even when the backup context was cancelled.
func resume(mode string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if mode == "stop" {
			err = docker.Start(ctx, ids...)
		} else {
			err = docker.Unpause(ctx, ids...)
		}
		if err == nil {
			clearPaused()
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return errors.Join(fmt.Errorf("could not resume containers %s", strings.Join(ids, ", ")), err)
}
