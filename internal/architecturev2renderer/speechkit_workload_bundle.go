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
	"strconv"
	"strings"
)

// Private AI speech (docs/use-case-expansion/ai-agents.md): the kombify
// SpeechKit server with local providers only, selected with the speech
// capability module. The server profile rendered here enables no cloud
// provider: speech-to-text runs in the Parakeet sidecar of the same image,
// text-to-speech in the Kokoro-FastAPI sidecar and Assist on the node's
// Ollama. Every /v1 route, including the OpenAI-compatible audio routes the
// chat module and the household's devices use, requires a bearer token: the
// server token from custody or a paired device's own token.
//
// The server listens on the LAN at port 18080 so the Box, phones and the
// desktop find it, and a host-network announcer advertises it over mDNS as
// _speechkit._tcp (multicast never leaves a bridge network). The chat module
// still reaches it on the private AI network; the kit's browser forward-auth
// does not apply to devices, which pair with their own token.
const (
	speechKitWorkloadModuleID     = "stackkits-speechkit-runtime"
	speechKitWorkloadUnitID       = "speechkit"
	speechKitWorkloadRef          = "ai-speech"
	speechKitWorkloadTemplateRef  = "builtin://workloads/speechkit/bundle/v2.json"
	speechKitWorkloadVersion      = "2.0.0"
	speechKitWorkloadOutputRef    = "workloads/speechkit/bundle.json"
	speechKitPort                 = 18080
	speechKitSTTComponentID       = "speechkit-stt"
	speechKitSTTPort              = 8080
	speechKitTTSComponentID       = "speechkit-tts"
	speechKitTTSPort              = 8880
	speechKitDiscoveryComponentID = "speechkit-discovery"

	// speechKitConfigPath is the profile path the server image's CMD names;
	// the config volume lets the governed file be mounted there.
	speechKitConfigPath = "/etc/speechkit/config.toml"
	speechKitTokenSlot  = "server-token"

	// The Parakeet Ultra int8 model in the sherpa-onnx layout: NVIDIA Parakeet
	// TDT 0.6B v3 post-trained by Moondream (CC-BY-4.0), the revision and
	// SHA-256 values SpeechKit's download catalog pins for parakeet.ultra-int8.
	speechKitSTTModelDir  = "/var/lib/speechkit/models/parakeet-ultra-int8"
	speechKitSTTModelBase = "https://huggingface.co/tpmaerk/parakeet-ultra-sherpa-onnx-int8/resolve/b86784a3a5356cbd126f710b61cd90c5fe5898f1"
)

// speechKitSTTFiles are the four model files, each verified before it is
// renamed from .part.
var speechKitSTTFiles = []struct{ Name, SHA256 string }{
	{"encoder.int8.onnx", "f4d06712d32431b1718eceba2153c314ac4425e8fe708a3534f5e56c5ad53697"},
	{"decoder.int8.onnx", "94373c6405c8149d1c9d1e9862514e0e3e88dab1c7de18fac9d5df9c6ba5efdd"},
	{"joiner.int8.onnx", "8a71aaccfdba3d451775507a889afd2257d2e754463fd81095a8c8f290f924c2"},
	{"tokens.txt", "d58544679ea4bc6ac563d1f545eb7d474bd6cfa467f0a6e2c1dc1c7d37e3c35d"},
}

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
	return `stackkit.workload-bundle/v2|SpeechKitWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:speechkit,speechkit-stt,speechkit-tts,speechkit-discovery|providers:local-only|egress:parakeet-model-download|volumes:data,config,models|governed-files:sha256:` +
		speechKitAssetsDigest() + `|stt-model:parakeet-ultra-int8@sha256:` + speechKitSTTFiles[0].SHA256 +
		`|lan-listener:` + strconv.Itoa(speechKitPort) + `|discovery:_speechkit._tcp|host-network:announcer-only|release:` + speechkitRelease + `|secret-material:references-only|peer:ai/private-ai-internal`
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

// speechKitSTTEntrypoint fetches the governed Parakeet model files once into
// the models cache volume, verifies each by SHA-256 and then hands over to
// speechkit-stt-sherpa on the OpenAI-compatible transcription path. A failed
// check leaves only the .part file, so the next start retries that file. The
// catalog declares the identical command (platform-eu2nt.8): the resolved
// plan is the executable authority and this renderer must match it.
func speechKitSTTEntrypoint() []string {
	dir := speechKitSTTModelDir
	var b strings.Builder
	b.WriteString("mkdir -p " + dir + "; ")
	for _, file := range speechKitSTTFiles {
		b.WriteString("test -s " + dir + "/" + file.Name + " || { curl -fsSL --retry 5 -o " + dir + "/" + file.Name + ".part " +
			speechKitSTTModelBase + "/" + file.Name +
			" && echo '" + file.SHA256 + "  " + dir + "/" + file.Name + ".part' | sha256sum -c - && mv " + dir + "/" + file.Name + ".part " + dir + "/" + file.Name + "; }; ")
	}
	b.WriteString("exec /app/speechkit-stt-sherpa --host 0.0.0.0 --port " + strconv.Itoa(speechKitSTTPort) +
		" --model " + dir + " --inference-path /v1/audio/transcriptions")
	return []string{"/bin/sh", "-ec", b.String()}
}

// speechKitDiscoveryCommand is the announce-only mode of the server image: it
// binds nothing but mDNS and announces the server that listens on the host's
// port 18080. The probe is its health check and makes no network call.
func speechKitDiscoveryCommand() []string {
	return []string{"--discovery-only", "--discovery-port", strconv.Itoa(speechKitPort),
		"--discovery-modes", "dictation,assist,voiceagent"}
}

