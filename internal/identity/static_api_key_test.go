package identity

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnsureStaticAPIKey_GeneratesOnce(t *testing.T) {
	dir := t.TempDir()

	k1, err := EnsureStaticAPIKey(dir)
	if err != nil {
		t.Fatalf("first EnsureStaticAPIKey: %v", err)
	}
	if len(k1) < 40 {
		// 32 random bytes base64-encoded is ~43 chars; 40 is a comfortable floor.
		t.Errorf("key too short: got %d chars, want >= 40", len(k1))
	}

	// Idempotency: a second call returns the same key.
	k2, err := EnsureStaticAPIKey(dir)
	if err != nil {
		t.Fatalf("second EnsureStaticAPIKey: %v", err)
	}
	if k1 != k2 {
		t.Errorf("key changed across calls: first=%q second=%q", k1, k2)
	}

	// File must exist with mode 0600 on POSIX. Windows ignores the bits —
	// os.Chmod on NTFS only flips the read-only flag — so skip the mode
	// assertion there.
	keyPath := filepath.Join(dir, ".stackkit", StaticAPIKeyFilename)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("key file mode = %o, want 0600", mode)
		}
	}
}

func TestEnsureStaticAPIKey_DirectoryAlreadyExists(t *testing.T) {
	// Pre-existing .stackkit/ (e.g., the operator already has state.yaml in
	// it) must not block key generation.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".stackkit"), 0o700); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}

	k, err := EnsureStaticAPIKey(dir)
	if err != nil {
		t.Fatalf("EnsureStaticAPIKey: %v", err)
	}
	if k == "" {
		t.Error("got empty key")
	}
}

func TestEnsureStaticAPIKey_RejectsTruncatedFile(t *testing.T) {
	// A truncated/edited key file must surface as a loud error rather than
	// silently being used to talk to PocketID and producing a 401 at
	// bootstrap time.
	dir := t.TempDir()
	stackkitDir := filepath.Join(dir, ".stackkit")
	if err := os.MkdirAll(stackkitDir, 0o700); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stackkitDir, StaticAPIKeyFilename), []byte("short"), 0o600); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	_, err := EnsureStaticAPIKey(dir)
	if err == nil {
		t.Fatal("expected error for truncated key file, got nil")
	}
	if !strings.Contains(err.Error(), "too short") {
		t.Errorf("error %q should mention 'too short'", err)
	}
}

func TestReadStaticAPIKey_Missing(t *testing.T) {
	_, err := ReadStaticAPIKey(t.TempDir())
	if err == nil {
		t.Fatal("expected error when key file is missing")
	}
	if !strings.Contains(err.Error(), "stackkit generate") {
		t.Errorf("error %q should hint to run 'stackkit generate'", err)
	}
}

func TestReadStaticAPIKey_Reads(t *testing.T) {
	dir := t.TempDir()
	want, err := EnsureStaticAPIKey(dir)
	if err != nil {
		t.Fatalf("setup EnsureStaticAPIKey: %v", err)
	}

	got, err := ReadStaticAPIKey(dir)
	if err != nil {
		t.Fatalf("ReadStaticAPIKey: %v", err)
	}
	if got != want {
		t.Errorf("ReadStaticAPIKey = %q, want %q", got, want)
	}
}

func TestReadStaticAPIKey_RejectsTruncatedFile(t *testing.T) {
	dir := t.TempDir()
	stackkitDir := filepath.Join(dir, ".stackkit")
	if err := os.MkdirAll(stackkitDir, 0o700); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stackkitDir, StaticAPIKeyFilename), []byte("x"), 0o600); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	_, err := ReadStaticAPIKey(dir)
	if err == nil {
		t.Fatal("expected error for truncated key file, got nil")
	}
	if !strings.Contains(err.Error(), "too short") {
		t.Errorf("error %q should mention 'too short'", err)
	}
}

// -----------------------------------------------------------------------------
// ENCRYPTION_KEY tests (Phase 1 / Task 14 stopper-fix)
//
// PocketID v2 refuses to start without ENCRYPTION_KEY (>=16 bytes raw). The
// test surface mirrors the STATIC_API_KEY tests above to keep both secrets
// on the same maintenance contract.
// -----------------------------------------------------------------------------

