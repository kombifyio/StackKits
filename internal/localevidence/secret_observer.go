package localevidence

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/applyevidencev2"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const (
	localSecretCustodyAPIVersion = "stackkit.local-secret-custody/v1"
	localSecretCustodyKind       = "LocalSecretCustody"
	localSecretCustodyDirectory  = ".stackkit/custody/secrets"
)

type localSecretCustody struct {
	APIVersion     string                    `json:"apiVersion"`
	Kind           string                    `json:"kind"`
	OwnerRef       string                    `json:"ownerRef"`
	KeyID          string                    `json:"keyId"`
	RefDigest      string                    `json:"refDigest"`
	Material       string                    `json:"material"`
	MaterialDigest string                    `json:"materialDigest"`
	Signature      OwnerPolicyStateSignature `json:"signature"`
}

// MaterializeLocalSecret creates or reuses one workspace-local, owner-signed,
// owner-only secret. Neither evidence nor diagnostics contain its value or ref.
func MaterializeLocalSecret(workspaceRoot, secretRef string) error {
	refDigest, err := localSecretRefDigest(secretRef)
	if err != nil {
		return err
	}
	if _, err := loadLocalSecretCustody(workspaceRoot, refDigest); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return fmt.Errorf("localevidence: load owner for local secret custody: %w", err)
	}
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		return fmt.Errorf("localevidence: generate local secret material: %w", err)
	}
	materialDigest := sha256.Sum256(material)
	record := localSecretCustody{
		APIVersion: localSecretCustodyAPIVersion, Kind: localSecretCustodyKind,
		OwnerRef: owner.OwnerRef, KeyID: owner.KeyID, RefDigest: refDigest,
		Material:       base64.RawStdEncoding.EncodeToString(material),
		MaterialDigest: hex.EncodeToString(materialDigest[:]),
	}
	record.Signature, err = SignOwnerPolicyState(workspaceRoot, localSecretSigningBytes(record))
	if err != nil {
		return fmt.Errorf("localevidence: sign local secret custody: %w", err)
	}
	if err := writeLocalSecretCustody(workspaceRoot, refDigest, record); err != nil {
		return fmt.Errorf("localevidence: persist local secret custody: %w", err)
	}
	return nil
}

// ResolveLocalSecretMaterial returns a defensive copy of the text-safe secret
// material after verifying the exact owner-signed local custody record. It is
// intended only for construction-owned local runtime adapters; callers must
// not persist it outside an owner-only runtime file or include it in evidence.
func ResolveLocalSecretMaterial(workspaceRoot, secretRef string) ([]byte, error) {
	refDigest, err := localSecretRefDigest(secretRef)
	if err != nil {
		return nil, err
	}
	record, err := loadLocalSecretCustody(workspaceRoot, refDigest)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), record.Material...), nil
}

// SecretObserver proves that the exact opaque secret locator in an Apply
// expectation resolves to valid owner-only local custody.
type SecretObserver struct{ workspaceRoot string }

func NewSecretObserver(workspaceRoot string) (*SecretObserver, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return nil, errors.New("localevidence: secret observer requires a workspace root")
	}
	return &SecretObserver{workspaceRoot: workspaceRoot}, nil
}

func (o *SecretObserver) Observe(_ context.Context, expectation applyevidence.Expectation) (map[string]string, error) {
	if o == nil || strings.TrimSpace(o.workspaceRoot) == "" {
		return nil, errors.New("localevidence: secret observer is not configured")
	}
	refDigest := strings.TrimSpace(expectation.Subject.ContractHash)
	if len(refDigest) != sha256.Size*2 {
		return nil, errors.New("secret expectation has no valid opaque custody locator")
	}
	if _, err := hex.DecodeString(refDigest); err != nil || strings.ToLower(refDigest) != refDigest {
		return nil, errors.New("secret expectation has no valid opaque custody locator")
	}
	record, err := loadLocalSecretCustody(o.workspaceRoot, refDigest)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"custody.refDigest": record.RefDigest, "custody.materialDigest": record.MaterialDigest,
		"custody.ownerRef": record.OwnerRef, "custody.keyId": record.KeyID, "custody.ownerOnly": "true",
	}, nil
}

