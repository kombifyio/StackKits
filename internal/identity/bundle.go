package identity

// Recovery-bundle builder.
//
// The bundle aggregates the two break-glass layers into a single YAML
// artifact: PocketID-admin (Layer 1, passkey-only via setup-token) and
// TinyAuth-static (Layer 2, username + bcrypt + plaintext for the case
// where PocketID itself is also dead). It is encrypted with age using a
// scrypt passphrase the owner chose during setup, and written to disk in
// two flavors: the encrypted .age (to back up out-of-band) and a
// root-only .txt convenience copy (so a logged-in operator on the node
// can read it without redecrypting).
//
// YAML field names mirror base/break-glass.cue #BundlePayload exactly so
// the encrypted artifact can be diffed against the CUE schema during
// review.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kombifyio/stackkits/internal/crypto"
	"gopkg.in/yaml.v3"
)

// BundlePayload is the structure that is serialized to YAML and then
// encrypted into the .age file. Field names match base/break-glass.cue
// #BundlePayload (camelCase).
type BundlePayload struct {
	Version             int               `yaml:"version"`
	GeneratedAt         string            `yaml:"generatedAt"`
	Node                BundleNode        `yaml:"node"`
	BreakGlass          BreakGlassSection `yaml:"breakGlass"`
	RestoreInstructions string            `yaml:"restoreInstructions"`
}

// BundleNode identifies the homelab node this bundle belongs to. Including
// hostname and cluster role makes the bundle self-describing during a
// recovery — the operator can match the artifact to physical hardware
// without consulting external state.
type BundleNode struct {
	Name        string `yaml:"name"`
	Hostname    string `yaml:"hostname"`
	ClusterRole string `yaml:"clusterRole"` // "main"|"worker"|"storage"
	PocketIDURL string `yaml:"pocketidUrl"`
}

// BreakGlassSection holds the recovery layers.
//
// PocketIDAdmin and TinyAuthStatic are mandatory — every node has them.
// BackupEncryptionKey is optional: it is only populated when the
// addons/backup add-on is enabled and the operator has chosen to escrow
// the Kopia encryption passphrase here. Without that escrow, "lost host"
// equals "lost backups", which defeats the addon's purpose.
type BreakGlassSection struct {
	PocketIDAdmin       PocketIDAdminPayload        `yaml:"pocketidAdmin"`
	TinyAuthStatic      TinyAuthStaticPayload       `yaml:"tinyauthStatic"`
	BackupEncryptionKey *BackupEncryptionKeyPayload `yaml:"backupEncryptionKey,omitempty"`
}

// BackupEncryptionKeyPayload escrows the Kopia repository passphrase that
// addons/backup uses to encrypt every snapshot. It is the only path back
// to the data if the host disk is lost: an operator decrypts the bundle
// and re-attaches the offsite repo with this passphrase.
//
// The plaintext passphrase lives only inside the encrypted .age bundle.
// Mode 0600 on the .txt convenience copy plus the mandatory recovery
// passphrase on the .age file are the same protections used for the
// other layers.
type BackupEncryptionKeyPayload struct {
	// Engine is the addon engine that owns the passphrase. "kopia" today;
	// the field is here so a future engine swap (extremely unlikely given
	// ADR-0016) does not require a bundle-format break.
	Engine string `yaml:"engine"`

	// Passphrase is the cleartext value the operator (or stackkit apply)
	// generated. Treat as secret. Same handling rules as the TinyAuth
	// plaintext password on the same bundle.
	Passphrase string `yaml:"passphrase"`

	// RepositoryHint is a human-readable pointer to where the data lives
	// (e.g. "b2://kombify-vault/host-a"). Optional. The recovery
	// operator typically already knows this from out-of-band sources;
	// the hint is here so a recovery scenario starting from "I only have
	// the bundle" is still tractable.
	RepositoryHint string `yaml:"repositoryHint,omitempty"`
}

// PocketIDAdminPayload is the Layer-1 recovery credential. The setup token
// + URL is the v2 passkey-only equivalent of the classical sealed
// password: redeeming it lets the recoverer enroll a WebAuthn credential
// and become a fully-privileged PocketID admin.
type PocketIDAdminPayload struct {
	Username   string `yaml:"username"`
	SetupToken string `yaml:"setupToken"`
	SetupURL   string `yaml:"setupUrl"`
	Group      string `yaml:"group"`
	UserID     string `yaml:"userId"`
}

