// =============================================================================
// STACKKIT: cloud-kit - Cloud single-environment homelab (VPS)
// =============================================================================
//
// Cloud Kit is the CLOUD adaptation of the Basement profile, derived from the
// shared base.#StackBase and constrained to the cloud context (public ingress,
// ACME TLS, public IP). It is NOT Modern Homelab (which is the hybrid
// local+cloud kit). The shared 90% core lives in base/ (ADR-0026).
//
// Installer: https://cloud.stackkit.cc
// =============================================================================

package cloud_kit

import (
	"github.com/kombifyio/stackkits/base"
)

// #CloudKitStack is the cloud product profile over #StackBase.
// Cloud-only extensions (e.g. tunnel / public DNS) are added here as they land;
// today it only pins the cloud context.
#CloudKitStack: base.#StackBase & {
	context: "cloud"
}
