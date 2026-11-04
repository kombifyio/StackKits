package localevidence

import (
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// custodyLockRelRoot is the custody directory below a workspace root.
const custodyLockRelRoot = ".stackkit/custody"

// AcquireCustodyLock serializes one read-modify-write of a custody file
// across goroutines and processes, also when a process sees custody through
// a bind mount at another workspace root. The Basement core's identity
// workspace mounts single custody subdirectories (home-verifier read-write,
// home-authority read-only), never the custody root, so the advisory lock
// lives in the custody subdirectory that holds the file and is keyed by that
// directory's identity, never by the workspace root: the host CLI, the
// host-side identity signer and the core server exclude each other. A file
// directly below the custody root locks in the custody root. relPath is the
// custody file below the workspace root (".stackkit/custody/..."). It waits
// up to attempts times 20 ms; attempts 1 tries once.
func AcquireCustodyLock(workspaceRoot, relPath string, attempts int) (func(), error) {
	key, ok := strings.CutPrefix(filepath.ToSlash(relPath), custodyLockRelRoot+"/")
	if !ok || key == "" || strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return nil, errors.New("localevidence: a custody lock names a file below .stackkit/custody")
	}
	custodyRoot := filepath.Join(workspaceRoot, filepath.FromSlash(custodyLockRelRoot))
	directory := custodyRoot
	if sub, rest, nested := strings.Cut(key, "/"); nested {
		if sub == "." || sub == ".." || rest == "" {
			return nil, errors.New("localevidence: a custody lock names a file below .stackkit/custody")
		}
		if err := ensureCustodySubdirectory(custodyRoot, sub); err != nil {
			return nil, err
		}
		directory, key = filepath.Join(custodyRoot, sub), rest
	}
	held, err := confinedfs.Open(directory)
	if err != nil {
		return nil, err
	}
	tx, err := held.BeginTransaction()
	if err != nil {
		_ = held.Close()
		return nil, err
	}
	var lock *confinedfs.OutputLock
	for attempt := 1; ; attempt++ {
		lock, err = tx.TryAcquireOutputLock(key)
		if err == nil {
			break
		}
		var busy *confinedfs.OutputLockBusyError
		if !errors.As(err, &busy) || attempt >= attempts {
			_ = tx.Close()
			_ = held.Close()
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return func() {
		_ = lock.Release()
		_ = tx.Close()
		_ = held.Close()
	}, nil
}

// ensureCustodySubdirectory creates one private custody subdirectory below
// the custody root, confined to it, so the first write of a custody file can
// take its lock.
func ensureCustodySubdirectory(custodyRoot, sub string) error {
	root, err := confinedfs.Open(custodyRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	if err := tx.MkdirAll(sub, 0o700); err != nil {
		_ = tx.Close()
		return err
	}
	return tx.Close()
}