func localSecretRefDigest(secretRef string) (string, error) {
	secretRef = strings.TrimSpace(secretRef)
	if !strings.HasPrefix(secretRef, "secret://") || len(secretRef) <= len("secret://") {
		return "", errors.New("localevidence: local secret reference must use secret://")
	}
	digest := sha256.Sum256([]byte(secretRef))
	return hex.EncodeToString(digest[:]), nil
}

func localSecretCustodyPath(workspaceRoot, refDigest string) (string, error) {
	return confinedCustodyPath(workspaceRoot, filepath.ToSlash(filepath.Join(localSecretCustodyDirectory, refDigest+".json")))
}

func writeLocalSecretCustody(workspaceRoot, refDigest string, record localSecretCustody) error {
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	if err := transaction.MkdirAll(localSecretCustodyDirectory, 0o700); err != nil {
		_ = transaction.Close()
		return err
	}
	if err := transaction.Close(); err != nil {
		return err
	}
	view, err := root.View(localSecretCustodyDirectory)
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(refDigest+".json", raw)
	if err != nil || !result.Installed || !result.FileSynced {
		if err == nil {
			err = errors.New("atomic write did not install and sync local secret custody")
		}
		return err
	}
	path, err := localSecretCustodyPath(workspaceRoot, refDigest)
	if err != nil {
		return err
	}
	return restrictFileToCurrentUser(path)
}

func loadLocalSecretCustody(workspaceRoot, refDigest string) (localSecretCustody, error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return localSecretCustody{}, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return localSecretCustody{}, err
	}
	defer func() { _ = transaction.Close() }()
	relative := filepath.ToSlash(filepath.Join(localSecretCustodyDirectory, refDigest+".json"))
	raw, info, err := transaction.ReadStable(relative)
	if err != nil {
		return localSecretCustody{}, fmt.Errorf("localevidence: read local secret custody: %w", err)
	}
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return localSecretCustody{}, errors.New("localevidence: local secret custody is not a regular file")
	}
	path, err := localSecretCustodyPath(workspaceRoot, refDigest)
	if err != nil {
		return localSecretCustody{}, err
	}
	if err := requireFilePrivateToCurrentUser(path); err != nil {
		return localSecretCustody{}, err
	}
	var record localSecretCustody
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return localSecretCustody{}, errors.New("localevidence: local secret custody is malformed")
	}
	material, err := base64.RawStdEncoding.Strict().DecodeString(record.Material)
	if err != nil || len(material) != 32 {
		return localSecretCustody{}, errors.New("localevidence: local secret custody material is malformed")
	}
	digest := sha256.Sum256(material)
	if record.APIVersion != localSecretCustodyAPIVersion || record.Kind != localSecretCustodyKind ||
		record.RefDigest != refDigest || record.MaterialDigest != hex.EncodeToString(digest[:]) {
		return localSecretCustody{}, errors.New("localevidence: local secret custody integrity check failed")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return localSecretCustody{}, err
	}
	if record.OwnerRef != owner.OwnerRef || record.KeyID != owner.KeyID ||
		record.Signature.OwnerRef != owner.OwnerRef || record.Signature.KeyID != owner.KeyID {
		return localSecretCustody{}, errors.New("localevidence: local secret custody is not bound to the current owner")
	}
	if err := VerifyOwnerPolicyState(workspaceRoot, localSecretSigningBytes(record), record.Signature); err != nil {
		return localSecretCustody{}, errors.New("localevidence: local secret custody owner signature does not verify")
	}
	return record, nil
}

func localSecretSigningBytes(record localSecretCustody) []byte {
	value := struct {
		APIVersion, Kind, OwnerRef, KeyID, RefDigest, MaterialDigest string
	}{
		record.APIVersion, record.Kind, record.OwnerRef, record.KeyID, record.RefDigest, record.MaterialDigest,
	}
	raw, _ := json.Marshal(value)
	return raw
}

var _ Observer = (*SecretObserver)(nil)
