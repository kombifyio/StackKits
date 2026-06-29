// =============================================================================
// STACKKIT: basement-kit - Local single-environment homelab (home base)
// =============================================================================
//
// Basement Kit is the LOCAL product profile derived from the shared
// base.#StackBase. Identical 90% core; constrained to the local/pi contexts.
// Cloud Kit (cloud-kit) is the sibling cloud profile over the same #StackBase.
// "base-kit" is retired as a kit — the shared core lives in base/ (ADR-0026).
//
// Installer: https://base.stackkit.cc  (base = home base)
// =============================================================================

package basement_kit

import (
	"github.com/kombifyio/stackkits/base"
)

// #BasementKitStack is the local Basement product profile over #StackBase.
// It adds no fields — only the locality constraint — so the shared core stays
// the single source of truth.
#BasementKitStack: base.#StackBase & {
	context: *"local" | "pi"
}
