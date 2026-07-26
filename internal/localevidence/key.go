package localevidence

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/applyevidencev2"
)

// ownerKeyRelPath is the workspace-relative private custody location.
const ownerKeyRelPath = ".stackkit/custody/owner-key.json"
const legacyOwnerKeyRelPath = ".stackkit/apply-evidence/owner-key.json"

// ownerKeyFileMode keeps the private key readable only by its owner. Windows
// ignores the bits, so callers must not treat the mode as the only control.
const ownerKeyFileMode os.FileMode = 0o600

// ErrOwnerKeyMissing reports that no local evidence identity has been
// established yet for this workspace.
var ErrOwnerKeyMissing = errors.New("localevidence: no local owner evidence key")

// ownerKeyFile is the on-disk custody record. Only the seed is persisted; the
// public half and key id are derived on load so a truncated or edited file
// cannot silently rebind the identity to a different key.
type ownerKeyFile struct {
	APIVersion string `json:"apiVersion"`
	OwnerRef   string `json:"ownerRef"`
	Seed       string `json:"seed"`
}

const ownerKeyAPIVersion = "stackkit.local-apply-evidence-key/v1"

// OwnerKey is the local signing identity for one workspace.
type OwnerKey struct {
	OwnerRef string
	KeyID    string
	private  ed25519.PrivateKey
}

// Public returns the public half, which is the trust anchor a verifier pins.
func (k OwnerKey) Public() ed25519.PublicKey {
	if len(k.private) != ed25519.PrivateKeySize {
		return nil
	}
	return k.private.Public().(ed25519.PublicKey)
}

// LoadOwnerKey reads the established local evidence identity for a workspace.
// It never creates one: establishing custody is an explicit owner action.
func LoadOwnerKey(workspaceRoot string) (OwnerKey, error) {
	path, err := ownerKeyPath(workspaceRoot)
	if err != nil {
		return OwnerKey{}, err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // path is workspace-derived and fixed
	if errors.Is(err, os.ErrNotExist) {
		raw, err = migrateLegacyOwnerKey(workspaceRoot, path)
		if errors.Is(err, os.ErrNotExist) {
			return OwnerKey{}, ErrOwnerKeyMissing
		}
	}
	if err != nil {
		return OwnerKey{}, fmt.Errorf("localevidence: read owner evidence key: %w", err)
	}
	return decodeOwnerKey(raw)
}

func decodeOwnerKey(raw []byte) (OwnerKey, error) {
	var file ownerKeyFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return OwnerKey{}, fmt.Errorf("localevidence: decode owner evidence key: %w", err)
	}
	if file.APIVersion != ownerKeyAPIVersion || file.OwnerRef == "" {
		return OwnerKey{}, errors.New("localevidence: owner evidence key is not a recognised custody record")
	}
	seed, err := base64.RawStdEncoding.DecodeString(file.Seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return OwnerKey{}, errors.New("localevidence: owner evidence key seed is malformed")
	}
	private := ed25519.NewKeyFromSeed(seed)
	return OwnerKey{
		OwnerRef: file.OwnerRef,
		KeyID:    applyevidence.ProducerKeyID(private.Public().(ed25519.PublicKey)),
		private:  private,
	}, nil
}

func migrateLegacyOwnerKey(workspaceRoot, newPath string) ([]byte, error) {
	legacyPath := filepath.Join(workspaceRoot, filepath.FromSlash(legacyOwnerKeyRelPath))
	raw, err := os.ReadFile(legacyPath) //nolint:gosec // fixed path below the explicit workspace
	if err != nil {
		return nil, err
	}
	key, err := decodeOwnerKey(raw)
	if err != nil {
		return nil, fmt.Errorf("localevidence: reject legacy owner evidence key: %w", err)
	}
	record := ownerKeyFile{
		APIVersion: ownerKeyAPIVersion, OwnerRef: key.OwnerRef,
		Seed: base64.RawStdEncoding.EncodeToString(key.private.Seed()),
	}
	if err := writePrivateJSON(newPath, record); err != nil {
		return nil, fmt.Errorf("localevidence: migrate owner evidence key into custody: %w", err)
	}
	if err := os.Remove(legacyPath); err != nil {
		return nil, fmt.Errorf("localevidence: remove migrated legacy owner evidence key: %w", err)
	}
	return os.ReadFile(newPath) //nolint:gosec // fixed path below the explicit workspace
}

// EstablishOwnerKey creates the local evidence identity exactly once. An
// existing record is returned unchanged: silently rotating custody would
// invalidate every receipt already anchored to the previous key.
func EstablishOwnerKey(workspaceRoot, ownerRef string) (OwnerKey, error) {
	if ownerRef == "" {
		return OwnerKey{}, errors.New("localevidence: owner reference is required to establish evidence custody")
	}
	existing, err := LoadOwnerKey(workspaceRoot)
	switch {
	case err == nil:
		if existing.OwnerRef != ownerRef {
			return OwnerKey{}, fmt.Errorf(
				"localevidence: workspace evidence custody belongs to owner %q, not %q",
				existing.OwnerRef, ownerRef,
			)
		}
		return existing, nil
	case !errors.Is(err, ErrOwnerKeyMissing):
		return OwnerKey{}, err
	}

	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return OwnerKey{}, fmt.Errorf("localevidence: generate owner evidence key: %w", err)
	}
	path, err := ownerKeyPath(workspaceRoot)
	if err != nil {
		return OwnerKey{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return OwnerKey{}, fmt.Errorf("localevidence: create evidence custody directory: %w", err)
	}
	record := ownerKeyFile{
		APIVersion: ownerKeyAPIVersion,
		OwnerRef:   ownerRef,
		Seed:       base64.RawStdEncoding.EncodeToString(seed),
	}
	if err := writePrivateJSON(path, record); err != nil {
		return OwnerKey{}, fmt.Errorf("localevidence: persist owner evidence key: %w", err)
	}
	private := ed25519.NewKeyFromSeed(seed)
	return OwnerKey{
		OwnerRef: ownerRef,
		KeyID:    applyevidence.ProducerKeyID(private.Public().(ed25519.PublicKey)),
		private:  private,
	}, nil
}

func ownerKeyPath(workspaceRoot string) (string, error) {
	if workspaceRoot == "" {
		return "", errors.New("localevidence: workspace root is required")
	}
	return filepath.Join(workspaceRoot, filepath.FromSlash(ownerKeyRelPath)), nil
}
