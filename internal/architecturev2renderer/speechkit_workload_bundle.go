package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// Private AI speech (docs/use-case-expansion/ai-agents.md): the kombify
// SpeechKit server with local providers only, selected with the speech
// capability module. The server profile rendered here enables no cloud
// provider: speech-to-text runs in the whisper.cpp sidecar, text-to-speech in
// the Kokoro-FastAPI sidecar and Assist on the node's Ollama. Every /v1
// route, including the OpenAI-compatible audio routes the chat module uses,
// requires the bearer token from custody. Only the server joins the private
// AI network; it has no route of its own in this slice.
const (
	speechKitWorkloadModuleID    = "stackkits-speechkit-runtime"
	speechKitWorkloadUnitID      = "speechkit"
	speechKitWorkloadRef         = "ai-speech"
	speechKitWorkloadTemplateRef = "builtin://workloads/speechkit/bundle/v2.json"
	speechKitWorkloadVersion     = "2.0.0"
	speechKitWorkloadOutputRef   = "workloads/speechkit/bundle.json"
	speechKitPort                = 8080
	speechKitWhisperPort         = 8080
	speechKitTTSPort             = 8880

	// speechKitConfigPath is the profile path the server image's CMD names;
	// the config volume lets the governed file be mounted there.
	speechKitConfigPath = "/etc/speechkit/config.toml"
	speechKitTokenSlot  = "server-token"

	// speechKitWhisperModelFile is the MIT-licensed whisper.cpp model the
	// sidecar fetches at first start and verifies by this SHA-256
	// (huggingface.co/ggerganov/whisper.cpp, 487,601,967 bytes).
	speechKitWhisperModelFile   = "ggml-small.bin"
	speechKitWhisperModelSHA256 = "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b"
)

var (
	//go:embed assets/speechkit/server.toml
	speechKitServerProfile string

	speechKitSecretSlots = []string{speechKitTokenSlot}
)

// speechKitConfigFiles is the governed read-only server profile. It carries
// no secret: the bearer token arrives through its named variable.
func speechKitConfigFiles() []selectedPaaSConfigFile {
	return []selectedPaaSConfigFile{{Path: speechKitConfigPath, Body: speechKitServerProfile}}
}

