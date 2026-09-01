package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	jellyfinWorkloadModuleID    = "stackkits-jellyfin-runtime"
	jellyfinWorkloadUnitID      = "jellyfin"
	jellyfinWorkloadTemplateRef = "builtin://workloads/jellyfin/bundle/v2.json"
	jellyfinWorkloadVersion     = "2.0.0"
	jellyfinWorkloadOutputRef   = "workloads/jellyfin/bundle.json"
	jellyfinImageRef            = "docker.io/jellyfin/jellyfin:10.10.7"
	jellyfinImageDigest         = "sha256:7ae36aab93ef9b6aaff02b37f8bb23df84bb2d7a3f6054ec8fc466072a648ce2"
)

const jellyfinWorkloadRendererSchema = `stackkit.workload-bundle/v2|JellyfinWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:jellyfin|release:10.10.7|secret-material:not-included|library-backup:owner-custodied`

type JellyfinWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func JellyfinWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(jellyfinWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: jellyfinWorkloadTemplateRef, Version: jellyfinWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type jellyfinWorkloadBundleRenderer struct{ contract RendererContract }

func newJellyfinWorkloadBundleRenderer() jellyfinWorkloadBundleRenderer {
	return jellyfinWorkloadBundleRenderer{contract: JellyfinWorkloadBundleRendererContract()}
}

func (r jellyfinWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateJellyfinWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.jellyfin-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: jellyfinWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseJellyfinWorkloadBundle(data []byte) (JellyfinWorkloadBundleDescriptor, error) {
	path := "jellyfinWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return JellyfinWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Jellyfin workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "JellyfinWorkloadBundle" ||
		bundle.Workload.Ref != "media" || bundle.Workload.AlternativeRef != "jellyfin" ||
		bundle.Workload.ModuleRef != jellyfinWorkloadModuleID || bundle.Workload.Release != "10.10.7" ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != jellyfinWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return JellyfinWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Jellyfin 10.10.7 contract")
	}
	if len(bundle.SecretRefs) != 0 {
		return JellyfinWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "Jellyfin single-container contract accepts no secret material")
	}
	components, err := validateJellyfinRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return JellyfinWorkloadBundleDescriptor{}, err
	}
	if err := validateJellyfinServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return JellyfinWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, jellyfinWorkloadModuleID, "media", 8096, path+".deliveryRoute"); err != nil {
			return JellyfinWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := JellyfinWorkloadBundleDescriptor{
		WorkloadRef: "media", ModuleRef: jellyfinWorkloadModuleID, Release: "10.10.7",
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

func validateJellyfinWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + jellyfinWorkloadModuleID + ".renderUnits." + jellyfinWorkloadUnitID
	if unit.ModuleID() != jellyfinWorkloadModuleID || unit.ID() != jellyfinWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", jellyfinWorkloadModuleID, jellyfinWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Jellyfin workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != jellyfinImageRef ||
		!hasDigest || imageDigest != jellyfinImageDigest || !hasEntry || entry != jellyfinWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Jellyfin 10.10.7 contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Jellyfin requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, jellyfinWorkloadModuleID, "media", 8096, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Jellyfin bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != jellyfinWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", jellyfinWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, err = validateJellyfinRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact media endpoint")
	}
	if err := validateJellyfinServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "JellyfinWorkloadBundle",
		SecretRefs: map[string]string{}, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "media", "jellyfin"
	bundle.Workload.ModuleRef, bundle.Workload.Release = jellyfinWorkloadModuleID, "10.10.7"
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateJellyfinRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 || components[0].ID != "jellyfin" || components[0].Lifecycle != "daemon" ||
		components[0].Image.Ref != jellyfinImageRef || components[0].Image.Digest != jellyfinImageDigest ||
		components[0].Health.Kind != "http" || components[0].Health.Path != "/health" || components[0].Health.Port != 8096 {
		return nil, fail(ErrInvalidPlan, path, "Jellyfin runtime graph differs from the closed 10.10.7 contract")
	}
	want := map[string]selectedPaaSRuntimeVolume{
		"config":  {ID: "config", Target: "/config", Class: "persistent", Backup: true},
		"cache":   {ID: "cache", Target: "/cache", Class: "cache", Backup: false},
		"library": {ID: "library", Target: "/media", Class: "persistent", Backup: false},
	}
	if len(components[0].Volumes) != len(want) {
		return nil, fail(ErrInvalidPlan, path+".volumes", "Jellyfin requires config, cache, and owner-custodied library volumes")
	}
	for _, volume := range components[0].Volumes {
		expected, ok := want[volume.ID]
		if !ok || volume != expected {
			return nil, fail(ErrInvalidPlan, path+".volumes", "Jellyfin volume %q differs from the closed contract", volume.ID)
		}
	}
	return components, nil
}

func validateJellyfinServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "media" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 8096 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "jellyfin-http" || endpoint.Data.BindingRef != "media" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"http", "https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "media route authority differs from the governed Jellyfin endpoint")
	}
	return nil
}
