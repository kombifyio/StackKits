package advancedtrust

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	RecordSchemaVersion = "stackkit.local-advanced-trust/v1"
	recordKind          = "LocalAdvancedTrust"
	recordRelativePath  = ".stackkit/advanced/trust/bundle.json"
	trustRelativeDir    = ".stackkit/advanced/trust"
)

// ErrMissing reports that no local Advanced trust bundle has been imported.
var ErrMissing = errors.New("advancedtrust: no local Advanced trust bundle")

// Record is the verified, Owner-bound local trust record. Raw public key
// material is deliberately not exported or JSON-serializable; callers must
// explicitly request a detached verifier bundle.
type Record struct {
	SchemaVersion string    `json:"schemaVersion"`
	Kind          string    `json:"kind"`
	BundleSHA256  string    `json:"bundleSHA256"`
	ImportedAt    time.Time `json:"importedAt"`
	OwnerRef      string    `json:"ownerRef"`
	OwnerKeyID    string    `json:"ownerKeyId"`
	KeyCount      int       `json:"keyCount"`

	bundleFactory func() advancedcapability.TrustBundle
	keyReferences []KeyReference
}

// Inspection is a secret- and key-material-free view suitable for CLI output.
type Inspection struct {
	SchemaVersion string         `json:"schemaVersion"`
	BundleSHA256  string         `json:"bundleSHA256"`
	ImportedAt    time.Time      `json:"importedAt"`
	OwnerRef      string         `json:"ownerRef"`
	OwnerKeyID    string         `json:"ownerKeyId"`
	Keys          []KeyReference `json:"keys"`
}

// KeyReference identifies a trusted public key without returning its bytes.
type KeyReference struct {
	IssuerID string `json:"issuerId"`
	KeyID    string `json:"keyId"`
}

type diskRecord struct {
	SchemaVersion string
	Kind          string
	Bundle        decodedBundle
	BundleSHA256  string
	ImportedAt    string
	OwnerKeyID    string
	OwnerRef      string
	Signature     localevidence.OwnerAdvancedTrustSignature
}

// Import verifies an exact SHA-256 pin before creating any trust-store state,
// binds the canonical bundle to current Owner custody, and atomically installs
// the private local record.
func Import(workspace string, raw []byte, expectedSHA256 string, now time.Time) (Record, error) {
	bundle, err := decodeBundle(raw)
	if err != nil {
		return Record{}, err
	}
	if !sha256Pin.MatchString(expectedSHA256) ||
		subtle.ConstantTimeCompare([]byte(bundle.digest), []byte(expectedSHA256)) != 1 {
		return Record{}, errors.New("advancedtrust: trust bundle SHA-256 pin does not match")
	}
	if now.IsZero() {
		return Record{}, errors.New("advancedtrust: trusted import time is required")
	}
	importedAt := now.UTC().Truncate(time.Second)
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return Record{}, fmt.Errorf("advancedtrust: load local Owner custody: %w", err)
	}
	record := diskRecord{
		SchemaVersion: RecordSchemaVersion,
		Kind:          recordKind,
		Bundle:        bundle,
		BundleSHA256:  bundle.digest,
		ImportedAt:    importedAt.Format(time.RFC3339),
		OwnerKeyID:    owner.KeyID,
		OwnerRef:      owner.OwnerRef,
	}
	signingBytes := canonicalRecord(record, false)
	record.Signature, err = localevidence.SignOwnerAdvancedTrust(workspace, signingBytes)
	if err != nil {
		return Record{}, fmt.Errorf("advancedtrust: sign local trust record: %w", err)
	}
	rawRecord := canonicalRecord(record, true)
	if err := persist(workspace, rawRecord); err != nil {
		return Record{}, err
	}
	return Load(workspace)
}