// speechKitAssetsDigest binds the renderer contract to the exact governed
// profile, so any edit to it changes the CUE-declared contract hash.
func speechKitAssetsDigest() string {
	digest := sha256.New()
	for _, file := range speechKitConfigFiles() {
		digest.Write([]byte(file.Path + "\x00" + file.Body + "\x00"))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func speechKitWorkloadRendererSchema() string {
	return `stackkit.workload-bundle/v2|SpeechKitWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:speechkit,speechkit-whisper,speechkit-tts|providers:local-only|egress:whisper-model-download|volumes:data,config,models|governed-files:sha256:` +
		speechKitAssetsDigest() + `|whisper-model:` + speechKitWhisperModelFile + `@sha256:` + speechKitWhisperModelSHA256 +
		`|release:` + speechkitRelease + `|secret-material:references-only|peer:ai/private-ai-internal`
}

// SpeechKitWorkloadBundleDescriptor is the closed runtime artifact accepted
// by the standalone application adapter. Secret references stay opaque.
type SpeechKitWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func SpeechKitWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(speechKitWorkloadRendererSchema()))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: speechKitWorkloadTemplateRef,
		Version: speechKitWorkloadVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type speechKitWorkloadBundleRenderer struct{ contract RendererContract }

func newSpeechKitWorkloadBundleRenderer() speechKitWorkloadBundleRenderer {
	return speechKitWorkloadBundleRenderer{contract: SpeechKitWorkloadBundleRendererContract()}
}

func (r speechKitWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateSpeechKitWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.speechkit-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: speechKitWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseSpeechKitWorkloadBundle validates the closed artifact before a runtime
// owner may consume it.
func ParseSpeechKitWorkloadBundle(data []byte) (SpeechKitWorkloadBundleDescriptor, error) {
	path := "speechKitWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return SpeechKitWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed SpeechKit workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "SpeechKitWorkloadBundle" ||
		bundle.Workload.Ref != speechKitWorkloadRef || bundle.Workload.AlternativeRef != "speechkit" ||
		bundle.Workload.ModuleRef != speechKitWorkloadModuleID || bundle.Workload.Release != speechkitRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != speechKitWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return SpeechKitWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed SpeechKit "+speechkitRelease+" contract")
	}
	if !validSpeechKitSecretRefs(bundle.SecretRefs) {
		return SpeechKitWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires an opaque server-token reference")
	}
	if !reflect.DeepEqual(bundle.ConfigFiles, speechKitConfigFiles()) {
		return SpeechKitWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "SpeechKit server profile differs from the governed renderer output")
	}
	if err := validateSpeechKitRuntimeComponents(bundle.Components, path+".components"); err != nil {
		return SpeechKitWorkloadBundleDescriptor{}, err
	}
	if err := validateSpeechKitServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return SpeechKitWorkloadBundleDescriptor{}, err
	}
	descriptor := SpeechKitWorkloadBundleDescriptor{
		WorkloadRef: speechKitWorkloadRef, ModuleRef: speechKitWorkloadModuleID, Release: speechkitRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Components: make([]SelectedPaaSWorkloadComponentDescriptor, len(bundle.Components)),
	}
	if bundle.DeliveryRoute != nil {
		if err := validateSpeechKitRoute(*bundle.DeliveryRoute, path+".deliveryRoute"); err != nil {
			return SpeechKitWorkloadBundleDescriptor{}, err
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
func validateSpeechKitWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + speechKitWorkloadModuleID + ".renderUnits." + speechKitWorkloadUnitID
	if unit.ModuleID() != speechKitWorkloadModuleID || unit.ID() != speechKitWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", speechKitWorkloadModuleID, speechKitWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered SpeechKit workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != speechkitImageRef ||
		!hasDigest || imageDigest != speechkitImageDigest || !hasEntry || entry != speechKitWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact SpeechKit "+speechkitRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "SpeechKit requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, speechKitWorkloadModuleID, speechKitWorkloadRef, speechKitPort, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute != nil && deliveryRoute.Exposure == "public" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "SpeechKit serves the node's chat module and is never published")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || !validSpeechKitSecretRefs(secretRefs) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires an opaque server-token reference and no secret material")
	}
	if !sameStringSet(unit.SecretInputRefs(), speechKitSecretSlots) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "SpeechKit bundle accepts no free input or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != speechKitWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", speechKitWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	if err := validateSpeechKitRuntimeComponents(components, path+".runtime.components"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact SpeechKit endpoint")
	}
	if err := validateSpeechKitServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "SpeechKitWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
		ConfigFiles: speechKitConfigFiles(),
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = speechKitWorkloadRef, "speechkit"
	bundle.Workload.ModuleRef, bundle.Workload.Release = speechKitWorkloadModuleID, speechkitRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validSpeechKitSecretRefs(refs map[string]string) bool {
	if len(refs) != len(speechKitSecretSlots) {
		return false
	}
	for _, slot := range speechKitSecretSlots {
		if !validSecretReference(refs[slot]) {
			return false
		}
	}
	return true
}

// speechKitWhisperEntrypoint fetches the governed model once into the models
// cache volume, verifies it by SHA-256 and then hands over to whisper-server
// on the OpenAI-compatible inference path. No shell variable is used.
func speechKitWhisperEntrypoint() []string {
	model := "/models/whisper/" + speechKitWhisperModelFile
	return []string{"/bin/sh", "-ec",
		"mkdir -p /models/whisper; test -s " + model + " || { curl -fsSL --retry 5 -o " + model + ".part https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + speechKitWhisperModelFile +
			" && echo '" + speechKitWhisperModelSHA256 + "  " + model + ".part' | sha256sum -c - && mv " + model + ".part " + model + "; }; exec /app/build/bin/whisper-server --host 0.0.0.0 --port 8080 --model " + model + " --inference-path /v1/audio/transcriptions"}
}

