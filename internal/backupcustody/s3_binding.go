package backupcustody

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"

	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

func canonicalS3Material(material S3TargetMaterial) (S3TargetMaterial, error) {
	repo, err := backupexec.CanonicalS3Repository(material.repository(), material.Passphrase)
	if err != nil {
		return S3TargetMaterial{}, err
	}
	material.Endpoint = repo.Endpoint
	return material, nil
}

// S3TargetReferences commits actual consumer bytes with the owner's existing
// private wrapping key. Public references cannot be dictionary-tested for endpoints.
func S3TargetReferences(workspace string, material S3TargetMaterial) (targetRef, custodyRef string, err error) {
	material, err = canonicalS3Material(material)
	if err != nil {
		return "", "", err
	}
	_, key, err := Establish(workspace)
	if err != nil {
		return "", "", err
	}
	defer Clear(key)
	commit := func(domain string, value any) string {
		raw, _ := json.Marshal(value)
		defer Clear(raw)
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(domain + "\x00"))
		_, _ = mac.Write(raw)
		return domain + "://sha256/" + hex.EncodeToString(mac.Sum(nil))
	}
	targetRef = commit("backup-target", struct{ Endpoint, Bucket, Prefix, Region string }{material.Endpoint, material.Bucket, material.Prefix, material.Region})
	custodyRef = commit("backup-custody-attestation", material)
	return targetRef, custodyRef, nil
}

// StoredS3TargetAuthority verifies owner custody without treating an expired
// binding as current authority. Only explicit rebind uses this historical value.
func StoredS3TargetAuthority(workspace string) (S3TargetAuthority, error) {
	record, err := readS3TargetRecord(workspace)
	return record.Authority, err
}

// RebindS3Target renews source/Plan authority while preserving identical target
// and credentials. The caller must hold the owner-authorized lifecycle mutation.
func RebindS3Target(workspace string, authority S3TargetAuthority, material S3TargetMaterial) error {
	if err := validateS3TargetAuthority(workspace, authority); err != nil {
		return err
	}
	material, err := canonicalS3Material(material)
	if err != nil {
		return err
	}
	record, err := readS3TargetRecord(workspace)
	if err != nil {
		return err
	}
	if err := VerifyS3RebindMaterial(workspace, material); err != nil {
		return err
	}
	target, custody, err := S3TargetReferences(workspace, material)
	if err != nil {
		return err
	}
	if authority.Binding.BackupTargetRef != target || authority.Binding.CustodyAttestationRef != custody {
		return errors.New("backupcustody: rebind references do not commit the imported target")
	}
	record.Authority = authority
	record.Signature = localevidence.OwnerPolicyStateSignature{}
	payload, _ := json.Marshal(record)
	record.Signature, err = localevidence.SignOwnerPolicyState(workspace, payload)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(record)
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(s3TargetPath, raw)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("backupcustody: target rebind durability was not confirmed")
	}
	if err := ProtectPrivatePath(filepath.Join(workspace, filepath.FromSlash(s3TargetPath)), false); err != nil {
		return err
	}
	if err := RequirePrivatePath(filepath.Join(workspace, filepath.FromSlash(s3TargetPath)), false); err != nil {
		return err
	}
	verified, err := LoadS3Target(workspace, authority)
	Clear(verified.Passphrase)
	return err
}

// VerifyS3RebindMaterial checks unchanged target material before Inventory mutation.
func VerifyS3RebindMaterial(workspace string, material S3TargetMaterial) error {
	material, err := canonicalS3Material(material)
	if err != nil {
		return err
	}
	record, err := readS3TargetRecord(workspace)
	if err != nil {
		return err
	}
	previous, err := decryptS3Target(workspace, record)
	if err != nil {
		return err
	}
	defer Clear(previous.Passphrase)
	previous, err = canonicalS3Material(previous)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(previous, material) {
		return errors.New("backupcustody: rebind cannot replace target or credentials")
	}
	return nil
}
