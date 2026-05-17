package rollout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecorderWritesRunFiles(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewRecorder(dir, Metadata{RunID: "20260515-120000", StackKit: "base-kit"})
	if err != nil {
		t.Fatalf("NewRecorder returned error: %v", err)
	}

	rec.Event(Event{Phase: "apply", Status: "running", Message: "starting"})
	if err := rec.Close(Summary{Status: "failed", FailureClass: "tofu_apply_failed"}); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	for _, rel := range []string{
		"runs/20260515-120000/metadata.json",
		"runs/20260515-120000/events.jsonl",
		"runs/20260515-120000/summary.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}
}

func TestRedactSecrets(t *testing.T) {
	got := Redact("token=abc password=hunter2 SENTRY_DSN=https://public@example/1")
	for _, secret := range []string{"abc", "hunter2"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted output still contains %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "SENTRY_DSN=") {
		t.Fatalf("expected key names to remain visible: %s", got)
	}
}

func TestClassifyFailure(t *testing.T) {
	cases := map[string]string{
		"docker daemon failed to start":                    "docker_daemon_failed",
		"init failed: provider registry request failed":    "tofu_init_failed",
		"OpenTofu apply failed":                            "tofu_apply_failed",
		"tenant-deployment spec fetch: admin returned 401": "spec_fetch_failed",
	}
	for input, want := range cases {
		if got := ClassifyFailure(input); got != want {
			t.Fatalf("ClassifyFailure(%q)=%q want %q", input, got, want)
		}
	}
}
