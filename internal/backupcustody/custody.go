// Package backupcustody owns the local Kopia repository passphrase without
// adding it to the Basement runtime-custody inventory.
package backupcustody

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"golang.org/x/crypto/blake2b"
)

const (
	// APIVersion identifies the on-disk, owner-bound backup custody contract.
	APIVersion = "stackkit.local-backup-passphrase-custody/v1"

	custodyKind        = "LocalBackupPassphraseCustody"
	custodyRelDir      = ".stackkit/custody/backup"
	custodyParent      = ".stackkit/custody"
	metadataFileName   = "metadata.json"
	passphraseFileName = "kopia-passphrase"
	privateFileMode    = os.FileMode(0o600)
)

// ErrMissing reports that no local backup passphrase custody exists.
var ErrMissing = errors.New("backupcustody: no local backup passphrase custody")

// Custody is the secret-free, owner-signed index for one local Kopia
// passphrase. SecretDigest binds the separate private file without placing the
// high-entropy passphrase in JSON. It is an integrity commitment, not a
// password verifier.
type Custody struct {
	APIVersion    string                                  `json:"apiVersion"`
	Kind          string                                  `json:"kind"`
	OwnerRef      string                                  `json:"ownerRef"`
	KeyID         string                                  `json:"keyId"`
	EstablishedAt time.Time                               `json:"establishedAt"`
	SecretDigest  string                                  `json:"secretDigest"`
	Signature     localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// Establish creates one random local Kopia passphrase exactly once. The
// returned bytes are a short-lived copy owned by the caller; call Clear as
// soon as the Kopia invocation has consumed them.
func Establish(workspaceRoot string) (Custody, []byte, error) {
	owner, err := localevidence.LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return Custody{}, nil, err
	}
	if existing, secret, loadErr := Load(workspaceRoot); loadErr == nil {
		return existing, secret, nil
	} else if !errors.Is(loadErr, ErrMissing) {
		return Custody{}, nil, loadErr
	}

	workspace, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return Custody{}, nil, fmt.Errorf("backupcustody: open workspace: %w", err)
	}
	defer func() { _ = workspace.Close() }()
	transaction, err := workspace.BeginTransaction()
	if err != nil {
		return Custody{}, nil, fmt.Errorf("backupcustody: begin custody transaction: %w", err)
	}
	if err := transaction.MkdirAll(custodyParent, 0o700); err != nil {
		_ = transaction.Close()
		return Custody{}, nil, fmt.Errorf("backupcustody: create custody parent: %w", err)
	}
	if err := transaction.Close(); err != nil {
		return Custody{}, nil, fmt.Errorf("backupcustody: close custody parent transaction: %w", err)
	}

	custodyRootPath := filepath.Join(workspaceRoot, filepath.FromSlash(custodyParent))
	custodyRoot, err := confinedfs.Open(custodyRootPath)
	if err != nil {
		return Custody{}, nil, fmt.Errorf("backupcustody: open custody parent: %w", err)
	}
	defer func() { _ = custodyRoot.Close() }()
	custodyTransaction, err := custodyRoot.BeginTransaction()
	if err != nil {
		return Custody{}, nil, fmt.Errorf("backupcustody: begin backup custody transaction: %w", err)
	}
	defer func() { _ = custodyTransaction.Close() }()

	stage, err := custodyTransaction.CreatePrivateDirectory(".backup-")
	if err != nil {
		return Custody{}, nil, fmt.Errorf("backupcustody: create private transaction: %w", err)
	}
	removeStage := true
	defer func() {
		if removeStage {
			_ = custodyTransaction.RemoveTree(stage)
		}
	}()
	stagePath := filepath.Join(custodyRootPath, filepath.FromSlash(stage))
	if err := restrictPathToCurrentUser(stagePath, true); err != nil {
		return Custody{}, nil, err
	}

	randomSecret := make([]byte, 32)
	defer Clear(randomSecret)
	if _, err := rand.Read(randomSecret); err != nil {
		return Custody{}, nil, errors.New("backupcustody: generate Kopia passphrase")
	}
	passphrase := []byte(base64.RawURLEncoding.EncodeToString(randomSecret))
	defer Clear(passphrase)

	record := Custody{
		APIVersion:    APIVersion,
		Kind:          custodyKind,
		OwnerRef:      owner.OwnerRef,
		KeyID:         owner.KeyID,
		EstablishedAt: time.Now().UTC().Truncate(time.Second),
		SecretDigest:  secretDigest(passphrase),
	}
	signingBytes, err := signingBytes(record)
	if err != nil {
		return Custody{}, nil, errors.New("backupcustody: encode owner-bound metadata")
	}
	record.Signature, err = localevidence.SignOwnerPolicyState(workspaceRoot, signingBytes)
	if err != nil {
		return Custody{}, nil, fmt.Errorf("backupcustody: sign owner-bound metadata: %w", err)
	}
	metadata, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return Custody{}, nil, errors.New("backupcustody: encode metadata")
	}

	stageSecret := stage + "/" + passphraseFileName
	if err := custodyTransaction.WriteFileExclusive(stageSecret, passphrase, privateFileMode); err != nil {
		return Custody{}, nil, fmt.Errorf("backupcustody: persist passphrase: %w", err)
	}
	if err := restrictPathToCurrentUser(filepath.Join(stagePath, passphraseFileName), false); err != nil {
		return Custody{}, nil, err
	}
	stageMetadata := stage + "/" + metadataFileName
	if err := custodyTransaction.WriteFileExclusive(stageMetadata, metadata, privateFileMode); err != nil {
		return Custody{}, nil, fmt.Errorf("backupcustody: persist metadata: %w", err)
	}
	if err := restrictPathToCurrentUser(filepath.Join(stagePath, metadataFileName), false); err != nil {
		return Custody{}, nil, err
	}
	installed, err := custodyTransaction.Rename(stage, "backup")
	if err != nil {
		if existing, secret, loadErr := Load(workspaceRoot); loadErr == nil {
			return existing, secret, nil
		}
		return Custody{}, nil, fmt.Errorf("backupcustody: install custody: %w", err)
	}
	if !installed {
		return Custody{}, nil, errors.New("backupcustody: custody was not installed")
	}
	removeStage = false
	return Load(workspaceRoot)
}

