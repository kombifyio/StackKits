package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const (
	privateAIWorkloadModuleID    = "stackkits-private-ai-runtime"
	privateAIWorkloadUnitID      = "private-ai"
	privateAIWorkloadTemplateRef = "builtin://workloads/private-ai/bundle/v2.json"
	privateAIWorkloadVersion     = "2.0.0"
	privateAIWorkloadOutputRef   = "workloads/private-ai/bundle.json"
)

const privateAIWorkloadRendererSchema = `stackkit.workload-bundle/v2|PrivateAIWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:privateAI|release:` + privateAIRelease + `|secret-material:not-included|companions:ai-search,ai-documents,ai-image-video,ai-assistant,ai-harness,ai-control-plane,ai-speech|optional:kombify-ai-connector:kombify-connector-setting:issued-token-file`

// The optional kombify AI connector (decision record 2026-09-27 §4). Its
// token is issued by the owner's kombify AI, never minted: `stackkit setup
// ai-connect` custodies it under PrivateAIConnectorTokenRef and turns on
// PrivateAIConnectorSetting. Plan secret inputs are always required and
// minted, so the token is not one; the renderer binds the fixed workload
// reference only while the connector is on, and Apply fails closed when the
// custody is absent.
const (
	PrivateAIConnectorComponent = "kombify-ai-connector"
	PrivateAIConnectorSetting   = "kombify-connector"
	PrivateAIConnectorTokenSlot = "kombify-ai-connector-token"
	PrivateAIConnectorTokenRef  = "secret://workloads/ai/kombify-ai-connector-token"
	privateAIConnectorTokenFile = "/run/secrets/kombify-ai-connector-token"
	privateAIConnectorMetrics   = "127.0.0.1:20241"
)

var (
	privateAIConnectorCommand     = []string{"tunnel", "--no-autoupdate", "--metrics", privateAIConnectorMetrics, "run", "--token-file", privateAIConnectorTokenFile}
	privateAIConnectorHealth      = []string{"cloudflared", "tunnel", "--metrics", privateAIConnectorMetrics, "ready"}
	privateAIConnectorSecretFiles = []selectedPaaSSecretFile{{Slot: PrivateAIConnectorTokenSlot, Target: privateAIConnectorTokenFile, PathEnvironment: "TUNNEL_TOKEN_FILE", UID: 65532, GID: 65532}}
)

// PrivateAIWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the selected-PaaS executor. OwnerPasswordRef is opaque.
type PrivateAIWorkloadBundleDescriptor struct {
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

func PrivateAIWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(privateAIWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: privateAIWorkloadTemplateRef, Version: privateAIWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type privateAIWorkloadBundleRenderer struct{ contract RendererContract }

func newPrivateAIWorkloadBundleRenderer() privateAIWorkloadBundleRenderer {
	return privateAIWorkloadBundleRenderer{contract: PrivateAIWorkloadBundleRendererContract()}
}

func (r privateAIWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validatePrivateAIWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.privateAI-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: privateAIWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParsePrivateAIWorkloadBundle validates the closed generated artifact before
// any selected-PaaS owner may consume it.
func ParsePrivateAIWorkloadBundle(data []byte) (PrivateAIWorkloadBundleDescriptor, error) {
	path := "privateAIWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return PrivateAIWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed PrivateAI workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "PrivateAIWorkloadBundle" ||
		bundle.Workload.Ref != "ai" || bundle.Workload.AlternativeRef != "private-ai" ||
		bundle.Workload.ModuleRef != privateAIWorkloadModuleID || bundle.Workload.Release != privateAIRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != "open-webui" ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return PrivateAIWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed PrivateAI image contract")
	}
	companionSlots, err := companionSecretSlotsOf(privateAIWorkloadModuleID, bundle.Components)
	if err != nil || !validPrivateAISecretRefs(bundle.SecretRefs, privateAIConnectorEnabled(bundle.Components), companionSlots) {
		return PrivateAIWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires opaque owner-password and session-key references, plus the connector token reference exactly while the connector runs and a companion secret reference exactly while its add-on is wired")
	}
	if len(bundle.ConfigFiles) != 0 {
		return PrivateAIWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "AI runtime accepts no startup configuration overrides")
	}
	components, err := validatePrivateAIRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return PrivateAIWorkloadBundleDescriptor{}, err
	}
	if err := validatePrivateAIServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return PrivateAIWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, privateAIWorkloadModuleID, "ai", 8080, path+".deliveryRoute"); err != nil {
			return PrivateAIWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := PrivateAIWorkloadBundleDescriptor{
		WorkloadRef: "ai", ModuleRef: privateAIWorkloadModuleID, Release: privateAIRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		OwnerPasswordRef: bundle.SecretRefs["owner-password"],
		Components:       make([]SelectedPaaSWorkloadComponentDescriptor, len(components)),
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

//nolint:gocyclo // Keep the complete PrivateAI authority check at one boundary.
func validatePrivateAIWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + privateAIWorkloadModuleID + ".renderUnits." + privateAIWorkloadUnitID
	if unit.ModuleID() != privateAIWorkloadModuleID || unit.ID() != privateAIWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", privateAIWorkloadModuleID, privateAIWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered PrivateAI workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != privateAIImageRef ||
		!hasDigest || imageDigest != privateAIImageDigest || !hasEntry || entry != "open-webui" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact PrivateAI image contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "PrivateAI requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, companions, settings, err := validateApplicationDeliveryInputsWithCompanions(unit, privateAIWorkloadModuleID, "ai", 8080, []string{PrivateAIConnectorSetting}, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"owner-password", "session-key"}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Private AI requires owner-password and session-key slots")
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
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != privateAIWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", privateAIWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, connector, err := materializePrivateAIConnector(components, settings, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if connector {
		secretRefs[PrivateAIConnectorTokenSlot] = PrivateAIConnectorTokenRef
	}
	// Open WebUI is wired to the selected add-ons only while they are selected
	// on this node; a selected add-on's custody secret travels as an opaque
	// reference under its own companion slot.
	for index := range components {
		if err := materializeCompanionEnvironment(privateAIWorkloadModuleID, &components[index], companions, path+".runtime.components"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
		if err := materializeCompanionSecretEnvironment(privateAIWorkloadModuleID, &components[index], companions, secretRefs, path+".runtime.components"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
	}
	components, err = validatePrivateAIRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if accelerator, selected := unit.ModuleAccelerator(); selected {
		components, err = applyModuleAccelerator(privateAIWorkloadModuleID, components, accelerator, path+".acceleratorProfile")
		if err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
		if _, err := validatePrivateAIRuntimeComponents(components, path+".runtime.components"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact AI endpoint")
	}
	if err := validatePrivateAIServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "PrivateAIWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "ai", "private-ai"
	bundle.Workload.ModuleRef, bundle.Workload.Release = privateAIWorkloadModuleID, privateAIRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validatePrivateAIRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 2 && (len(components) != 3 || !privateAIConnectorEnabled(components)) {
		return nil, fail(ErrInvalidPlan, path, "requires Ollama and Open WebUI, and optionally the kombify AI connector")
	}
	seen := map[string]bool{}
	for _, c := range components {
		if seen[c.ID] || c.Role != "application" || c.Lifecycle != "daemon" || len(c.Entrypoint) != 0 || c.EnabledBySetting != "" || !exactStringList(c.NetworkRefs, []string{"private-ai-internal"}) {
			return nil, fail(ErrInvalidPlan, path, "AI runtime identity or network differs")
		}
		if c.ID != PrivateAIConnectorComponent && (len(c.Command) != 0 || c.HealthFailure != "" || len(c.SecretFiles) != 0) {
			return nil, fail(ErrInvalidPlan, path, "AI runtime identity differs")
		}
		seen[c.ID] = true
		switch c.ID {
		case "open-webui":
			if c.Accelerator != nil {
				return nil, fail(ErrInvalidPlan, path, "Open WebUI runs on the CPU")
			}
			if c.Egress || len(c.CompanionEnvironment) != 0 || len(c.CompanionSecretEnvironment) != 0 {
				return nil, fail(ErrInvalidPlan, path, "only the model-serving component requests egress")
			}
			environment, err := splitCompanionEnvironment(privateAIWorkloadModuleID, c.ID, c.Environment)
			if err != nil {
				return nil, fail(ErrInvalidPlan, path, "Open WebUI companion wiring differs from the governed search and document add-ons")
			}
			secretEnvironment, _, err := splitCompanionSecretEnvironment(privateAIWorkloadModuleID, c.ID, c.SecretEnvironment)
			if err != nil {
				return nil, fail(ErrInvalidPlan, path, "Open WebUI companion secret wiring differs from the governed speech add-on")
			}
			if !exactStringList(c.DependsOn, []string{"ollama"}) || len(secretEnvironment) != 2 || c.Health.Kind != "http" || c.Health.Path != "/health" || c.Health.Port != 8080 || environment["ENABLE_PERSISTENT_CONFIG"] != "false" || environment["ENABLE_OPENAI_API"] != "false" || environment["RAG_EMBEDDING_MODEL_AUTO_UPDATE"] != "false" || environment["WHISPER_MODEL_AUTO_UPDATE"] != "false" || environment["OFFLINE_MODE"] != "true" || environment["HF_HUB_OFFLINE"] != "1" || len(environment) != 8 {
				return nil, fail(ErrInvalidPlan, path, "Open WebUI must retain its private configuration")
			}
			if c.Image.Ref != privateAIImageRef || c.Image.Digest != privateAIImageDigest || environment["OLLAMA_BASE_URL"] != "http://ollama:11434" || environment["ENABLE_SIGNUP"] != "false" || secretEnvironment["WEBUI_ADMIN_PASSWORD"] != "owner-password" || c.OwnerEnvironment["WEBUI_ADMIN_EMAIL"] != "email" || len(c.OwnerEnvironment) != 1 || secretEnvironment["WEBUI_SECRET_KEY"] != "session-key" {
				return nil, fail(ErrInvalidPlan, path, "Open WebUI owner and inference boundary differs")
			}
			if len(c.Volumes) != 1 || c.Volumes[0].ID != "data" || c.Volumes[0].Target != "/app/backend/data" || c.Volumes[0].Class != "persistent" || !c.Volumes[0].Backup || c.Volumes[0].ReadOnly || c.Volumes[0].HostPath != "" {
				return nil, fail(ErrInvalidPlan, path, "chat and owner data must persist")
			}
		case "ollama":
			if !c.Egress || len(c.CompanionEnvironment) != 0 || len(c.CompanionSecretEnvironment) != 0 {
				return nil, fail(ErrInvalidPlan, path, "explicit model downloads require Ollama egress")
			}
			if len(c.DependsOn) != 0 || len(c.SecretEnvironment) != 0 || len(c.OwnerEnvironment) != 0 || c.Health.Kind != "command" || !exactStringList(c.Health.Command, []string{"ollama", "list"}) || len(c.Environment) != 2 || c.Environment["OLLAMA_KEEP_ALIVE"] != "5m" || c.Environment["OLLAMA_CONTEXT_LENGTH"] != "8192" {
				return nil, fail(ErrInvalidPlan, path, "Ollama must retain its private serving configuration")
			}
			if !validPrivateAIOllamaImage(c) || len(c.Command) != 0 || len(c.Entrypoint) != 0 {
				return nil, fail(ErrInvalidPlan, path, "Ollama runtime must not implicitly download models")
			}
			if len(c.Volumes) != 1 || c.Volumes[0].ID != "models" || c.Volumes[0].Target != "/root/.ollama" || c.Volumes[0].Class != "persistent" || c.Volumes[0].ReadOnly || c.Volumes[0].HostPath != "" {
				return nil, fail(ErrInvalidPlan, path, "models must persist")
			}
		case PrivateAIConnectorComponent:
			if err := validatePrivateAIConnector(c, path); err != nil {
				return nil, err
			}
		default:
			return nil, fail(ErrInvalidPlan, path, "unknown AI component")
		}
	}
	if !seen["open-webui"] || !seen["ollama"] {
		return nil, fail(ErrInvalidPlan, path, "requires Ollama and Open WebUI")
	}
	return components, nil
}

// materializePrivateAIConnector checks the catalog declaration of the optional
// connector and keeps it only while the owner turned its boolean setting on.
// Every other component is unconditional.
func materializePrivateAIConnector(components []selectedPaaSRuntimeComponent, settings map[string]json.RawMessage, path string) ([]selectedPaaSRuntimeComponent, bool, error) {
	enabled := false
	if raw, set := settings[PrivateAIConnectorSetting]; set {
		if err := json.Unmarshal(raw, &enabled); err != nil {
			return nil, false, fail(ErrInvalidPlan, path+"."+PrivateAIConnectorSetting, "must be a boolean")
		}
	}
	result := make([]selectedPaaSRuntimeComponent, 0, len(components))
	declared := false
	for _, component := range components {
		if component.ID != PrivateAIConnectorComponent {
			if component.EnabledBySetting != "" {
				return nil, false, fail(ErrInvalidPlan, path, "only the kombify AI connector is optional")
			}
			result = append(result, component)
			continue
		}
		if declared || component.EnabledBySetting != PrivateAIConnectorSetting {
			return nil, false, fail(ErrInvalidPlan, path, "the kombify AI connector must be declared once and gated by its setting")
		}
		declared = true
		if enabled {
			component.EnabledBySetting = ""
			result = append(result, component)
		}
	}
	if enabled && !declared {
		return nil, false, fail(ErrInvalidPlan, path, "the kombify AI connector setting is on but the catalog declares no connector")
	}
	return result, enabled, nil
}

func privateAIConnectorEnabled(components []selectedPaaSRuntimeComponent) bool {
	for _, component := range components {
		if component.ID == PrivateAIConnectorComponent {
			return true
		}
	}
	return false
}

// validatePrivateAIConnector admits only the outbound-only connector: the
// pinned cloudflared image on the internal network next to Ollama, reading its
// token from a read-only file, with egress, no port, no volume, no route and
// no other credential.
func validatePrivateAIConnector(c selectedPaaSRuntimeComponent, path string) error {
	if c.Image.Ref != kombifyAIConnectorImageRef || c.Image.Digest != kombifyAIConnectorImageDigest ||
		!validPrivateAIConnectorCommand(c.Command) ||
		!c.Egress || c.HealthFailure != "degraded" || !exactStringList(c.DependsOn, []string{"ollama"}) ||
		len(c.Environment) != 0 || len(c.SecretEnvironment) != 0 || len(c.OwnerEnvironment) != 0 || len(c.CompanionEnvironment) != 0 || len(c.CompanionSecretEnvironment) != 0 ||
		!reflect.DeepEqual(c.SecretFiles, privateAIConnectorSecretFiles) || len(c.RestoreActivationEnvironment) != 0 ||
		len(c.Volumes) != 0 || c.Accelerator != nil || c.Health.Kind != "command" || !exactStringList(c.Health.Command, privateAIConnectorHealth) ||
		c.Resources == nil || c.Resources.MemoryLimit != "256m" || c.Resources.MemoryReservation != "" || c.Resources.CPUs != 0 {
		return fail(ErrInvalidPlan, path, "the kombify AI connector must stay the pinned outbound-only tunnel with its token file")
	}
	if len(c.PublishedPorts) != 0 || len(c.LANListeners) != 0 || c.DevicePassthrough != nil || len(c.Devices) != 0 ||
		len(c.PeerNetworks) != 0 || c.RouteHostLoopback || c.DockerLifecycleOwner != nil || len(c.RouteHostEnvironment) != 0 || c.AcmeTLSALPNPort != 0 ||
		c.HomeIdentityAccess != nil || c.PocketIDClient != nil || c.JellyfinSSOPlugin != nil || c.HomeAssistantOIDC != nil {
		return fail(ErrInvalidPlan, path, "the kombify AI connector receives no inbound, host or identity right")
	}
	return nil
}

// validPrivateAIConnectorCommand requires the governed command, which the
// catalog projects as kombifyAIConnectorCommandJSON: the token only from its
// file and metrics only on loopback.
func validPrivateAIConnectorCommand(command []string) bool {
	var catalog []string
	return json.Unmarshal([]byte(kombifyAIConnectorCommandJSON), &catalog) == nil &&
		exactStringList(catalog, privateAIConnectorCommand) && exactStringList(command, privateAIConnectorCommand)
}

func init() {
	governedCustodyNodeRights[privateAIWorkloadModuleID] = parsePrivateAIConnectorNodeFields
}

// parsePrivateAIConnectorNodeFields admits the connector token file only for
// the governed connector component.
func parsePrivateAIConnectorNodeFields(component selectedPaaSRuntimeComponent, secretRefs map[string]string, path string) ([]ApplicationDeliverySecretFile, []string, error) {
	if component.ID != PrivateAIConnectorComponent || len(component.RestoreActivationEnvironment) != 0 ||
		!reflect.DeepEqual(component.SecretFiles, privateAIConnectorSecretFiles) ||
		secretRefs[PrivateAIConnectorTokenSlot] != PrivateAIConnectorTokenRef {
		return nil, nil, fail(ErrInvalidPlan, path, "a custody file is admitted only for the kombify AI connector token")
	}
	file := component.SecretFiles[0]
	return []ApplicationDeliverySecretFile{{Slot: file.Slot, Target: file.Target, PathEnvironment: file.PathEnvironment, UID: file.UID, GID: file.GID}}, nil, nil
}

func validatePrivateAIServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "ai" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 8080 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "private-ai-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "ai" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"http", "https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "AI route authority differs from the governed PrivateAI endpoint")
	}
	return nil
}