// speechKitComponents are the governed components. The server's data volume
// (SQLite store: transcripts, vocabulary, customizations) is the backup
// source; the config volume carries the rendered profile and the Parakeet
// models volume is a re-downloadable cache.
func speechKitComponents() []selectedPaaSRuntimeComponent {
	return []selectedPaaSRuntimeComponent{
		{
			ID: speechKitWorkloadUnitID, Role: "application", Lifecycle: "daemon",
			Image:       selectedPaaSRuntimeImage{Ref: speechkitImageRef, Digest: speechkitImageDigest},
			DependsOn:   []string{speechKitSTTComponentID, speechKitTTSComponentID},
			NetworkRefs: []string{"speechkit-internal"},
			SecretEnvironment: map[string]string{
				"SPEECHKIT_SERVER_TOKEN": speechKitTokenSlot,
			},
			// The Box sends no Origin header on its ticketed Voice Agent
			// WebSocket; every other client still has to send one.
			Environment: map[string]string{"SPEECHKIT_ALLOW_EMPTY_ORIGIN": "1"},
			Volumes: []selectedPaaSRuntimeVolume{
				{ID: "data", Target: "/var/lib/speechkit/data", Class: "persistent", Backup: true},
				{ID: "config", Target: "/etc/speechkit", Class: "cache"},
			},
			// Selecting the speech module is the owner's choice of a LAN
			// voice server: the listener is on with the module.
			LANListeners: []selectedPaaSLANListener{{Port: speechKitPort, Protocol: "tcp"}},
			Health:       selectedPaaSRuntimeHealth{Kind: "http", Path: "/healthz", Port: speechKitPort},
			Resources:    &selectedPaaSRuntimeLimits{MemoryLimit: "1g", MemoryReservation: "256m"},
			PeerNetworks: []selectedPaaSPeerNetwork{{WorkloadRef: "ai", NetworkRef: "private-ai-internal"}},
		},
		{
			// Local speech-to-text: the server image's speechkit-stt-sherpa
			// runs NVIDIA Parakeet TDT 0.6B v3 (Parakeet Ultra, int8, 25
			// European languages) behind the OpenAI-compatible route.
			ID: speechKitSTTComponentID, Role: "machine-learning", Lifecycle: "daemon", Egress: true,
			Image:       selectedPaaSRuntimeImage{Ref: speechkitImageRef, Digest: speechkitImageDigest},
			DependsOn:   []string{},
			NetworkRefs: []string{"speechkit-internal"},
			Entrypoint:  speechKitSTTEntrypoint(),
			Volumes: []selectedPaaSRuntimeVolume{
				{ID: "models", Target: "/var/lib/speechkit/models", Class: "cache"},
			},
			Health:    selectedPaaSRuntimeHealth{Kind: "http", Path: "/health", Port: speechKitSTTPort},
			Resources: &selectedPaaSRuntimeLimits{MemoryLimit: "3g", MemoryReservation: "1g"},
		},
		{
			ID: speechKitTTSComponentID, Role: "machine-learning", Lifecycle: "daemon",
			Image:       selectedPaaSRuntimeImage{Ref: speechkitTTSImageRef, Digest: speechkitTTSImageDigest},
			DependsOn:   []string{},
			NetworkRefs: []string{"speechkit-internal"},
			Health:      selectedPaaSRuntimeHealth{Kind: "http", Path: "/health", Port: speechKitTTSPort},
			Resources:   &selectedPaaSRuntimeLimits{MemoryLimit: "2g", MemoryReservation: "512m"},
		},
		{
			// LAN discovery: mDNS multicast never leaves a bridge network, so
			// the same image announces _speechkit._tcp from the host network
			// and does nothing else (no listener, store, token or provider).
			ID: speechKitDiscoveryComponentID, Role: "application", Lifecycle: "daemon",
			Image:       selectedPaaSRuntimeImage{Ref: speechkitImageRef, Digest: speechkitImageDigest},
			DependsOn:   []string{speechKitWorkloadUnitID},
			NetworkRefs: []string{},
			HostNetwork: true,
			Entrypoint:  []string{"/app/speechkit-server"},
			Command:     speechKitDiscoveryCommand(),
			Health: selectedPaaSRuntimeHealth{Kind: "command", Command: []string{
				"/app/speechkit-server", "--discovery-only", "--probe",
			}},
			Resources: &selectedPaaSRuntimeLimits{MemoryLimit: "64m", MemoryReservation: "16m"},
		},
	}
}

// validateSpeechKitRuntimeComponents admits exactly the governed graph: the
// server with its token, the two local provider sidecars and the LAN
// announcer; no accelerator, no cloud egress from the server. The plan's
// component list is set-semantic (the canonical form sorts it), so components
// are matched by ID, never by position.
func validateSpeechKitRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) error {
	expected := speechKitComponents()
	if len(components) != len(expected) {
		return fail(ErrInvalidPlan, path, "SpeechKit runtime graph differs from the closed "+speechkitRelease+" contract")
	}
	wantByID := make(map[string]selectedPaaSRuntimeComponent, len(expected))
	for _, want := range expected {
		wantByID[want.ID] = want
	}
	byVolume := func(a, b selectedPaaSRuntimeVolume) int { return strings.Compare(a.ID, b.ID) }
	seen := make(map[string]struct{}, len(expected))
	for _, actual := range components {
		want, governed := wantByID[actual.ID]
		if _, duplicate := seen[actual.ID]; !governed || duplicate {
			return fail(ErrInvalidPlan, path, "SpeechKit runtime graph differs from the closed "+speechkitRelease+" contract")
		}
		seen[actual.ID] = struct{}{}
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
		actual.Volumes = slices.SortedFunc(slices.Values(actual.Volumes), byVolume)
		want.Volumes = slices.SortedFunc(slices.Values(want.Volumes), byVolume)
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
