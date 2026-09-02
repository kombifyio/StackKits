// Package clibinding binds local process dispatch to an explicit StackKit CLI
// from one build. It is shared by MCP and local lifecycle triggers.
package clibinding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Identity is the secret-free identity of an explicitly selected CLI file.
type Identity struct {
	Path      string `json:"path"`
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
	Digest    string `json:"digest"`
}

type Binding struct{ identity Identity }

func (binding *Binding) Path() string       { return binding.identity.Path }
func (binding *Binding) Identity() Identity { return binding.identity }

// Sibling returns the packaged CLI beside the current CLI, server or MCP
// executable. It never searches PATH.
func Sibling() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	name := "stackkit"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return explicitPath(filepath.Join(filepath.Dir(executable), name))
}

func Bind(parent context.Context, path, version, commit string) (*Binding, error) {
	if version == "" || version == "dev" || commit == "" || commit == "unknown" {
		return nil, fmt.Errorf("local CLI binding requires an exact version and git commit")
	}
	path, err := explicitPath(path)
	if err != nil {
		return nil, err
	}
	digest, err := hash(path)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "version").CombinedOutput() // #nosec G204 -- explicit packaged CLI; no PATH lookup or shell.
	if err != nil {
		return nil, fmt.Errorf("read bound stackkit CLI identity: %w", err)
	}
	actualVersion, actualCommit := parseIdentity(string(output))
	if actualVersion != version || actualCommit != commit {
		return nil, fmt.Errorf("stackkit CLI identity mismatch: got version=%q commit=%q, want version=%q commit=%q", actualVersion, actualCommit, version, commit)
	}
	binding := &Binding{identity: Identity{Path: path, Version: version, GitCommit: commit, Digest: digest}}
	if err := binding.Verify(); err != nil {
		return nil, err
	}
	return binding, nil
}

// VerifyIdentity checks the current file against a previously authenticated
// identity. Callers must authenticate persisted identities before using them.
func VerifyIdentity(identity Identity) error {
	if identity.Version == "" || identity.Version == "dev" || identity.GitCommit == "" || identity.GitCommit == "unknown" ||
		len(identity.Digest) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(identity.Digest, "sha256:") {
		return fmt.Errorf("bound stackkit CLI identity is incomplete")
	}
	if !filepath.IsAbs(identity.Path) {
		return fmt.Errorf("bound stackkit CLI path must be absolute")
	}
	path, err := explicitPath(identity.Path)
	if err != nil {
		return err
	}
	if path != identity.Path {
		return fmt.Errorf("bound stackkit CLI path is not canonical")
	}
	digest, err := hash(path)
	if err != nil {
		return err
	}
	if digest != identity.Digest {
		return fmt.Errorf("bound stackkit CLI changed; rebind the same-build packaged CLI")
	}
	return nil
}

func (binding *Binding) Verify() error {
	if binding == nil {
		return fmt.Errorf("local CLI is not bound")
	}
	return VerifyIdentity(binding.identity)
}

func explicitPath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("local process dispatch requires an explicit packaged stackkit CLI path")
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
		return "", fmt.Errorf("stackkit CLI path is not a regular file")
	}
	return filepath.Clean(path), nil
}

func parseIdentity(output string) (version, commit string) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "stackkit version ") {
			version = strings.TrimSpace(strings.TrimPrefix(line, "stackkit version "))
		}
		if strings.HasPrefix(line, "Git commit: ") {
			commit = strings.TrimSpace(strings.TrimPrefix(line, "Git commit: "))
		}
	}
	return version, commit
}

func hash(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- explicit regular CLI file.
	if err != nil {
		return "", fmt.Errorf("open stackkit CLI: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash stackkit CLI: %w", err)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}