// Load returns only after strict wire, SHA-256, local Owner binding, signature,
// private permission, and stable-read verification succeeds.
func Load(workspace string) (Record, error) {
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return Record{}, fmt.Errorf("advancedtrust: load local Owner custody: %w", err)
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return Record{}, fmt.Errorf("advancedtrust: open workspace: %w", err)
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return Record{}, fmt.Errorf("advancedtrust: begin read transaction: %w", err)
	}
	defer func() { _ = transaction.Close() }()
	exists, _, err := transaction.Exists(recordRelativePath)
	if err != nil {
		return Record{}, fmt.Errorf("advancedtrust: inspect local trust record: %w", err)
	}
	if !exists {
		return Record{}, ErrMissing
	}
	trustPath := filepath.Join(workspace, filepath.FromSlash(trustRelativeDir))
	recordPath := filepath.Join(workspace, filepath.FromSlash(recordRelativePath))
	if err := backupcustody.RequirePrivatePath(filepath.Dir(trustPath), true); err != nil {
		return Record{}, fmt.Errorf("advancedtrust: Advanced directory is not private: %w", err)
	}
	if err := backupcustody.RequirePrivatePath(trustPath, true); err != nil {
		return Record{}, fmt.Errorf("advancedtrust: trust directory is not private: %w", err)
	}
	if err := backupcustody.RequirePrivatePath(recordPath, false); err != nil {
		return Record{}, fmt.Errorf("advancedtrust: trust record is not private: %w", err)
	}
	raw, info, err := transaction.ReadStable(recordRelativePath)
	if err != nil {
		return Record{}, fmt.Errorf("advancedtrust: read local trust record: %w", err)
	}
	if info == nil || !info.Mode().IsRegular() ||
		(info.Mode()&os.ModeSymlink) != 0 {
		return Record{}, errors.New("advancedtrust: trust record is not a plain regular file")
	}
	if err := backupcustody.RequirePrivatePath(recordPath, false); err != nil {
		return Record{}, fmt.Errorf("advancedtrust: trust record privacy changed during read: %w", err)
	}
	record, err := decodeRecord(raw)
	if err != nil {
		return Record{}, err
	}
	if record.OwnerRef != owner.OwnerRef || record.OwnerKeyID != owner.KeyID ||
		record.Signature.OwnerRef != owner.OwnerRef || record.Signature.KeyID != owner.KeyID {
		return Record{}, errors.New("advancedtrust: local trust record is not bound to current Owner custody")
	}
	if err := localevidence.VerifyOwnerAdvancedTrust(
		workspace,
		canonicalRecord(record, false),
		record.Signature,
	); err != nil {
		return Record{}, errors.New("advancedtrust: local trust record Owner signature does not verify")
	}
	return publicRecord(record), nil
}

// Inspect returns only issuer/key identifiers and verified record metadata.
// It never returns raw public-key or signature material.
func Inspect(workspace string) (Inspection, error) {
	record, err := Load(workspace)
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{
		SchemaVersion: record.SchemaVersion,
		BundleSHA256:  record.BundleSHA256,
		ImportedAt:    record.ImportedAt,
		OwnerRef:      record.OwnerRef,
		OwnerKeyID:    record.OwnerKeyID,
		Keys:          slices.Clone(record.keyReferences),
	}, nil
}

// TrustBundle returns a detached public-key bundle for offline capability
// verification. Mutating it cannot alter the loaded record.
func (record Record) TrustBundle() advancedcapability.TrustBundle {
	if record.bundleFactory == nil {
		return advancedcapability.TrustBundle{}
	}
	return record.bundleFactory()
}

