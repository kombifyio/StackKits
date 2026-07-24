package architecturev2

import (
	"fmt"
	"strings"
)

// ProductRuntimeOwnerID is a stable service-construction identifier for one
// static CUE/catalog-owned Runtime Owner selector. Its value is the exact
// selector OwnerRef; callers never reconstruct the remaining selector fields.
type ProductRuntimeOwnerID string

const (
	ProductRuntimeOwnerSecurityBaseline      ProductRuntimeOwnerID = "security-baseline"
	ProductRuntimeOwnerCoreHostBootstrap     ProductRuntimeOwnerID = "stackkits-core-host-bootstrap"
	ProductRuntimeOwnerHomeBackupTarget      ProductRuntimeOwnerID = "stackkits-home-backup-target"
	ProductRuntimeOwnerBasementCompose       ProductRuntimeOwnerID = "socket-proxy"
	ProductRuntimeOwnerBasementIdentityTrust ProductRuntimeOwnerID = "stackkits-basement-identity-trust-policy-manifest"
	ProductRuntimeOwnerCloudIdentityTrust    ProductRuntimeOwnerID = "stackkits-cloud-identity-trust-policy-manifest"
	ProductRuntimeOwnerCloudHostSecurity     ProductRuntimeOwnerID = "stackkits-cloud-host-security-runtime"
	ProductRuntimeOwnerCloudPublicEdge       ProductRuntimeOwnerID = "stackkits-cloud-public-edge-runtime"
	ProductRuntimeOwnerCloudOffsiteBackup    ProductRuntimeOwnerID = "stackkits-cloud-offsite-backup-runtime"
	ProductRuntimeOwnerPublicTLS             ProductRuntimeOwnerID = "stackkits-public-tls-contract"
	ProductRuntimeOwnerHomeDeviceAuthority   ProductRuntimeOwnerID = "stackkits-home-device-authority-policy-manifest"
	ProductRuntimeOwnerHomeAccess            ProductRuntimeOwnerID = "stackkits-home-access-policy-manifest"
	ProductRuntimeOwnerLocalAutonomy         ProductRuntimeOwnerID = "stackkits-local-autonomy-policy-manifest"
	ProductRuntimeOwnerModernHomeIdentity    ProductRuntimeOwnerID = "stackkits-modern-home-identity-trust-policy-manifest"
	ProductRuntimeOwnerModernCloudIdentity   ProductRuntimeOwnerID = "stackkits-modern-cloud-identity-verifier-policy-manifest"
	ProductRuntimeOwnerFederationLink        ProductRuntimeOwnerID = "stackkits-federation-link-runtime"
	ProductRuntimeOwnerBridgePublication     ProductRuntimeOwnerID = "stackkits-bridge-publication-runtime"
	ProductRuntimeOwnerBridgeOriginMTLS      ProductRuntimeOwnerID = "stackkits-bridge-origin-mtls-runtime"
)

// ProductRuntimeOwnerDescriptor exposes one immutable value projection of a
// static StackKits selector. It contains no target, channel, endpoint,
// credential, provider resource, lease, generation, or Operations authority.
type ProductRuntimeOwnerDescriptor struct {
	ID       ProductRuntimeOwnerID
	Selector ProductRuntimeOwnerSelector
}