// speechKitComponents are the three governed components. The server's data
// volume (SQLite store: transcripts, vocabulary, customizations) is the
// backup source; the config volume carries the rendered profile and the
// whisper models volume is a re-downloadable cache.
func speechKitComponents() []selectedPaaSRuntimeComponent {
	return []selectedPaaSRuntimeComponent{
		{
			ID: speechKitWorkloadUnitID, Role: "application", Lifecycle: "daemon",
			Image:       selectedPaaSRuntimeImage{Ref: speechkitImageRef, Digest: speechkitImageDigest},
			DependsOn:   []string{"speechkit-tts", "speechkit-whisper"},
			NetworkRefs: []string{"speechkit-internal"},
			SecretEnvironment: map[string]string{
				"SPEECHKIT_SERVER_TOKEN": speechKitTokenSlot,
			},
			Volumes: []selectedPaaSRuntimeVolume{
				{ID: "data", Target: "/var/lib/speechkit/data", Class: "persistent", Backup: true},
				{ID: "config", Target: "/etc/speechkit", Class: "cache"},
			},
			Health:       selectedPaaSRuntimeHealth{Kind: "http", Path: "/healthz", Port: speechKitPort},
			Resources:    &selectedPaaSRuntimeLimits{MemoryLimit: "1g", MemoryReservation: "256m"},
			PeerNetworks: []selectedPaaSPeerNetwork{{WorkloadRef: "ai", NetworkRef: "private-ai-internal"}},
		},
		{
			ID: "speechkit-whisper", Role: "machine-learning", Lifecycle: "daemon", Egress: true,
			Image:       selectedPaaSRuntimeImage{Ref: speechkitWhisperImageRef, Digest: speechkitWhisperImageDigest},
			DependsOn:   []string{},
			NetworkRefs: []string{"speechkit-internal"},
			Entrypoint:  speechKitWhisperEntrypoint(),
			Volumes: []selectedPaaSRuntimeVolume{
				{ID: "models", Target: "/models", Class: "cache"},
			},
			Health:    selectedPaaSRuntimeHealth{Kind: "http", Path: "/", Port: speechKitWhisperPort},
			Resources: &selectedPaaSRuntimeLimits{MemoryLimit: "3g", MemoryReservation: "1g"},
		},
		{
			ID: "speechkit-tts", Role: "machine-learning", Lifecycle: "daemon",
			Image:       selectedPaaSRuntimeImage{Ref: speechkitTTSImageRef, Digest: speechkitTTSImageDigest},
			DependsOn:   []string{},
			NetworkRefs: []string{"speechkit-internal"},
			Health:      selectedPaaSRuntimeHealth{Kind: "http", Path: "/health", Port: speechKitTTSPort},
			Resources:   &selectedPaaSRuntimeLimits{MemoryLimit: "2g", MemoryReservation: "512m"},
		},
	}
}

// validateSpeechKitRuntimeComponents admits exactly the governed graph: the
// server with its token and the two local provider sidecars; no accelerator,
// no host rights, no cloud egress from the server.
func validateSpeechKitRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) error {
	expected := speechKitComponents()
	if len(components) != len(expected) {
		return fail(ErrInvalidPlan, path, "SpeechKit runtime graph differs from the closed "+speechkitRelease+" contract")
	}
	byID := func(a, b selectedPaaSRuntimeVolume) int { return strings.Compare(a.ID, b.ID) }
	for index, want := range expected {
		actual := components[index]
		if actual.ID != want.ID {
			return fail(ErrInvalidPlan, path, "SpeechKit runtime graph differs from the closed "+speechkitRelease+" contract")
		}
		if actual.Accelerator != nil {
			return fail(ErrInvalidPlan, path+".accelerator", "SpeechKit runs its providers on the CPU and admits no accelerator grant")
		}
		if err := validatePeerNetworks(speechKitWorkloadModuleID, actual, path); err != nil {
			return err
		}
		if !sameEnvironment(actual.Environment, want.Environment) || !sameEnvironment(actual.SecretEnvironment, want.SecretEnvironment) {
			return fail(ErrInvalidPlan, path, "SpeechKit environment differs from the closed "+speechkitRelease+" contract")
		}
		actual.Environment, actual.SecretEnvironment = maps.Clone(want.Environment), maps.Clone(want.SecretEnvironment)
		// The plan orders volumes by its own rule; the set is what is governed.
		actual.Volumes = slices.SortedFunc(slices.Values(actual.Volumes), byID)
		want.Volumes = slices.SortedFunc(slices.Values(want.Volumes), byID)
		if !reflect.DeepEqual(actual, want) {
			return fail(ErrInvalidPlan, path, "SpeechKit runtime graph differs from the closed "+speechkitRelease+" contract")
		}
	}
	return nil
}

func validateSpeechKitServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != speechKitWorkloadRef || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != speechKitPort ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "speechkit-http" || endpoint.IngressAuth != "forward-auth" ||
		endpoint.Data.BindingRef != speechKitWorkloadRef || endpoint.Data.Locality != "primary-site" ||
		!exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private"}) {
		return fail(ErrInvalidPlan, path, "route authority differs from the governed private SpeechKit endpoint")
	}
	return nil
}

func validateSpeechKitRoute(route applicationDeliveryRoute, path string) error {
	if err := validateParsedApplicationDeliveryRoute(route, speechKitWorkloadModuleID, speechKitWorkloadRef, speechKitPort, path); err != nil {
		return err
	}
	if route.Exposure == "public" {
		return fail(ErrInvalidPlan, path, "SpeechKit serves the node's chat module and is never published")
	}
	return nil
}
