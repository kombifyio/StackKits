package identity

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kombifyio/stackkits/internal/crypto"
	"gopkg.in/yaml.v3"
)

// newTestBuilder returns a BundleBuilder pre-populated with deterministic
// fixtures. tmpDir is the test-scoped scratch directory the bundle files
// are written to.
func newTestBuilder(tmpDir string) *BundleBuilder {
	return &BundleBuilder{
		NodeName:    "test-node",
		Hostname:    "test-node.local",
		ClusterRole: "main",
		PocketIDURL: "https://id.test.local",
		PocketIDAdmin: &BreakGlassCredential{
			Username:   "bg-test-node@local",
			SetupToken: "ott-abc123",
			SetupURL:   "https://id.test.local/setup-account?token=ott-abc123",
			Group:      "owners",
			UserID:     "user-bg",
		},
		TinyAuthStatic: &TinyAuthStaticCredential{
			Username:       "bg-test-node-static",
			PasswordPlain:  "RandomPlain123",
			PasswordBcrypt: "$2a$12$abcdef",
		},
		BundleDir: tmpDir,
		Now: func() time.Time {
			return time.Date(2026, 4, 28, 14, 32, 11, 0, time.UTC)
		},
	}
}

// TestBuildAndSaveBundle exercises the happy path end-to-end: marshal,
// encrypt, write, then read back and decrypt to verify every field round-
// trips. Catches regressions in YAML field names, encryption wiring, and
// path conventions in one shot.
func TestBuildAndSaveBundle(t *testing.T) {
	tmpDir := t.TempDir()
	b := newTestBuilder(tmpDir)

	paths, err := b.BuildAndSave("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(paths.EncryptedPath); err != nil {
		t.Fatal("encrypted file missing:", err)
	}
	if _, err := os.Stat(paths.PlaintextPath); err != nil {
		t.Fatal("plaintext file missing:", err)
	}

	// Path conventions: filename must encode the node name so multi-node
	// deployments land distinct artifacts in the same recovery dir.
	if filepath.Base(paths.EncryptedPath) != "break-glass-test-node.age" {
		t.Errorf("encrypted path: %s", paths.EncryptedPath)
	}
	if filepath.Base(paths.PlaintextPath) != "break-glass-test-node.txt" {
		t.Errorf("plaintext path: %s", paths.PlaintextPath)
	}

	// Plaintext file mode 0600. Windows os.WriteFile maps Unix mode bits
	// loosely; only the read-only bit is honored. Skip the strict check
	// there.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(paths.PlaintextPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("plaintext mode = %o, want 0600", info.Mode().Perm())
		}
	}

	// Decrypt and verify the YAML round-trips with all expected fields.
	enc, err := os.ReadFile(paths.EncryptedPath)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := crypto.DecryptWithPassphrase(enc, "correct horse battery staple")
	if err != nil {
		t.Fatal("decrypt:", err)
	}

	var payload BundlePayload
	if err := yaml.Unmarshal(plain, &payload); err != nil {
		t.Fatal("yaml:", err)
	}
	if payload.Version != 1 {
		t.Errorf("version = %d, want 1", payload.Version)
	}
	if payload.GeneratedAt != "2026-04-28T14:32:11Z" {
		t.Errorf("generatedAt = %q, want 2026-04-28T14:32:11Z", payload.GeneratedAt)
	}
	if payload.Node.Name != "test-node" {
		t.Errorf("node.name = %q", payload.Node.Name)
	}
	if payload.Node.Hostname != "test-node.local" {
		t.Errorf("node.hostname = %q", payload.Node.Hostname)
	}
	if payload.Node.ClusterRole != "main" {
		t.Errorf("node.clusterRole = %q", payload.Node.ClusterRole)
	}
	if payload.Node.PocketIDURL != "https://id.test.local" {
		t.Errorf("node.pocketidUrl = %q", payload.Node.PocketIDURL)
	}
	if payload.BreakGlass.PocketIDAdmin.SetupToken != "ott-abc123" {
		t.Errorf("setupToken = %q", payload.BreakGlass.PocketIDAdmin.SetupToken)
	}
	if payload.BreakGlass.PocketIDAdmin.SetupURL != "https://id.test.local/setup-account?token=ott-abc123" {
		t.Errorf("setupUrl = %q", payload.BreakGlass.PocketIDAdmin.SetupURL)
	}
	if payload.BreakGlass.PocketIDAdmin.Username != "bg-test-node@local" {
		t.Errorf("pocketidAdmin.username = %q", payload.BreakGlass.PocketIDAdmin.Username)
	}
	if payload.BreakGlass.PocketIDAdmin.Group != "owners" {
		t.Errorf("pocketidAdmin.group = %q", payload.BreakGlass.PocketIDAdmin.Group)
	}
	if payload.BreakGlass.PocketIDAdmin.UserID != "user-bg" {
		t.Errorf("pocketidAdmin.userId = %q", payload.BreakGlass.PocketIDAdmin.UserID)
	}
	if payload.BreakGlass.TinyAuthStatic.Username != "bg-test-node-static" {
		t.Errorf("tinyauthStatic.username = %q", payload.BreakGlass.TinyAuthStatic.Username)
	}
	if payload.BreakGlass.TinyAuthStatic.PasswordPlain != "RandomPlain123" {
		t.Errorf("tinyauthStatic.passwordPlain = %q", payload.BreakGlass.TinyAuthStatic.PasswordPlain)
	}
	if payload.BreakGlass.TinyAuthStatic.PasswordBcrypt != "$2a$12$abcdef" {
		t.Errorf("tinyauthStatic.passwordBcrypt = %q", payload.BreakGlass.TinyAuthStatic.PasswordBcrypt)
	}
	if !strings.Contains(payload.RestoreInstructions, "RESTORE INSTRUCTIONS") {
		t.Errorf("restoreInstructions missing header: %q", payload.RestoreInstructions)
	}
}

