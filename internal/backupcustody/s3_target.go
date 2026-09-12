package backupcustody

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	stackcrypto "github.com/kombifyio/stackkits/internal/crypto"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const s3TargetPath = custodyRelDir + "/s3-target.json"

// S3TargetAuthority is projected from the verified Plan, never from secret input.
type S3TargetAuthority struct {
	Binding      generationartifact.ApplyBackupTargetBindingRequirement `json:"binding"`
	SourceDigest string                                                 `json:"sourceDigest"`
}

// S3TargetMaterial stays encrypted in local owner custody. It never enters a Plan.
type S3TargetMaterial struct {
	Endpoint        string `json:"endpoint"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	Region          string `json:"region"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	Passphrase      []byte `json:"passphrase"`
}

type s3TargetRecord struct {
	APIVersion string                                  `json:"apiVersion"`
	Authority  S3TargetAuthority                       `json:"authority"`
	Ciphertext []byte                                  `json:"ciphertext"`
	Signature  localevidence.OwnerPolicyStateSignature `json:"signature"`
}

func (m S3TargetMaterial) repository() backupexec.S3Repository {
	return backupexec.S3Repository{Endpoint: m.Endpoint, Bucket: m.Bucket, Prefix: m.Prefix, Region: m.Region, AccessKeyID: m.AccessKeyID, SecretAccessKey: m.SecretAccessKey}
}

func validateS3TargetAuthority(workspace string, authority S3TargetAuthority) error {
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return err
	}
	b := authority.Binding
	until, err := time.Parse(time.RFC3339Nano, b.ValidUntil)
	if err != nil || !time.Now().Before(until) || b.SiteRef != owner.Binding.SiteRef || len(b.TargetNodeRefs) != 1 || b.TargetNodeRefs[0] != owner.Binding.NodeRef || b.CapabilityRef != "offsite-object-backup" || b.ContractOwnerRef != "stackkits-cloud-offsite-backup" {
		return errors.New("backupcustody: S3 target authority is expired or differs from the local owner target")
	}
	for _, value := range []string{b.BindingHash, b.RequirementsHash, b.SpecHash, authority.SourceDigest} {
		if !validTargetDigest(value) {
			return errors.New("backupcustody: incomplete S3 target authority")
		}
	}
	for prefix, value := range map[string]string{"backup-target-binding://sha256/": b.BindingRef, "backup-target://sha256/": b.BackupTargetRef, "backup-custody-attestation://sha256/": b.CustodyAttestationRef} {
		if !strings.HasPrefix(value, prefix) || !validTargetDigest("sha256:"+strings.TrimPrefix(value, prefix)) {
			return errors.New("backupcustody: invalid opaque S3 target reference")
		}
	}
	return nil
}

func validTargetDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, c := range value[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// EstablishS3Target binds one immutable target to an exact owner-approved Plan.
// Validation uses the native engine boundary without executing an operation.
func EstablishS3Target(workspace string, authority S3TargetAuthority, material S3TargetMaterial) error {
	if err := validateS3TargetAuthority(workspace, authority); err != nil {
		return err
	}
	material, err := canonicalS3Material(material)
	if err != nil {
		return err
	}
	targetRef, custodyRef, err := S3TargetReferences(workspace, material)
	if err != nil {
		return err
	}
	if authority.Binding.BackupTargetRef != targetRef || authority.Binding.CustodyAttestationRef != custodyRef {
		return errors.New("backupcustody: target references do not commit imported material")
	}
	if existing, err := LoadS3Target(workspace, authority); err == nil {
		defer Clear(existing.Passphrase)
		if !reflect.DeepEqual(existing, material) {
			return errors.New("backupcustody: S3 target replacement is not authorized")
		}
		return nil
	} else if !errors.Is(err, ErrMissing) {
		return err
	}
	_, wrapping, err := Establish(workspace)
	if err != nil {
		return err
	}
	defer Clear(wrapping)
	plaintext, err := json.Marshal(material)
	if err != nil {
		return errors.New("backupcustody: encode S3 material")
	}
	defer Clear(plaintext)
	if len(plaintext) > 32<<10 {
		return errors.New("backupcustody: oversized S3 target material")
	}
	ciphertext, err := stackcrypto.EncryptWithPassphrase(plaintext, string(wrapping))
	if err != nil {
		return errors.New("backupcustody: encrypt S3 material")
	}
	record := s3TargetRecord{APIVersion: "stackkit.owner-s3-target-custody/v1", Authority: authority, Ciphertext: ciphertext}
	payload, _ := json.Marshal(record)
	record.Signature, err = localevidence.SignOwnerPolicyState(workspace, payload)
	if err != nil {
		return err
	}
	encoded, _ := json.Marshal(record)
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	if err := tx.WriteFileExclusive(s3TargetPath, encoded, privateFileMode); err != nil {
		return errors.New("backupcustody: S3 custody already exists or cannot be installed")
	}
	if err := restrictPathToCurrentUser(filepath.Join(workspace, filepath.FromSlash(s3TargetPath)), false); err != nil {
		return err
	}
	loaded, err := LoadS3Target(workspace, authority)
	Clear(loaded.Passphrase)
	return err
}

// LoadS3Target verifies owner, exact target/source binding and ciphertext before decryption.
func LoadS3Target(workspace string, authority S3TargetAuthority) (S3TargetMaterial, error) {
	if err := validateS3TargetAuthority(workspace, authority); err != nil {
		return S3TargetMaterial{}, err
	}
	record, err := readS3TargetRecord(workspace)
	if err != nil {
		return S3TargetMaterial{}, err
	}
	if !reflect.DeepEqual(record.Authority, authority) {
		return S3TargetMaterial{}, errors.New("backupcustody: S3 target differs from current source or target authority")
	}
	material, err := decryptS3Target(workspace, record)
	if err != nil {
		return S3TargetMaterial{}, err
	}
	target, custody, err := S3TargetReferences(workspace, material)
	if err != nil || target != authority.Binding.BackupTargetRef || custody != authority.Binding.CustodyAttestationRef {
		Clear(material.Passphrase)
		return S3TargetMaterial{}, errors.New("backupcustody: target references differ from decrypted consumer material")
	}
	return material, nil
}

func readS3TargetRecord(workspace string) (s3TargetRecord, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return s3TargetRecord{}, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return s3TargetRecord{}, err
	}
	defer tx.Close()
	info, err := tx.Lstat(s3TargetPath)
	if errors.Is(err, os.ErrNotExist) {
		return s3TargetRecord{}, ErrMissing
	}
	if err != nil {
		return s3TargetRecord{}, err
	}
	if err := requirePrivateFile(info); err != nil {
		return s3TargetRecord{}, err
	}
	path := filepath.Join(workspace, filepath.FromSlash(s3TargetPath))
	if err := requirePrivatePath(path, false); err != nil {
		return s3TargetRecord{}, err
	}
	raw, _, err := tx.ReadStable(s3TargetPath)
	if err != nil {
		return s3TargetRecord{}, err
	}
	if len(raw) > 64<<10 {
		return s3TargetRecord{}, errors.New("backupcustody: oversized S3 target record")
	}
	var record s3TargetRecord
	if err := decodeS3Target(raw, &record); err != nil {
		return s3TargetRecord{}, err
	}
	if record.APIVersion != "stackkit.owner-s3-target-custody/v1" {
		return s3TargetRecord{}, errors.New("backupcustody: S3 target differs from current source or target authority")
	}
	signature := record.Signature
	record.Signature = localevidence.OwnerPolicyStateSignature{}
	payload, _ := json.Marshal(record)
	if err := localevidence.VerifyOwnerPolicyState(workspace, payload, signature); err != nil {
		return s3TargetRecord{}, err
	}
	record.Signature = signature
	if err := requirePrivatePath(path, false); err != nil {
		return s3TargetRecord{}, err
	}
	return record, nil
}

func decryptS3Target(workspace string, record s3TargetRecord) (S3TargetMaterial, error) {
	_, wrapping, err := Load(workspace)
	if err != nil {
		return S3TargetMaterial{}, err
	}
	defer Clear(wrapping)
	plaintext, err := stackcrypto.DecryptWithPassphrase(record.Ciphertext, string(wrapping))
	if err != nil {
		return S3TargetMaterial{}, errors.New("backupcustody: cannot decrypt S3 target")
	}
	defer Clear(plaintext)
	var material S3TargetMaterial
	if err := decodeS3Target(plaintext, &material); err != nil {
		Clear(material.Passphrase)
		return S3TargetMaterial{}, err
	}
	return material, nil
}

func decodeS3Target(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("backupcustody: invalid S3 target record")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("backupcustody: trailing S3 target content")
	}
	return nil
}
