package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
)

// Private AI agent harness (docs/use-case-expansion/ai-agents.md): OpenHands
// Agent Canvas. The agent's shell, editor and automations run inside this one
// container (agent-server conversation runtime "local"), so the container is
// the sandbox and the governed runtime makes it a gVisor one: no host Docker
// socket, no owner home, its own settings and workspace volumes, the node's
// Ollama as model endpoint, and a private route behind the kit's login only.
const (
	openHandsWorkloadModuleID    = "stackkits-openhands-runtime"
	openHandsWorkloadUnitID      = "openhands"
	openHandsWorkloadRef         = "ai-harness"
	openHandsWorkloadTemplateRef = "builtin://workloads/openhands/bundle/v2.json"
	openHandsWorkloadVersion     = "2.0.0"
	openHandsWorkloadOutputRef   = "workloads/openhands/bundle.json"
	openHandsPort                = 8000

	// OpenHandsSettingsSeedEnv carries the governed settings seed the
	// container's entrypoint writes to the agent server's settings file on the
	// first start only: the node's Ollama endpoint as the agent's model. An
	// environment value, not a mounted file: governed files are root-owned
	// 0600 and the image runs as uid 10001, which could not read them
	// (measured 2026-09-28; the seed was never applied).
	OpenHandsSettingsSeedEnv = "STACKKIT_OPENHANDS_SETTINGS_SEED"
	// OpenHandsDefaultModel is the LiteLLM model reference of the seed: the
	// plan's general-assistant preset served by the node's Ollama through the
	// tool-calling ollama_chat provider. The owner pulls it in Open WebUI.
	OpenHandsDefaultModel = "ollama_chat/qwen3.5:9b"
	openHandsOllamaURL    = "http://ollama:11434"
)

// openHandsSettingsSeed is agent-server PersistedSettings v3 with agent
// settings v6: only the LLM model and endpoint; no secret. The catalog
// declares the same value as the component's seed variable.
const openHandsSettingsSeed = `{"schema_version":3,"agent_settings":{"schema_version":6,"agent_kind":"openhands","llm":{"model":"` +
	OpenHandsDefaultModel + `","base_url":"` + openHandsOllamaURL + `"}}}`

func openHandsWorkloadRendererSchema() string {
	return `stackkit.workload-bundle/v2|OpenHandsWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:openhands|sandbox-runtime:runsc|docker-socket:none|egress:agent|volumes:settings,workspace|settings-seed:env:` +
		OpenHandsSettingsSeedEnv + `|entrypoint:` + openhandsEntrypointJSON + `|release:` + openhandsRelease + `|secret-material:not-included|peer:ai/private-ai-internal`
}

// OpenHandsWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the standalone application adapter.
type OpenHandsWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func OpenHandsWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(openHandsWorkloadRendererSchema()))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: openHandsWorkloadTemplateRef,
		Version: openHandsWorkloadVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type openHandsWorkloadBundleRenderer struct{ contract RendererContract }

func newOpenHandsWorkloadBundleRenderer() openHandsWorkloadBundleRenderer {
	return openHandsWorkloadBundleRenderer{contract: OpenHandsWorkloadBundleRendererContract()}
}

func (r openHandsWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateOpenHandsWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.openhands-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: openHandsWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseOpenHandsWorkloadBundle validates the closed artifact before a runtime
// owner may consume it.
func ParseOpenHandsWorkloadBundle(data []byte) (OpenHandsWorkloadBundleDescriptor, error) {
	path := "openHandsWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return OpenHandsWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed OpenHands workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "OpenHandsWorkloadBundle" ||
		bundle.Workload.Ref != openHandsWorkloadRef || bundle.Workload.AlternativeRef != "openhands" ||
		bundle.Workload.ModuleRef != openHandsWorkloadModuleID || bundle.Workload.Release != openhandsRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != openHandsWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return OpenHandsWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed OpenHands "+openhandsRelease+" contract")
	}
	if len(bundle.SecretRefs) != 0 {
		return OpenHandsWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "OpenHands receives no secret from StackKits")
	}
	if len(bundle.ConfigFiles) != 0 {
		return OpenHandsWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "OpenHands receives no mounted file; its settings seed is an environment value")
	}
	if err := validateOpenHandsRuntimeComponents(bundle.Components, path+".components"); err != nil {
		return OpenHandsWorkloadBundleDescriptor{}, err
	}
	if err := validateOpenHandsServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return OpenHandsWorkloadBundleDescriptor{}, err
	}
	descriptor := OpenHandsWorkloadBundleDescriptor{
		WorkloadRef: openHandsWorkloadRef, ModuleRef: openHandsWorkloadModuleID, Release: openhandsRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Components: make([]SelectedPaaSWorkloadComponentDescriptor, len(bundle.Components)),
	}
	if bundle.DeliveryRoute != nil {
		if err := validateOpenHandsRoute(*bundle.DeliveryRoute, path+".deliveryRoute"); err != nil {
			return OpenHandsWorkloadBundleDescriptor{}, err
		}
		descriptor.Route = bundle.DeliveryRoute.descriptor()
	}
	for index, component := range bundle.Components {
		descriptor.Components[index] = SelectedPaaSWorkloadComponentDescriptor{
			ID: component.ID, Lifecycle: component.Lifecycle,
			ImageRef: component.Image.Ref, ImageDigest: component.Image.Digest,
		}
	}
	return descriptor, nil
}

