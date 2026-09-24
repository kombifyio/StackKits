package runtimeexecutorlocal

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// stackKitServerBinarySource resolves the stackkit-server executable of the
// release that runs Apply. Production uses releaseStackKitServerBinary.
type stackKitServerBinarySource func() (string, error)

// releaseStackKitServerBinary returns the stackkit-server shipped beside the
// running stackkit CLI. Every release archive carries both, so the Core serves
// the MCP of exactly the release that applied it.
func releaseStackKitServerBinary() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the running stackkit CLI: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	// Keep the running CLI's extension, so a Windows release pairs
	// stackkit.exe with stackkit-server.exe as upgrade staging writes it.
	candidate := filepath.Join(filepath.Dir(executable), architecturev2renderer.StackKitServerStagedBinary+filepath.Ext(executable))
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf(
			"every StackKits Core runs stackkit-server, but no executable %s ships beside the running CLI; install the complete release archive (stackkit, stackkit-server, stackkit-mcp) and apply again",
			candidate,
		)
	}
	return candidate, nil
}

// stackKitServerRecreateMarker records that the running server must be
// recreated. It survives a failed Apply, so a retry still recreates it.
const stackKitServerRecreateMarker = ".stackkit-server-recreate"

// resolveCoreStackKitServerBinary runs before Apply rewrites compose.yaml, so
// a missing release binary leaves the previous project untouched.
func resolveCoreStackKitServerBinary(source stackKitServerBinarySource) (string, error) {
	if source == nil {
		return "", errors.New("stackkit-server binary source is not configured")
	}
	return source()
}

// prepareCoreStackKitServer mints the MCP credentials and stages the release
// binary next to compose.yaml, where Compose mounts it read-only. Compose sees
// no configuration change when either one changes, and the server reads its
// token only at start. It therefore reports recreate=true while an earlier
// Apply's server still runs the replaced binary or a superseded token.
func prepareCoreStackKitServer(workspaceRoot, runtimeDir, binary string) (recreate bool, err error) {
	// Staging runs before compose.yaml is rewritten, so the runtime directory
	// may not exist yet.
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return false, fmt.Errorf("create private Core runtime directory: %w", err)
	}
	credentials, err := localevidence.EnsureStackKitServerCredentials(workspaceRoot)
	if err != nil {
		return false, err
	}
	staged, changed, err := stageStackKitServerBinary(binary, filepath.Join(runtimeDir, architecturev2renderer.StackKitServerStagedBinary))
	if err != nil {
		return false, err
	}
	marker := filepath.Join(runtimeDir, stackKitServerRecreateMarker)
	if staged && (changed || credentials.MCPTokenMinted) {
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			return false, fmt.Errorf("record the pending stackkit-server recreate: %w", err)
		}
	}
	if _, err := os.Lstat(marker); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect the pending stackkit-server recreate: %w", err)
	}
	return false, nil
}

// completeCoreStackKitServerRecreate clears the marker after the recreate.
func completeCoreStackKitServerRecreate(runtimeDir string) error {
	if err := os.Remove(filepath.Join(runtimeDir, stackKitServerRecreateMarker)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear the pending stackkit-server recreate: %w", err)
	}
	return nil
}

// stageStackKitServerBinary reports whether an earlier Apply had staged a
// binary and whether this call replaced it.
func stageStackKitServerBinary(source, target string) (existed, changed bool, err error) {
	want, err := os.ReadFile(source) //nolint:gosec // the release binary beside the running CLI
	if err != nil {
		return false, false, fmt.Errorf("read stackkit-server release binary: %w", err)
	}
	current, readErr := os.ReadFile(target) //nolint:gosec // fixed file in the private runtime directory
	existed = readErr == nil
	if existed && sha256.Sum256(current) == sha256.Sum256(want) {
		return true, false, nil
	}
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return false, false, fmt.Errorf("inspect staged stackkit-server: %w", readErr)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".stackkit-server-*")
	if err != nil {
		return false, false, fmt.Errorf("stage stackkit-server: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(temporary.Name())
		}
	}()
	if _, err = io.Copy(temporary, bytes.NewReader(want)); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		// Not secret; the container executes it as a mapped read-only file.
		err = os.Chmod(temporary.Name(), 0o755) //nolint:gosec // executable release binary
	}
	if err == nil {
		// Rename, never rewrite in place: the running server keeps its old
		// inode, and writing to a running executable fails on Linux.
		err = os.Rename(temporary.Name(), target)
	}
	if err != nil {
		return false, false, fmt.Errorf("stage stackkit-server: %w", err)
	}
	return existed, true, nil
}