// TinyAuthStaticPayload is the Layer-2 recovery credential. PasswordPlain
// is here so the operator has something to type even if PocketID itself
// is corrupt; PasswordBcrypt mirrors what was rendered into the TinyAuth
// container env so the bundle is a complete record of state.
type TinyAuthStaticPayload struct {
	Username       string `yaml:"username"`
	PasswordPlain  string `yaml:"passwordPlain"`
	PasswordBcrypt string `yaml:"passwordBcrypt"`
}

// BundlePaths returns the on-disk locations of the written bundle files.
type BundlePaths struct {
	// EncryptedPath is the .age file. This is the disaster-recovery
	// artifact that must be backed up out-of-band; it is safe to copy
	// to cloud storage, USB sticks, paper QR codes, etc.
	EncryptedPath string

	// PlaintextPath is the .txt file (mode 0600). Convenience-only — a
	// root-on-the-node operator can read it without redecrypting. Lives
	// next to the encrypted file in the bundle directory.
	PlaintextPath string
}

// BundleBuilder aggregates the inputs needed to build a recovery bundle.
// Construct one, populate the credentials produced by the Task-7 and
// Task-8 generators, and call BuildAndSave.
type BundleBuilder struct {
	// NodeName identifies the homelab node and drives the bundle's
	// filename ("break-glass-<nodename>.age"). Required.
	NodeName string

	// Hostname is the node's network hostname (FQDN or short name); a
	// purely-documentation field but useful during recovery for matching
	// the bundle to physical hardware.
	Hostname string

	// ClusterRole is one of "main", "worker", or "storage". Required.
	ClusterRole string

	// PocketIDURL is the public origin of the PocketID instance, mirrored
	// into the bundle so the recovery operator knows which install this
	// belongs to.
	PocketIDURL string

	// PocketIDAdmin is the Layer-1 break-glass credential from Task 7.
	// Required.
	PocketIDAdmin *BreakGlassCredential

	// TinyAuthStatic is the Layer-2 break-glass credential from Task 8.
	// Required.
	TinyAuthStatic *TinyAuthStaticCredential

	// BackupEncryptionKey escrows the Kopia repository passphrase used by
	// the addons/backup add-on. Optional — only populated when the addon
	// is enabled. nil leaves the BackupEncryptionKey field out of the
	// emitted YAML entirely (yaml:"...,omitempty").
	BackupEncryptionKey *BackupEncryptionKeyCredential

	// BundleDir is the directory the .age and .txt files are written to.
	// Defaults to "/var/lib/stackkit/recovery" when empty.
	BundleDir string

	// Now is injectable for deterministic tests. Defaults to time.Now.
	Now func() time.Time
}

// defaultBundleDir is the on-host location for recovery bundles when no
// explicit BundleDir is configured. The directory is created with mode
// 0700 so only root (and the stackkit user) can list it.
const defaultBundleDir = "/var/lib/stackkit/recovery"

// BuildAndSave validates the builder, marshals the bundle YAML, encrypts
// it with the supplied passphrase, and writes both the encrypted (.age,
// 0644) and plaintext (.txt, 0600) files. It returns the on-disk paths.
//
// The plaintext file is convenience-only — the encrypted bundle is the
// disaster-recovery artifact that must be backed up out-of-band.
func (b *BundleBuilder) BuildAndSave(passphrase string) (*BundlePaths, error) {
	if err := b.validate(); err != nil {
		return nil, err
	}

	payload := b.buildPayload()
	plain, err := yaml.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("yaml marshal: %w", err)
	}

	bundleDir := b.BundleDir
	if bundleDir == "" {
		bundleDir = defaultBundleDir
	}
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir bundle dir: %w", err)
	}

	encrypted, err := crypto.EncryptWithPassphrase(plain, passphrase)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}

	encPath := filepath.Join(bundleDir, fmt.Sprintf("break-glass-%s.age", b.NodeName))
	plainPath := filepath.Join(bundleDir, fmt.Sprintf("break-glass-%s.txt", b.NodeName))

	if err := os.WriteFile(encPath, encrypted, 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", encPath, err)
	}
	if err := os.WriteFile(plainPath, plain, 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", plainPath, err)
	}

	return &BundlePaths{EncryptedPath: encPath, PlaintextPath: plainPath}, nil
}

