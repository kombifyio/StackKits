package identity

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/crypto"
)

// StaticAPIKeyFilename is the basename of the on-disk file that holds the
// PocketID STATIC_API_KEY for a homelab. It lives under <homelab>/.stackkit/.
const StaticAPIKeyFilename = "pocketid-static-api-key"

// EncryptionKeyFilename is the basename of the on-disk file that holds the
// PocketID ENCRYPTION_KEY for a homelab. It lives under <homelab>/.stackkit/.
//
// PocketID v2 refuses to start without ENCRYPTION_KEY (>=16 bytes raw) — the
// container exits immediately with "config error: ENCRYPTION_KEY must be at
// least 16 bytes long" — so this is provisioned alongside StaticAPIKeyFilename
// and rendered into the container env block.
const EncryptionKeyFilename = "pocketid-encryption-key"

// secretDirMode is the mode applied to <homelab>/.stackkit/ when it has to be
// created. Restrictive enough to keep secrets away from other local users on
// a multi-tenant box.
const secretDirMode = 0o700

// secretFileMode is the mode applied to secret files. PocketID v2's
// STATIC_API_KEY is bootstrap-time admin access — anyone holding it can mint
// owner records — so we treat it like an SSH key. Same logic for the
// ENCRYPTION_KEY: leaking it lets an attacker decrypt data at rest.
const secretFileMode = 0o600

// minStaticAPIKeyLen is the smallest length we accept on read for the static
// API key. RandomPassword emits ~43 chars for a 32-byte input, so anything
// below 16 means the file is truncated, manually edited, or otherwise corrupt
// and we should refuse to use it rather than send a bad token to PocketID and
// chase a 401.
const minStaticAPIKeyLen = 16

// minEncryptionKeyLen is the smallest length we accept on read for the
// encryption key. PocketID enforces >=16 bytes raw at startup; base64-encoding
// 16 raw bytes produces 22+ chars (24 with padding). Anything shorter is a
// truncated/edited file and the container would refuse to start anyway.
const minEncryptionKeyLen = 22

// staticAPIKeyByteLen is the entropy budget for newly-generated STATIC_API_KEY
// values: 32 raw bytes -> ~43 chars base64. Plenty for a bearer token.
const staticAPIKeyByteLen = 32

// encryptionKeyByteLen is the entropy budget for newly-generated
// ENCRYPTION_KEY values: 24 raw bytes -> ~32 chars base64. Comfortably above
// PocketID's 16-byte minimum.
const encryptionKeyByteLen = 24

// EnsureStaticAPIKey returns the PocketID STATIC_API_KEY persisted under
// <baseDir>/.stackkit/pocketid-static-api-key.
//
// Behavior:
//   - If the file already exists, its content is returned verbatim. The call
//     is idempotent across `stackkit generate` / `stackkit apply` runs so a
//     destroy → re-apply round-trip with the same identity reuses the same
//     key (which is what we want — PocketID's bootstrap-time STATIC_API_KEY
//     can't change mid-life without orphaning the existing admin records).
//   - Otherwise a fresh 32-byte random key is generated, written with mode
//     0600 to a 0700 directory, and returned.
//
// The chosen persistence location is documented in the Phase 1 plan
// (Task 12): a homelab-local file rather than a tfvars entry, because the
// key is needed by the CLI before terraform-apply runs (the CLI needs to
// pre-render the value into terraform.tfvars.json that terraform then
// passes to the container) and after terraform-apply runs (the
// owner-bootstrap step calls the PocketID admin API with it). Keeping it
// in a single dotfile keeps both call sites honest.
//
// Call site: cmd/stackkit/commands/generate.go gates this on PocketID
// being enabled in the composition; kits without PocketID never call this.
func EnsureStaticAPIKey(baseDir string) (string, error) {
	return ensureSecretFile(baseDir, StaticAPIKeyFilename, staticAPIKeyByteLen, minStaticAPIKeyLen)
}

// ReadStaticAPIKey returns the existing STATIC_API_KEY for a homelab without
// generating one when the file is missing. Use this from `apply` (post-generate)
// so a missing file produces a clear "run stackkit generate first" error
// rather than silently regenerating a key that doesn't match the one already
// baked into the running PocketID container.
func ReadStaticAPIKey(baseDir string) (string, error) {
	return readSecretFile(baseDir, StaticAPIKeyFilename, minStaticAPIKeyLen, "static API key")
}

// EnsureEncryptionKey returns the PocketID ENCRYPTION_KEY persisted under
// <baseDir>/.stackkit/pocketid-encryption-key. Symmetric to EnsureStaticAPIKey:
// idempotent across runs and only regenerated when the file is missing.
//
// PocketID v2 refuses to start without this env var (it must be at least 16
// raw bytes). We provision 24 raw bytes (~32 base64 chars) for a comfortable
// margin, persist it locally so destroy → re-apply round-trips reuse the same
// key (otherwise existing data in the PocketID volume becomes undecryptable),
// and render it into the container env via terraform.tfvars.json.
func EnsureEncryptionKey(baseDir string) (string, error) {
	return ensureSecretFile(baseDir, EncryptionKeyFilename, encryptionKeyByteLen, minEncryptionKeyLen)
}

// ReadEncryptionKey returns the existing ENCRYPTION_KEY for a homelab without
// generating one when the file is missing. Mirrors ReadStaticAPIKey so a
// missing file produces a "run stackkit generate first" error instead of
// silently regenerating a key that doesn't match the encrypted data on the
// volume.
func ReadEncryptionKey(baseDir string) (string, error) {
	return readSecretFile(baseDir, EncryptionKeyFilename, minEncryptionKeyLen, "encryption key")
}

// ensureSecretFile is the shared implementation for EnsureStaticAPIKey and
// EnsureEncryptionKey. It persists a base64-encoded random secret of byteLen
// raw bytes at <baseDir>/.stackkit/<filename> with mode 0600 and returns the
// existing value when the file is already present. minLen is enforced when
// reading back an existing file so a truncated/edited file surfaces as an
// error rather than producing a silent runtime failure downstream.
func ensureSecretFile(baseDir, filename string, byteLen, minLen int) (string, error) {
	dir := filepath.Join(baseDir, ".stackkit")
	path := filepath.Join(dir, filename)

	if data, err := os.ReadFile(path); err == nil {
		key := string(data)
		if len(key) < minLen {
			return "", fmt.Errorf("existing key file %s is too short (%d bytes); delete it to regenerate", path, len(key))
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	if err := os.MkdirAll(dir, secretDirMode); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	key, err := crypto.RandomPassword(byteLen)
	if err != nil {
		return "", fmt.Errorf("generate %s: %w", filename, err)
	}
	if err := os.WriteFile(path, []byte(key), secretFileMode); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return key, nil
}

// readSecretFile is the shared implementation for ReadStaticAPIKey and
// ReadEncryptionKey. label is the human-readable kind of secret used in error
// messages so operators get a clear hint about which file is missing or
// corrupt without having to grep the source.
func readSecretFile(baseDir, filename string, minLen int, label string) (string, error) {
	path := filepath.Join(baseDir, ".stackkit", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("PocketID %s not found at %s — run 'stackkit generate' first", label, path)
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	key := string(data)
	if len(key) < minLen {
		return "", fmt.Errorf("%s file %s is too short (%d bytes); delete it and re-run 'stackkit generate'", label, path, len(key))
	}
	return key, nil
}
