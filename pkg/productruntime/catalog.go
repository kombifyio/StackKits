// Package productruntime exposes the provider-free StackKits Product Runtime
// construction contract and its high-level prepared-Apply composition.
// StackKits remains the authority for CUE-owned selectors, target intent,
// workspace custody, and authorization; this package contains no transport,
// credential, provider, lease, generation, discovery, or persistence
// implementation.
package productruntime

import (
	"github.com/kombifyio/stackkits/internal/applyevidencev2"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/runtimeapplyv2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// OwnerID is the stable StackKits-owned identity of one static Product Runtime
// owner. Consumers never reconstruct selector fields from this value.
type OwnerID = architecturev2.ProductRuntimeOwnerID

const (
	OwnerSecurityBaseline      = architecturev2.ProductRuntimeOwnerSecurityBaseline
	OwnerCoreHostBootstrap     = architecturev2.ProductRuntimeOwnerCoreHostBootstrap
	OwnerHomeBackupTarget      = architecturev2.ProductRuntimeOwnerHomeBackupTarget
	OwnerBasementCompose       = architecturev2.ProductRuntimeOwnerBasementCompose
	OwnerBasementIdentityTrust = architecturev2.ProductRuntimeOwnerBasementIdentityTrust
	OwnerCloudIdentityTrust    = architecturev2.ProductRuntimeOwnerCloudIdentityTrust
	OwnerCloudHostSecurity     = architecturev2.ProductRuntimeOwnerCloudHostSecurity
	OwnerCloudPublicEdge       = architecturev2.ProductRuntimeOwnerCloudPublicEdge
	OwnerPublicTLS             = architecturev2.ProductRuntimeOwnerPublicTLS
	OwnerHomeDeviceAuthority   = architecturev2.ProductRuntimeOwnerHomeDeviceAuthority
	OwnerHomeAccess            = architecturev2.ProductRuntimeOwnerHomeAccess
	OwnerLocalAutonomy         = architecturev2.ProductRuntimeOwnerLocalAutonomy
	OwnerModernHomeIdentity    = architecturev2.ProductRuntimeOwnerModernHomeIdentity
	OwnerModernCloudIdentity   = architecturev2.ProductRuntimeOwnerModernCloudIdentity
)

// OwnerSelector is the exact provider-free CUE/catalog selector used to match
// one verified RuntimeTarget to its service-owned owner.
type OwnerSelector = architecturev2.ProductRuntimeOwnerSelector

// OwnerDescriptor is a value-only projection of one governed owner selector.
type OwnerDescriptor = architecturev2.ProductRuntimeOwnerDescriptor

// StaticOwnerCatalog returns a fresh projection of every stable static owner.
// Selected-PaaS owners are constructed separately because adapter identity is
// a required service-owned input.
func StaticOwnerCatalog() []OwnerDescriptor {
	return architecturev2.ProductStaticRuntimeOwnerCatalog()
}

// ImmichSelectedPaaSOwner returns the exact governed workload selector for one
// explicitly selected adapter identity. It grants no Operations or transport
// authority and does not register or execute the owner.
func ImmichSelectedPaaSOwner(runtimeAdapterRef, runtimeAdapterModuleRef string) (OwnerDescriptor, error) {
	registration, err := architecturev2.NewProductRemoteImmichSelectedPaaSRegistration(runtimeAdapterRef, runtimeAdapterModuleRef)
	if err != nil {
		return OwnerDescriptor{}, err
	}
	return OwnerDescriptor{ID: OwnerID(registration.Selector.OwnerRef), Selector: registration.Selector}, nil
}

// ExecutionChannelRequest and the related interfaces are aliases of the
// canonical go-common contract. They are repeated here only as the StackKits
// integration entry surface; their ownership remains in runtimeexecutor.
type ExecutionChannelRequest = runtimeexecutor.ExecutionChannelRequest
type ExecutionChannelLocalExecutor = runtimeexecutor.ExecutionChannelLocalExecutor
type ExecutionChannelAdmission = runtimeexecutor.ExecutionChannelAdmission
type ExecutionChannelFactory = runtimeexecutor.ExecutionChannelFactory

// ApplyEvidenceCollector and RecoveryStore are the shared custody SPIs used by
// a service-owned composition. Implementations retain observation/signing and
// persistence behavior; StackKits receives only exact requests and opaque
// canonical bytes.
type ApplyEvidenceCollector = applyevidence.Collector
type ApplyEvidenceCollectionRequest = applyevidence.CollectionRequest
type Journal = runtimeapply.Journal
type RecoveryStore = runtimeapply.RecoveryStore
