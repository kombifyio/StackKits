package workloadrelocation

import (
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/hostcustody"
)

// LoadTransferSecret reads the move's transfer secret from this node's Wallet
// item custody. Techstack delivers the same item, sealed, to the source and
// the destination (`stackkit wallet-item import`); it never arrives any other
// way. The caller clears the returned value after deriving from it.
func LoadTransferSecret(workspace, tenantRef, itemRef string) ([]byte, error) {
	if tenantRef == "" || itemRef == "" {
		return nil, errors.New("--tenant-ref and --secret-ref name the Wallet item that holds this move's transfer secret")
	}
	_, value, err := hostcustody.LoadWalletItem(backupcustody.HostStore(workspace), tenantRef, itemRef)
	if errors.Is(err, hostcustody.ErrMissing) {
		return nil, fmt.Errorf("Wallet item %q is not in this server's custody; deliver it with `stackkit wallet-item import` first", itemRef)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read Wallet item %q: %w", itemRef, err)
	}
	return value, nil
}

// Clear overwrites a secret value in place.
func Clear(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
