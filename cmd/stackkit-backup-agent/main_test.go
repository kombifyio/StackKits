package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRun_NoArgs is the friendly-default for `stackkit-backup-agent`
// with no subcommand: print usage to stderr and exit 2 (POSIX usage-
// error convention). Catches regressions where main starts implicitly
// running as an agent because the dispatch fell through.
func TestRun_NoArgs(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run(nil, &out, &errBuf)
	assert.Equal(t, 2, code)
	assert.Contains(t, errBuf.String(), "Usage:")
	assert.Empty(t, out.String(), "no-arg run must not write to stdout")
}

// TestRun_Help is the explicit help path. Must exit 0 and write the
// usage to STDOUT (so `stackkit-backup-agent help | less` works
// cleanly).
func TestRun_Help(t *testing.T) {
	for _, flag := range []string{"-h", "--help", "help"} {
		t.Run(flag, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			code := run([]string{flag}, &out, &errBuf)
			assert.Equal(t, 0, code)
			assert.Contains(t, out.String(), "stackkit-backup-agent")
			assert.Empty(t, errBuf.String(), "help must not write to stderr")
		})
	}
}

// TestRun_UnknownSubcommand catches typos with a clear error and
// usage output rather than silently matching nothing.
func TestRun_UnknownSubcommand(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"deploy"}, &out, &errBuf)
	assert.Equal(t, 2, code)
	assert.Contains(t, errBuf.String(), `unknown subcommand "deploy"`)
}

// TestRun_EnrollMissingToken locks down the flag-validation contract:
// missing --token is a usage error (exit 2), not a scaffold-incomplete
// signal (exit 1). The distinction matters because the live Phase-4
// controller will return exit 1 for "controller unreachable" and
// operators need to be able to tell those two cases apart from CI.
func TestRun_EnrollMissingToken(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"enroll", "--endpoint", "https://x.example"}, &out, &errBuf)
	assert.Equal(t, 2, code)
	assert.Contains(t, errBuf.String(), "--token is required")
}

// TestRun_EnrollMissingEndpoint mirrors the token check.
func TestRun_EnrollMissingEndpoint(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"enroll", "--token", "abc"}, &out, &errBuf)
	assert.Equal(t, 2, code)
	assert.Contains(t, errBuf.String(), "--endpoint is required")
}

// TestRun_EnrollScaffoldExits1 documents the current scaffold exit
// code. When Phase 4 wires the controller, this test should be
// updated to expect 0 on success — keeping the test ensures the
// transition is intentional, not silent.
func TestRun_EnrollScaffoldExits1(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"enroll", "--token", "real-token-value", "--endpoint", "https://x.example"}, &out, &errBuf)
	assert.Equal(t, 1, code, "scaffold must exit 1 (not-implemented), distinct from usage errors (2)")
	assert.Contains(t, out.String(), "not implemented yet")
	// The token must be truncated, never echoed in full — even in a
	// stub message. Operators paste real tokens here and we don't
	// want logs full of them.
	assert.NotContains(t, out.String(), "real-token-value")
}

// TestRun_StatusExits0 — `status` is the one subcommand that always
// works because it just reports state. Smoke-test it so a future
// change that accidentally added I/O to it gets caught.
func TestRun_StatusExits0(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"status"}, &out, &errBuf)
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "stackkit-backup-agent")
	assert.Contains(t, out.String(), "scaffold")
	assert.Empty(t, errBuf.String())
}

// TestTruncate is a tiny unit test for the helper that prevents
// secret leakage in the enroll-stub output.
func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 5))
	assert.Equal(t, "abc", truncate("abc", 3))
	assert.Equal(t, "ab…", truncate("abcdef", 2))
	// Sanity: never panics on empty input.
	assert.Equal(t, "", truncate("", 4))

	// The truncated form must be strictly shorter than the input when
	// truncation actually happened — otherwise the secret bleeds.
	long := strings.Repeat("X", 100)
	got := truncate(long, 12)
	assert.True(t, len(got) < len(long))
	assert.True(t, strings.HasSuffix(got, "…"))
}
