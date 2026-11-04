//go:build linux

package runtimeprofile

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const rilAuthorityDirectory = "/var/lib/techstack-guard-executor"
const rilProfileFile = "ril-applied-profile.json"

// InvalidateRILAuthority runs before Product Apply. A failed or interrupted
// apply cannot leave the preceding hosted-profile grant usable. A system
// without the privileged Guard has no RIL authority to invalidate.
func InvalidateRILAuthority() error {
	if os.Geteuid() != 0 {
		return nil
	}
	before, err := os.Lstat(rilAuthorityDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, dir := range []string{"/", "/var", "/var/lib"} {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("unsafe RIL authority ancestor")
		}
	}
	stat, ok := before.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || before.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("unsafe RIL authority directory")
	}
	root, err := os.OpenRoot(rilAuthorityDirectory)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	opened, err := dir.Stat()
	if err != nil || !os.SameFile(before, opened) {
		dir.Close()
		return fmt.Errorf("RIL authority directory changed")
	}
	defer dir.Close()
	if err := root.Remove(rilProfileFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return dir.Sync()
}

// RILEvidence binds exact bytes that have already passed product verification.
type RILEvidence struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}
type rilAppliedAuthority struct {
	Schema     string        `json:"schema"`
	Profile    string        `json:"profile"`
	Workspace  string        `json:"workspace"`
	PlanHash   string        `json:"planHash"`
	ResultHash string        `json:"resultHash"`
	ExpiresAt  string        `json:"expiresAt,omitempty"`
	Evidence   []RILEvidence `json:"evidence"`
}

// RILWorkspaceConfigured is set only by the privileged Guard executor.
func RILWorkspaceConfigured() bool {
	return os.Geteuid() == 0 && os.Getenv("TECHSTACK_RIL_WORKSPACE_ROOT") != ""
}

// Only the executor's canonical workspace can publish the host authority.
// Admitted descendants still invalidate the previous authority before Apply.
func RILCanonicalWorkspace(workspace string) bool {
	configured := os.Getenv("TECHSTACK_RIL_WORKSPACE_ROOT")
	if !RILWorkspaceConfigured() || !filepath.IsAbs(configured) || filepath.Clean(configured) != configured {
		return false
	}
	absolute, err := filepath.Abs(workspace)
	return err == nil && filepath.Clean(absolute) == configured
}

// ReadRILEvidence accepts only root-owned, non-writable directory chains and
// single-link regular files; it never follows a symlink. This is also used for
// the evidence recheck immediately before publishing the root assertion.
func ReadRILEvidence(workspace, name string) ([]byte, error) {
	if !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace || !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return nil, errors.New("invalid RIL evidence path")
	}
	rel, err := filepath.Rel(workspace, name)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, errors.New("RIL evidence outside workspace")
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	parts := strings.Split(strings.TrimPrefix(name, "/"), "/")
	current := root
	var opened []*os.Root
	defer func() {
		for _, r := range opened {
			r.Close()
		}
	}()
	check := func(info os.FileInfo, dir bool) error {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || info.Mode().Perm()&0o022 != 0 || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe RIL evidence ownership")
		}
		if dir && !info.IsDir() {
			return errors.New("unsafe RIL evidence directory")
		}
		if !dir && (!info.Mode().IsRegular() || st.Nlink != 1) {
			return errors.New("unsafe RIL evidence file")
		}
		return nil
	}
	info, err := current.Stat(".")
	if err != nil {
		return nil, err
	}
	if err = check(info, true); err != nil {
		return nil, err
	}
	for _, part := range parts[:len(parts)-1] {
		before, e := current.Lstat(part)
		if e != nil {
			return nil, e
		}
		if e = check(before, true); e != nil {
			return nil, e
		}
		next, e := current.OpenRoot(part)
		if e != nil {
			return nil, e
		}
		opened = append(opened, next)
		after, e := next.Stat(".")
		if e != nil || !os.SameFile(before, after) {
			return nil, errors.New("RIL evidence directory changed")
		}
		current = next
	}
	leaf := parts[len(parts)-1]
	before, err := current.Lstat(leaf)
	if err != nil {
		return nil, err
	}
	if err = check(before, false); err != nil {
		return nil, err
	}
	f, err := current.Open(leaf)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("RIL evidence file changed")
	}
	return io.ReadAll(f)
}

