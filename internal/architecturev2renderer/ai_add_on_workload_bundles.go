package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"reflect"
)

// Private AI add-ons (docs/use-case-expansion/ai-agents.md): stateless
// services that join the node's private AI network so Open WebUI reaches
// them by name. They hold no owner data, publish no port and get no initial
// route; an owner route may be local or remote-private, never public.
const (
	searxngWorkloadModuleID = "stackkits-searxng-runtime"
	tikaWorkloadModuleID    = "stackkits-tika-runtime"
	doclingWorkloadModuleID = "stackkits-docling-runtime"
	litellmWorkloadModuleID = "stackkits-litellm-runtime"
	qdrantWorkloadModuleID  = "stackkits-qdrant-runtime"
)

// aiAddOnWorkload is the closed contract of one Private AI add-on bundle.
type aiAddOnWorkload struct {
	kind           string
	name           string
	moduleID       string
	unitID         string
	workloadRef    string
	alternativeRef string
	release        string
	imageRef       string
	imageDigest    string
	port           int
	secretRefs     []string
	component      selectedPaaSRuntimeComponent
	// dataBinding names the owner-data binding of a stateful add-on; empty
	// for the stateless ones, whose endpoint declares no data.
	dataBinding string
	// Optional recipe-owned boundaries retain existing AI defaults.
	schemaBoundary    string
	requiredPrivilege string
	ingressAuth       string
}

func (w aiAddOnWorkload) templateRef() string {
	return "builtin://workloads/" + w.unitID + "/bundle/v2.json"
}

func (w aiAddOnWorkload) outputRef() string { return "workloads/" + w.unitID + "/bundle.json" }