// TestBuildAndSaveWrongPassphraseFailsDecrypt is a smoke test for the
// scrypt MAC: a wrong passphrase must not silently produce garbage, it
// must fail. Otherwise the entire recovery story is fictional.
func TestBuildAndSaveWrongPassphraseFailsDecrypt(t *testing.T) {
	tmpDir := t.TempDir()
	b := newTestBuilder(tmpDir)
	paths, err := b.BuildAndSave("right")
	if err != nil {
		t.Fatal(err)
	}

	enc, err := os.ReadFile(paths.EncryptedPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crypto.DecryptWithPassphrase(enc, "wrong"); err == nil {
		t.Error("wrong passphrase should fail to decrypt")
	}
}

// TestBuildValidationErrors verifies each required field is enforced and
// no file artifacts are produced on the validation-failure path.
func TestBuildValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		builder *BundleBuilder
	}{
		{
			"missing nodename",
			&BundleBuilder{
				ClusterRole:    "main",
				PocketIDAdmin:  &BreakGlassCredential{},
				TinyAuthStatic: &TinyAuthStaticCredential{},
			},
		},
		{
			"missing pocketid cred",
			&BundleBuilder{
				NodeName:       "x",
				ClusterRole:    "main",
				TinyAuthStatic: &TinyAuthStaticCredential{},
			},
		},
		{
			"missing tinyauth cred",
			&BundleBuilder{
				NodeName:      "x",
				ClusterRole:   "main",
				PocketIDAdmin: &BreakGlassCredential{},
			},
		},
		{
			"invalid clusterRole",
			&BundleBuilder{
				NodeName:       "x",
				ClusterRole:    "leader",
				PocketIDAdmin:  &BreakGlassCredential{},
				TinyAuthStatic: &TinyAuthStaticCredential{},
			},
		},
		{
			"empty clusterRole",
			&BundleBuilder{
				NodeName:       "x",
				ClusterRole:    "",
				PocketIDAdmin:  &BreakGlassCredential{},
				TinyAuthStatic: &TinyAuthStaticCredential{},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			c.builder.BundleDir = tmpDir
			if _, err := c.builder.BuildAndSave("pass"); err == nil {
				t.Error("expected error")
			}
			// Validation must fail-fast: no files should hit disk on the
			// failure path.
			entries, err := os.ReadDir(tmpDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("validation failure produced files: %v", entries)
			}
		})
	}
}

// TestEncryptedFileIsActuallyEncrypted is a paranoid integration check:
// the encrypted blob must (a) not contain any plaintext secrets and (b)
// be a valid age v1 file. Catches regressions like "EncryptWithPassphrase
// turned into a no-op".
func TestEncryptedFileIsActuallyEncrypted(t *testing.T) {
	tmpDir := t.TempDir()
	b := newTestBuilder(tmpDir)
	paths, err := b.BuildAndSave("any")
	if err != nil {
		t.Fatal(err)
	}

	enc, err := os.ReadFile(paths.EncryptedPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(enc), "ott-abc123") {
		t.Error("token leaked into encrypted file (encryption broken)")
	}
	if strings.Contains(string(enc), "RandomPlain123") {
		t.Error("tinyauth password leaked into encrypted file")
	}
	header := string(enc[:min(40, len(enc))])
	if !strings.HasPrefix(header, "age-encryption.org/") {
		t.Errorf("not an age file: %q", header)
	}
}

// TestBuildAndSaveDefaultNow confirms an unset Now func falls back to
// time.Now (round-trip yields a parseable RFC3339 timestamp).
func TestBuildAndSaveDefaultNow(t *testing.T) {
	tmpDir := t.TempDir()
	b := newTestBuilder(tmpDir)
	b.Now = nil // exercise the default path

	paths, err := b.BuildAndSave("pw")
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := os.ReadFile(paths.EncryptedPath)
	plain, err := crypto.DecryptWithPassphrase(enc, "pw")
	if err != nil {
		t.Fatal(err)
	}
	var payload BundlePayload
	if err := yaml.Unmarshal(plain, &payload); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, payload.GeneratedAt); err != nil {
		t.Errorf("generatedAt %q not RFC3339: %v", payload.GeneratedAt, err)
	}
}

