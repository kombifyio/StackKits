package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	immichPowerToolsWorkloadModuleID    = "stackkits-immich-power-tools-runtime"
	immichPowerToolsWorkloadUnitID      = "immich-power-tools"
	immichPowerToolsWorkloadTemplateRef = "builtin://workloads/immich-power-tools/bundle/v2.json"
	immichPowerToolsWorkloadVersion     = "2.0.0"
	immichPowerToolsWorkloadOutputRef   = "workloads/immich-power-tools/bundle.json"
)

const immichPowerToolsWorkloadRendererSchema = `stackkit.workload-bundle/v2|ImmichPowerToolsWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:immich-power-tools|release:` +
	immichPowerToolsRelease + `|secret-material:references-only|peer:photos/immich-internal`

type ImmichPowerToolsWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func ImmichPowerToolsWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(immichPowerToolsWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: immichPowerToolsWorkloadTemplateRef, Version: immichPowerToolsWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type immichPowerToolsWorkloadBundleRenderer struct{ contract RendererContract }

func newImmichPowerToolsWorkloadBundleRenderer() immichPowerToolsWorkloadBundleRenderer {
	return immichPowerToolsWorkloadBundleRenderer{contract: ImmichPowerToolsWorkloadBundleRendererContract()}
}

func (r immichPowerToolsWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateImmichPowerToolsWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.immichPowerTools-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: immichPowerToolsWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseImmichPowerToolsWorkloadBundle(data []byte) (ImmichPowerToolsWorkloadBundleDescriptor, error) {
	path := "immichPowerToolsWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return ImmichPowerToolsWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed ImmichPowerTools workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "ImmichPowerToolsWorkloadBundle" ||
		bundle.Workload.Ref != "photos-tools" || bundle.Workload.AlternativeRef != "immich-power-tools" ||
		bundle.Workload.ModuleRef != immichPowerToolsWorkloadModuleID || bundle.Workload.Release != immichPowerToolsRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != immichPowerToolsWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return ImmichPowerToolsWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed ImmichPowerTools "+immichPowerToolsRelease+" contract")
	}
	if len(bundle.SecretRefs) != 2 || !(validSecretReference(bundle.SecretRefs["database-password"]) && validSecretReference(bundle.SecretRefs["immich-api-key"])) {
		return ImmichPowerToolsWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires only the declared opaque secret references")
	}
	components, err := validateImmichPowerToolsRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return ImmichPowerToolsWorkloadBundleDescriptor{}, err
	}
	if err := validateImmichPowerToolsServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return ImmichPowerToolsWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, immichPowerToolsWorkloadModuleID, "photos-tools", 3000, path+".deliveryRoute"); err != nil {
			return ImmichPowerToolsWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := ImmichPowerToolsWorkloadBundleDescriptor{
		WorkloadRef: "photos-tools", ModuleRef: immichPowerToolsWorkloadModuleID, Release: immichPowerToolsRelease,
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

func validateImmichPowerToolsWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + immichPowerToolsWorkloadModuleID + ".renderUnits." + immichPowerToolsWorkloadUnitID
	if unit.ModuleID() != immichPowerToolsWorkloadModuleID || unit.ID() != immichPowerToolsWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", immichPowerToolsWorkloadModuleID, immichPowerToolsWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered ImmichPowerTools workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != immichPowerToolsImageRef ||
		!hasDigest || imageDigest != immichPowerToolsImageDigest || !hasEntry || entry != immichPowerToolsWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact ImmichPowerTools "+immichPowerToolsRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "ImmichPowerTools requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, immichPowerToolsWorkloadModuleID, "photos-tools", 3000, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || len(secretRefs) != 2 || !(validSecretReference(secretRefs["database-password"]) && validSecretReference(secretRefs["immich-api-key"])) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires the declared opaque secret references")
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"database-password", "immich-api-key"}) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "ImmichPowerTools bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != immichPowerToolsWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", immichPowerToolsWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, err = validateImmichPowerToolsRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact media endpoint")
	}
	if err := validateImmichPowerToolsServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "ImmichPowerToolsWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "photos-tools", "immich-power-tools"
	bundle.Workload.ModuleRef, bundle.Workload.Release = immichPowerToolsWorkloadModuleID, immichPowerToolsRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateImmichPowerToolsRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 || components[0].ID != "immich-power-tools" || components[0].Lifecycle != "daemon" ||
		components[0].Image.Ref != immichPowerToolsImageRef || components[0].Image.Digest != immichPowerToolsImageDigest ||
		components[0].Health.Kind != "http" || components[0].Health.Path != "/api/health" || components[0].Health.Port != 3000 || !sameEnvironment(components[0].Environment, immichPowerToolsEnvironment()) ||
		!sameEnvironment(components[0].SecretEnvironment, map[string]string{"IMMICH_API_KEY": "immich-api-key", "DB_PASSWORD": "database-password"}) ||
		validatePeerNetworks(immichPowerToolsWorkloadModuleID, components[0], path) != nil {
		return nil, fail(ErrInvalidPlan, path, "ImmichPowerTools runtime graph differs from the closed "+immichPowerToolsRelease+" contract")
	}
	want := map[string]selectedPaaSRuntimeVolume{
		"data": {ID: "data", Target: "/app/data", Class: "persistent", Backup: true},
	}
	if len(components[0].Volumes) != len(want) {
		return nil, fail(ErrInvalidPlan, path+".volumes", "ImmichPowerTools volume set differs from the closed contract")
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
			return nil, fail(ErrInvalidPlan, path+".volumes", "ImmichPowerTools volume %q differs from the closed contract", volume.ID)
		}
	}
	return components, nil
}

func validateImmichPowerToolsServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "photos-tools" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 3000 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "immich-power-tools-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "photos-tools" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "media route authority differs from the governed ImmichPowerTools endpoint")
	}
	return nil
}

func immichPowerToolsEnvironment() map[string]string {
	return map[string]string{"IMMICH_URL": "http://immich-server:2283", "DB_HOST": "immich-postgres", "DB_PORT": "5432", "DB_USERNAME": "immich", "DB_DATABASE_NAME": "immich"}
}
