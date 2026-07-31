package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const (
	cloudreveWorkloadModuleID    = "stackkits-cloudreve-runtime"
	cloudreveWorkloadUnitID      = "cloudreve"
	cloudreveWorkloadTemplateRef = "builtin://workloads/cloudreve/bundle/v2.json"
	cloudreveWorkloadVersion     = "2.0.0"
	cloudreveWorkloadOutputRef   = "workloads/cloudreve/bundle.json"
	cloudreveImageRef            = "docker.io/cloudreve/cloudreve:4.18.0"
	cloudreveImageDigest         = "sha256:f7a464100bf6325e9ba58cb2b0ee60f9a24c58fc2eb90647720bc4b8f3cddd9a"
)

const cloudreveWorkloadRendererSchema = `stackkit.workload-bundle/v2|CloudreveWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:cloudreve|release:4.18.0|secret-material:not-included`

// CloudreveWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the selected-PaaS executor.
type CloudreveWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func CloudreveWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(cloudreveWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: cloudreveWorkloadTemplateRef, Version: cloudreveWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type cloudreveWorkloadBundleRenderer struct{ contract RendererContract }

func newCloudreveWorkloadBundleRenderer() cloudreveWorkloadBundleRenderer {
	return cloudreveWorkloadBundleRenderer{contract: CloudreveWorkloadBundleRendererContract()}
}

func (r cloudreveWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateCloudreveWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.cloudreve-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: cloudreveWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseCloudreveWorkloadBundle(data []byte) (CloudreveWorkloadBundleDescriptor, error) {
	path := "cloudreveWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return CloudreveWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Cloudreve workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "CloudreveWorkloadBundle" ||
		bundle.Workload.Ref != "files" || bundle.Workload.AlternativeRef != "cloudreve" ||
		bundle.Workload.ModuleRef != cloudreveWorkloadModuleID || bundle.Workload.Release != "4.18.0" ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != cloudreveWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return CloudreveWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Cloudreve 4.18.0 contract")
	}
	if len(bundle.SecretRefs) != 0 {
		return CloudreveWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "Cloudreve single-container contract accepts no secret material")
	}
	for field, value := range map[string]string{"siteRef": bundle.Target.SiteRef, "nodeRef": bundle.Target.NodeRef, "instanceRef": bundle.Target.InstanceRef} {
		if err := requireContractID(value, path+".target."+field); err != nil {
			return CloudreveWorkloadBundleDescriptor{}, err
		}
	}
	components, err := validateCloudreveRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return CloudreveWorkloadBundleDescriptor{}, err
	}
	if err := validateCloudreveServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return CloudreveWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, cloudreveWorkloadModuleID, "files", 5212, path+".deliveryRoute"); err != nil {
			return CloudreveWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := CloudreveWorkloadBundleDescriptor{
		WorkloadRef: "files", ModuleRef: cloudreveWorkloadModuleID, Release: "4.18.0",
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

func validateCloudreveWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + cloudreveWorkloadModuleID + ".renderUnits." + cloudreveWorkloadUnitID
	if unit.ModuleID() != cloudreveWorkloadModuleID || unit.ID() != cloudreveWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", cloudreveWorkloadModuleID, cloudreveWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Cloudreve workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != cloudreveImageRef ||
		!hasDigest || imageDigest != cloudreveImageDigest || !hasEntry || entry != cloudreveWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Cloudreve 4.18.0 contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Cloudreve requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, cloudreveWorkloadModuleID, "files", 5212, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Cloudreve bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != cloudreveWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", cloudreveWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, err = validateCloudreveRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact files endpoint")
	}
	if err := validateCloudreveServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "CloudreveWorkloadBundle",
		SecretRefs: map[string]string{}, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "files", "cloudreve"
	bundle.Workload.ModuleRef, bundle.Workload.Release = cloudreveWorkloadModuleID, "4.18.0"
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateCloudreveRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	expected := []selectedPaaSRuntimeComponent{{
		ID: "cloudreve", Role: "application", Lifecycle: "daemon",
		Image:     selectedPaaSRuntimeImage{Ref: cloudreveImageRef, Digest: cloudreveImageDigest},
		DependsOn: []string{}, NetworkRefs: []string{"cloudreve-internal"},
		Volumes: []selectedPaaSRuntimeVolume{{ID: "data", Target: "/cloudreve/data", Class: "persistent", Backup: true}},
		Health:  selectedPaaSRuntimeHealth{Kind: "http", Path: "/", Port: 5212},
	}}
	if !reflect.DeepEqual(components, expected) {
		return nil, fail(ErrInvalidPlan, path, "component graph differs from the exact Cloudreve 4.18.0 workload contract")
	}
	return components, nil
}

func validateCloudreveServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "files" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 5212 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "cloudreve-http" || endpoint.Data.BindingRef != "files" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"http", "https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "files route authority differs from the governed Cloudreve endpoint")
	}
	return nil
}
