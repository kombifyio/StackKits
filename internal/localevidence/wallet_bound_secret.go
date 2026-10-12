package localevidence

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
)

// A wallet-bound secret is a secret:// slot whose value is a kombify Wallet
// item delivered to this node (`stackkit wallet-item import`). The local
// custody record holds only the owner-signed binding to the item (tenant and
// item reference), never the value: every resolution reads the newest version
// in the node's encrypted Wallet item custody, so a `wallet-item import` of a
// rotated item takes effect on the next apply without touching the binding.
const (
	localWalletBoundSecretCustodyKind = "LocalWalletBoundSecretCustody"
	maxWalletBindingBytes             = 1024
)

type walletSecretBinding struct {
	TenantRef string `json:"tenantRef"`
	ItemRef   string `json:"itemRef"`
}

// WalletItemResolver returns a caller-owned copy of the newest value of a
// Wallet item in this workspace's host custody.
type WalletItemResolver func(workspaceRoot, tenantRef, itemRef string) ([]byte, error)

var (
	walletResolverMu sync.RWMutex
	walletResolver   WalletItemResolver
)

// RegisterWalletItemResolver installs the host custody reader. The Wallet item
// custody lives in a package that depends on this one, so it registers itself;
// without a resolver every wallet-bound slot fails closed.
func RegisterWalletItemResolver(resolver WalletItemResolver) {
	walletResolverMu.Lock()
	defer walletResolverMu.Unlock()
	walletResolver = resolver
}

// ErrWalletBoundSecretUnavailable reports a bound Wallet item that is not in
// this node's custody, or a resolver that is not installed. It names neither
// the item nor the value.
var ErrWalletBoundSecretUnavailable = errors.New("localevidence: the Wallet item bound to this secret is not in this node's custody; deliver it with stackkit wallet-item import")

// ErrWalletBoundSecretValue reports a delivered value that cannot be handed to
// an application as one text value. It never contains the value.
var ErrWalletBoundSecretValue = errors.New("localevidence: the bound Wallet item value must be 1 to 8192 bytes of printable text")

// BindLocalWalletSecret binds secretRef to a Wallet item, replacing an earlier
// issued or wallet-bound record of that ref. The item must already be in this
// node's custody with a usable value, so a binding never names an absent item.
func BindLocalWalletSecret(workspaceRoot, secretRef, tenantRef, itemRef string) error {
	if tenantRef == "" || itemRef == "" {
		return errors.New("localevidence: a Wallet binding needs a tenant and an item reference")
	}
	binding, err := json.Marshal(walletSecretBinding{TenantRef: tenantRef, ItemRef: itemRef})
	if err != nil || len(binding) > maxWalletBindingBytes {
		return errors.New("localevidence: the Wallet binding is too large")
	}
	value, err := resolveWalletBinding(workspaceRoot, binding)
	if err != nil {
		return err
	}
	clear(value)
	if refDigest, err := localSecretRefDigest(secretRef); err != nil {
		return err
	} else if record, err := loadLocalSecretCustody(workspaceRoot, refDigest); err == nil && record.Kind == localSecretCustodyKind {
		return errors.New("localevidence: a generated secret cannot be replaced by a Wallet binding")
	}
	return storeLocalIssuedSecret(workspaceRoot, secretRef, binding, localWalletBoundSecretCustodyKind)
}

func resolveWalletBinding(workspaceRoot string, raw []byte) ([]byte, error) {
	var binding walletSecretBinding
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&binding); err != nil || binding.TenantRef == "" || binding.ItemRef == "" {
		return nil, errors.New("localevidence: the Wallet binding is malformed")
	}
	walletResolverMu.RLock()
	resolver := walletResolver
	walletResolverMu.RUnlock()
	if resolver == nil {
		return nil, ErrWalletBoundSecretUnavailable
	}
	value, err := resolver(workspaceRoot, binding.TenantRef, binding.ItemRef)
	if err != nil {
		clear(value)
		return nil, ErrWalletBoundSecretUnavailable
	}
	defer clear(value)
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || len(trimmed) > maxIssuedSecretBytes {
		return nil, ErrWalletBoundSecretValue
	}
	for _, c := range trimmed {
		if c < 0x20 || c == 0x7f {
			return nil, ErrWalletBoundSecretValue
		}
	}
	return append([]byte(nil), trimmed...), nil
}

func decodeWalletBindingMaterial(record localSecretCustody) ([]byte, error) {
	return base64.RawStdEncoding.Strict().DecodeString(record.Material)
}
