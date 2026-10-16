// Package run executes external commands (docker, psql through docker exec, ...)
// with consistent error messages and a debug log.
package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	logMu  sync.Mutex
	logOut io.Writer = io.Discard
)

// SetLog sets the destination of the debug log (a file, usually).
func SetLog(w io.Writer) {
	logMu.Lock()
	logOut = w
	logMu.Unlock()
}

// Logf writes one line to the debug log.
func Logf(format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	fmt.Fprintf(logOut, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
}

// Error is returned when a command exits unsuccessfully.
type Error struct {
	Cmd    string
	Code   int
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if len(msg) > 1500 {
		msg = "…" + msg[len(msg)-1500:]
	}
	return fmt.Sprintf("%s failed (exit %d): %s", e.Cmd, e.Code, msg)
}

func (e *Error) Unwrap() error { return e.Err }

// Spec describes a command invocation.
type Spec struct {
	Name   string
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer // nil = captured and returned
	Env    []string  // extra KEY=VALUE entries
	// Redact hides the arguments in logs and errors (they contain secrets).
	Redact bool
}

func (s Spec) display() string {
	if s.Redact {
		return s.Name + " [args hidden]"
	}
	parts := append([]string{s.Name}, s.Args...)
	d := strings.Join(parts, " ")
	if len(d) > 300 {
		d = d[:300] + "…"
	}
	return d
}

// Do runs the command and returns captured stdout (when Spec.Stdout is nil).
func Do(ctx context.Context, s Spec) ([]byte, error) {
	cmd := exec.CommandContext(ctx, s.Name, s.Args...)
	cmd.Stdin = s.Stdin
	var out bytes.Buffer
	if s.Stdout != nil {
		cmd.Stdout = s.Stdout
	} else {
		cmd.Stdout = &out
	}
	stderr := &tailBuffer{max: 64 << 10}
	cmd.Stderr = stderr
	if len(s.Env) > 0 {
		cmd.Env = append(os.Environ(), s.Env...)
	}
	start := time.Now()
	err := cmd.Run()
	Logf("exec %s (%s) err=%v", s.display(), time.Since(start).Round(time.Millisecond), err)
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		se := stderr.String()
		if se != "" {
			Logf("  stderr: %s", truncate(se, 4000))
		}
		return out.Bytes(), &Error{Cmd: s.display(), Code: code, Stderr: se, Err: err}
	}
	return out.Bytes(), nil
}

// Output runs name with args and returns stdout.
func Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return Do(ctx, Spec{Name: name, Args: args})
}

// Text is Output trimmed to a string.
func Text(ctx context.Context, name string, args ...string) (string, error) {
	b, err := Output(ctx, name, args...)
	return strings.TrimSpace(string(b)), err
}

// Exists reports whether a binary is on PATH.
func Exists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// tailBuffer keeps only the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
