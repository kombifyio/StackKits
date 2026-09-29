package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"reflect"
)

// AnythingLLM is the chat alternative of the Private AI workload (owner
// decision 2026-09-27): the same pinned Ollama as private-ai plus AnythingLLM
// instead of Open WebUI, so a switch keeps the model volume. AnythingLLM
// runs in single-user password mode with the owner password from custody,
// signs sessions with the custody session key, talks only to the local
// Ollama, keeps documents in LanceDB inside its storage volume and sends no
// telemetry. Its upstream entrypoint is wrapped once to link server/.env into
// the storage volume, because AnythingLLM persists UI-made settings there.
const (
	anythingLLMWorkloadModuleID    = "stackkits-anythingllm-runtime"
	anythingLLMWorkloadUnitID      = "anythingllm"
	anythingLLMWorkloadTemplateRef = "builtin://workloads/anythingllm/bundle/v2.json"
	anythingLLMWorkloadVersion     = "2.0.0"
	anythingLLMWorkloadOutputRef   = "workloads/anythingllm/bundle.json"
	anythingLLMPort                = 3001
)

const anythingLLMWorkloadRendererSchema = `stackkit.workload-bundle/v2|AnythingLLMWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:anythingllm,ollama|entrypoint:governed-env-link-v1|release:` + anythingLLMRelease + `|secret-material:not-included|companions:ai-search,ai-image-video,ai-harness,ai-control-plane,ai-speech|init:true`

// AnythingLLMWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the selected-PaaS executor.
type AnythingLLMWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func AnythingLLMWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(anythingLLMWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: anythingLLMWorkloadTemplateRef, Version: anythingLLMWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

// anythingLLMExpectedComponents is the closed component graph before companion
// wiring and accelerator grants are applied. The Ollama component is the
// Private AI one: same image, network, volume and health.
func anythingLLMExpectedComponents() []selectedPaaSRuntimeComponent {
	var entrypoint []string
	if err := json.Unmarshal([]byte(anythingLLMEntrypointJSON), &entrypoint); err != nil {
		panic(err)
	}
	return []selectedPaaSRuntimeComponent{{
		ID: "anythingllm", Role: "application", Lifecycle: "daemon",
		Image:       selectedPaaSRuntimeImage{Ref: anythingLLMImageRef, Digest: anythingLLMImageDigest},
		DependsOn:   []string{"ollama"},
		NetworkRefs: []string{"private-ai-internal"},
		Entrypoint:  entrypoint,
		Environment: map[string]string{
			"SERVER_PORT": "3001", "STORAGE_DIR": "/app/server/storage",
			"LLM_PROVIDER": "ollama", "OLLAMA_BASE_PATH": "http://ollama:11434",
			"EMBEDDING_ENGINE": "ollama", "EMBEDDING_BASE_PATH": "http://ollama:11434",
			"VECTOR_DB": "lancedb", "DISABLE_TELEMETRY": "true",
		},
		SecretEnvironment: map[string]string{"AUTH_TOKEN": "owner-password", "JWT_SECRET": "session-key"},
		Volumes:           []selectedPaaSRuntimeVolume{{ID: "storage", Target: "/app/server/storage", Class: "persistent", Backup: true}},
		Health:            selectedPaaSRuntimeHealth{Kind: "http", Path: "/api/ping", Port: anythingLLMPort},
		Resources:         &selectedPaaSRuntimeLimits{MemoryLimit: "2g", MemoryReservation: "512m"},
		// The entrypoint shell as PID 1 has no signal handlers; under init
		// docker stop no longer waits for SIGKILL (measured 2026-09-28).
		Init: true,
	}, {
		ID: "ollama", Role: "application", Lifecycle: "daemon", Egress: true,
		Image:       selectedPaaSRuntimeImage{Ref: ollamaImageRef, Digest: ollamaImageDigest},
		DependsOn:   []string{},
		NetworkRefs: []string{"private-ai-internal"},
		Environment: map[string]string{"OLLAMA_KEEP_ALIVE": "5m"},
		Volumes:     []selectedPaaSRuntimeVolume{{ID: "models", Target: "/root/.ollama", Class: "persistent", Backup: false}},
		Health:      selectedPaaSRuntimeHealth{Kind: "command", Command: []string{"ollama", "list"}},
		Resources:   &selectedPaaSRuntimeLimits{MemoryLimit: "10g", MemoryReservation: "1g"},
	}}
}

type anythingLLMWorkloadBundleRenderer struct{ contract RendererContract }

func newAnythingLLMWorkloadBundleRenderer() anythingLLMWorkloadBundleRenderer {
	return anythingLLMWorkloadBundleRenderer{contract: AnythingLLMWorkloadBundleRendererContract()}
}

func (r anythingLLMWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateAnythingLLMWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.anythingllm-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: anythingLLMWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseAnythingLLMWorkloadBundle validates the closed generated artifact
// before any selected-PaaS owner may consume it.
func ParseAnythingLLMWorkloadBundle(data []byte) (AnythingLLMWorkloadBundleDescriptor, error) {
	path := "anythingLLMWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return AnythingLLMWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed AnythingLLM workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "AnythingLLMWorkloadBundle" ||
		bundle.Workload.Ref != "ai" || bundle.Workload.AlternativeRef != "anythingllm" ||
		bundle.Workload.ModuleRef != anythingLLMWorkloadModuleID || bundle.Workload.Release != anythingLLMRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != anythingLLMWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return AnythingLLMWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed AnythingLLM "+anythingLLMRelease+" contract")
	}
	companionSlots, err := companionSecretSlotsOf(anythingLLMWorkloadModuleID, bundle.Components)
	if err != nil || !validPrivateAISecretRefs(bundle.SecretRefs, false, companionSlots) {
		return AnythingLLMWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires opaque owner-password and session-key references and a companion secret reference exactly while its add-on is wired")
	}
	if len(bundle.ConfigFiles) != 0 {
		return AnythingLLMWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "AnythingLLM accepts no startup configuration overrides")
	}
	if err := validateAnythingLLMRuntimeComponents(bundle.Components, path+".components"); err != nil {
		return AnythingLLMWorkloadBundleDescriptor{}, err
	}
	if err := validateAnythingLLMServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return AnythingLLMWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, anythingLLMWorkloadModuleID, "ai", anythingLLMPort, path+".deliveryRoute"); err != nil {
			return AnythingLLMWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := AnythingLLMWorkloadBundleDescriptor{
		WorkloadRef: "ai", ModuleRef: anythingLLMWorkloadModuleID, Release: anythingLLMRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Components: make([]SelectedPaaSWorkloadComponentDescriptor, len(bundle.Components)),
	}
	if bundle.DeliveryRoute != nil {
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

//nolint:gocyclo // Keep the complete AnythingLLM authority check at one boundary.
func validateAnythingLLMWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + anythingLLMWorkloadModuleID + ".renderUnits." + anythingLLMWorkloadUnitID
	if unit.ModuleID() != anythingLLMWorkloadModuleID || unit.ID() != anythingLLMWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", anythingLLMWorkloadModuleID, anythingLLMWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered AnythingLLM workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != anythingLLMImageRef ||
		!hasDigest || imageDigest != anythingLLMImageDigest || !hasEntry || entry != anythingLLMWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact AnythingLLM image contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "AnythingLLM requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	// AnythingLLM declares no owner settings; the kombify AI connector belongs to
	// the Open WebUI alternative only.
	deliveryRoute, companions, _, err := validateApplicationDeliveryInputsWithCompanions(unit, anythingLLMWorkloadModuleID, "ai", anythingLLMPort, nil, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"owner-password", "session-key"}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "AnythingLLM requires owner-password and session-key slots")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil ||
		!validPrivateAISecretRefs(secretRefs, false, nil) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires opaque owner-password and session-key references and no secret material")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".interfaces", "selected-PaaS bundle receives no host, socket, or runtime-network authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != anythingLLMWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", anythingLLMWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	// AnythingLLM is wired to the selected web-search add-on only while it is
	// selected on this node; ComfyUI coexists without wiring.
	for index := range components {
		if err := materializeCompanionEnvironment(anythingLLMWorkloadModuleID, &components[index], companions, path+".runtime.components"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
		if err := materializeCompanionSecretEnvironment(anythingLLMWorkloadModuleID, &components[index], companions, secretRefs, path+".runtime.components"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
	}
	if err := validateAnythingLLMRuntimeComponents(components, path+".runtime.components"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if accelerator, selected := unit.ModuleAccelerator(); selected {
		components, err = applyModuleAccelerator(anythingLLMWorkloadModuleID, components, accelerator, path+".acceleratorProfile")
		if err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
		if err := validateAnythingLLMRuntimeComponents(components, path+".runtime.components"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact AI endpoint")
	}
	if err := validateAnythingLLMServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "AnythingLLMWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "ai", "anythingllm"
	bundle.Workload.ModuleRef, bundle.Workload.Release = anythingLLMWorkloadModuleID, anythingLLMRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

// validateAnythingLLMRuntimeComponents admits exactly the closed graph: the
// AnythingLLM component with its pinned private configuration plus governed
// companion wiring, and the Private AI Ollama component, optionally with the
// module's accelerator grant.
func validateAnythingLLMRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) error {
	expected := anythingLLMExpectedComponents()
	if len(components) != len(expected) {
		return fail(ErrInvalidPlan, path, "requires AnythingLLM and Ollama")
	}
	for index, actual := range components {
		want := expected[index]
		if actual.ID != want.ID {
			return fail(ErrInvalidPlan, path, "AI runtime component order differs from the closed AnythingLLM contract")
		}
		if len(actual.CompanionEnvironment) != 0 || len(actual.CompanionSecretEnvironment) != 0 {
			return fail(ErrInvalidPlan, path, "rendered components carry only materialized companion wiring")
		}
		switch actual.ID {
		case "anythingllm":
			if actual.Accelerator != nil {
				return fail(ErrInvalidPlan, path, "AnythingLLM runs on the CPU")
			}
			environment, err := splitCompanionEnvironment(anythingLLMWorkloadModuleID, actual.ID, actual.Environment)
			if err != nil {
				return fail(ErrInvalidPlan, path, "AnythingLLM companion wiring differs from the governed web-search add-on")
			}
			if !sameEnvironment(environment, want.Environment) {
				return fail(ErrInvalidPlan, path, "AnythingLLM must retain its private Ollama, LanceDB and telemetry configuration")
			}
			secretEnvironment, _, err := splitCompanionSecretEnvironment(anythingLLMWorkloadModuleID, actual.ID, actual.SecretEnvironment)
			if err != nil || !sameEnvironment(secretEnvironment, want.SecretEnvironment) {
				return fail(ErrInvalidPlan, path, "AnythingLLM companion secret wiring differs from the governed speech add-on")
			}
			actual.Environment = maps.Clone(want.Environment)
			actual.SecretEnvironment = maps.Clone(want.SecretEnvironment)
		case "ollama":
			if !validPrivateAIOllamaImage(actual) {
				return fail(ErrInvalidPlan, path, "Ollama runtime must not implicitly download models")
			}
			actual.Image, actual.Accelerator = want.Image, nil
		}
		if !reflect.DeepEqual(actual, want) {
			return fail(ErrInvalidPlan, path, "%s runtime identity differs from the closed AnythingLLM contract", actual.ID)
		}
	}
	return nil
}

func validateAnythingLLMServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "ai" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != anythingLLMPort ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "anythingllm-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "ai" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"http", "https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "AI route authority differs from the governed AnythingLLM endpoint")
	}
	return nil
}
