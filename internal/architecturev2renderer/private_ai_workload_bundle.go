package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	privateAIWorkloadModuleID    = "stackkits-private-ai-runtime"
	privateAIWorkloadUnitID      = "private-ai"
	privateAIWorkloadTemplateRef = "builtin://workloads/private-ai/bundle/v2.json"
	privateAIWorkloadVersion     = "2.0.0"
	privateAIWorkloadOutputRef   = "workloads/private-ai/bundle.json"
)

const privateAIWorkloadRendererSchema = `stackkit.workload-bundle/v2|PrivateAIWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:privateAI|release:` + privateAIRelease + `|secret-material:not-included`

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
	if !validPrivateAISecretRefs(bundle.SecretRefs) {
		return PrivateAIWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires opaque owner-password and session-key references")
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
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, privateAIWorkloadModuleID, "ai", 8080, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"owner-password", "session-key"}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Private AI requires owner-password and session-key slots")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil ||
		!validPrivateAISecretRefs(secretRefs) {
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
	components, err = validatePrivateAIRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
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
	if len(components) != 2 {
		return nil, fail(ErrInvalidPlan, path, "requires Ollama and Open WebUI")
	}
	seen := map[string]bool{}
	for _, c := range components {
		if seen[c.ID] || c.Role != "application" || c.Lifecycle != "daemon" || len(c.Command) != 0 || len(c.Entrypoint) != 0 || !exactStringList(c.NetworkRefs, []string{"private-ai-internal"}) {
			return nil, fail(ErrInvalidPlan, path, "AI runtime identity or network differs")
		}
		seen[c.ID] = true
		switch c.ID {
		case "open-webui":
			if c.Egress {
				return nil, fail(ErrInvalidPlan, path, "only the model-serving component requests egress")
			}
			if !exactStringList(c.DependsOn, []string{"ollama"}) || len(c.SecretEnvironment) != 2 || c.Health.Kind != "http" || c.Health.Path != "/health" || c.Health.Port != 8080 || c.Environment["ENABLE_PERSISTENT_CONFIG"] != "false" || c.Environment["ENABLE_OPENAI_API"] != "false" || c.Environment["RAG_EMBEDDING_MODEL_AUTO_UPDATE"] != "false" || c.Environment["WHISPER_MODEL_AUTO_UPDATE"] != "false" || c.Environment["OFFLINE_MODE"] != "true" || c.Environment["HF_HUB_OFFLINE"] != "1" || len(c.Environment) != 8 {
				return nil, fail(ErrInvalidPlan, path, "Open WebUI must retain its private configuration")
			}
			if c.Image.Ref != privateAIImageRef || c.Image.Digest != privateAIImageDigest || c.Environment["OLLAMA_BASE_URL"] != "http://ollama:11434" || c.Environment["ENABLE_SIGNUP"] != "false" || c.SecretEnvironment["WEBUI_ADMIN_PASSWORD"] != "owner-password" || c.OwnerEnvironment["WEBUI_ADMIN_EMAIL"] != "email" || len(c.OwnerEnvironment) != 1 || c.SecretEnvironment["WEBUI_SECRET_KEY"] != "session-key" {
				return nil, fail(ErrInvalidPlan, path, "Open WebUI owner and inference boundary differs")
			}
			if len(c.Volumes) != 1 || c.Volumes[0].ID != "data" || c.Volumes[0].Target != "/app/backend/data" || c.Volumes[0].Class != "persistent" || !c.Volumes[0].Backup || c.Volumes[0].ReadOnly || c.Volumes[0].HostPath != "" {
				return nil, fail(ErrInvalidPlan, path, "chat and owner data must persist")
			}
		case "ollama":
			if !c.Egress {
				return nil, fail(ErrInvalidPlan, path, "explicit model downloads require Ollama egress")
			}
			if len(c.DependsOn) != 0 || len(c.SecretEnvironment) != 0 || len(c.OwnerEnvironment) != 0 || c.Health.Kind != "command" || !exactStringList(c.Health.Command, []string{"ollama", "list"}) || len(c.Environment) != 1 || c.Environment["OLLAMA_KEEP_ALIVE"] != "5m" {
				return nil, fail(ErrInvalidPlan, path, "Ollama must retain its private serving configuration")
			}
			if c.Image.Ref != ollamaImageRef || c.Image.Digest != ollamaImageDigest || len(c.Command) != 0 || len(c.Entrypoint) != 0 {
				return nil, fail(ErrInvalidPlan, path, "Ollama runtime must not implicitly download models")
			}
			if len(c.Volumes) != 1 || c.Volumes[0].ID != "models" || c.Volumes[0].Target != "/root/.ollama" || c.Volumes[0].Class != "persistent" || c.Volumes[0].ReadOnly || c.Volumes[0].HostPath != "" {
				return nil, fail(ErrInvalidPlan, path, "models must persist")
			}
		default:
			return nil, fail(ErrInvalidPlan, path, "unknown AI component")
		}
	}
	return components, nil
}

func validatePrivateAIServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "ai" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 8080 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "private-ai-http" || !validBundleIngressAuthNative(endpoint.IngressAuth) || endpoint.Data.BindingRef != "ai" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"http", "https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "AI route authority differs from the governed PrivateAI endpoint")
	}
	return nil
}

func validPrivateAISecretRefs(refs map[string]string) bool {
	return len(refs) == 2 && validSecretReference(refs["owner-password"]) && validSecretReference(refs["session-key"])
}