// validPrivateAISecretRefs requires exactly the owner-password and
// session-key references, the connector token reference while the connector
// runs, and one opaque reference per companion secret slot the governed
// component uses; nothing else.
func validPrivateAISecretRefs(refs map[string]string, connector bool, companionSlots []string) bool {
	expected := 2 + len(companionSlots)
	if connector {
		expected++
		if refs[PrivateAIConnectorTokenSlot] != PrivateAIConnectorTokenRef {
			return false
		}
	}
	for _, slot := range companionSlots {
		if !validSecretReference(refs[slot]) {
			return false
		}
	}
	return len(refs) == expected && validSecretReference(refs["owner-password"]) && validSecretReference(refs["session-key"])
}

// privateAIOllamaROCmImageRef is the only image variant Private AI admits: the
// ROCm build of the pinned Ollama release, selected by the amd accelerator
// profile, whose digest the hash-bound plan carries.
const privateAIOllamaROCmImageRef = ollamaImageRef + "-rocm"

func validPrivateAIOllamaImage(c selectedPaaSRuntimeComponent) bool {
	if c.Accelerator != nil && c.Accelerator.Vendor == "amd" {
		return c.Image.Ref == privateAIOllamaROCmImageRef && acceleratorImageDigestPattern.MatchString(c.Image.Digest)
	}
	return c.Image.Ref == ollamaImageRef && c.Image.Digest == ollamaImageDigest
}