// ProductStaticRuntimeOwnerCatalog returns a fresh value-only projection of
// every static Product factory selector. Selected-PaaS workload selectors are
// deliberately excluded because their exact adapter identity is a required
// service-construction input.
func ProductStaticRuntimeOwnerCatalog() []ProductRuntimeOwnerDescriptor {
	return []ProductRuntimeOwnerDescriptor{
		{ID: ProductRuntimeOwnerSecurityBaseline, Selector: productSecurityBaselineSelector()},
		{ID: ProductRuntimeOwnerCoreHostBootstrap, Selector: productCoreHostBootstrapSelector()},
		{ID: ProductRuntimeOwnerHomeBackupTarget, Selector: productHomeBackupTargetSelector()},
		{ID: ProductRuntimeOwnerBasementCompose, Selector: productBasementComposeSelector()},
		{ID: ProductRuntimeOwnerBasementIdentityTrust, Selector: productBasementIdentityTrustSelector()},
		{ID: ProductRuntimeOwnerCloudIdentityTrust, Selector: productCloudIdentityTrustSelector()},
		{ID: ProductRuntimeOwnerCloudHostSecurity, Selector: productCloudHostSecuritySelector()},
		{ID: ProductRuntimeOwnerCloudPublicEdge, Selector: productCloudPublicEdgeSelector()},
		{ID: ProductRuntimeOwnerCloudOffsiteBackup, Selector: productCloudOffsiteBackupSelector()},
		{ID: ProductRuntimeOwnerPublicTLS, Selector: productPublicTLSSelector()},
		{ID: ProductRuntimeOwnerHomeDeviceAuthority, Selector: productHomeDeviceAuthoritySelector()},
		{ID: ProductRuntimeOwnerHomeAccess, Selector: productHomeAccessSelector()},
		{ID: ProductRuntimeOwnerLocalAutonomy, Selector: productLocalAutonomySelector()},
		{ID: ProductRuntimeOwnerModernHomeIdentity, Selector: productModernHomeIdentitySelector()},
		{ID: ProductRuntimeOwnerModernCloudIdentity, Selector: productModernCloudIdentitySelector()},
		{ID: ProductRuntimeOwnerFederationLink, Selector: productFederationLinkSelector()},
		{ID: ProductRuntimeOwnerBridgePublication, Selector: productBridgePublicationSelector()},
		{ID: ProductRuntimeOwnerBridgeOriginMTLS, Selector: productBridgeOriginMTLSSelector()},
	}
}

// NewProductRemoteStaticRuntimeOwnerRegistrations resolves a service-owned
// allowlist of stable IDs into exact remote-only registrations. Input order is
// preserved for deterministic construction diagnostics; unknown or duplicate
// IDs fail before any registry or channel admission exists.
func NewProductRemoteStaticRuntimeOwnerRegistrations(ids ...ProductRuntimeOwnerID) ([]ProductRuntimeOwnerRegistration, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("remote Product runtime-owner registration requires at least one static owner ID")
	}
	catalog := ProductStaticRuntimeOwnerCatalog()
	byID := make(map[ProductRuntimeOwnerID]ProductRuntimeOwnerSelector, len(catalog))
	for _, descriptor := range catalog {
		byID[descriptor.ID] = descriptor.Selector
	}
	seen := make(map[ProductRuntimeOwnerID]struct{}, len(ids))
	registrations := make([]ProductRuntimeOwnerRegistration, 0, len(ids))
	for index, id := range ids {
		if id == "" || string(id) != strings.TrimSpace(string(id)) {
			return nil, fmt.Errorf("remote Product runtime-owner ID %d is empty or not normalized", index)
		}
		selector, exists := byID[id]
		if !exists {
			return nil, fmt.Errorf("remote Product runtime-owner ID %q is not in the static StackKits catalog", id)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("remote Product runtime-owner ID %q is selected more than once", id)
		}
		registration, err := NewProductRemoteRuntimeOwnerRegistration(selector)
		if err != nil {
			return nil, fmt.Errorf("construct remote Product runtime-owner %q: %w", id, err)
		}
		seen[id] = struct{}{}
		registrations = append(registrations, registration)
	}
	return registrations, nil
}

// NewProductRemoteImmichSelectedPaaSRegistration binds the governed Immich
// selector to one exact service-owned PaaS adapter identity without requiring
// that service to possess a local PaaS Operations implementation.
func NewProductRemoteImmichSelectedPaaSRegistration(runtimeAdapterRef, runtimeAdapterModuleRef string) (ProductRuntimeOwnerRegistration, error) {
	if runtimeAdapterRef == "" || runtimeAdapterRef != strings.TrimSpace(runtimeAdapterRef) ||
		runtimeAdapterModuleRef == "" || runtimeAdapterModuleRef != strings.TrimSpace(runtimeAdapterModuleRef) {
		return ProductRuntimeOwnerRegistration{}, fmt.Errorf("remote Immich selected-PaaS registration requires an exact normalized adapter ref and module ref")
	}
	return NewProductRemoteRuntimeOwnerRegistration(productImmichSelectedPaaSSelector(runtimeAdapterRef, runtimeAdapterModuleRef))
}
