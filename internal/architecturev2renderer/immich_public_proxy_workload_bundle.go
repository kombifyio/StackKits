package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	immichPublicProxyWorkloadModuleID    = "stackkits-immich-public-proxy-runtime"
	immichPublicProxyWorkloadUnitID      = "immich-public-proxy"
	immichPublicProxyWorkloadTemplateRef = "builtin://workloads/immich-public-proxy/bundle/v2.json"
	immichPublicProxyWorkloadVersion     = "2.0.0"
	immichPublicProxyWorkloadOutputRef   = "workloads/immich-public-proxy/bundle.json"
)

const immichPublicProxyWorkloadRendererSchema = `stackkit.workload-bundle/v2|ImmichPublicProxyWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:immich-public-proxy|release:` +
	immichPublicProxyRelease + `|secret-material:references-only|peer:photos/immich-internal`

type ImmichPublicProxyWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func ImmichPublicProxyWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(immichPublicProxyWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: immichPublicProxyWorkloadTemplateRef, Version: immichPublicProxyWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type immichPublicProxyWorkloadBundleRenderer struct{ contract RendererContract }

func newImmichPublicProxyWorkloadBundleRenderer() immichPublicProxyWorkloadBundleRenderer {
	return immichPublicProxyWorkloadBundleRenderer{contract: ImmichPublicProxyWorkloadBundleRendererContract()}
}

func (r immichPublicProxyWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateImmichPublicProxyWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.immichPublicProxy-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: immichPublicProxyWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseImmichPublicProxyWorkloadBundle(data []byte) (ImmichPublicProxyWorkloadBundleDescriptor, error) {
	path := "immichPublicProxyWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return ImmichPublicProxyWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed ImmichPublicProxy workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "ImmichPublicProxyWorkloadBundle" ||
		bundle.Workload.Ref != "photos-share" || bundle.Workload.AlternativeRef != "immich-public-proxy" ||
		bundle.Workload.ModuleRef != immichPublicProxyWorkloadModuleID || bundle.Workload.Release != immichPublicProxyRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != immichPublicProxyWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return ImmichPublicProxyWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed ImmichPublicProxy "+immichPublicProxyRelease+" contract")
	}
	if len(bundle.SecretRefs) != 0 {
		return ImmichPublicProxyWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "ImmichPublicProxy single-container contract accepts no secret material")
	}
	components, err := validateImmichPublicProxyRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return ImmichPublicProxyWorkloadBundleDescriptor{}, err
	}
	if err := validateImmichPublicProxyServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return ImmichPublicProxyWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, immichPublicProxyWorkloadModuleID, "photos-share", 3000, path+".deliveryRoute"); err != nil {
			return ImmichPublicProxyWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := ImmichPublicProxyWorkloadBundleDescriptor{
		WorkloadRef: "photos-share", ModuleRef: immichPublicProxyWorkloadModuleID, Release: immichPublicProxyRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Components: make([]SelectedPaaSWorkloadComponentDescriptor, len(components)),
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

func validateImmichPublicProxyWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + immichPublicProxyWorkloadModuleID + ".renderUnits." + immichPublicProxyWorkloadUnitID
	if unit.ModuleID() != immichPublicProxyWorkloadModuleID || unit.ID() != immichPublicProxyWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", immichPublicProxyWorkloadModuleID, immichPublicProxyWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered ImmichPublicProxy workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != immichPublicProxyImageRef ||
		!hasDigest || imageDigest != immichPublicProxyImageDigest || !hasEntry || entry != immichPublicProxyWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact ImmichPublicProxy "+immichPublicProxyRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "ImmichPublicProxy requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, immichPublicProxyWorkloadModuleID, "photos-share", 3000, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "ImmichPublicProxy bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != immichPublicProxyWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", immichPublicProxyWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, err = validateImmichPublicProxyRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact media endpoint")
	}
	if err := validateImmichPublicProxyServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "ImmichPublicProxyWorkloadBundle",
		SecretRefs: map[string]string{}, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "photos-share", "immich-public-proxy"
	bundle.Workload.ModuleRef, bundle.Workload.Release = immichPublicProxyWorkloadModuleID, immichPublicProxyRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateImmichPublicProxyRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 || components[0].ID != "immich-public-proxy" || components[0].Lifecycle != "daemon" ||
		components[0].Image.Ref != immichPublicProxyImageRef || components[0].Image.Digest != immichPublicProxyImageDigest ||
		components[0].Health.Kind != "http" || components[0].Health.Path != "/share/healthcheck" || components[0].Health.Port != 3000 || !sameEnvironment(components[0].Environment, immichPublicProxyEnvironment()) ||
		!sameEnvironment(components[0].SecretEnvironment, map[string]string{}) ||
		validatePeerNetworks(immichPublicProxyWorkloadModuleID, components[0], path) != nil {
		return nil, fail(ErrInvalidPlan, path, "ImmichPublicProxy runtime graph differs from the closed "+immichPublicProxyRelease+" contract")
	}
	want := map[string]selectedPaaSRuntimeVolume{}
	if len(components[0].Volumes) != len(want) {
		return nil, fail(ErrInvalidPlan, path+".volumes", "ImmichPublicProxy volume set differs from the closed contract")
	}
	for _, volume := range components[0].Volumes {
		expected, ok := want[volume.ID]
		if volume.ID == "library" && volume.HostPath != "" {
			if !safeCoreHostBootstrapStoragePath(volume.HostPath) {
				return nil, fail(ErrInvalidPlan, path+".volumes", "media source must be a clean path beneath a governed storage root")
			}
			expected.HostPath = volume.HostPath
		}
		if !ok || volume != expected {
			return nil, fail(ErrInvalidPlan, path+".volumes", "ImmichPublicProxy volume %q differs from the closed contract", volume.ID)
		}
	}
	return components, nil
}

func validateImmichPublicProxyServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "photos-share" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 3000 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "immich-public-proxy-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "" ||
		endpoint.Data.Locality != "" || len(endpoint.Data.RequiredClasses) != 0 ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "media route authority differs from the governed ImmichPublicProxy endpoint")
	}
	return nil
}

func immichPublicProxyEnvironment() map[string]string {
	return map[string]string{"IMMICH_URL": "http://immich-server:2283"}
}
