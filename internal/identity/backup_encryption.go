package identity

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// BackupEncryptionKeyCredential carries the Kopia repository passphrase
// that the addons/backup add-on uses to encrypt every snapshot. It is
// the third break-glass layer (after PocketID-admin and TinyAuth-static)
// and lives in the recovery bundle so a host loss does not equal a data
// loss.
//
// Unlike the other layers, this credential is OPTIONAL: a node that has
// not enabled the backup add-on does not have one, and the bundle's
// BackupEncryptionKey field is omitted entirely.
type BackupEncryptionKeyCredential struct {
	// Engine is the addon engine name. Always "kopia" today; here so a
	// future format change does not require a bundle break.
	Engine string

	// Passphrase is the cleartext value passed to Kopia at
	// repository-create time. Goes into the recovery bundle; never
	// logged. Same handling discipline as TinyAuthStaticCredential.PasswordPlain.
	Passphrase string

	// RepositoryHint is a human-readable pointer to where the data
	// lives ("b2://kombify-vault/host-a", "sftp://u@host:/repo", …).
	// Optional. Helps a recovery operator who has only the bundle find
	// the offsite repo.
	RepositoryHint string
}

// BackupEncryptionKeyGenerator creates a fresh repository passphrase.
// The output is suitable for direct use as KOPIA_PASSWORD.
type BackupEncryptionKeyGenerator struct {
	// Engine is recorded into the credential. Defaults to "kopia".
	Engine string

	// RepositoryHint is recorded verbatim. Optional.
	RepositoryHint string
}

// Generate produces 32 random bytes encoded as base64 (~43 characters).
// Same shape as TinyAuthStaticGenerator's plaintext: high entropy, safe
// for the YAML bundle, copy-pasteable in a recovery scenario.
func (g *BackupEncryptionKeyGenerator) Generate() (*BackupEncryptionKeyCredential, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("backup-encryption-key: read random: %w", err)
	}
	engine := g.Engine
	if engine == "" {
		engine = "kopia"
	}
	return &BackupEncryptionKeyCredential{
		Engine:         engine,
		Passphrase:     base64.StdEncoding.EncodeToString(buf),
		RepositoryHint: g.RepositoryHint,
	}, nil
}
