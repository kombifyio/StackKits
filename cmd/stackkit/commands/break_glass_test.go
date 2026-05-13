package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetBreakGlassFlags returns a cleanup func that restores the package-level
// flag global. We mutate it directly in tests because cobra parses --dir
// into the same global. Without a reset, test order matters; with the
// reset, each test is isolated.
func resetBreakGlassFlags(t *testing.T) {
	t.Helper()
	prev := breakGlassDir
	t.Cleanup(func() { breakGlassDir = prev })
	breakGlassDir = ""
}

func TestBreakGlassList_EmptyDir(t *testing.T) {
	resetBreakGlassFlags(t)
	tmp := t.TempDir()
	t.Setenv("STACKKIT_BREAK_GLASS_DIR", tmp)

	var out bytes.Buffer
	breakGlassListCmd.SetOut(&out)
	breakGlassListCmd.SetErr(&out)
	if err := breakGlassListCmd.RunE(breakGlassListCmd, nil); err != nil {
		t.Fatalf("list returned error: %v", err)
	}

	if !strings.Contains(out.String(), "No break-glass bundles found") {
		t.Errorf("unexpected output: %q", out.String())
	}
}

func TestBreakGlassList_ListsBundles(t *testing.T) {
	resetBreakGlassFlags(t)
	tmp := t.TempDir()

	mustWrite(t, filepath.Join(tmp, "break-glass-node-a.age"), "dummy", 0o644)
	mustWrite(t, filepath.Join(tmp, "break-glass-node-b.age"), "dummy", 0o644)
	// .txt convenience copies are intentionally ignored by `list`.
	mustWrite(t, filepath.Join(tmp, "break-glass-node-a.txt"), "dummy", 0o600)
	// .age files that are not break-glass bundles must not appear.
	mustWrite(t, filepath.Join(tmp, "unrelated.age"), "dummy", 0o644)

	t.Setenv("STACKKIT_BREAK_GLASS_DIR", tmp)

	var out bytes.Buffer
	breakGlassListCmd.SetOut(&out)
	breakGlassListCmd.SetErr(&out)
	if err := breakGlassListCmd.RunE(breakGlassListCmd, nil); err != nil {
		t.Fatalf("list returned error: %v", err)
	}

	s := out.String()
	if !strings.Contains(s, "break-glass-node-a.age") {
		t.Errorf("missing node-a bundle in output: %q", s)
	}
	if !strings.Contains(s, "break-glass-node-b.age") {
		t.Errorf("missing node-b bundle in output: %q", s)
	}
	if strings.Contains(s, "unrelated.age") {
		t.Errorf("unrelated.age should not be listed: %q", s)
	}
	if strings.Contains(s, ".txt") {
		t.Errorf("plaintext files should not be listed by 'list': %q", s)
	}
	if !strings.Contains(s, "(node: node-a)") || !strings.Contains(s, "(node: node-b)") {
		t.Errorf("expected node-name annotations in output: %q", s)
	}
}

func TestBreakGlassList_NonexistentDir(t *testing.T) {
	resetBreakGlassFlags(t)
	t.Setenv("STACKKIT_BREAK_GLASS_DIR", filepath.Join(t.TempDir(), "does", "not", "exist"))

	var out bytes.Buffer
	breakGlassListCmd.SetOut(&out)
	breakGlassListCmd.SetErr(&out)
	if err := breakGlassListCmd.RunE(breakGlassListCmd, nil); err != nil {
		t.Fatalf("list on nonexistent dir should not error: %v", err)
	}

	if !strings.Contains(out.String(), "does not exist") {
		t.Errorf("unexpected output: %q", out.String())
	}
}

func TestBreakGlassShowBundle_Exists(t *testing.T) {
	resetBreakGlassFlags(t)
	tmp := t.TempDir()
	bundlePath := filepath.Join(tmp, "break-glass-mynode.age")
	mustWrite(t, bundlePath, "encrypted bytes", 0o644)
	t.Setenv("STACKKIT_BREAK_GLASS_DIR", tmp)

	var stdout, stderr bytes.Buffer
	breakGlassShowBundleCmd.SetOut(&stdout)
	breakGlassShowBundleCmd.SetErr(&stderr)
	if err := breakGlassShowBundleCmd.RunE(breakGlassShowBundleCmd, []string{"mynode"}); err != nil {
		t.Fatalf("show-bundle returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "break-glass-mynode.age") {
		t.Errorf("expected path on stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Size:") {
		t.Errorf("expected size metadata on stderr: %q", stderr.String())
	}
}

func TestBreakGlassShowBundle_Missing(t *testing.T) {
	resetBreakGlassFlags(t)
	tmp := t.TempDir()
	t.Setenv("STACKKIT_BREAK_GLASS_DIR", tmp)

	err := breakGlassShowBundleCmd.RunE(breakGlassShowBundleCmd, []string{"unknownnode"})
	if err == nil || !strings.Contains(err.Error(), "no bundle found") {
		t.Errorf("expected 'no bundle found' error, got: %v", err)
	}
}

func TestBreakGlassShowBundle_RejectsPathTraversal(t *testing.T) {
	resetBreakGlassFlags(t)
	tmp := t.TempDir()
	t.Setenv("STACKKIT_BREAK_GLASS_DIR", tmp)

	cases := []string{
		"../etc/passwd",
		"../../something",
		"node/with/slash",
		`node\with\backslash`,
		"",
	}
	for _, n := range cases {
		t.Run(n, func(t *testing.T) {
			if err := breakGlassShowBundleCmd.RunE(breakGlassShowBundleCmd, []string{n}); err == nil {
				t.Errorf("expected error for invalid name %q", n)
			}
		})
	}
}

func TestBreakGlassRotate_StubsToPhase5(t *testing.T) {
	err := breakGlassRotateCmd.RunE(breakGlassRotateCmd, nil)
	if err == nil {
		t.Fatal("expected stub error, got nil")
	}
	if !strings.Contains(err.Error(), "Phase 5") {
		t.Errorf("expected 'Phase 5' in error, got: %v", err)
	}
}

func TestBreakGlassDirOrDefault_PriorityOrder(t *testing.T) {
	resetBreakGlassFlags(t)

	// 1. Default with no flag and no env.
	t.Setenv("STACKKIT_BREAK_GLASS_DIR", "")
	if got := breakGlassDirOrDefault(); got != defaultBreakGlassDir {
		t.Errorf("default fallback: got %q want %q", got, defaultBreakGlassDir)
	}

	// 2. Env var beats default.
	t.Setenv("STACKKIT_BREAK_GLASS_DIR", "/tmp/env-override")
	if got := breakGlassDirOrDefault(); got != "/tmp/env-override" {
		t.Errorf("env override: got %q want /tmp/env-override", got)
	}

	// 3. --dir flag beats env var.
	breakGlassDir = "/tmp/flag-override"
	if got := breakGlassDirOrDefault(); got != "/tmp/flag-override" {
		t.Errorf("flag override: got %q want /tmp/flag-override", got)
	}
}

func mustWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
