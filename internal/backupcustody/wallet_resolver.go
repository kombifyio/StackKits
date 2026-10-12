package backupcustody

import (
	"github.com/kombifyio/stackkits/internal/hostcustody"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// Wallet-bound secret:// slots (localevidence.BindLocalWalletSecret) resolve
// from the node's Wallet item custody, which lives in HostStore.
func init() {
	localevidence.RegisterWalletItemResolver(func(workspaceRoot, tenantRef, itemRef string) ([]byte, error) {
		_, value, err := hostcustody.LoadWalletItem(HostStore(workspaceRoot), tenantRef, itemRef)
		return value, err
	})
}