func (w aiAddOnWorkload) contract() RendererContract {
	secretMaterial := "not-included"
	if len(w.secretRefs) > 0 {
		secretMaterial = "references-only"
	}
	boundary := w.schemaBoundary
	if boundary == "" {
		boundary = "|peer:ai/private-ai-internal"
	}
	state := ""
	if w.dataBinding != "" {
		state = "|data:" + w.dataBinding
	}
	schema := "stackkit.workload-bundle/v2|" + w.kind + "|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:" +
		w.unitID + state + "|release:" + w.release + "|secret-material:" + secretMaterial + boundary
	sum := sha256.Sum256([]byte(schema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: w.templateRef(), Version: "2.0.0",
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

func searxngWorkload() aiAddOnWorkload {
	return aiAddOnWorkload{
		kind: "SearxngWorkloadBundle", name: "SearXNG", moduleID: searxngWorkloadModuleID, unitID: "searxng",
		workloadRef: "ai-search", alternativeRef: "searxng", release: searxngRelease,
		imageRef: searxngImageRef, imageDigest: searxngImageDigest, port: 8080, secretRefs: []string{"searxng-secret"},
		component: selectedPaaSRuntimeComponent{
			ID: "searxng", Role: "application", Lifecycle: "daemon", Egress: true,
			Image:     selectedPaaSRuntimeImage{Ref: searxngImageRef, Digest: searxngImageDigest},
			DependsOn: []string{}, NetworkRefs: []string{"searxng-internal"},
			Entrypoint: []string{"/bin/sh", "-ec", `printf 'use_default_settings: true\nsearch:\n  formats:\n    - html\n    - json\n' > /etc/searxng/settings.yml && ` +
				`if [ -f ` + SearxngSecretSettingsPath + ` ]; then cat ` + SearxngSecretSettingsPath + ` >> /etc/searxng/settings.yml && chmod 0600 /etc/searxng/settings.yml; fi && ` +
				`exec /usr/local/searxng/entrypoint.sh`},
			Environment:       map[string]string{"SEARXNG_LIMITER": "false", "SEARXNG_PUBLIC_INSTANCE": "false"},
			SecretEnvironment: map[string]string{"SEARXNG_SECRET": "searxng-secret"},
			Health:            selectedPaaSRuntimeHealth{Kind: "http", Path: "/healthz", Port: 8080},
			Resources:         &selectedPaaSRuntimeLimits{MemoryLimit: "512m", MemoryReservation: "128m"},
			PeerNetworks:      []selectedPaaSPeerNetwork{{WorkloadRef: "ai", NetworkRef: "private-ai-internal"}},
		},
	}
}

func tikaWorkload() aiAddOnWorkload {
	return aiAddOnWorkload{
		kind: "TikaWorkloadBundle", name: "Tika", moduleID: tikaWorkloadModuleID, unitID: "tika",
		workloadRef: "ai-documents", alternativeRef: "tika", release: tikaRelease,
		imageRef: tikaImageRef, imageDigest: tikaImageDigest, port: 9998,
		component: selectedPaaSRuntimeComponent{
			ID: "tika", Role: "application", Lifecycle: "daemon",
			Image:     selectedPaaSRuntimeImage{Ref: tikaImageRef, Digest: tikaImageDigest},
			DependsOn: []string{}, NetworkRefs: []string{"tika-internal"},
			Health:       selectedPaaSRuntimeHealth{Kind: "http", Path: "/tika", Port: 9998},
			Resources:    &selectedPaaSRuntimeLimits{MemoryLimit: "2g", MemoryReservation: "512m"},
			PeerNetworks: []selectedPaaSPeerNetwork{{WorkloadRef: "ai", NetworkRef: "private-ai-internal"}},
		},
	}
}

func doclingWorkload() aiAddOnWorkload {
	return aiAddOnWorkload{
		kind: "DoclingWorkloadBundle", name: "Docling", moduleID: doclingWorkloadModuleID, unitID: "docling",
		workloadRef: "ai-documents", alternativeRef: "docling", release: doclingRelease,
		imageRef: doclingImageRef, imageDigest: doclingImageDigest, port: 5001,
		component: selectedPaaSRuntimeComponent{
			ID: "docling", Role: "application", Lifecycle: "daemon",
			Image:     selectedPaaSRuntimeImage{Ref: doclingImageRef, Digest: doclingImageDigest},
			DependsOn: []string{}, NetworkRefs: []string{"docling-internal"},
			Environment:  map[string]string{"HF_HUB_OFFLINE": "1", "DOCLING_SERVE_ENABLE_UI": "false", "DOCLING_SERVE_MAX_SYNC_WAIT": "600"},
			Health:       selectedPaaSRuntimeHealth{Kind: "http", Path: "/health", Port: 5001},
			Resources:    &selectedPaaSRuntimeLimits{MemoryLimit: "6g", MemoryReservation: "1g"},
			PeerNetworks: []selectedPaaSPeerNetwork{{WorkloadRef: "ai", NetworkRef: "private-ai-internal"}},
		},
	}
}

// litellmWorkload is the model router: one OpenAI-compatible endpoint in
// front of the node's Ollama for the owner's apps and devices. Unlike the
// other add-ons it is meant to be routed, but devices cannot complete the
// kit's browser login, so its route uses LiteLLM's own authentication: the
// master key from custody guards every model route. It stays private (local
// or remote-private, never public), has no egress and holds no data.
func litellmWorkload() aiAddOnWorkload {
	return aiAddOnWorkload{
		kind: "LitellmWorkloadBundle", name: "LiteLLM", moduleID: litellmWorkloadModuleID, unitID: "litellm",
		workloadRef: "ai-gateway", alternativeRef: "litellm", release: litellmRelease,
		imageRef: litellmImageRef, imageDigest: litellmImageDigest, port: 4000, secretRefs: []string{"master-key"},
		component: selectedPaaSRuntimeComponent{
			ID: "litellm", Role: "application", Lifecycle: "daemon",
			Image:     selectedPaaSRuntimeImage{Ref: litellmImageRef, Digest: litellmImageDigest},
			DependsOn: []string{}, NetworkRefs: []string{"litellm-internal"},
			Entrypoint: []string{"/bin/sh", "-ec", `mkdir -p /etc/litellm && printf 'model_list:\n  - model_name: "ollama/*"\n    litellm_params:\n      model: "ollama_chat/*"\n      api_base: "http://ollama:11434"\n` +
				`litellm_settings:\n  drop_params: true\n  check_provider_endpoint: true\ngeneral_settings:\n  master_key: os.environ/LITELLM_MASTER_KEY\n' > /etc/litellm/config.yaml && ` +
				`exec /app/docker/prod_entrypoint.sh --config /etc/litellm/config.yaml --port 4000`},
			Environment:       map[string]string{"DISABLE_ADMIN_UI": "True", "LITELLM_LOCAL_MODEL_COST_MAP": "True", "LITELLM_LOG": "INFO"},
			SecretEnvironment: map[string]string{"LITELLM_MASTER_KEY": "master-key"},
			Health:            selectedPaaSRuntimeHealth{Kind: "http", Path: "/health/liveliness", Port: 4000},
			Resources:         &selectedPaaSRuntimeLimits{MemoryLimit: "2g", MemoryReservation: "512m"},
			PeerNetworks:      []selectedPaaSPeerNetwork{{WorkloadRef: "ai", NetworkRef: "private-ai-internal"}},
		},
		schemaBoundary: "|peer:ai/private-ai-internal|ingress:native",
		ingressAuth:    "native",
	}
}

// qdrantWorkload is the vector store of the Private AI use case: Qdrant on the
// node's private AI network, reached by the selected chat module over its REST
// API (6333) with the API key from custody. It is the one add-on that keeps
// owner data (the collections), so its storage volume is a backup source. It
// has no egress, publishes no port and is never routed publicly.
func qdrantWorkload() aiAddOnWorkload {
	return aiAddOnWorkload{
		kind: "QdrantWorkloadBundle", name: "Qdrant", moduleID: qdrantWorkloadModuleID, unitID: "qdrant",
		workloadRef: "ai-vectors", alternativeRef: "qdrant", release: qdrantRelease,
		imageRef: qdrantImageRef, imageDigest: qdrantImageDigest, port: 6333, secretRefs: []string{"api-key"},
		dataBinding: "ai-vectors",
		component: selectedPaaSRuntimeComponent{
			ID: "qdrant", Role: "application", Lifecycle: "daemon",
			Image:     selectedPaaSRuntimeImage{Ref: qdrantImageRef, Digest: qdrantImageDigest},
			DependsOn: []string{}, NetworkRefs: []string{"qdrant-internal"},
			Environment:       map[string]string{"QDRANT__TELEMETRY_DISABLED": "true"},
			SecretEnvironment: map[string]string{"QDRANT__SERVICE__API_KEY": "api-key"},
			Volumes:           []selectedPaaSRuntimeVolume{{ID: "storage", Target: "/qdrant/storage", Class: "persistent", Backup: true}},
			Health:            selectedPaaSRuntimeHealth{Kind: "http", Path: "/healthz", Port: 6333},
			Resources:         &selectedPaaSRuntimeLimits{MemoryLimit: "2g", MemoryReservation: "256m"},
			PeerNetworks:      []selectedPaaSPeerNetwork{{WorkloadRef: "ai", NetworkRef: "private-ai-internal"}},
		},
	}
}

func SearxngWorkloadBundleRendererContract() RendererContract { return searxngWorkload().contract() }
func LitellmWorkloadBundleRendererContract() RendererContract { return litellmWorkload().contract() }
func TikaWorkloadBundleRendererContract() RendererContract    { return tikaWorkload().contract() }
func DoclingWorkloadBundleRendererContract() RendererContract { return doclingWorkload().contract() }
func QdrantWorkloadBundleRendererContract() RendererContract  { return qdrantWorkload().contract() }

// AIAddOnWorkloadBundleDescriptor is the closed runtime artifact of one
// Private AI add-on accepted by the selected-PaaS executor.
type AIAddOnWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func ParseSearxngWorkloadBundle(data []byte) (AIAddOnWorkloadBundleDescriptor, error) {
	return searxngWorkload().parse(data)
}

func ParseTikaWorkloadBundle(data []byte) (AIAddOnWorkloadBundleDescriptor, error) {
	return tikaWorkload().parse(data)
}

func ParseDoclingWorkloadBundle(data []byte) (AIAddOnWorkloadBundleDescriptor, error) {
	return doclingWorkload().parse(data)
}

func ParseLitellmWorkloadBundle(data []byte) (AIAddOnWorkloadBundleDescriptor, error) {
	return litellmWorkload().parse(data)
}

func ParseQdrantWorkloadBundle(data []byte) (AIAddOnWorkloadBundleDescriptor, error) {
	return qdrantWorkload().parse(data)
}

type aiAddOnWorkloadBundleRenderer struct {
	workload aiAddOnWorkload
	contract RendererContract
}

func newAIAddOnWorkloadBundleRenderer(workload aiAddOnWorkload) aiAddOnWorkloadBundleRenderer {
	return aiAddOnWorkloadBundleRenderer{workload: workload, contract: workload.contract()}
}

func (r aiAddOnWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := r.workload.validateUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer."+r.workload.unitID+"-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: r.workload.outputRef(), Bytes: append(data, '\n')}}, nil
}

func (w aiAddOnWorkload) parse(data []byte) (AIAddOnWorkloadBundleDescriptor, error) {
	path := w.unitID + "WorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return AIAddOnWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed "+w.name+" workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != w.kind ||
		bundle.Workload.Ref != w.workloadRef || bundle.Workload.AlternativeRef != w.alternativeRef ||
		bundle.Workload.ModuleRef != w.moduleID || bundle.Workload.Release != w.release ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != w.unitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return AIAddOnWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "%s", "workload or ownership identity differs from the closed "+w.name+" "+w.release+" contract")
	}
	if !w.validSecretRefs(bundle.SecretRefs) {
		return AIAddOnWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires only the declared opaque secret references")
	}
	if err := w.validateComponents(bundle.Components, path+".components"); err != nil {
		return AIAddOnWorkloadBundleDescriptor{}, err
	}
	if err := w.validateServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return AIAddOnWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := w.validateRoute(*bundle.DeliveryRoute, path+".deliveryRoute"); err != nil {
			return AIAddOnWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := AIAddOnWorkloadBundleDescriptor{
		WorkloadRef: w.workloadRef, ModuleRef: w.moduleID, Release: w.release,
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

func (w aiAddOnWorkload) validateUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + w.moduleID + ".renderUnits." + w.unitID
	if unit.ModuleID() != w.moduleID || unit.ID() != w.unitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", w.moduleID, w.unitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "%s", "render-unit implementation identity differs from the registered "+w.name+" workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != w.imageRef ||
		!hasDigest || imageDigest != w.imageDigest || !hasEntry || entry != w.unitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "%s", "runtime identity must match the exact "+w.name+" "+w.release+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "%s", w.name+" requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, w.moduleID, w.workloadRef, w.port, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute != nil && deliveryRoute.Exposure == "public" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "%s", w.name+" is never published")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || !w.validSecretRefs(secretRefs) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires the declared opaque secret references")
	}
	if !sameStringSet(unit.SecretInputRefs(), w.secretRefs) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "%s", w.name+" bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != w.outputRef() {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", w.outputRef())
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	if err := w.validateComponents(components, path+".runtime.components"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "%s", "requires one exact "+w.name+" endpoint")
	}
	if err := w.validateServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: w.kind,
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = w.workloadRef, w.alternativeRef
	bundle.Workload.ModuleRef, bundle.Workload.Release = w.moduleID, w.release
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func (w aiAddOnWorkload) validSecretRefs(refs map[string]string) bool {
	if len(refs) != len(w.secretRefs) {
		return false
	}
	for _, slot := range w.secretRefs {
		if !validSecretReference(refs[slot]) {
			return false
		}
	}
	return true
}

