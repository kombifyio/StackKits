//go:build !linux

package lifecyclemutation

import "errors"

// requireLegacyClaimantGone has no complete process table to read outside
// Linux, so an older child's claim stays refused.
func requireLegacyClaimantGone(string) error {
	return errors.New("an older StackKits child claimed this phase; only Linux can prove it ended")
}