func persist(workspace string, raw []byte) error {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return fmt.Errorf("advancedtrust: open workspace: %w", err)
	}
	transaction, err := root.BeginTransaction()
	if err != nil {
		_ = root.Close()
		return fmt.Errorf("advancedtrust: begin trust-store transaction: %w", err)
	}
	if err := transaction.MkdirAll(trustRelativeDir, 0o700); err != nil {
		_ = transaction.Close()
		_ = root.Close()
		return fmt.Errorf("advancedtrust: create trust-store directories: %w", err)
	}
	if err := transaction.Close(); err != nil {
		_ = root.Close()
		return fmt.Errorf("advancedtrust: close directory transaction: %w", err)
	}
	advancedPath := filepath.Join(workspace, ".stackkit", "advanced")
	trustPath := filepath.Join(advancedPath, "trust")
	if err := backupcustody.ProtectPrivatePath(advancedPath, true); err != nil {
		_ = root.Close()
		return fmt.Errorf("advancedtrust: protect Advanced directory: %w", err)
	}
	if err := backupcustody.ProtectPrivatePath(trustPath, true); err != nil {
		_ = root.Close()
		return fmt.Errorf("advancedtrust: protect trust directory: %w", err)
	}
	view, err := root.View(trustRelativeDir)
	if err != nil {
		_ = root.Close()
		return fmt.Errorf("advancedtrust: open trust-store view: %w", err)
	}
	result, err := view.WriteAtomic0600("bundle.json", raw)
	if err != nil || !result.Installed || !result.FileSynced {
		_ = root.Close()
		if err == nil {
			err = errors.New("atomic write did not install and sync the record")
		}
		return fmt.Errorf("advancedtrust: persist trust record: %w", err)
	}
	if err := root.Close(); err != nil {
		return fmt.Errorf("advancedtrust: close trust-store root: %w", err)
	}
	recordPath := filepath.Join(trustPath, "bundle.json")
	if err := backupcustody.ProtectPrivatePath(recordPath, false); err != nil {
		return fmt.Errorf("advancedtrust: protect trust record: %w", err)
	}
	return nil
}

func decodeRecord(raw []byte) (diskRecord, error) {
	if len(raw) == 0 || len(raw) > maxDocumentBytes || !json.Valid(raw) {
		return diskRecord{}, errors.New("advancedtrust: local trust record is malformed")
	}
	fields, err := decodeExactObject(raw, map[string]struct{}{
		"bundle": {}, "bundleSHA256": {}, "importedAt": {}, "kind": {},
		"ownerKeyId": {}, "ownerRef": {}, "ownerSignature": {}, "schemaVersion": {},
	})
	if err != nil {
		return diskRecord{}, fmt.Errorf("advancedtrust: malformed local trust record: %w", err)
	}
	var record diskRecord
	if record.SchemaVersion, err = strictString(fields["schemaVersion"]); err != nil ||
		record.SchemaVersion != RecordSchemaVersion {
		return diskRecord{}, errors.New("advancedtrust: unsupported local trust record schemaVersion")
	}
	if record.Kind, err = strictString(fields["kind"]); err != nil || record.Kind != recordKind {
		return diskRecord{}, errors.New("advancedtrust: local trust record kind is invalid")
	}
	if record.BundleSHA256, err = strictString(fields["bundleSHA256"]); err != nil ||
		!sha256Pin.MatchString(record.BundleSHA256) {
		return diskRecord{}, errors.New("advancedtrust: local trust record bundleSHA256 is invalid")
	}
	if record.ImportedAt, err = strictString(fields["importedAt"]); err != nil {
		return diskRecord{}, errors.New("advancedtrust: local trust record importedAt is invalid")
	}
	importedAt, err := time.Parse(time.RFC3339, record.ImportedAt)
	if err != nil || importedAt.Location() != time.UTC ||
		importedAt.Format(time.RFC3339) != record.ImportedAt {
		return diskRecord{}, errors.New("advancedtrust: local trust record importedAt is not canonical UTC RFC3339")
	}
	if record.OwnerKeyID, err = strictString(fields["ownerKeyId"]); err != nil ||
		record.OwnerKeyID == "" {
		return diskRecord{}, errors.New("advancedtrust: local trust record ownerKeyId is invalid")
	}
	if record.OwnerRef, err = strictString(fields["ownerRef"]); err != nil ||
		record.OwnerRef == "" {
		return diskRecord{}, errors.New("advancedtrust: local trust record ownerRef is invalid")
	}
	record.Bundle, err = decodeBundle(fields["bundle"])
	if err != nil {
		return diskRecord{}, err
	}
	if subtle.ConstantTimeCompare([]byte(record.Bundle.digest), []byte(record.BundleSHA256)) != 1 {
		return diskRecord{}, errors.New("advancedtrust: local trust record bundle digest does not match")
	}
	record.Signature, err = decodeSignature(fields["ownerSignature"])
	if err != nil {
		return diskRecord{}, err
	}
	if !bytes.Equal(raw, canonicalRecord(record, true)) {
		return diskRecord{}, errors.New("advancedtrust: local trust record must be exact canonical JSON")
	}
	return record, nil
}

