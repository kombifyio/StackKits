package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	euroofficeWorkloadModuleID    = "stackkits-euro-office-runtime"
	euroofficeWorkloadUnitID      = "euro-office"
	euroofficeWorkloadTemplateRef = "builtin://workloads/euro-office/bundle/v2.json"
	euroofficeWorkloadVersion     = "2.0.0"
	euroofficeWorkloadOutputRef   = "workloads/euro-office/bundle.json"
)

const euroofficeWorkloadRendererSchema = `stackkit.workload-bundle/v2|EuroofficeWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:euro-office|release:` +
	euroofficeRelease + `|secret-material:not-included`

type EuroofficeWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func EuroofficeWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(euroofficeWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: euroofficeWorkloadTemplateRef, Version: euroofficeWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type euroofficeWorkloadBundleRenderer struct{ contract RendererContract }

func newEuroofficeWorkloadBundleRenderer() euroofficeWorkloadBundleRenderer {
	return euroofficeWorkloadBundleRenderer{contract: EuroofficeWorkloadBundleRendererContract()}
}

func (r euroofficeWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateEuroofficeWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.eurooffice-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: euroofficeWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseEuroofficeWorkloadBundle(data []byte) (EuroofficeWorkloadBundleDescriptor, error) {
	path := "euroofficeWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return EuroofficeWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Eurooffice workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "EuroofficeWorkloadBundle" ||
		bundle.Workload.Ref != "files-office" || bundle.Workload.AlternativeRef != "euro-office" ||
		bundle.Workload.ModuleRef != euroofficeWorkloadModuleID || bundle.Workload.Release != euroofficeRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != euroofficeWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return EuroofficeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Eurooffice "+euroofficeRelease+" contract")
	}
	if len(bundle.SecretRefs) != 1 || !validSecretReference(bundle.SecretRefs["jwt-secret"]) {
		return EuroofficeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "Euro-Office requires only the opaque JWT secret reference")
	}
	components, err := validateEuroofficeRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return EuroofficeWorkloadBundleDescriptor{}, err
	}
	if err := validateEuroofficeServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return EuroofficeWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, euroofficeWorkloadModuleID, "files-office", 80, path+".deliveryRoute"); err != nil {
			return EuroofficeWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := EuroofficeWorkloadBundleDescriptor{
		WorkloadRef: "files-office", ModuleRef: euroofficeWorkloadModuleID, Release: euroofficeRelease,
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

func validateEuroofficeWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + euroofficeWorkloadModuleID + ".renderUnits." + euroofficeWorkloadUnitID
	if unit.ModuleID() != euroofficeWorkloadModuleID || unit.ID() != euroofficeWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", euroofficeWorkloadModuleID, euroofficeWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Eurooffice workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != euroofficeImageRef ||
		!hasDigest || imageDigest != euroofficeImageDigest || !hasEntry || entry != euroofficeWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Eurooffice "+euroofficeRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Eurooffice requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, euroofficeWorkloadModuleID, "files-office", 80, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || len(secretRefs) != 1 || !validSecretReference(secretRefs["jwt-secret"]) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "Euro-Office requires the opaque JWT secret reference")
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"jwt-secret"}) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Euro-Office bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != euroofficeWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", euroofficeWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, err = validateEuroofficeRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact media endpoint")
	}
	if err := validateEuroofficeServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "EuroofficeWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "files-office", "euro-office"
	bundle.Workload.ModuleRef, bundle.Workload.Release = euroofficeWorkloadModuleID, euroofficeRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateEuroofficeRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 || components[0].ID != "euro-office" || components[0].Lifecycle != "daemon" ||
		components[0].Image.Ref != euroofficeImageRef || components[0].Image.Digest != euroofficeImageDigest ||
		components[0].Health.Kind != "http" || components[0].Health.Path != "/healthcheck" || components[0].Health.Port != 80 || !sameEnvironment(components[0].Environment, euroofficeEnvironment()) || !sameEnvironment(components[0].SecretEnvironment, map[string]string{"JWT_SECRET": "jwt-secret"}) {
		return nil, fail(ErrInvalidPlan, path, "Eurooffice runtime graph differs from the closed "+euroofficeRelease+" contract")
	}
	want := map[string]selectedPaaSRuntimeVolume{
		"data": {ID: "data", Target: "/var/www/euro-office/Data", Class: "persistent", Backup: true},
	}
	if len(components[0].Volumes) != len(want) {
		return nil, fail(ErrInvalidPlan, path+".volumes", "Eurooffice volume set differs from the closed contract")
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
			return nil, fail(ErrInvalidPlan, path+".volumes", "Eurooffice volume %q differs from the closed contract", volume.ID)
		}
	}
	return components, nil
}

func validateEuroofficeServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "files-office" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 80 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "euro-office-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "files-office" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "media route authority differs from the governed Eurooffice endpoint")
	}
	return nil
}

func euroofficeEnvironment() map[string]string {
	return map[string]string{"JWT_ENABLED": "true"}
}
