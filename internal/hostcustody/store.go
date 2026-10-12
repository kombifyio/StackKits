// Package hostcustody is the node side of sealed host delivery
// (pkg/hostdelivery). It owns this server's recipient key and its
// generations, the delivering authority key pinned for each generation, the
// replay ledger, the registry of delivered secrets, detachment, and the local
// custody of delivered Wallet items and SSH access state.
//
// A StackKit acts only on the server it is installed on: every record lives in
// the node's owner-signed private custody, the recipient private key and every
// delivered value are encrypted under the node's local custody key, and no
// value is ever written to a log, receipt or stored command. Callers hold the
// lifecycle mutation while they call a mutating function.
package hostcustody

import (
	"errors"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// ErrMissing reports that a host custody record does not exist.
var ErrMissing = errors.New("hostcustody: no host custody record")

// Store is the node's owner-bound private storage. Backup custody provides it
// so the recipient key and the delivery ledger stay in the files and under the
// local custody key the node already holds.
type Store interface {
	// Workspace is the StackKit workspace root whose Owner custody signs records.
	Workspace() string
	// WrappingKey returns a fresh caller-owned copy of the local custody key,
	// establishing it once. Callers clear it after use.
	WrappingKey() ([]byte, error)
	// Read decodes one owner-signed private record by file name, strictly and
	// within maxBytes, and verifies its signature. A missing record is
	// ErrMissing.
	Read(name string, maxBytes int64, record any, signature *localevidence.OwnerPolicyStateSignature) error
	// Write signs and durably writes one private record. With create the
	// record must not exist yet.
	Write(name string, record any, signature *localevidence.OwnerPolicyStateSignature, create bool) error
	// Remove deletes one private record; a missing record is not an error.
	Remove(name string) error
}

const (
	recipientName = "recipient.json"
	retiredName   = "recipient-retired.json"
	ledgerName    = "delivery-ledger.json"
	stateName     = "delivery-state.json"

	// recipientAPIVersion keeps the record name it had when only backup
	// targets were delivered, so existing nodes keep their key.
	recipientAPIVersion = "stackkit.backup-recipient-custody/v1"
	retiredAPIVersion   = "stackkit.host-recipient-retired/v1"
	ledgerAPIVersion    = "stackkit.sealed-delivery-ledger/v1"
	stateAPIVersion     = "stackkit.host-delivery-state/v1"

	maxRecordBytes = 64 << 10
)

func clearBytes(value []byte) { clear(value) }
