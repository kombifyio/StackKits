package backupcustody

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	stackcrypto "github.com/kombifyio/stackkits/internal/crypto"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/pkg/hostdelivery"
)

const (
	recipientPath      = custodyRelDir + "/recipient.json"
	deliveryLedgerPath = custodyRelDir + "/delivery-ledger.json"

	recipientAPIVersion = "stackkit.backup-recipient-custody/v1"
	ledgerAPIVersion    = "stackkit.sealed-delivery-ledger/v1"

	// statementMaxChallenge bounds the freshness value a delivering authority supplies.
	statementMaxChallenge = 128
	// ledgerRetention keeps a consumed delivery recorded beyond its expiry.
	ledgerRetention = time.Hour
)

// SealedExpectation is what the importing node requires of a delivery beyond
// its own identity: the tenant the owner is operating for.
type SealedExpectation struct {
	TenantRef string
	// Operation is OperationBackupTargetImport or OperationBackupTargetRebind.
	Operation string
}

type recipientRecord struct {
	APIVersion string                                  `json:"apiVersion"`
	Generation uint64                                  `json:"generation"`
	PublicKey  string                                  `json:"publicKey"`
	Ciphertext []byte                                  `json:"ciphertext"`
	Signature  localevidence.OwnerPolicyStateSignature `json:"signature"`
}

type deliveryLedger struct {
	APIVersion string                                  `json:"apiVersion"`
	Generation uint64                                  `json:"generation"`
	Consumed   []consumedDelivery                      `json:"consumed"`
	Signature  localevidence.OwnerPolicyStateSignature `json:"signature"`
}

type consumedDelivery struct {
	OperationID string `json:"operationId"`
	NotAfter    string `json:"notAfter"`
}

// EstablishRecipient creates the node's separate encryption recipient key once.
// The private half is encrypted under the owner custody wrapping key and never
// leaves this node; Guard's or any other signing key is never used to decrypt.
func EstablishRecipient(workspace string) error {
	if _, err := readRecipient(workspace); err == nil {
		return nil
	} else if !errors.Is(err, ErrMissing) {
		return err
	}
	_, wrapping, err := Establish(workspace)
	if err != nil {
		return err
	}
	defer Clear(wrapping)
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return errors.New("backupcustody: generate recipient key")
	}
	seed := private.Bytes()
	defer Clear(seed)
	ciphertext, err := stackcrypto.EncryptWithPassphrase(seed, string(wrapping))
	if err != nil {
		return errors.New("backupcustody: encrypt recipient key")
	}
	record := recipientRecord{APIVersion: recipientAPIVersion, Generation: 1, PublicKey: base64.RawStdEncoding.EncodeToString(private.PublicKey().Bytes()), Ciphertext: ciphertext}
	return writeSignedRecord(workspace, recipientPath, &record, &record.Signature, true)
}

// RecipientStatement proves the recipient key belongs to this node's owner and
// answers the delivering authority's challenge.
func RecipientStatement(workspace, challenge string, now time.Time) (hostdelivery.Statement, error) {
	if challenge == "" || len(challenge) > statementMaxChallenge {
		return hostdelivery.Statement{}, errors.New("backupcustody: recipient challenge is empty or oversized")
	}
	if err := EstablishRecipient(workspace); err != nil {
		return hostdelivery.Statement{}, err
	}
	record, err := readRecipient(workspace)
	if err != nil {
		return hostdelivery.Statement{}, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return hostdelivery.Statement{}, err
	}
	key, err := localevidence.LoadOwnerKey(workspace)
	if err != nil {
		return hostdelivery.Statement{}, err
	}
	public, err := base64.RawStdEncoding.DecodeString(record.PublicKey)
	if err != nil {
		return hostdelivery.Statement{}, errors.New("backupcustody: recipient record is invalid")
	}
	statement := hostdelivery.Statement{
		APIVersion: hostdelivery.StatementAPIVersion, SiteRef: owner.Binding.SiteRef, NodeRef: owner.Binding.NodeRef,
		RecipientKey: record.PublicKey, Fingerprint: hostdelivery.Fingerprint(public), Generation: record.Generation,
		Challenge: challenge, IssuedAt: now.UTC().Format(time.RFC3339Nano),
		OwnerKeyID: owner.KeyID, OwnerPublic: base64.RawStdEncoding.EncodeToString(key.Public()),
	}
	payload, err := statement.SigningBytes()
	if err != nil {
		return hostdelivery.Statement{}, err
	}
	signature, err := localevidence.SignOwnerPolicyState(workspace, payload)
	if err != nil {
		return hostdelivery.Statement{}, err
	}
	statement.Signature = hostdelivery.Signature{OwnerRef: signature.OwnerRef, KeyID: signature.KeyID, Value: signature.Value}
	return statement, nil
}