// validateComponents admits exactly the governed component: the add-on image,
// its private network, the peer join to the private AI network and, for a
// stateful add-on, its declared volumes (no published ports, no host rights).
func (w aiAddOnWorkload) validateComponents(components []selectedPaaSRuntimeComponent, path string) error {
	if len(components) != 1 {
		return fail(ErrInvalidPlan, path, "%s", w.name+" runtime graph differs from the closed "+w.release+" contract")
	}
	actual := components[0]
	if err := validatePeerNetworks(w.moduleID, actual, path); err != nil {
		return err
	}
	expected := w.component
	if !sameEnvironment(actual.Environment, expected.Environment) || !sameEnvironment(actual.SecretEnvironment, expected.SecretEnvironment) {
		return fail(ErrInvalidPlan, path, "%s", w.name+" environment differs from the closed "+w.release+" contract")
	}
	actual.Environment, actual.SecretEnvironment = maps.Clone(expected.Environment), maps.Clone(expected.SecretEnvironment)
	if !reflect.DeepEqual(actual, expected) {
		return fail(ErrInvalidPlan, path, "%s", w.name+" runtime graph differs from the closed "+w.release+" contract")
	}
	return nil
}

func (w aiAddOnWorkload) validateServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	privilege := w.requiredPrivilege
	if privilege == "" {
		privilege = "user"
	}
	if w.ingressAuth != "" && endpoint.IngressAuth != w.ingressAuth {
		return fail(ErrInvalidPlan, path, "private service ingress authentication differs from its recipe")
	}
	if endpoint.ServiceRef != w.workloadRef || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != w.port ||
		endpoint.RequiredPrivilege != privilege || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != w.unitID+"-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) ||
		!w.validData(endpoint) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private"}) {
		return fail(ErrInvalidPlan, path, "%s", "route authority differs from the governed private "+w.name+" endpoint")
	}
	return nil
}

// validData admits no data declaration on a stateless add-on and exactly the
// personal-data binding on a stateful one.
func (w aiAddOnWorkload) validData(endpoint selectedPaaSServiceEndpoint) bool {
	if w.dataBinding == "" {
		return endpoint.Data.BindingRef == "" && len(endpoint.Data.RequiredClasses) == 0
	}
	return endpoint.Data.BindingRef == w.dataBinding && endpoint.Data.Locality == "primary-site" &&
		exactStringList(endpoint.Data.RequiredClasses, []string{"personal"})
}

func (w aiAddOnWorkload) validateRoute(route applicationDeliveryRoute, path string) error {
	if w.ingressAuth != "" && (route.IngressAuth != w.ingressAuth || route.Protocol != "https" || route.UpstreamProtocol != "http" || !route.TLS.Required) {
		return fail(ErrInvalidPlan, path, "private service route differs from its authenticated HTTPS recipe")
	}
	if err := validateParsedApplicationDeliveryRoute(route, w.moduleID, w.workloadRef, w.port, path); err != nil {
		return err
	}
	if route.Exposure == "public" {
		return fail(ErrInvalidPlan, path, "%s", w.name+" is never published")
	}
	return nil
}
