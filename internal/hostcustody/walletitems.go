package hostcustody

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	stackcrypto "github.com/kombifyio/stackkits/internal/crypto"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/pkg/hostdelivery"
)

const (
	walletIndexName       = "wallet-items.json"
	walletIndexAPIVersion = "stackkit.wallet-item-custody/v1"
	walletValueAPIVersion = "stackkit.wallet-item-value/v1"

	maxWalletItems       = 256
	maxWalletRefBytes    = 256
	maxWalletValueRecord = 256 << 10
)

type walletIndex struct {
	APIVersion string                                  `json:"apiVersion"`
	Items      []walletEntry                           `json:"items"`
	Signature  localevidence.OwnerPolicyStateSignature `json:"signature"`
}

type walletEntry struct {
	TenantRef   string `json:"tenantRef"`
	ItemRef     string `json:"itemRef"`
	Version     uint64 `json:"version"`
	Record      string `json:"record"`
	OperationID string `json:"operationId"`
	StoredAt    string `json:"storedAt"`
}

type walletValue struct {
	APIVersion string                                  `json:"apiVersion"`
	TenantRef  string                                  `json:"tenantRef"`
	ItemRef    string                                  `json:"itemRef"`
	Version    uint64                                  `json:"version"`
	Ciphertext []byte                                  `json:"ciphertext"`
	Signature  localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// WalletItemReceipt reports a stored item by reference; it carries no value.
type WalletItemReceipt struct {
	TenantRef string   `json:"tenantRef"`
	ItemRef   string   `json:"itemRef"`
	Version   uint64   `json:"version"`
	Removed   []uint64 `json:"removedVersions,omitempty"`
	// CleanupPending names older version records that could not be removed;
	// the index no longer serves them.
	CleanupPending []string `json:"cleanupPending,omitempty"`
}

// WalletItemStatus lists one item by reference and the versions in custody.
type WalletItemStatus struct {
	TenantRef     string   `json:"tenantRef"`
	ItemRef       string   `json:"itemRef"`
	Versions      []uint64 `json:"versions"`
	NeedsRotation bool     `json:"needsRotation"`
}

func validItemRef(ref string) bool {
	if ref == "" || len(ref) > maxWalletRefBytes {
		return false
	}
	for i, c := range ref {
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum && (i == 0 || !slices.Contains([]rune("._:/@+-"), c)) {
			return false
		}
	}
	return true
}

func walletRecordName(tenantRef, itemRef string, version uint64) string {
	sum := sha256.Sum256([]byte(tenantRef + "\x00" + itemRef))
	return fmt.Sprintf("wallet-item-%s-v%d.json", hex.EncodeToString(sum[:16]), version)
}

func readWalletIndex(store Store) (walletIndex, bool, error) {
	index := walletIndex{APIVersion: walletIndexAPIVersion}
	if err := store.Read(walletIndexName, maxRecordBytes, &index, &index.Signature); errors.Is(err, ErrMissing) {
		return walletIndex{APIVersion: walletIndexAPIVersion}, false, nil
	} else if err != nil {
		return walletIndex{}, false, err
	}
	if index.APIVersion != walletIndexAPIVersion {
		return walletIndex{}, false, errors.New("hostcustody: wallet item index is invalid")
	}
	return index, true, nil
}

// StoreWalletItem places an opened wallet-item-import or wallet-item-rotate
// delivery in encrypted local custody, keyed by tenant, item reference and
// version. Versions are immutable: an import must be newer than every version
// in custody, and a rotation must replace an item already held, after which
// every older version is removed.
func StoreWalletItem(store Store, delivery Delivery, now time.Time) (WalletItemReceipt, error) {
	context := delivery.Context
	if context.Operation != hostdelivery.OperationWalletItemImport && context.Operation != hostdelivery.OperationWalletItemRotate {
		return WalletItemReceipt{}, errors.New("hostcustody: delivery is not a Wallet item delivery")
	}
	if !validItemRef(context.DeliveryRef) || context.ItemVersion == 0 || len(delivery.Plaintext) == 0 {
		return WalletItemReceipt{}, errors.New("hostcustody: Wallet item reference or version is invalid")
	}
	index, existing, err := readWalletIndex(store)
	if err != nil {
		return WalletItemReceipt{}, err
	}
	rotate := context.Operation == hostdelivery.OperationWalletItemRotate
	held, err := admitWalletVersion(index, context, rotate)
	if err != nil {
		return WalletItemReceipt{}, err
	}
	wrapping, err := store.WrappingKey()
	if err != nil {
		return WalletItemReceipt{}, err
	}
	ciphertext, err := stackcrypto.EncryptWithPassphrase(delivery.Plaintext, string(wrapping))
	clearBytes(wrapping)
	if err != nil {
		return WalletItemReceipt{}, errors.New("hostcustody: encrypt Wallet item")
	}
	name := walletRecordName(context.TenantRef, context.DeliveryRef, context.ItemVersion)
	value := walletValue{APIVersion: walletValueAPIVersion, TenantRef: context.TenantRef, ItemRef: context.DeliveryRef, Version: context.ItemVersion, Ciphertext: ciphertext}
	// The value record is written before the index names it; an orphan left
	// by a crash is overwritten by the next delivery of that version.
	if err := store.Write(name, &value, &value.Signature, false); err != nil {
		return WalletItemReceipt{}, err
	}
	entry := walletEntry{TenantRef: context.TenantRef, ItemRef: context.DeliveryRef, Version: context.ItemVersion, Record: name, OperationID: context.OperationID, StoredAt: now.UTC().Format(time.RFC3339Nano)}
	receipt := WalletItemReceipt{TenantRef: context.TenantRef, ItemRef: context.DeliveryRef, Version: context.ItemVersion}
	if rotate {
		index.Items = slices.DeleteFunc(index.Items, func(candidate walletEntry) bool {
			return candidate.TenantRef == context.TenantRef && candidate.ItemRef == context.DeliveryRef
		})
	}
	index.Items = append(index.Items, entry)
	if err := store.Write(walletIndexName, &index, &index.Signature, !existing); err != nil {
		return WalletItemReceipt{}, err
	}
	if rotate {
		for _, old := range held {
			receipt.Removed = append(receipt.Removed, old.Version)
			if err := store.Remove(old.Record); err != nil {
				receipt.CleanupPending = append(receipt.CleanupPending, old.Record)
			}
		}
	}
	return receipt, nil
}

// admitWalletVersion returns the versions of the item already in custody and
// refuses a version that is not newer than all of them, a rotation of an item
// not held, and a new item when custody is full.
func admitWalletVersion(index walletIndex, context hostdelivery.Context, rotate bool) ([]walletEntry, error) {
	var held []walletEntry
	for _, entry := range index.Items {
		if entry.TenantRef != context.TenantRef || entry.ItemRef != context.DeliveryRef {
			continue
		}
		if entry.Version >= context.ItemVersion {
			return nil, fmt.Errorf("hostcustody: Wallet item version %d is not newer than version %d in custody", context.ItemVersion, entry.Version)
		}
		held = append(held, entry)
	}
	switch {
	case rotate && len(held) == 0:
		return nil, errors.New("hostcustody: a Wallet item rotation needs the item in custody; deliver it with wallet-item-import first")
	case len(held) == 0 && len(index.Items) >= maxWalletItems:
		return nil, errors.New("hostcustody: local Wallet item custody is full")
	}
	return held, nil
}

// LoadWalletItem returns the newest version of an item in custody and a
// caller-owned copy of its value. Callers clear the value after use.
func LoadWalletItem(store Store, tenantRef, itemRef string) (uint64, []byte, error) {
	index, _, err := readWalletIndex(store)
	if err != nil {
		return 0, nil, err
	}
	var newest *walletEntry
	for i := range index.Items {
		entry := &index.Items[i]
		if entry.TenantRef == tenantRef && entry.ItemRef == itemRef && (newest == nil || entry.Version > newest.Version) {
			newest = entry
		}
	}
	if newest == nil {
		return 0, nil, ErrMissing
	}
	var value walletValue
	if err := store.Read(newest.Record, maxWalletValueRecord, &value, &value.Signature); err != nil {
		return 0, nil, err
	}
	if value.APIVersion != walletValueAPIVersion || value.TenantRef != tenantRef || value.ItemRef != itemRef || value.Version != newest.Version {
		return 0, nil, errors.New("hostcustody: Wallet item record differs from its index entry")
	}
	wrapping, err := store.WrappingKey()
	if err != nil {
		return 0, nil, err
	}
	plaintext, err := stackcrypto.DecryptWithPassphrase(value.Ciphertext, string(wrapping))
	clearBytes(wrapping)
	if err != nil {
		return 0, nil, errors.New("hostcustody: cannot decrypt Wallet item")
	}
	return value.Version, plaintext, nil
}

// ListWalletItems lists every item by tenant, reference and versions, with its
// rotation mark. It never reads a value.
func ListWalletItems(store Store) ([]WalletItemStatus, error) {
	index, _, err := readWalletIndex(store)
	if err != nil {
		return nil, err
	}
	rotation := map[[2]string]bool{}
	if record, err := readRecipient(store); err == nil {
		state, _, stateErr := readState(store, record.Generation)
		if stateErr != nil {
			return nil, stateErr
		}
		for _, item := range state.Delivered {
			rotation[[2]string{item.TenantRef, item.DeliveryRef}] = item.NeedsRotation
		}
	} else if !errors.Is(err, ErrMissing) {
		return nil, err
	}
	grouped := map[[2]string]*WalletItemStatus{}
	for _, entry := range index.Items {
		key := [2]string{entry.TenantRef, entry.ItemRef}
		status := grouped[key]
		if status == nil {
			status = &WalletItemStatus{TenantRef: entry.TenantRef, ItemRef: entry.ItemRef, NeedsRotation: rotation[key]}
			grouped[key] = status
		}
		status.Versions = append(status.Versions, entry.Version)
	}
	result := make([]WalletItemStatus, 0, len(grouped))
	for _, status := range grouped {
		slices.Sort(status.Versions)
		result = append(result, *status)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TenantRef != result[j].TenantRef {
			return result[i].TenantRef < result[j].TenantRef
		}
		return result[i].ItemRef < result[j].ItemRef
	})
	return result, nil
}