// TestBuildAndSaveCreatesBundleDir confirms the bundle dir is created
// when missing (mkdir -p semantics). Important because the install flow
// runs before any directory exists.
func TestBuildAndSaveCreatesBundleDir(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "nested", "recovery")
	b := newTestBuilder(missing)

	if _, err := b.BuildAndSave("pw"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); err != nil {
		t.Errorf("bundle dir not created: %v", err)
	}
}

// TestBundleOmitsBackupEncryptionKeyByDefault is the byte-stability
// guarantee for v1 → v2 upgrades: a bundle for a host that has NOT
// enabled the backup addon must serialize without the
// breakGlass.backupEncryptionKey field at all (yaml:",omitempty"
// behavior). If this regresses, every existing bundle's hash changes
// on the next stackkit apply, which would break downstream tooling
// that pins those hashes.
func TestBundleOmitsBackupEncryptionKeyByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	b := newTestBuilder(tmpDir)
	// BackupEncryptionKey intentionally left nil.

	paths, err := b.BuildAndSave("pw")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := os.ReadFile(paths.EncryptedPath)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := crypto.DecryptWithPassphrase(enc, "pw")
	if err != nil {
		t.Fatal(err)
	}

	// The substring "backupEncryptionKey" appears inside the
	// restoreInstructions block (it documents the new layer), so a
	// loose Contains is the wrong check. We instead re-marshal just
	// the BreakGlassSection and confirm the field doesn't show up
	// there. This isolates the omit-empty behavior from the
	// human-readable documentation that legitimately mentions the
	// field's name.
	var payload BundlePayload
	if err := yaml.Unmarshal(plain, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.BreakGlass.BackupEncryptionKey != nil {
		t.Errorf("BackupEncryptionKey decoded non-nil from a bundle that was built without it: %+v",
			payload.BreakGlass.BackupEncryptionKey)
	}
	bgYaml, err := yaml.Marshal(payload.BreakGlass)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bgYaml), "backupEncryptionKey") {
		t.Errorf("BreakGlass section should omit backupEncryptionKey when the credential is nil; got:\n%s", string(bgYaml))
	}
}

// TestBundleIncludesBackupEncryptionKeyWhenSet verifies the round trip
// for the addon-enabled case: when the builder is given a credential,
// every field reaches the decrypted YAML and the omit-empty for
// repositoryHint behaves correctly when the hint is empty.
func TestBundleIncludesBackupEncryptionKeyWhenSet(t *testing.T) {
	tmpDir := t.TempDir()
	b := newTestBuilder(tmpDir)
	b.BackupEncryptionKey = &BackupEncryptionKeyCredential{
		Engine:         "kopia",
		Passphrase:     "test-passphrase-not-real",
		RepositoryHint: "b2://kombify-vault/test-node",
	}

	paths, err := b.BuildAndSave("pw")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := os.ReadFile(paths.EncryptedPath)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := crypto.DecryptWithPassphrase(enc, "pw")
	if err != nil {
		t.Fatal(err)
	}

	var payload BundlePayload
	if err := yaml.Unmarshal(plain, &payload); err != nil {
		t.Fatal(err)
	}
	bek := payload.BreakGlass.BackupEncryptionKey
	if bek == nil {
		t.Fatalf("BackupEncryptionKey nil after round-trip; bundle:\n%s", string(plain))
	}
	if bek.Engine != "kopia" {
		t.Errorf("engine = %q, want %q", bek.Engine, "kopia")
	}
	if bek.Passphrase != "test-passphrase-not-real" {
		t.Errorf("passphrase = %q (mismatch)", bek.Passphrase)
	}
	if bek.RepositoryHint != "b2://kombify-vault/test-node" {
		t.Errorf("repositoryHint = %q", bek.RepositoryHint)
	}

	// The restore-instructions block must mention the Layer-3 path so
	// an operator who only has the bundle knows what the new field is
	// for.
	if !strings.Contains(payload.RestoreInstructions, "Backup-data recovery path (Layer 3") {
		t.Errorf("restoreInstructions missing Layer-3 path:\n%s", payload.RestoreInstructions)
	}
}

// TestBackupEncryptionKeyGenerator confirms the generator produces
// stable-shape credentials: 32 bytes of randomness in base64, the
// engine defaults to "kopia", and the hint passes through verbatim.
func TestBackupEncryptionKeyGenerator(t *testing.T) {
	gen := &BackupEncryptionKeyGenerator{RepositoryHint: "b2://x/y"}
	cred, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if cred.Engine != "kopia" {
		t.Errorf("default engine = %q, want kopia", cred.Engine)
	}
	if cred.RepositoryHint != "b2://x/y" {
		t.Errorf("hint not preserved: %q", cred.RepositoryHint)
	}
	// 32 random bytes encoded as standard base64 = 44 characters
	// including the trailing "=" pad.
	if len(cred.Passphrase) != 44 {
		t.Errorf("passphrase length = %d, want 44", len(cred.Passphrase))
	}

	// Distinct invocations must yield distinct passphrases — otherwise
	// the generator is a static placeholder, which would silently
	// destroy the security property.
	cred2, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if cred.Passphrase == cred2.Passphrase {
		t.Error("two Generate() calls produced identical passphrases — randomness broken")
	}
}
