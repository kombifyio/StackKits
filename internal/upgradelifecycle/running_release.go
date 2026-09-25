package upgradelifecycle

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/releaseindex"
)

// ExecutorStateReleaseRunningExecutable is the release authority of a
// checkpoint whose prior release is the executable running the capture: a
// host that runs the release it last applied, without a workspace release
// cache (a Techstack-managed host, whose Agent verified the pinned release
// before it started this executable). The snapshot then carries no archive,
// SBOM, index or attestation identity; the captured executable blob digest
// is its only release identity.
const ExecutorStateReleaseRunningExecutable = "running-executable"

// currentExecutablePath resolves the executable running this process.
var currentExecutablePath = os.Executable

type runningExecutableReleaseToken struct{}

// RunningExecutableRelease is the release proof of the executable that runs
// this process, for a checkpoint whose prior release is that same release.
// It is created only by NewRunningExecutableRelease, which reads the exact
// executable bytes (and the stackkit-server beside it, when present).
type RunningExecutableRelease struct {
	token      *runningExecutableReleaseToken
	release    ExecutorStateRelease
	executable []byte
	server     []byte
}

// NewRunningExecutableRelease binds kit, exact release tag and platform to
// the bytes of the running executable.
func NewRunningExecutableRelease(
	kit, version string,
	platform releaseindex.Platform,
) (RunningExecutableRelease, error) {
	release := ExecutorStateRelease{
		Authority: ExecutorStateReleaseRunningExecutable,
		Kit:       strings.TrimSpace(kit), Version: version, Platform: platform,
	}
	if err := validateExecutorStateRelease(release); err != nil {
		return RunningExecutableRelease{}, err
	}
	path, err := currentExecutablePath()
	if err != nil {
		return RunningExecutableRelease{}, fmt.Errorf("executor state: resolve the running executable: %w", err)
	}
	executable, err := readRunningReleaseFile(path)
	if err != nil {
		return RunningExecutableRelease{}, fmt.Errorf("executor state: read the running executable: %w", err)
	}
	if len(executable) == 0 {
		return RunningExecutableRelease{}, errors.New("executor state: the running executable is empty")
	}
	server, err := readRunningReleaseFile(filepath.Join(
		filepath.Dir(path), executorStateServerExecutablePath(platform),
	))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return RunningExecutableRelease{}, fmt.Errorf("executor state: read the stackkit-server beside the running executable: %w", err)
	}
	return RunningExecutableRelease{
		token: &runningExecutableReleaseToken{}, release: release,
		executable: executable, server: server,
	}, nil
}

// Executables returns copies of the running stackkit executable and the
// stackkit-server beside it (nil when the release ships none).
func (running RunningExecutableRelease) Executables() (cli, server []byte) {
	return bytes.Clone(running.executable), bytes.Clone(running.server)
}

func readRunningReleaseFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > executorStateMaxBlobBytes {
		return nil, fmt.Errorf("%s is not a bounded regular file", filepath.Base(path))
	}
	return os.ReadFile(path)
}

// verifyRunningExecutableReleaseProof binds the captured executables to the
// bytes of the running executable, exactly as a verified installed release
// binds them to its archive.
func verifyRunningExecutableReleaseProof(
	running *RunningExecutableRelease,
	executable ExecutorStateExecutableInput,
) (ExecutorStateRelease, error) {
	if running == nil || running.token == nil {
		return ExecutorStateRelease{}, errors.New("executor state: running executable release proof is required")
	}
	if len(executable.Blob.Data) == 0 || !bytes.Equal(running.executable, executable.Blob.Data) {
		return ExecutorStateRelease{}, errors.New("executor state: recovery executable differs from the running executable")
	}
	var capturedServer []byte
	if executable.Server != nil {
		capturedServer = executable.Server.Data
	}
	if (running.server == nil) != (executable.Server == nil) || !bytes.Equal(running.server, capturedServer) {
		return ExecutorStateRelease{}, errors.New(
			"executor state: recovery stackkit-server differs from the one beside the running executable",
		)
	}
	return running.release, nil
}
