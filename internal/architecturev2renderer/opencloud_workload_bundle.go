package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const (
	opencloudWorkloadModuleID    = "stackkits-opencloud-runtime"
	opencloudWorkloadUnitID      = "opencloud"
	opencloudWorkloadTemplateRef = "builtin://workloads/opencloud/bundle/v1.json"
	opencloudWorkloadVersion     = "1.0.0"
	opencloudWorkloadOutputRef   = "workloads/opencloud/bundle.json"
)

//go:embed assets/opencloud/start.sh
var opencloudStartupScript string

func opencloudWorkloadRendererSchema() string {
	sum := sha256.Sum256([]byte(opencloudStartupScript))
	return "stackkit.workload-bundle/v2|OpencloudWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:opencloud|bootstrap:node-owned-private-env|startup:sha256:" + hex.EncodeToString(sum[:]) + "|secret-material:not-included"
}

// OpencloudWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the selected-PaaS executor.
type OpencloudWorkloadBundleDescriptor struct {
	WorkloadRef      string
	ModuleRef        string
	Release          string
	SiteRef          string
	NodeRef          string
	InstanceRef      string
	OwnerPasswordRef string
	Components       []SelectedPaaSWorkloadComponentDescriptor
	Route            ApplicationDeliveryRouteDescriptor
}

func OpencloudWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(opencloudWorkloadRendererSchema()))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: opencloudWorkloadTemplateRef, Version: opencloudWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type opencloudWorkloadBundleRenderer struct{ contract RendererContract }

func newOpencloudWorkloadBundleRenderer() opencloudWorkloadBundleRenderer {
	return opencloudWorkloadBundleRenderer{contract: OpencloudWorkloadBundleRendererContract()}
}

