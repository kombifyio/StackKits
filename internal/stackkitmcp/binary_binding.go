package stackkitmcp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const cliIdentityTimeout = 5 * time.Second

type cliBinaryBinding struct {
	path   string
	digest [sha256.Size]byte
}

// SiblingStackkitBinary returns the packaged CLI beside stackkit-server or
// stackkit-mcp. It deliberately never searches PATH: process-backed MCP tools
// must execute the CLI shipped in the same release bundle.
func SiblingStackkitBinary() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve MCP executable: %w", err)
	}
	name := "stackkit"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return validateExplicitCLIBinary(filepath.Join(filepath.Dir(executable), name))
}

func bindCLIBinary(opts Options) (*cliBinaryBinding, error) {
	if opts.Version == "dev" || opts.GitCommit == "unknown" {
		return nil, fmt.Errorf("process-backed MCP CLI binding requires an exact release version and git commit")
	}
	path, err := validateExplicitCLIBinary(opts.Binary)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cliIdentityTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "version").CombinedOutput() // #nosec G204 -- path is an absolute packaged sibling or explicit operator-bound executable.
	if err != nil {
		return nil, fmt.Errorf("read bound stackkit CLI identity: %w", err)
	}
	version, commit, err := parseCLIIdentity(string(output))
	if err != nil {
		return nil, err
	}
	if version != opts.Version || commit != opts.GitCommit {
		return nil, fmt.Errorf("stackkit CLI identity mismatch: got version=%q commit=%q, want version=%q commit=%q", version, commit, opts.Version, opts.GitCommit)
	}
	digest, err := hashCLIBinary(path)
	if err != nil {
		return nil, err
	}
	return &cliBinaryBinding{path: path, digest: digest}, nil
}

func validateExplicitCLIBinary(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("process-backed MCP tools require an explicit packaged stackkit CLI path")
	}
	path, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve stackkit CLI path: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat stackkit CLI: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("stackkit CLI path is not a regular file: %s", path)
	}
	return filepath.Clean(path), nil
}

func parseCLIIdentity(output string) (string, string, error) {
	var version, commit string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "stackkit version "):
			version = strings.TrimSpace(strings.TrimPrefix(line, "stackkit version "))
		case strings.HasPrefix(line, "Git commit: "):
			commit = strings.TrimSpace(strings.TrimPrefix(line, "Git commit: "))
		}
	}
	if version == "" || commit == "" {
		return "", "", fmt.Errorf("stackkit CLI version output does not contain exact version and git commit")
	}
	return version, commit, nil
}

func hashCLIBinary(path string) ([sha256.Size]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- path was resolved and verified as an explicit regular CLI file.
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("open stackkit CLI: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("hash stackkit CLI: %w", err)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}

func (a *App) verifyCLIBinding() error {
	if a.cliBinding == nil {
		if a.cliBindingError != nil {
			return a.cliBindingError
		}
		return fmt.Errorf("process-backed MCP CLI is not bound")
	}
	digest, err := hashCLIBinary(a.cliBinding.path)
	if err != nil {
		return err
	}
	if digest != a.cliBinding.digest {
		return fmt.Errorf("bound stackkit CLI changed after MCP startup; restart with the packaged same-build CLI")
	}
	return nil
}