// Load verifies the current owner binding, owner signature, private-file mode,
// and passphrase digest before returning a fresh caller-owned byte slice.
func Load(workspaceRoot string) (Custody, []byte, error) {
	owner, err := localevidence.LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return Custody{}, nil, err
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return Custody{}, nil, fmt.Errorf("backupcustody: open workspace: %w", err)
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return Custody{}, nil, fmt.Errorf("backupcustody: begin read transaction: %w", err)
	}
	defer func() { _ = transaction.Close() }()

	custodyPath := filepath.Join(workspaceRoot, filepath.FromSlash(custodyRelDir))
	metadataPath := filepath.Join(custodyPath, metadataFileName)
	secretPath := filepath.Join(custodyPath, passphraseFileName)

	_, metadataPathErr := transaction.Lstat(custodyRelDir + "/" + metadataFileName)
	_, secretPathErr := transaction.Lstat(custodyRelDir + "/" + passphraseFileName)
	if errors.Is(metadataPathErr, os.ErrNotExist) && errors.Is(secretPathErr, os.ErrNotExist) {
		return Custody{}, nil, ErrMissing
	}
	if metadataPathErr != nil || secretPathErr != nil {
		return Custody{}, nil, errors.New("backupcustody: local backup custody is incomplete or unreadable")
	}
	if err := requirePrivatePath(custodyPath, true); err != nil {
		return Custody{}, nil, err
	}
	if err := requirePrivatePath(metadataPath, false); err != nil {
		return Custody{}, nil, err
	}
	if err := requirePrivatePath(secretPath, false); err != nil {
		return Custody{}, nil, err
	}

	metadata, metadataInfo, metadataErr := transaction.ReadStable(custodyRelDir + "/" + metadataFileName)
	secret, secretInfo, secretErr := transaction.ReadStable(custodyRelDir + "/" + passphraseFileName)
	if metadataErr != nil || secretErr != nil {
		Clear(secret)
		return Custody{}, nil, errors.New("backupcustody: local backup custody is incomplete or unreadable")
	}
	defer Clear(secret)
	if err := requirePrivateFile(metadataInfo); err != nil {
		return Custody{}, nil, err
	}
	if err := requirePrivateFile(secretInfo); err != nil {
		return Custody{}, nil, err
	}
	// Re-read the path security descriptors after the stable content reads so
	// an ACL change concurrent with validation cannot silently broaden custody.
	if err := requirePrivatePath(custodyPath, true); err != nil {
		return Custody{}, nil, err
	}
	if err := requirePrivatePath(metadataPath, false); err != nil {
		return Custody{}, nil, err
	}
	if err := requirePrivatePath(secretPath, false); err != nil {
		return Custody{}, nil, err
	}

	var record Custody
	decoder := json.NewDecoder(bytes.NewReader(metadata))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Custody{}, nil, errors.New("backupcustody: metadata is malformed")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Custody{}, nil, errors.New("backupcustody: metadata contains trailing content")
	}
	if record.APIVersion != APIVersion || record.Kind != custodyKind ||
		record.OwnerRef == "" || record.KeyID == "" || record.EstablishedAt.IsZero() ||
		!strings.HasPrefix(record.SecretDigest, "blake2b-256:") {
		return Custody{}, nil, errors.New("backupcustody: metadata is not a recognised record")
	}
	if record.OwnerRef != owner.OwnerRef || record.KeyID != owner.KeyID ||
		record.Signature.OwnerRef != owner.OwnerRef || record.Signature.KeyID != owner.KeyID {
		return Custody{}, nil, errors.New("backupcustody: metadata is not bound to the established owner")
	}
	signingBytes, err := signingBytes(record)
	if err != nil {
		return Custody{}, nil, errors.New("backupcustody: encode metadata for verification")
	}
	if err := localevidence.VerifyOwnerPolicyState(workspaceRoot, signingBytes, record.Signature); err != nil {
		return Custody{}, nil, errors.New("backupcustody: owner signature does not verify")
	}
	actualDigest := secretDigest(secret)
	if subtle.ConstantTimeCompare([]byte(actualDigest), []byte(record.SecretDigest)) != 1 {
		return Custody{}, nil, errors.New("backupcustody: passphrase does not match owner-bound metadata")
	}
	decoded := make([]byte, base64.RawURLEncoding.DecodedLen(len(secret)))
	decodedSize, err := base64.RawURLEncoding.Strict().Decode(decoded, secret)
	Clear(decoded)
	if err != nil || decodedSize != 32 {
		return Custody{}, nil, errors.New("backupcustody: passphrase is malformed")
	}
	return record, bytes.Clone(secret), nil
}

// Clear overwrites one caller-owned secret copy.
func Clear(secret []byte) {
	clear(secret)
}

func signingBytes(record Custody) ([]byte, error) {
	record.Signature = localevidence.OwnerPolicyStateSignature{}
	return json.Marshal(record)
}

func secretDigest(secret []byte) string {
	const domain = "stackkit.local-backup-passphrase-custody/v1\x00"
	material := make([]byte, len(domain)+len(secret))
	defer clear(material)
	copy(material, domain)
	copy(material[len(domain):], secret)
	digest := blake2b.Sum256(material)
	return "blake2b-256:" + hex.EncodeToString(digest[:])
}

func requirePrivateFile(info os.FileInfo) error {
	if info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("backupcustody: custody contains a non-regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != privateFileMode {
		return errors.New("backupcustody: custody file permissions are not owner-only")
	}
	return nil
}
