package backupcustody

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/hostcustody"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/pkg/hostdelivery"
)

// SealedExpectation is what the importing node requires of a backup target
// delivery beyond its own identity: the tenant the owner is operating for.
type SealedExpectation struct {
	TenantRef string
	// Operation is OperationBackupTargetImport or OperationBackupTargetRebind.
	Operation string
}

// HostStore is the node's private custody for sealed host delivery
// (hostcustody). Its records live beside the backup custody, under the same
// owner signature and local custody key, so a node keeps the recipient key and
// delivery ledger it already holds.
func HostStore(workspace string) hostcustody.Store { return hostStore{workspace: workspace} }

// EstablishRecipient creates the node's separate encryption recipient key once.
func EstablishRecipient(workspace string) error {
	return hostcustody.EstablishRecipient(HostStore(workspace))
}

// RecipientStatement proves the recipient key belongs to this node's owner and
// answers the delivering authority's challenge.
func RecipientStatement(workspace, challenge string, now time.Time) (hostdelivery.Statement, error) {
	return hostcustody.RecipientStatement(HostStore(workspace), challenge, now)
}

// OpenSealedDelivery opens one sealed backup-target delivery for this node and
// consumes it. The caller holds the lifecycle mutation. The delivery is
// recorded as consumed before the plaintext is returned, so a replay,
// including one after a crash, is denied and the delivering authority must
// seal a fresh one.
func OpenSealedDelivery(workspace string, raw []byte, expect SealedExpectation, now time.Time) ([]byte, error) {
	if expect.TenantRef == "" {
		return nil, errors.New("backupcustody: sealed delivery requires the expected tenant")
	}
	if expect.Operation != hostdelivery.OperationBackupTargetImport && expect.Operation != hostdelivery.OperationBackupTargetRebind {
		return nil, errors.New("backupcustody: sealed delivery is not a backup target operation")
	}
	delivery, err := hostcustody.OpenDelivery(HostStore(workspace), raw, hostcustody.Expectation{TenantRef: expect.TenantRef, Operations: []string{expect.Operation}}, now)
	if err != nil {
		return nil, err
	}
	return delivery.Plaintext, nil
}

var hostRecordName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(\.[a-z0-9-]+)*\.json$`)

type hostStore struct{ workspace string }

func (s hostStore) Workspace() string { return s.workspace }

func (s hostStore) WrappingKey() ([]byte, error) {
	_, wrapping, err := Establish(s.workspace)
	return wrapping, err
}

func (s hostStore) relative(name string) (string, error) {
	if !hostRecordName.MatchString(name) {
		return "", errors.New("backupcustody: invalid host custody record name")
	}
	return custodyRelDir + "/" + name, nil
}

func (s hostStore) Read(name string, maxBytes int64, record any, signature *localevidence.OwnerPolicyStateSignature) error {
	relative, err := s.relative(name)
	if err != nil {
		return err
	}
	if err := readSignedRecord(s.workspace, relative, maxBytes, record, signature); errors.Is(err, ErrMissing) {
		return hostcustody.ErrMissing
	} else if err != nil {
		return err
	}
	return nil
}

func (s hostStore) Write(name string, record any, signature *localevidence.OwnerPolicyStateSignature, create bool) error {
	relative, err := s.relative(name)
	if err != nil {
		return err
	}
	// The private custody directory exists once the local custody key does.
	_, wrapping, err := Establish(s.workspace)
	Clear(wrapping)
	if err != nil {
		return err
	}
	return writeSignedRecord(s.workspace, relative, record, signature, create)
}

func (s hostStore) Remove(name string) error {
	relative, err := s.relative(name)
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(s.workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	info, err := tx.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := requirePrivateFile(info); err != nil {
		return err
	}
	return tx.RemoveRegularFile(relative, info)
}

func readSignedRecord(workspace, relative string, maxBytes int64, record any, signature *localevidence.OwnerPolicyStateSignature) error {
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
	info, err := tx.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return ErrMissing
	}
	if err != nil {
		return err
	}
	if err := requirePrivateFile(info); err != nil {
		return err
	}
	path := filepath.Join(workspace, filepath.FromSlash(relative))
	if err := requirePrivatePath(path, false); err != nil {
		return err
	}
	raw, _, err := tx.ReadStableBounded(relative, maxBytes)
	if err != nil {
		return errors.New("backupcustody: custody record is unreadable or oversized")
	}
	if err := decodeStrictRecord(raw, record); err != nil {
		return err
	}
	signed := *signature
	*signature = localevidence.OwnerPolicyStateSignature{}
	payload, _ := json.Marshal(record)
	if err := localevidence.VerifyOwnerPolicyState(workspace, payload, signed); err != nil {
		return err
	}
	*signature = signed
	return nil
}

func decodeStrictRecord(raw []byte, record any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(record); err != nil {
		return errors.New("backupcustody: invalid custody record")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("backupcustody: trailing custody record content")
	}
	return nil
}

func writeSignedRecord(workspace, relative string, record any, signature *localevidence.OwnerPolicyStateSignature, create bool) error {
	*signature = localevidence.OwnerPolicyStateSignature{}
	payload, _ := json.Marshal(record)
	signed, err := localevidence.SignOwnerPolicyState(workspace, payload)
	if err != nil {
		return err
	}
	*signature = signed
	raw, _ := json.Marshal(record)
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	if create {
		tx, err := root.BeginTransaction()
		if err != nil {
			return err
		}
		defer tx.Close()
		if err := tx.WriteFileExclusive(relative, raw, privateFileMode); err != nil {
			return errors.New("backupcustody: custody record already exists or cannot be installed")
		}
		return restrictPathToCurrentUser(filepath.Join(workspace, filepath.FromSlash(relative)), false)
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(relative, raw)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("backupcustody: custody record durability was not confirmed")
	}
	if err := ProtectPrivatePath(filepath.Join(workspace, filepath.FromSlash(relative)), false); err != nil {
		return err
	}
	return RequirePrivatePath(filepath.Join(workspace, filepath.FromSlash(relative)), false)
}
