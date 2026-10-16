package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

const (
	mosquittoWorkloadModuleID    = "stackkits-mosquitto-runtime"
	mosquittoWorkloadUnitID      = "mosquitto"
	mosquittoWorkloadTemplateRef = "builtin://workloads/mosquitto/bundle/v3.json"
	mosquittoWorkloadVersion     = "3.0.0"
	mosquittoWorkloadOutputRef   = "workloads/mosquitto/bundle.json"
	mosquittoWorkloadAPIVersion  = "stackkit.workload-bundle/v3"
)

const mosquittoWorkloadRendererSchema = `stackkit.workload-bundle/v3|MosquittoWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:mosquitto|release:` +
	mosquittoRelease + `|secret-material:mqtt-password-reference|lan-listener:1883-owner-setting|compute:module-compute-profile-v1|limits:reservation-plus-headroom|mqtt-flow-control:upstream-count-bounds|home-assistant-link:owner-setting-private-peer-network`

type MosquittoWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func MosquittoWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(mosquittoWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: mosquittoWorkloadTemplateRef, Version: mosquittoWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type mosquittoWorkloadBundleRenderer struct{ contract RendererContract }

func newMosquittoWorkloadBundleRenderer() mosquittoWorkloadBundleRenderer {
	return mosquittoWorkloadBundleRenderer{contract: MosquittoWorkloadBundleRendererContract()}
}

func (r mosquittoWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateMosquittoWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.mosquitto-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: mosquittoWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseMosquittoWorkloadBundle(data []byte) (MosquittoWorkloadBundleDescriptor, error) {
	path := "mosquittoWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return MosquittoWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Mosquitto workload bundle", err)
	}
	legacy := bundle.APIVersion == mosquittoV2APIVersion
	release := mosquittoRelease
	if legacy {
		release = mosquittoV2Release
	}
	if (!legacy && bundle.APIVersion != mosquittoWorkloadAPIVersion) || bundle.Kind != "MosquittoWorkloadBundle" ||
		bundle.Workload.Ref != "smart-home-mqtt" || bundle.Workload.AlternativeRef != "mosquitto" ||
		bundle.Workload.ModuleRef != mosquittoWorkloadModuleID || bundle.Workload.Release != release ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != mosquittoWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return MosquittoWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Mosquitto "+mosquittoRelease+" contract")
	}
	if len(bundle.SecretRefs) != 1 || !validSecretReference(bundle.SecretRefs["mqtt-password"]) {
		return MosquittoWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "Mosquitto requires only the opaque MQTT password reference")
	}
	for index, component := range bundle.Components {
		if _, _, err := parseLANRights(component, mosquittoWorkloadModuleID, fmt.Sprintf("%s.components[%d]", path, index)); err != nil {
			return MosquittoWorkloadBundleDescriptor{}, err
		}
	}
	var components []selectedPaaSRuntimeComponent
	var err error
	if legacy {
		if len(bundle.ComputeProfile) != 0 {
			return MosquittoWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".computeProfile", "legacy v2 bundles cannot carry a v3 profile")
		}
		components, err = validateMosquittoV2RuntimeComponents(bundle.Components, path+".components")
	} else {
		components, err = validateMosquittoRuntimeComponents(bundle.Components, bundle.ComputeProfile, path+".components")
	}
	if err != nil {
		return MosquittoWorkloadBundleDescriptor{}, err
	}
	if err := validateMosquittoServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return MosquittoWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, mosquittoWorkloadModuleID, "smart-home-mqtt", 8080, path+".deliveryRoute"); err != nil {
			return MosquittoWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := MosquittoWorkloadBundleDescriptor{
		WorkloadRef: "smart-home-mqtt", ModuleRef: mosquittoWorkloadModuleID, Release: release,
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

func validateMosquittoWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + mosquittoWorkloadModuleID + ".renderUnits." + mosquittoWorkloadUnitID
	if unit.ModuleID() != mosquittoWorkloadModuleID || unit.ID() != mosquittoWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", mosquittoWorkloadModuleID, mosquittoWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Mosquitto workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != mosquittoImageRef ||
		!hasDigest || imageDigest != mosquittoImageDigest || !hasEntry || entry != mosquittoWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Mosquitto "+mosquittoRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Mosquitto requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	routeUnit, computeProfile, err := consumeModuleComputeProfile(unit, path+".inputs.compute-profile")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	deliveryRoute, settings, err := validateApplicationDeliveryInputsWithSettings(routeUnit, mosquittoWorkloadModuleID, "smart-home-mqtt", 8080, []string{"lan-listener", "home-assistant-link"}, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || len(secretRefs) != 1 || !validSecretReference(secretRefs["mqtt-password"]) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "Mosquitto requires the opaque MQTT password reference")
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"mqtt-password"}) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Mosquitto bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != mosquittoWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", mosquittoWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	for index := range components {
		var link bool
		if raw, ok := settings["home-assistant-link"]; ok {
			if err := json.Unmarshal(raw, &link); err != nil {
				return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs.home-assistant-link", "requires an explicit boolean")
			}
		}
		if !link {
			components[index].PeerNetworks = nil
		}
		if !mosquittoCommandMatches(components[index].Command) {
			return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime.components.command", "catalog command differs from the pinned broker command")
		}
		limits, _, err := computeProfileLimits(computeProfile, mosquittoWorkloadModuleID)
		if err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
		command, err := mosquittoProfileCommand(computeProfile)
		if err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
		components[index].Resources, components[index].Command = limits, command
		if err := validateDeclaredLANRights(mosquittoWorkloadModuleID, components[index], path+".runtime.components"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
		if err := materializeLANRights(&components[index], settings, path+".inputs"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
	}
	components, err = validateMosquittoRuntimeComponents(components, computeProfile, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact MQTT broker endpoint")
	}
	if err := validateMosquittoServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: mosquittoWorkloadAPIVersion, Kind: "MosquittoWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute, ComputeProfile: computeProfile,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "smart-home-mqtt", "mosquitto"
	bundle.Workload.ModuleRef, bundle.Workload.Release = mosquittoWorkloadModuleID, mosquittoRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateMosquittoRuntimeComponents(components []selectedPaaSRuntimeComponent, profile json.RawMessage, path string) ([]selectedPaaSRuntimeComponent, error) {
	limits, _, err := computeProfileLimits(profile, mosquittoWorkloadModuleID)
	if err != nil {
		return nil, wrap(ErrInvalidPlan, path, "validate selected broker resources", err)
	}
	command, err := mosquittoProfileCommand(profile)
	if err != nil {
		return nil, err
	}
	validated, err := validateMosquittoRuntimeComponentsAgainst(components, command, limits, mosquittoImageRef, mosquittoImageDigest, path)
	if err == nil {
		return validated, nil
	}
	// Read retained v3 custody from the diagnosed pre-correction recipe only.
	// Generation never uses these historical byte limits.
	legacyCommand, legacyErr := mosquittoRetainedV3Command(profile)
	if legacyErr == nil {
		if retained, legacyErr := validateMosquittoRuntimeComponentsAgainst(components, legacyCommand, limits, mosquittoImageRef, mosquittoImageDigest, path); legacyErr == nil {
			return retained, nil
		}
	}
	return nil, err
}

func validateMosquittoRuntimeComponentsAgainst(components []selectedPaaSRuntimeComponent, command []string, limits *selectedPaaSRuntimeLimits, imageRef, imageDigest, path string) ([]selectedPaaSRuntimeComponent, error) {
	for _, component := range components {
		if err := validatePeerNetworks(mosquittoWorkloadModuleID, component, path); err != nil {
			return nil, err
		}
	}
	if len(components) != 1 || components[0].ID != "mosquitto" || components[0].Lifecycle != "daemon" ||
		components[0].Image.Ref != imageRef || components[0].Image.Digest != imageDigest ||
		components[0].Health.Kind != "http" || components[0].Health.Path != "/api/v1/systree" || components[0].Health.Port != 8080 || !sameEnvironment(components[0].Environment, mosquittoEnvironment()) ||
		len(components[0].SecretEnvironment) != 0 || !slices.Equal(components[0].SecretFiles, []selectedPaaSSecretFile{mosquittoPasswordFile}) ||
		!exactStringList(components[0].Command, command) || components[0].Resources == nil || *components[0].Resources != *limits {
		return nil, fail(ErrInvalidPlan, path, "Mosquitto runtime graph differs from the closed "+mosquittoRelease+" contract")
	}
	want := map[string]selectedPaaSRuntimeVolume{
		"data": {ID: "data", Target: "/mosquitto/data", Class: "persistent", Backup: true},
	}
	if len(components[0].Volumes) != len(want) {
		return nil, fail(ErrInvalidPlan, path+".volumes", "Mosquitto volume set differs from the closed contract")
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
			return nil, fail(ErrInvalidPlan, path+".volumes", "Mosquitto volume %q differs from the closed contract", volume.ID)
		}
	}
	return components, nil
}

func validateMosquittoServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "smart-home-mqtt" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 8080 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "mosquitto-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "smart-home-mqtt" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "MQTT broker route authority differs from the governed Mosquitto endpoint")
	}
	return nil
}

func mosquittoEnvironment() map[string]string {
	return map[string]string{}
}

func mosquittoCommandMatches(command []string) bool {
	var want []string
	return json.Unmarshal([]byte(mosquittoCommandJSON), &want) == nil && exactStringList(command, want)
}

func mosquittoProfileCommand(profile json.RawMessage) ([]string, error) {
	_, _, err := computeProfileLimits(profile, mosquittoWorkloadModuleID)
	if err != nil {
		return nil, err
	}
	var command []string
	if err := json.Unmarshal([]byte(mosquittoCommandJSON), &command); err != nil || len(command) != 3 {
		return nil, fmt.Errorf("decode pinned Mosquitto command")
	}
	// Compute profiles allocate resources; they do not change MQTT delivery semantics.
	// Keep the pinned recipe and upstream queue/inflight count bounds for every
	// allocation instead of inventing per-client byte budgets from host RAM.
	return command, nil
}