func (b *BundleBuilder) validate() error {
	if b.NodeName == "" {
		return fmt.Errorf("bundle: NodeName required")
	}
	if b.PocketIDAdmin == nil {
		return fmt.Errorf("bundle: PocketIDAdmin credential required")
	}
	if b.TinyAuthStatic == nil {
		return fmt.Errorf("bundle: TinyAuthStatic credential required")
	}
	switch b.ClusterRole {
	case "main", "worker", "storage":
		// ok
	default:
		return fmt.Errorf("bundle: invalid ClusterRole %q (want main|worker|storage)", b.ClusterRole)
	}
	return nil
}

func (b *BundleBuilder) buildPayload() *BundlePayload {
	nowFn := b.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	return &BundlePayload{
		Version:     1,
		GeneratedAt: nowFn().UTC().Format(time.RFC3339),
		Node: BundleNode{
			Name:        b.NodeName,
			Hostname:    b.Hostname,
			ClusterRole: b.ClusterRole,
			PocketIDURL: b.PocketIDURL,
		},
		BreakGlass: BreakGlassSection{
			PocketIDAdmin: PocketIDAdminPayload{
				Username:   b.PocketIDAdmin.Username,
				SetupToken: b.PocketIDAdmin.SetupToken,
				SetupURL:   b.PocketIDAdmin.SetupURL,
				Group:      b.PocketIDAdmin.Group,
				UserID:     b.PocketIDAdmin.UserID,
			},
			TinyAuthStatic: TinyAuthStaticPayload{
				Username:       b.TinyAuthStatic.Username,
				PasswordPlain:  b.TinyAuthStatic.PasswordPlain,
				PasswordBcrypt: b.TinyAuthStatic.PasswordBcrypt,
			},
			BackupEncryptionKey: backupKeyPayload(b.BackupEncryptionKey),
		},
		RestoreInstructions: restoreInstructions(),
	}
}

// backupKeyPayload converts a BackupEncryptionKeyCredential into the
// YAML-emitted payload form. Returns nil when the credential is absent
// so the field is omitted from the bundle entirely (omitempty), keeping
// existing bundles byte-identical when the addon is not in use.
func backupKeyPayload(c *BackupEncryptionKeyCredential) *BackupEncryptionKeyPayload {
	if c == nil {
		return nil
	}
	return &BackupEncryptionKeyPayload{
		Engine:         c.Engine,
		Passphrase:     c.Passphrase,
		RepositoryHint: c.RepositoryHint,
	}
}

// restoreInstructions is the human-readable runbook embedded in every
// bundle. It documents both recovery paths and sets the expectation that
// each break-glass use triggers an investigation and rotation.
func restoreInstructions() string {
	return `RESTORE INSTRUCTIONS

This is the break-glass recovery bundle for your homelab node. It was
encrypted with the recovery passphrase you set during setup.

To recover:
  1. Decrypt:  age -d -o break-glass.txt break-glass-<node>.age
               (you will be prompted for the passphrase)
  2. Read the decrypted YAML.
  3. Use one of two paths:

     PocketID-admin path (Layer 1):
       Open the SetupURL in a browser, enroll a WebAuthn credential,
       you are now the PocketID admin for this node.

     TinyAuth-static-cred path (Layer 2, used when PocketID is also down):
       Log in to TinyAuth directly with the username/password shown.
       This bypasses PocketID and brings you to the node's recovery UIs.

     Backup-data recovery path (Layer 3, only present if the backup addon
     was enabled — see the breakGlass.backupEncryptionKey block):
       Use the passphrase to attach the offsite Kopia repository
       described by repositoryHint, then restore. The host can be lost
       entirely; this layer is what brings the data back.
         kopia repository connect <repositoryHint>
         (enter the passphrase when prompted)
         kopia snapshot list
         kopia snapshot restore <id> <target>

This bundle, your recovery passphrase, and physical access to the node
are three separate factors. Keep them separate. The bundle alone is
worthless without the passphrase. The passphrase alone is worthless
without the bundle. Both stolen but no node access still requires
network reachability to the node.

Each break-glass use should trigger an investigation and a rotation:
  stackkit break-glass rotate
`
}
