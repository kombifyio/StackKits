package identity

import (
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// The Authority's identity processes split by custody (homelab identity
// target §4, §9; StackKits enpy). The host (Apply, the console and the
// host-side identity signer) holds the Owner custody: the Owner key, the
// step-ca root key, the Pocket ID admin key and the recovery state. The
// Basement core's identity workspace (enrollment listener, Access Gate, Hub)
// holds verification custody only: the device registry and the device issuer
// it serves, the Owner-signed Home trust, and the public Authority record
// (localevidence.HomeAuthorityPublic). Everything the core needs from the
// Owner custody it reads from that record or asks the host-side signer for
// (HomeAuthoritySigner).

// homeAuthority is the Authority's public identity record: derived from the
// Owner custody where this process holds it, read from the host's public copy
// in the core.
func homeAuthority(root string) (localevidence.HomeAuthorityPublic, error) {
	return localevidence.CurrentHomeAuthority(root)
}

// homeIdentityAddress is the identity runtime address of the Authority.
func homeIdentityAddress(root string) (localevidence.IdentityRuntimeAddress, error) {
	authority, err := homeAuthority(root)
	if err != nil {
		return localevidence.IdentityRuntimeAddress{}, err
	}
	return authority.IdentityRuntimeAddress()
}