//nolint:gocyclo // One closed-contract check per render-unit field.
func validateOpenHandsWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + openHandsWorkloadModuleID + ".renderUnits." + openHandsWorkloadUnitID
	if unit.ModuleID() != openHandsWorkloadModuleID || unit.ID() != openHandsWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", openHandsWorkloadModuleID, openHandsWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered OpenHands workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != openhandsImageRef ||
		!hasDigest || imageDigest != openhandsImageDigest || !hasEntry || entry != openHandsWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact OpenHands "+openhandsRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "OpenHands requires one exact node-local target")
	}
	// The agent never receives the host's Docker daemon: not the socket, not
	// a daemon binding, not a proxy. Its sandbox is the gVisor container.
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "the agent harness receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, openHandsWorkloadModuleID, openHandsWorkloadRef, openHandsPort, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute != nil && deliveryRoute.Exposure == "public" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "OpenHands signs its browser session in by itself and is never published")
	}
	if !emptyJSONObject(unit.SecretRefsJSON()) || len(unit.SecretInputRefs()) != 0 ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "OpenHands bundle accepts no secret, free input or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != openHandsWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", openHandsWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	if _, selected := unit.ModuleAccelerator(); selected {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".acceleratorProfile", "OpenHands has no GPU: inference runs in Ollama")
	}
	if err := validateOpenHandsRuntimeComponents(components, path+".runtime.components"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact OpenHands endpoint")
	}
	if err := validateOpenHandsServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "OpenHandsWorkloadBundle",
		SecretRefs: map[string]string{}, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = openHandsWorkloadRef, "openhands"
	bundle.Workload.ModuleRef, bundle.Workload.Release = openHandsWorkloadModuleID, openhandsRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

// openHandsComponent is the one governed OpenHands component: the all-in-one
// image under the gVisor runtime, its two backed-up volumes, egress for the
// agent's clones and installs, and the private AI network to reach Ollama.
func openHandsComponent() (selectedPaaSRuntimeComponent, error) {
	var entrypoint []string
	if err := json.Unmarshal([]byte(openhandsEntrypointJSON), &entrypoint); err != nil {
		return selectedPaaSRuntimeComponent{}, err
	}
	return selectedPaaSRuntimeComponent{
		ID: "openhands", Role: "application", Lifecycle: "daemon", Egress: true,
		SandboxRuntime: SandboxRuntimeRunsc,
		Image:          selectedPaaSRuntimeImage{Ref: openhandsImageRef, Digest: openhandsImageDigest},
		DependsOn:      []string{}, NetworkRefs: []string{"openhands-internal"},
		Entrypoint:  entrypoint,
		Environment: map[string]string{"DO_NOT_TRACK": "1", "VITE_DO_NOT_TRACK": "1", OpenHandsSettingsSeedEnv: openHandsSettingsSeed},
		Volumes: []selectedPaaSRuntimeVolume{
			{ID: "settings", Target: "/home/openhands/.openhands", Class: "persistent", Backup: true},
			{ID: "workspace", Target: "/projects", Class: "persistent", Backup: true},
		},
		Health:       selectedPaaSRuntimeHealth{Kind: "http", Path: "/alive", Port: openHandsPort},
		Resources:    &selectedPaaSRuntimeLimits{MemoryLimit: "6g", MemoryReservation: "1g", CPUs: 2},
		PeerNetworks: []selectedPaaSPeerNetwork{{WorkloadRef: "ai", NetworkRef: "private-ai-internal"}},
	}, nil
}

// validateOpenHandsRuntimeComponents admits exactly the governed component;
// nothing else reaches the container, and the container never runs outside
// the sandbox runtime.
func validateOpenHandsRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) error {
	expected, err := openHandsComponent()
	if err != nil {
		return wrap(ErrInvalidPlan, path, "decode governed OpenHands entrypoint", err)
	}
	if len(components) != 1 {
		return fail(ErrInvalidPlan, path, "OpenHands runtime graph differs from the closed "+openhandsRelease+" contract")
	}
	actual := components[0]
	if err := validateSandboxRuntime(openHandsWorkloadModuleID, actual, path); err != nil {
		return err
	}
	if err := validatePeerNetworks(openHandsWorkloadModuleID, actual, path); err != nil {
		return err
	}
	byID := func(a, b selectedPaaSRuntimeVolume) int { return strings.Compare(a.ID, b.ID) }
	actual.Volumes = slices.SortedFunc(slices.Values(actual.Volumes), byID)
	expected.Volumes = slices.SortedFunc(slices.Values(expected.Volumes), byID)
	if !reflect.DeepEqual(actual, expected) {
		return fail(ErrInvalidPlan, path, "OpenHands runtime graph differs from the closed "+openhandsRelease+" contract")
	}
	return nil
}

func validateOpenHandsServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != openHandsWorkloadRef || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != openHandsPort ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "openhands-http" || endpoint.IngressAuth != "forward-auth" ||
		endpoint.Data.BindingRef != openHandsWorkloadRef || endpoint.Data.Locality != "primary-site" ||
		!exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private"}) {
		return fail(ErrInvalidPlan, path, "route authority differs from the governed private OpenHands endpoint")
	}
	return nil
}

func validateOpenHandsRoute(route applicationDeliveryRoute, path string) error {
	if err := validateParsedApplicationDeliveryRoute(route, openHandsWorkloadModuleID, openHandsWorkloadRef, openHandsPort, path); err != nil {
		return err
	}
	if route.Exposure == "public" {
		return fail(ErrInvalidPlan, path, "OpenHands signs its browser session in by itself and is never published")
	}
	return nil
}