func decodeSignature(raw []byte) (localevidence.OwnerAdvancedTrustSignature, error) {
	fields, err := decodeExactObject(raw, map[string]struct{}{
		"keyId": {}, "ownerRef": {}, "value": {},
	})
	if err != nil {
		return localevidence.OwnerAdvancedTrustSignature{}, errors.New("advancedtrust: Owner signature is malformed")
	}
	keyID, keyErr := strictString(fields["keyId"])
	ownerRef, ownerErr := strictString(fields["ownerRef"])
	value, valueErr := strictString(fields["value"])
	if keyErr != nil || ownerErr != nil || valueErr != nil ||
		keyID == "" || ownerRef == "" || value == "" {
		return localevidence.OwnerAdvancedTrustSignature{}, errors.New("advancedtrust: Owner signature is malformed")
	}
	return localevidence.OwnerAdvancedTrustSignature{OwnerRef: ownerRef, KeyID: keyID, Value: value}, nil
}

func canonicalRecord(record diskRecord, includeSignature bool) []byte {
	document := []byte(`{"bundle":`)
	document = append(document, record.Bundle.canonical...)
	document = append(document, `,"bundleSHA256":`...)
	document = strconv.AppendQuote(document, record.BundleSHA256)
	document = append(document, `,"importedAt":`...)
	document = strconv.AppendQuote(document, record.ImportedAt)
	document = append(document, `,"kind":`...)
	document = strconv.AppendQuote(document, record.Kind)
	document = append(document, `,"ownerKeyId":`...)
	document = strconv.AppendQuote(document, record.OwnerKeyID)
	document = append(document, `,"ownerRef":`...)
	document = strconv.AppendQuote(document, record.OwnerRef)
	if includeSignature {
		document = append(document, `,"ownerSignature":{"keyId":`...)
		document = strconv.AppendQuote(document, record.Signature.KeyID)
		document = append(document, `,"ownerRef":`...)
		document = strconv.AppendQuote(document, record.Signature.OwnerRef)
		document = append(document, `,"value":`...)
		document = strconv.AppendQuote(document, record.Signature.Value)
		document = append(document, '}')
	}
	document = append(document, `,"schemaVersion":`...)
	document = strconv.AppendQuote(document, record.SchemaVersion)
	return append(document, '}')
}

func publicRecord(record diskRecord) Record {
	importedAt, _ := time.Parse(time.RFC3339, record.ImportedAt)
	bundle := decodedBundle{
		keys:      slices.Clone(record.Bundle.keys),
		canonical: bytes.Clone(record.Bundle.canonical),
		digest:    record.Bundle.digest,
	}
	references := make([]KeyReference, 0, len(bundle.keys))
	for _, key := range bundle.keys {
		references = append(references, KeyReference{IssuerID: key.IssuerID, KeyID: key.KeyID})
	}
	return Record{
		SchemaVersion: record.SchemaVersion,
		Kind:          record.Kind,
		BundleSHA256:  record.BundleSHA256,
		ImportedAt:    importedAt,
		OwnerRef:      record.OwnerRef,
		OwnerKeyID:    record.OwnerKeyID,
		KeyCount:      len(record.Bundle.keys),
		bundleFactory: func() advancedcapability.TrustBundle {
			return bundle.verifierBundle()
		},
		keyReferences: references,
	}
}