// OpenSealedDelivery opens one sealed delivery for this node and consumes it.
// The caller holds the lifecycle mutation. The delivery is recorded as consumed
// before the plaintext is returned, so a replay, including one after a crash,
// is denied and the delivering authority must seal a fresh one.
func OpenSealedDelivery(workspace string, raw []byte, expect SealedExpectation, now time.Time) ([]byte, error) {
	if expect.TenantRef == "" {
		return nil, errors.New("backupcustody: sealed delivery requires the expected tenant")
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return nil, err
	}
	record, err := readRecipient(workspace)
	if err != nil {
		return nil, err
	}
	_, wrapping, err := Load(workspace)
	if err != nil {
		return nil, err
	}
	defer Clear(wrapping)
	seed, err := stackcrypto.DecryptWithPassphrase(record.Ciphertext, string(wrapping))
	if err != nil {
		return nil, errors.New("backupcustody: cannot decrypt recipient key")
	}
	defer Clear(seed)
	context, plaintext, err := hostdelivery.Open(seed, raw, now)
	if err != nil {
		return nil, err
	}
	if (expect.Operation != hostdelivery.OperationBackupTargetImport && expect.Operation != hostdelivery.OperationBackupTargetRebind) || context.Operation != expect.Operation || context.TenantRef != expect.TenantRef ||
		context.SiteRef != owner.Binding.SiteRef || context.NodeRef != owner.Binding.NodeRef || context.BindingGeneration != record.Generation {
		Clear(plaintext)
		return nil, errors.New("backupcustody: sealed delivery is not addressed to this node, tenant, generation and operation")
	}
	if err := consumeDelivery(workspace, record.Generation, context, now); err != nil {
		Clear(plaintext)
		return nil, err
	}
	return plaintext, nil
}

func consumeDelivery(workspace string, generation uint64, context hostdelivery.Context, now time.Time) error {
	ledger := deliveryLedger{APIVersion: ledgerAPIVersion, Generation: generation}
	existing := false
	if err := readSignedRecord(workspace, deliveryLedgerPath, &ledger, &ledger.Signature); err == nil {
		existing = true
	} else if !errors.Is(err, ErrMissing) {
		return err
	}
	if ledger.APIVersion != ledgerAPIVersion || ledger.Generation != generation {
		return errors.New("backupcustody: delivery ledger does not match the recipient generation")
	}
	kept := ledger.Consumed[:0]
	for _, entry := range ledger.Consumed {
		if entry.OperationID == context.OperationID {
			return errors.New("backupcustody: sealed delivery was already consumed")
		}
		if until, err := time.Parse(time.RFC3339Nano, entry.NotAfter); err == nil && now.Sub(until) < ledgerRetention {
			kept = append(kept, entry)
		}
	}
	ledger.Consumed = append(kept, consumedDelivery{OperationID: context.OperationID, NotAfter: context.NotAfter})
	return writeSignedRecord(workspace, deliveryLedgerPath, &ledger, &ledger.Signature, !existing)
}

func readRecipient(workspace string) (recipientRecord, error) {
	var record recipientRecord
	if err := readSignedRecord(workspace, recipientPath, &record, &record.Signature); err != nil {
		return recipientRecord{}, err
	}
	if record.APIVersion != recipientAPIVersion || record.Generation == 0 {
		return recipientRecord{}, errors.New("backupcustody: recipient record is invalid")
	}
	return record, nil
}

func readSignedRecord(workspace, relative string, record any, signature *localevidence.OwnerPolicyStateSignature) error {
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
	raw, _, err := tx.ReadStable(relative)
	if err != nil {
		return err
	}
	if len(raw) > 64<<10 {
		return errors.New("backupcustody: oversized custody record")
	}
	if err := decodeS3Target(raw, record); err != nil {
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
