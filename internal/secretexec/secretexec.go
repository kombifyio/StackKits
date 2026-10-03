// Package secretexec is the governed entrypoint shim of ADR-0045 Stage 2
// (plan 20, "Images that read a secret only from the environment"). A native
// container whose image reads a secret only from its environment starts this
// shim as its entrypoint: the shim reads each mounted owner-only secret file,
// adds the value to its own environment and replaces itself (execve) with the
// image's original entrypoint and command. The value then exists only in the
// running process, never in the OpenTofu root, its state or the container
// configuration (`docker inspect`).
//
// The shim is the static stackkit binary itself, started under the name
// BinaryName (argv[0] dispatch), so no further binary ships with a release.
package secretexec

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// BinaryName is the argv[0] under which the stackkit binary acts as the shim.
const BinaryName = "stackkit-secret-exec"

// MountPath is where a native container sees the shim (read-only).
const MountPath = "/run/stackkit/bin/" + BinaryName

// maxSecretBytes bounds one secret file.
const maxSecretBytes = 64 << 10

var variablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// Invoked reports whether argv0 names the shim.
func Invoked(argv0 string) bool { return filepath.Base(argv0) == BinaryName }

// Plan resolves the shim's arguments `--env NAME=FILE ... -- PROGRAM ARGS...`
// into the program, its argv and its environment. A secret file must hold
// exactly one value: no NUL, no line break, no surrounding space; a variable
// the environment already carries is refused, so an image cannot receive the
// value twice or have a declared variable silently replaced.
func Plan(args, environ []string, readFile func(string) ([]byte, error)) (string, []string, []string, error) {
	env := append([]string(nil), environ...)
	present := map[string]bool{}
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		present[name] = true
	}
	index := 0
	for ; index < len(args) && args[index] != "--"; index += 2 {
		if args[index] != "--env" || index+1 >= len(args) {
			return "", nil, nil, errors.New("usage: " + BinaryName + " --env NAME=FILE ... -- PROGRAM [ARGS...]")
		}
		name, file, ok := strings.Cut(args[index+1], "=")
		if !ok || !variablePattern.MatchString(name) || !filepath.IsAbs(file) {
			return "", nil, nil, fmt.Errorf("%s: --env needs NAME=/absolute/file", BinaryName)
		}
		if present[name] {
			return "", nil, nil, fmt.Errorf("%s: %s is already set", BinaryName, name)
		}
		value, err := readFile(file)
		if err != nil {
			return "", nil, nil, fmt.Errorf("%s: read the secret file for %s: %w", BinaryName, name, err)
		}
		if len(value) == 0 || len(value) > maxSecretBytes || strings.ContainsAny(string(value), "\x00\r\n") || strings.TrimSpace(string(value)) != string(value) {
			return "", nil, nil, fmt.Errorf("%s: the secret file for %s is not exactly one value", BinaryName, name)
		}
		env = append(env, name+"="+string(value))
		present[name] = true
	}
	if index >= len(args) || index+1 >= len(args) {
		return "", nil, nil, fmt.Errorf("%s: no program after --", BinaryName)
	}
	argv := args[index+1:]
	program, err := lookPath(argv[0], env)
	if err != nil {
		return "", nil, nil, err
	}
	return program, argv, env, nil
}

// Main runs the shim with the process arguments and never returns on
// success.
func Main(args []string) int {
	program, argv, env, err := Plan(args, os.Environ(), func(name string) ([]byte, error) {
		file, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		return io.ReadAll(io.LimitReader(file, maxSecretBytes+1))
	})
	if err == nil {
		err = syscall.Exec(program, argv, env)
	}
	fmt.Fprintln(os.Stderr, err)
	return 127
}

// lookPath resolves program like execvp, against the PATH of env.
func lookPath(program string, env []string) (string, error) {
	if strings.Contains(program, "/") {
		return program, nil
	}
	path := ""
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			path = value
		}
	}
	for _, directory := range filepath.SplitList(path) {
		candidate := filepath.Join(directory, program)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s: %s not found in PATH", BinaryName, program)
}
