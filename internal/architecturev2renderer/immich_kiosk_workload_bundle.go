package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	immichKioskWorkloadModuleID    = "stackkits-immich-kiosk-runtime"
	immichKioskWorkloadUnitID      = "immich-kiosk"
	immichKioskWorkloadTemplateRef = "builtin://workloads/immich-kiosk/bundle/v2.json"
	immichKioskWorkloadVersion     = "2.0.0"
	immichKioskWorkloadOutputRef   = "workloads/immich-kiosk/bundle.json"
)

const immichKioskWorkloadRendererSchema = `stackkit.workload-bundle/v2|ImmichKioskWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:immich-kiosk|release:` +
	immichKioskRelease + `|secret-material:references-only|peer:photos/immich-internal`

type ImmichKioskWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func ImmichKioskWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(immichKioskWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: immichKioskWorkloadTemplateRef, Version: immichKioskWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type immichKioskWorkloadBundleRenderer struct{ contract RendererContract }

func newImmichKioskWorkloadBundleRenderer() immichKioskWorkloadBundleRenderer {
	return immichKioskWorkloadBundleRenderer{contract: ImmichKioskWorkloadBundleRendererContract()}
}

func (r immichKioskWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateImmichKioskWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.immichKiosk-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: immichKioskWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseImmichKioskWorkloadBundle(data []byte) (ImmichKioskWorkloadBundleDescriptor, error) {
	path := "immichKioskWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return ImmichKioskWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed ImmichKiosk workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "ImmichKioskWorkloadBundle" ||
		bundle.Workload.Ref != "photos-kiosk" || bundle.Workload.AlternativeRef != "immich-kiosk" ||
		bundle.Workload.ModuleRef != immichKioskWorkloadModuleID || bundle.Workload.Release != immichKioskRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != immichKioskWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return ImmichKioskWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed ImmichKiosk "+immichKioskRelease+" contract")
	}
	if len(bundle.SecretRefs) != 1 || !(validSecretReference(bundle.SecretRefs["immich-api-key"])) {
		return ImmichKioskWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires only the declared opaque secret references")
	}
	components, err := validateImmichKioskRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return ImmichKioskWorkloadBundleDescriptor{}, err
	}
	if err := validateImmichKioskServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return ImmichKioskWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, immichKioskWorkloadModuleID, "photos-kiosk", 3000, path+".deliveryRoute"); err != nil {
			return ImmichKioskWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := ImmichKioskWorkloadBundleDescriptor{
		WorkloadRef: "photos-kiosk", ModuleRef: immichKioskWorkloadModuleID, Release: immichKioskRelease,
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

func validateImmichKioskWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + immichKioskWorkloadModuleID + ".renderUnits." + immichKioskWorkloadUnitID
	if unit.ModuleID() != immichKioskWorkloadModuleID || unit.ID() != immichKioskWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", immichKioskWorkloadModuleID, immichKioskWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered ImmichKiosk workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != immichKioskImageRef ||
		!hasDigest || imageDigest != immichKioskImageDigest || !hasEntry || entry != immichKioskWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact ImmichKiosk "+immichKioskRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "ImmichKiosk requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, immichKioskWorkloadModuleID, "photos-kiosk", 3000, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || len(secretRefs) != 1 || !(validSecretReference(secretRefs["immich-api-key"])) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires the declared opaque secret references")
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"immich-api-key"}) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "ImmichKiosk bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != immichKioskWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", immichKioskWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, err = validateImmichKioskRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact media endpoint")
	}
	if err := validateImmichKioskServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "ImmichKioskWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "photos-kiosk", "immich-kiosk"
	bundle.Workload.ModuleRef, bundle.Workload.Release = immichKioskWorkloadModuleID, immichKioskRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateImmichKioskRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 || components[0].ID != "immich-kiosk" || components[0].Lifecycle != "daemon" ||
		components[0].Image.Ref != immichKioskImageRef || components[0].Image.Digest != immichKioskImageDigest ||
		components[0].Health.Kind != "http" || components[0].Health.Path != "/health" || components[0].Health.Port != 3000 || !sameEnvironment(components[0].Environment, immichKioskEnvironment()) ||
		!sameEnvironment(components[0].SecretEnvironment, map[string]string{"KIOSK_IMMICH_API_KEY": "immich-api-key"}) ||
		validatePeerNetworks(immichKioskWorkloadModuleID, components[0], path) != nil {
		return nil, fail(ErrInvalidPlan, path, "ImmichKiosk runtime graph differs from the closed "+immichKioskRelease+" contract")
	}
	want := map[string]selectedPaaSRuntimeVolume{
		"config": {ID: "config", Target: "/config", Class: "persistent", Backup: true},
	}
	if len(components[0].Volumes) != len(want) {
		return nil, fail(ErrInvalidPlan, path+".volumes", "ImmichKiosk volume set differs from the closed contract")
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
			return nil, fail(ErrInvalidPlan, path+".volumes", "ImmichKiosk volume %q differs from the closed contract", volume.ID)
		}
	}
	return components, nil
}

func validateImmichKioskServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "photos-kiosk" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 3000 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "immich-kiosk-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "photos-kiosk" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "media route authority differs from the governed ImmichKiosk endpoint")
	}
	return nil
}

func immichKioskEnvironment() map[string]string {
	return map[string]string{"KIOSK_IMMICH_URL": "http://immich-server:2283"}
}
