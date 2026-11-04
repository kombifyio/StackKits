package commands

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/advancedtrust"
)

// advancedManagedIntent reports whether this workspace's desired intent is
// Advanced: its StackSpec says install.mode advanced, or Techstack manages it.
// Every managed rollout imports an Owner-signed Advanced trust bundle before
// its first generate and never writes install.mode, so the imported record is
// the marker. Without it, a removal retired the managed selection as a
// Standard one, and the next removal of the same destroy refused with
// binding_mismatch at resolvedPlan (kombify-StackKits-uoe2). A record that
// exists but does not verify fails closed instead of selecting Standard intent
// changes.
func advancedManagedIntent(workspace, installMode string) (bool, error) {
	if installMode == "advanced" {
		return true, nil
	}
	present, err := advancedtrust.Present(workspace)
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	if _, err := advancedtrust.Load(workspace); err != nil {
		return false, fmt.Errorf("this workspace holds an Advanced trust record that does not verify, so it is managed and its intent is not changed as a Standard installation: %w", err)
	}
	return true, nil
}
