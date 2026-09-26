package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const (
	navidromeWorkloadModuleID    = "stackkits-navidrome-runtime"
	navidromeWorkloadUnitID      = "navidrome"
	navidromeWorkloadTemplateRef = "builtin://workloads/navidrome/bundle/v2.json"
	navidromeWorkloadVersion     = "2.0.0"
	navidromeWorkloadOutputRef   = "workloads/navidrome/bundle.json"
)

const navidromeWorkloadRendererSchema = `stackkit.workload-bundle/v2|NavidromeWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:navidrome|release:` +
	navidromeRelease + `|secret-material:not-included|library-backup:owner-custodied|library-mount:read-only|source:storage.hostRoots.mediaRoot`

type NavidromeWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func NavidromeWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(navidromeWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: navidromeWorkloadTemplateRef, Version: navidromeWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type navidromeWorkloadBundleRenderer struct{ contract RendererContract }

func newNavidromeWorkloadBundleRenderer() navidromeWorkloadBundleRenderer {
	return navidromeWorkloadBundleRenderer{contract: NavidromeWorkloadBundleRendererContract()}
}

func (r navidromeWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateNavidromeWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.navidrome-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: navidromeWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseNavidromeWorkloadBundle(data []byte) (NavidromeWorkloadBundleDescriptor, error) {
	path := "navidromeWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return NavidromeWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Navidrome workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "NavidromeWorkloadBundle" ||
		bundle.Workload.Ref != "media-music" || bundle.Workload.AlternativeRef != "navidrome" ||
		bundle.Workload.ModuleRef != navidromeWorkloadModuleID || bundle.Workload.Release != navidromeRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != navidromeWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return NavidromeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Navidrome "+navidromeRelease+" contract")
	}
	if len(bundle.SecretRefs) != 0 {
		return NavidromeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "Navidrome single-container contract accepts no secret material")
	}
	components, err := validateNavidromeRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return NavidromeWorkloadBundleDescriptor{}, err
	}
	if err := validateNavidromeServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return NavidromeWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, navidromeWorkloadModuleID, "media-music", 4533, path+".deliveryRoute"); err != nil {
			return NavidromeWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := NavidromeWorkloadBundleDescriptor{
		WorkloadRef: "media-music", ModuleRef: navidromeWorkloadModuleID, Release: navidromeRelease,
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

func validateNavidromeWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + navidromeWorkloadModuleID + ".renderUnits." + navidromeWorkloadUnitID
	if unit.ModuleID() != navidromeWorkloadModuleID || unit.ID() != navidromeWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", navidromeWorkloadModuleID, navidromeWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Navidrome workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != navidromeImageRef ||
		!hasDigest || imageDigest != navidromeImageDigest || !hasEntry || entry != navidromeWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Navidrome "+navidromeRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Navidrome requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, mediaRoot, err := validateNavidromeDeliveryInputs(unit, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Navidrome bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != navidromeWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", navidromeWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, err = validateNavidromeRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	for index := range components[0].Volumes {
		if components[0].Volumes[index].ID == "library" {
			components[0].Volumes[index].HostPath = mediaRoot
		}
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact media endpoint")
	}
	if err := validateNavidromeServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "NavidromeWorkloadBundle",
		SecretRefs: map[string]string{}, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "media-music", "navidrome"
	bundle.Workload.ModuleRef, bundle.Workload.Release = navidromeWorkloadModuleID, navidromeRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateNavidromeRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 || components[0].ID != "navidrome" || components[0].Lifecycle != "daemon" ||
		components[0].Image.Ref != navidromeImageRef || components[0].Image.Digest != navidromeImageDigest ||
		components[0].Health.Kind != "http" || components[0].Health.Path != "/ping" || components[0].Health.Port != 4533 || !sameEnvironment(components[0].Environment, navidromeEnvironment()) {
		return nil, fail(ErrInvalidPlan, path, "Navidrome runtime graph differs from the closed "+navidromeRelease+" contract")
	}
	want := map[string]selectedPaaSRuntimeVolume{
		"data":    {ID: "data", Target: "/data", Class: "persistent", Backup: true},
		"library": {ID: "library", Target: "/music", Class: "persistent", Backup: false, ReadOnly: true},
	}
	if len(components[0].Volumes) != len(want) {
		return nil, fail(ErrInvalidPlan, path+".volumes", "Navidrome volume set differs from the closed contract")
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
			return nil, fail(ErrInvalidPlan, path+".volumes", "Navidrome volume %q differs from the closed contract", volume.ID)
		}
	}
	return components, nil
}

func validateNavidromeDeliveryInputs(unit RenderUnit, path string) (*applicationDeliveryRoute, string, error) {
	if !exactStringList(unit.PublicInputRefs(), []string{"delivery-route", "storage-roots"}) || len(unit.PlanInputRefs()) != 0 || !emptyJSONObject(unit.PlanInputsJSON()) {
		return nil, "", fail(ErrInvalidPlan, path, "requires the exact delivery route and host storage bindings")
	}
	var bindings []rawModuleRenderInputBinding
	expected := []rawModuleRenderInputBinding{
		{TargetRef: "delivery-route", SourceRef: "network.moduleRoute", ValueType: "authority-bound-module-route-v1", Cardinality: "single", DefaultValue: json.RawMessage("null")},
		{TargetRef: "storage-roots", SourceRef: "storage.hostRoots", ValueType: "host-storage-roots-v1", Cardinality: "single", Required: true},
	}
	if err := decodeStrict(unit.InputBindingsJSON(), &bindings); err != nil || !reflect.DeepEqual(bindings, expected) {
		return nil, "", fail(ErrInvalidPlan, path, "media bindings differ from the compiler-owned contract")
	}
	var values struct {
		Route   *applicationDeliveryRoute `json:"delivery-route"`
		Storage coreHostStorageRootsInput `json:"storage-roots"`
	}
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return nil, "", wrap(ErrInvalidPlan, path, "decode media delivery inputs", err)
	}
	storage, err := decodeCoreHostStorageRootsJSON(values.Storage, path+".storage-roots")
	if err != nil {
		return nil, "", err
	}
	if values.Route != nil {
		if err := validateParsedApplicationDeliveryRoute(*values.Route, navidromeWorkloadModuleID, "media-music", 4533, path+".delivery-route"); err != nil {
			return nil, "", err
		}
	}
	return values.Route, storage.MediaRoot, nil
}

func validateNavidromeServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "media-music" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 4533 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "navidrome-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "media-music" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"http", "https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "media route authority differs from the governed Navidrome endpoint")
	}
	return nil
}

func navidromeEnvironment() map[string]string {
	return map[string]string{"ND_DATAFOLDER": "/data", "ND_MUSICFOLDER": "/music"}
}
