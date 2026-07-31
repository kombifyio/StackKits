package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const (
	vaultwardenWorkloadModuleID    = "stackkits-vaultwarden-runtime"
	vaultwardenWorkloadUnitID      = "vaultwarden"
	vaultwardenWorkloadTemplateRef = "builtin://workloads/vaultwarden/bundle/v2.json"
	vaultwardenWorkloadVersion     = "2.0.0"
	vaultwardenWorkloadOutputRef   = "workloads/vaultwarden/bundle.json"
	vaultwardenImageRef            = "ghcr.io/dani-garcia/vaultwarden:1.35.4"
	vaultwardenImageDigest         = "sha256:43498a94b22f9563f2a94b53760ab3e710eefc0d0cac2efda4b12b9eb8690664"
)

const vaultwardenWorkloadRendererSchema = `stackkit.workload-bundle/v2|VaultwardenWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:vaultwarden|release:1.35.4|secret-material:not-included`

// VaultwardenWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the selected-PaaS executor. AdminTokenRef is opaque.
type VaultwardenWorkloadBundleDescriptor struct {
	WorkloadRef   string
	ModuleRef     string
	Release       string
	SiteRef       string
	NodeRef       string
	InstanceRef   string
	AdminTokenRef string
	Components    []SelectedPaaSWorkloadComponentDescriptor
	Route         ApplicationDeliveryRouteDescriptor
}

func VaultwardenWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(vaultwardenWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: vaultwardenWorkloadTemplateRef, Version: vaultwardenWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type vaultwardenWorkloadBundleRenderer struct{ contract RendererContract }

func newVaultwardenWorkloadBundleRenderer() vaultwardenWorkloadBundleRenderer {
	return vaultwardenWorkloadBundleRenderer{contract: VaultwardenWorkloadBundleRendererContract()}
}

func (r vaultwardenWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateVaultwardenWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.vaultwarden-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: vaultwardenWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseVaultwardenWorkloadBundle validates the closed generated artifact before
// any selected-PaaS owner may consume it.
func ParseVaultwardenWorkloadBundle(data []byte) (VaultwardenWorkloadBundleDescriptor, error) {
	path := "vaultwardenWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return VaultwardenWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Vaultwarden workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "VaultwardenWorkloadBundle" ||
		bundle.Workload.Ref != "vault" || bundle.Workload.AlternativeRef != "vaultwarden" ||
		bundle.Workload.ModuleRef != vaultwardenWorkloadModuleID || bundle.Workload.Release != "1.35.4" ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != vaultwardenWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return VaultwardenWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Vaultwarden 1.35.4 contract")
	}
	for field, value := range map[string]string{"siteRef": bundle.Target.SiteRef, "nodeRef": bundle.Target.NodeRef, "instanceRef": bundle.Target.InstanceRef} {
		if err := requireContractID(value, path+".target."+field); err != nil {
			return VaultwardenWorkloadBundleDescriptor{}, err
		}
	}
	if len(bundle.SecretRefs) != 1 || !validSecretReference(bundle.SecretRefs["admin-token"]) {
		return VaultwardenWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires exactly one opaque admin-token reference")
	}
	components, err := validateVaultwardenRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return VaultwardenWorkloadBundleDescriptor{}, err
	}
	if err := validateVaultwardenServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return VaultwardenWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, vaultwardenWorkloadModuleID, "vault", 80, path+".deliveryRoute"); err != nil {
			return VaultwardenWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := VaultwardenWorkloadBundleDescriptor{
		WorkloadRef: "vault", ModuleRef: vaultwardenWorkloadModuleID, Release: "1.35.4",
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		AdminTokenRef: bundle.SecretRefs["admin-token"],
		Components:    make([]SelectedPaaSWorkloadComponentDescriptor, len(components)),
	}
	if bundle.DeliveryRoute != nil {
		descriptor.Route = bundle.DeliveryRoute.descriptor()
	}
	for index, component := range components {
		descriptor.Components[index] = SelectedPaaSWorkloadComponentDescriptor{
			ID: component.ID, Lifecycle: component.Lifecycle,
			ImageRef: component.Image.Ref, ImageDigest: component.Image.Digest,
		}
	}
	return descriptor, nil
}

//nolint:gocyclo // Keep the complete Vaultwarden authority check at one boundary.
func validateVaultwardenWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + vaultwardenWorkloadModuleID + ".renderUnits." + vaultwardenWorkloadUnitID
	if unit.ModuleID() != vaultwardenWorkloadModuleID || unit.ID() != vaultwardenWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", vaultwardenWorkloadModuleID, vaultwardenWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Vaultwarden workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != vaultwardenImageRef ||
		!hasDigest || imageDigest != vaultwardenImageDigest || !hasEntry || entry != vaultwardenWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Vaultwarden 1.35.4 contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Vaultwarden requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, vaultwardenWorkloadModuleID, "vault", 80, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if !exactStringList(unit.SecretInputRefs(), []string{"admin-token"}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Vaultwarden requires only its exact opaque admin-token slot")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil ||
		len(secretRefs) != 1 || !validSecretReference(secretRefs["admin-token"]) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires one opaque admin-token reference and no secret material")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".interfaces", "selected-PaaS bundle receives no host, socket, or runtime-network authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != vaultwardenWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", vaultwardenWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, err = validateVaultwardenRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact vault endpoint")
	}
	if err := validateVaultwardenServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "VaultwardenWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "vault", "vaultwarden"
	bundle.Workload.ModuleRef, bundle.Workload.Release = vaultwardenWorkloadModuleID, "1.35.4"
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateVaultwardenRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	expected := []selectedPaaSRuntimeComponent{{
		ID: "vaultwarden", Role: "application", Lifecycle: "daemon",
		Image:     selectedPaaSRuntimeImage{Ref: vaultwardenImageRef, Digest: vaultwardenImageDigest},
		DependsOn: []string{}, NetworkRefs: []string{"vaultwarden-internal"},
		Environment:       map[string]string{"SIGNUPS_ALLOWED": "false"},
		SecretEnvironment: map[string]string{"ADMIN_TOKEN": "admin-token"},
		Volumes:           []selectedPaaSRuntimeVolume{{ID: "data", Target: "/data", Class: "persistent", Backup: true}},
		Health:            selectedPaaSRuntimeHealth{Kind: "http", Path: "/alive", Port: 80},
	}}
	if !reflect.DeepEqual(components, expected) {
		return nil, fail(ErrInvalidPlan, path, "component graph differs from the exact Vaultwarden 1.35.4 workload contract")
	}
	return components, nil
}

func validateVaultwardenServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "vault" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 80 ||
		endpoint.RequiredPrivilege != "vault" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "vaultwarden-http" || endpoint.Data.BindingRef != "vault" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"secret"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"http", "https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "vault route authority differs from the governed Vaultwarden endpoint")
	}
	return nil
}