func PublishRILAuthority(workspace, planHash, resultHash, expiresAt string, paths []string, verified [][]byte) error {
	if !RILWorkspaceConfigured() {
		return nil
	}
	configured := os.Getenv("TECHSTACK_RIL_WORKSPACE_ROOT")
	if configured != workspace || (len(paths) != 7 && len(paths) != 8) || len(paths) != len(verified) {
		return errors.New("RIL configured workspace or evidence invalid")
	}
	record := rilAppliedAuthority{Schema: "kombify.ril-applied-profile.v2", Profile: "standard", Workspace: workspace, PlanHash: planHash, ResultHash: resultHash, ExpiresAt: expiresAt}
	for i, p := range paths {
		raw, err := ReadRILEvidence(workspace, p)
		if err != nil {
			return err
		}
		if !bytes.Equal(raw, verified[i]) {
			return errors.New("RIL verified evidence changed")
		}
		sum := sha256.Sum256(raw)
		record.Evidence = append(record.Evidence, RILEvidence{Path: p, Digest: "sha256:" + hex.EncodeToString(sum[:])})
	}
	// The same root-owned ancestor chain is required for publication.
	for _, dir := range []string{"/", "/var", "/var/lib", rilAuthorityDirectory} {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
			return errors.New("unsafe RIL authority ancestor")
		}
	}
	root, err := os.OpenRoot(rilAuthorityDirectory)
	if err != nil {
		return err
	}
	defer root.Close()
	before, err := os.Lstat(rilAuthorityDirectory)
	if err != nil {
		return err
	}
	info, err := root.Stat(".")
	if err != nil || !os.SameFile(before, info) {
		return errors.New("RIL authority directory changed")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("unsafe RIL authority directory")
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temporary := ".ril-applied-profile." + rand.Text()
	f, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = root.Rename(temporary, rilProfileFile); err != nil {
		return err
	}
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// BeginRILAuthorityApply serializes activation against active hosted dials.
// Acquire before any workspace or output lock; retain it through Apply and
// authority publication. Invalidation happens after plan verification.
func BeginRILAuthorityApply() (func(), error) {
	if os.Geteuid() != 0 {
		return func() {}, nil
	}
	// /run is common to installer, executor and standalone Apply. The inode is
	// never removed during a boot; persistent Guard custody is not needed.
	for _, dir := range []string{"/", "/run"} {
		i, err := os.Lstat(dir)
		if err != nil {
			return nil, err
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0o022 != 0 {
			return nil, errors.New("unsafe RIL lock ancestor")
		}
	}
	parent, err := os.OpenRoot("/run")
	if err != nil {
		return nil, err
	}
	if err = parent.Mkdir("techstack-ril", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		parent.Close()
		return nil, err
	}
	parent.Close()
	const lockDirectory = "/run/techstack-ril"
	info, err := os.Lstat(lockDirectory)
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("unsafe RIL lock directory")
	}
	// Validate the complete directory chain before opening the common lock.
	for _, dir := range []string{"/", "/run", lockDirectory} {
		i, e := os.Lstat(dir)
		if e != nil {
			return nil, e
		}
		x, ok := i.Sys().(*syscall.Stat_t)
		if !ok || x.Uid != 0 || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0o022 != 0 {
			return nil, errors.New("unsafe RIL lock ancestor")
		}
	}
	root, err := os.OpenRoot(lockDirectory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("RIL lock directory changed")
	}
	f, err := root.OpenFile("activation.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	before, err := root.Lstat("activation.lock")
	if err != nil {
		f.Close()
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		return nil, errors.New("RIL lock changed")
	}
	fs, ok := after.Sys().(*syscall.Stat_t)
	if !ok || fs.Uid != 0 || fs.Nlink != 1 || !after.Mode().IsRegular() || after.Mode().Perm()&0o077 != 0 {
		f.Close()
		return nil, errors.New("unsafe RIL lock")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