func (r opencloudWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateOpencloudWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.opencloud-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: opencloudWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseOpencloudWorkloadBundle(data []byte) (OpencloudWorkloadBundleDescriptor, error) {
	path := "opencloudWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return OpencloudWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Opencloud workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "OpencloudWorkloadBundle" ||
		bundle.Workload.Ref != "files" || bundle.Workload.AlternativeRef != "opencloud" ||
		bundle.Workload.ModuleRef != opencloudWorkloadModuleID || bundle.Workload.Release != "8.1.0" ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != opencloudWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return OpencloudWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Opencloud 8.1.0 contract")
	}
	if len(bundle.SecretRefs) != 1 || !validSecretReference(bundle.SecretRefs["owner-password"]) {
		return OpencloudWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "Opencloud requires its opaque node-owned bootstrap reference")
	}
	components, err := validateOpencloudRuntimeComponents(bundle.Components, path+".components", true)
	if err != nil {
		return OpencloudWorkloadBundleDescriptor{}, err
	}
	if err := validateOpencloudServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return OpencloudWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, opencloudWorkloadModuleID, "files", 9200, path+".deliveryRoute"); err != nil {
			return OpencloudWorkloadBundleDescriptor{}, err
		}
	}
	for _, component := range components {
		if configured, present := component.Environment["OC_URL"]; present || bundle.DeliveryRoute != nil {
			expected, err := applicationHTTPSRootURL(bundle.DeliveryRoute)
			if err != nil || component.ID != opencloudWorkloadUnitID || configured != expected {
				return OpencloudWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".components", "Opencloud site URL differs from its declared HTTPS route")
			}
		}
	}
	descriptor := OpencloudWorkloadBundleDescriptor{
		WorkloadRef: "files", ModuleRef: opencloudWorkloadModuleID, Release: "8.1.0",
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef, OwnerPasswordRef: bundle.SecretRefs["owner-password"],
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

func validateOpencloudWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + opencloudWorkloadModuleID + ".renderUnits." + opencloudWorkloadUnitID
	if unit.ModuleID() != opencloudWorkloadModuleID || unit.ID() != opencloudWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", opencloudWorkloadModuleID, opencloudWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Opencloud workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != opencloudImageRef ||
		!hasDigest || imageDigest != opencloudImageDigest || !hasEntry || entry != opencloudWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Opencloud 8.1.0 contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Opencloud requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, opencloudWorkloadModuleID, "files", 9200, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"owner-password"}) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Opencloud bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != opencloudWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", opencloudWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, err = validateOpencloudRuntimeComponents(components, path+".runtime.components", false)
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact files endpoint")
	}
	if err := validateOpencloudServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	for index := range components {
		if _, provided := components[index].Environment["CR_SETTING_siteURL"]; provided {
			return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "Opencloud site URL comes only from the delivery route")
		}
		if components[index].ID == opencloudWorkloadUnitID && deliveryRoute != nil {
			siteURL, err := applicationHTTPSRootURL(deliveryRoute)
			if err != nil {
				return selectedPaaSWorkloadBundle{}, err
			}
			if components[index].Environment == nil {
				components[index].Environment = map[string]string{}
			}
			components[index].Environment["OC_URL"] = siteURL
		}
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || len(secretRefs) != 1 || !validSecretReference(secretRefs["owner-password"]) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires only the opaque owner-password reference")
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "OpencloudWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "files", "opencloud"
	bundle.Workload.ModuleRef, bundle.Workload.Release = opencloudWorkloadModuleID, "8.1.0"
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateOpencloudRuntimeComponents(components []selectedPaaSRuntimeComponent, path string, generated bool) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 {
		return nil, fail(ErrInvalidPlan, path, "requires the single governed OpenCloud service")
	}
	actual := components[0]
	siteURL, hasURL := actual.Environment["OC_URL"]
	if hasURL {
		delete(actual.Environment, "OC_URL")
	}
	expected := selectedPaaSRuntimeComponent{ID: "opencloud", Role: "application", Lifecycle: "daemon", Image: selectedPaaSRuntimeImage{Ref: opencloudImageRef, Digest: opencloudImageDigest}, DependsOn: []string{}, NetworkRefs: []string{"opencloud-internal"}, Environment: map[string]string{"OC_CONFIG_DIR": "/etc/opencloud", "OC_BASE_DATA_PATH": "/var/lib/opencloud", "OC_INSECURE": "false", "PROXY_TLS": "false", "IDM_CREATE_DEMO_USERS": "false", "PROXY_ENABLE_BASIC_AUTH": "false"}, SecretEnvironment: map[string]string{"IDM_ADMIN_PASSWORD": "owner-password"}, Volumes: []selectedPaaSRuntimeVolume{{ID: "config", Target: "/etc/opencloud", Class: "persistent", Backup: true}, {ID: "data", Target: "/var/lib/opencloud", Class: "persistent", Backup: true}}, Health: selectedPaaSRuntimeHealth{Kind: "http", Path: "/", Port: 9200}, Resources: &selectedPaaSRuntimeLimits{MemoryLimit: "2g", MemoryReservation: "1g", CPUs: 1}}

	hasCommand := len(actual.Entrypoint) > 0
	if generated != hasCommand {
		return nil, fail(ErrInvalidPlan, path, "bootstrap command must match the artifact boundary")
	}
	if hasCommand {
		if !reflect.DeepEqual(actual.Entrypoint, []string{"/bin/sh", "-ec", opencloudStartupScript}) {
			return nil, fail(ErrInvalidPlan, path, "startup differs from the governed bootstrap")
		}
		actual.Entrypoint = nil
	}
	if !reflect.DeepEqual(actual, expected) {
		return nil, fail(ErrInvalidPlan, path, "OpenCloud image, graph, custody, mounts or network differs from its authority")
	}
	actual.Entrypoint = []string{"/bin/sh", "-ec", opencloudStartupScript}
	if hasURL {
		actual.Environment["OC_URL"] = siteURL
	}
	return []selectedPaaSRuntimeComponent{actual}, nil
}

func validateOpencloudServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "files" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 9200 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "opencloud-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "files" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private"}) {
		return fail(ErrInvalidPlan, path, "files route authority differs from the governed Opencloud endpoint")
	}
	return nil
}