func TestEnsureEncryptionKey_GeneratesOnce(t *testing.T) {
	dir := t.TempDir()

	k1, err := EnsureEncryptionKey(dir)
	if err != nil {
		t.Fatalf("first EnsureEncryptionKey: %v", err)
	}
	// 24 raw bytes base64-encoded is ~32 chars; 30 is a comfortable floor
	// that's still well above PocketID's 16-byte (22-char base64) minimum.
	if len(k1) < 30 {
		t.Errorf("key too short: got %d chars, want >= 30", len(k1))
	}

	k2, err := EnsureEncryptionKey(dir)
	if err != nil {
		t.Fatalf("second EnsureEncryptionKey: %v", err)
	}
	if k1 != k2 {
		t.Errorf("key changed across calls: first=%q second=%q", k1, k2)
	}

	keyPath := filepath.Join(dir, ".stackkit", EncryptionKeyFilename)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("key file mode = %o, want 0600", mode)
		}
	}
}

func TestEnsureEncryptionKey_DirectoryAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".stackkit"), 0o700); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}

	k, err := EnsureEncryptionKey(dir)
	if err != nil {
		t.Fatalf("EnsureEncryptionKey: %v", err)
	}
	if k == "" {
		t.Error("got empty key")
	}
}

func TestEnsureEncryptionKey_RejectsTruncatedFile(t *testing.T) {
	dir := t.TempDir()
	stackkitDir := filepath.Join(dir, ".stackkit")
	if err := os.MkdirAll(stackkitDir, 0o700); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	// 10 chars is well below the 22-char minimum (16 raw bytes -> 22+ chars
	// base64) and would also fail PocketID's runtime check.
	if err := os.WriteFile(filepath.Join(stackkitDir, EncryptionKeyFilename), []byte("tooshort"), 0o600); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	_, err := EnsureEncryptionKey(dir)
	if err == nil {
		t.Fatal("expected error for truncated key file, got nil")
	}
	if !strings.Contains(err.Error(), "too short") {
		t.Errorf("error %q should mention 'too short'", err)
	}
}

func TestReadEncryptionKey_Missing(t *testing.T) {
	_, err := ReadEncryptionKey(t.TempDir())
	if err == nil {
		t.Fatal("expected error when key file is missing")
	}
	if !strings.Contains(err.Error(), "stackkit generate") {
		t.Errorf("error %q should hint to run 'stackkit generate'", err)
	}
}

func TestReadEncryptionKey_Reads(t *testing.T) {
	dir := t.TempDir()
	want, err := EnsureEncryptionKey(dir)
	if err != nil {
		t.Fatalf("setup EnsureEncryptionKey: %v", err)
	}

	got, err := ReadEncryptionKey(dir)
	if err != nil {
		t.Fatalf("ReadEncryptionKey: %v", err)
	}
	if got != want {
		t.Errorf("ReadEncryptionKey = %q, want %q", got, want)
	}
}

func TestReadEncryptionKey_RejectsTruncatedFile(t *testing.T) {
	dir := t.TempDir()
	stackkitDir := filepath.Join(dir, ".stackkit")
	if err := os.MkdirAll(stackkitDir, 0o700); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stackkitDir, EncryptionKeyFilename), []byte("x"), 0o600); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	_, err := ReadEncryptionKey(dir)
	if err == nil {
		t.Fatal("expected error for truncated key file, got nil")
	}
	if !strings.Contains(err.Error(), "too short") {
		t.Errorf("error %q should mention 'too short'", err)
	}
}

func TestEnsureKeys_AreIndependent(t *testing.T) {
	// Both keys must be generated independently and not collide on filename.
	// A single .stackkit/ directory should hold both files with distinct
	// values so neither can be mistaken for the other.
	dir := t.TempDir()

	apiKey, err := EnsureStaticAPIKey(dir)
	if err != nil {
		t.Fatalf("EnsureStaticAPIKey: %v", err)
	}
	encKey, err := EnsureEncryptionKey(dir)
	if err != nil {
		t.Fatalf("EnsureEncryptionKey: %v", err)
	}
	if apiKey == encKey {
		t.Errorf("api key and encryption key collided: %q", apiKey)
	}

	for _, fn := range []string{StaticAPIKeyFilename, EncryptionKeyFilename} {
		if _, err := os.Stat(filepath.Join(dir, ".stackkit", fn)); err != nil {
			t.Errorf("expected %s to exist: %v", fn, err)
		}
	}
}
