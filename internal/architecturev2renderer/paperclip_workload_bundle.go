package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// Private AI agent control plane (docs/use-case-expansion/ai-agents.md):
// Paperclip with its own PostgreSQL in one bundle. Paperclip starts the agents
// of its local adapters (Claude Code, Codex, OpenCode, Hermes CLI, the process
// adapter) as child processes of its own container, so the container is the
// sandbox and the governed runtime makes it a gVisor one: no host Docker
// socket, no owner home, no egress, no StackKits credential, budgets at zero
// and no provider key. It signs its operators in itself (Better Auth,
// authenticated + private) behind the kit's login on a private route only.
const (
	paperclipWorkloadModuleID    = "stackkits-paperclip-runtime"
	paperclipWorkloadUnitID      = "paperclip"
	paperclipWorkloadRef         = "ai-control-plane"
	paperclipWorkloadTemplateRef = "builtin://workloads/paperclip/bundle/v2.json"
	paperclipWorkloadVersion     = "2.0.0"
	paperclipWorkloadOutputRef   = "workloads/paperclip/bundle.json"
	paperclipPort                = 3100

	// PaperclipAllowedHostnamesEnv receives the route host at apply time:
	// Paperclip's private-mode hostname guard answers 403 to every other
	// host, and Better Auth trusts the browser origin derived from it.
	PaperclipAllowedHostnamesEnv = "PAPERCLIP_ALLOWED_HOSTNAMES"
)

var paperclipSecretSlots = []string{"database-password", "session-secret"}

func paperclipWorkloadRendererSchema() string {
	return `stackkit.workload-bundle/v2|PaperclipWorkloadBundle|application-adapter|route:authority-bound-module-route-v1:private-only|provider-lifecycle:not-owned|components:paperclip,postgres|sandbox-runtime:runsc|docker-socket:none|egress:none|volumes:home,database|route-host:` +
		PaperclipAllowedHostnamesEnv + `|entrypoint:` + paperclipEntrypointJSON + `|release:` + paperclipRelease + `|postgres:` + paperclipPostgresRelease + `|secret-material:references-only|peer:ai/private-ai-internal`
}

// PaperclipWorkloadBundleDescriptor is the closed runtime artifact accepted by
// the standalone application adapter. Secret references stay opaque.
type PaperclipWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func PaperclipWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(paperclipWorkloadRendererSchema()))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: paperclipWorkloadTemplateRef,
		Version: paperclipWorkloadVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type paperclipWorkloadBundleRenderer struct{ contract RendererContract }

func newPaperclipWorkloadBundleRenderer() paperclipWorkloadBundleRenderer {
	return paperclipWorkloadBundleRenderer{contract: PaperclipWorkloadBundleRendererContract()}
}

func (r paperclipWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validatePaperclipWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.paperclip-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: paperclipWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParsePaperclipWorkloadBundle validates the closed artifact before a runtime
// owner may consume it.
func ParsePaperclipWorkloadBundle(data []byte) (PaperclipWorkloadBundleDescriptor, error) {
	path := "paperclipWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return PaperclipWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Paperclip workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "PaperclipWorkloadBundle" ||
		bundle.Workload.Ref != paperclipWorkloadRef || bundle.Workload.AlternativeRef != "paperclip" ||
		bundle.Workload.ModuleRef != paperclipWorkloadModuleID || bundle.Workload.Release != paperclipRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != paperclipWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return PaperclipWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Paperclip "+paperclipRelease+" contract")
	}
	if !validPaperclipSecretRefs(bundle.SecretRefs) || len(bundle.ConfigFiles) != 0 {
		return PaperclipWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires opaque database-password and session-secret references and no file override")
	}
	if err := validatePaperclipRuntimeComponents(bundle.Components, path+".components"); err != nil {
		return PaperclipWorkloadBundleDescriptor{}, err
	}
	if err := validatePaperclipServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return PaperclipWorkloadBundleDescriptor{}, err
	}
	descriptor := PaperclipWorkloadBundleDescriptor{
		WorkloadRef: paperclipWorkloadRef, ModuleRef: paperclipWorkloadModuleID, Release: paperclipRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Components: make([]SelectedPaaSWorkloadComponentDescriptor, len(bundle.Components)),
	}
	if bundle.DeliveryRoute != nil {
		if err := validatePaperclipRoute(*bundle.DeliveryRoute, path+".deliveryRoute"); err != nil {
			return PaperclipWorkloadBundleDescriptor{}, err
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
func validatePaperclipWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + paperclipWorkloadModuleID + ".renderUnits." + paperclipWorkloadUnitID
	if unit.ModuleID() != paperclipWorkloadModuleID || unit.ID() != paperclipWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", paperclipWorkloadModuleID, paperclipWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Paperclip workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != paperclipImageRef ||
		!hasDigest || imageDigest != paperclipImageDigest || !hasEntry || entry != paperclipWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Paperclip "+paperclipRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Paperclip requires one exact node-local target")
	}
	// The control plane never receives the host's Docker daemon: not the
	// socket, not a daemon binding, not a proxy. Its sandbox is the gVisor
	// container.
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "the agent control plane receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, paperclipWorkloadModuleID, paperclipWorkloadRef, paperclipPort, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute != nil && deliveryRoute.Exposure == "public" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Paperclip runs the owner's agents and is never published")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || !validPaperclipSecretRefs(secretRefs) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires opaque database-password and session-secret references and no secret material")
	}
	if !sameStringSet(unit.SecretInputRefs(), paperclipSecretSlots) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Paperclip bundle accepts no free input or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != paperclipWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", paperclipWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	if _, selected := unit.ModuleAccelerator(); selected {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".acceleratorProfile", "Paperclip has no GPU: inference runs in Ollama or Hermes")
	}
	if err := validatePaperclipRuntimeComponents(components, path+".runtime.components"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact Paperclip endpoint")
	}
	if err := validatePaperclipServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "PaperclipWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = paperclipWorkloadRef, "paperclip"
	bundle.Workload.ModuleRef, bundle.Workload.Release = paperclipWorkloadModuleID, paperclipRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validPaperclipSecretRefs(refs map[string]string) bool {
	if len(refs) != len(paperclipSecretSlots) {
		return false
	}
	for _, slot := range paperclipSecretSlots {
		if !validSecretReference(refs[slot]) {
			return false
		}
	}
	return true
}

// paperclipComponents are the two governed components: the Paperclip image
// under the gVisor runtime with its home volume, and its PostgreSQL. The
// entrypoint wrapper composes DATABASE_URL from the custody password (URL
// encoded by Node) and hands over to the image entrypoint, which chowns the
// home volume and drops to the unprivileged node user.
func paperclipComponents() ([]selectedPaaSRuntimeComponent, error) {
	var entrypoint []string
	if err := json.Unmarshal([]byte(paperclipEntrypointJSON), &entrypoint); err != nil {
		return nil, err
	}
	return []selectedPaaSRuntimeComponent{
		{
			ID: "paperclip", Role: "application", Lifecycle: "daemon",
			SandboxRuntime: SandboxRuntimeRunsc,
			Image:          selectedPaaSRuntimeImage{Ref: paperclipImageRef, Digest: paperclipImageDigest},
			DependsOn:      []string{"paperclip-postgres"}, NetworkRefs: []string{"paperclip-internal"},
			Entrypoint: entrypoint,
			Environment: map[string]string{
				"HOST":                          "0.0.0.0",
				"PORT":                          "3100",
				"SERVE_UI":                      "true",
				"PAPERCLIP_HOME":                "/paperclip",
				"PAPERCLIP_INSTANCE_ID":         "default",
				"PAPERCLIP_DEPLOYMENT_MODE":     "authenticated",
				"PAPERCLIP_DEPLOYMENT_EXPOSURE": "private",
				"PAPERCLIP_SETTING_DEFAULTS":    `{"feedbackDataSharingPreference":"not_allowed"}`,
				"PAPERCLIP_HIDDEN_SETTINGS":     "instance.general.feedbackDataSharingPreference",
			},
			SecretEnvironment: map[string]string{
				"STACKKIT_PAPERCLIP_DB_PASSWORD": "database-password",
				"BETTER_AUTH_SECRET":             "session-secret",
			},
			RouteHostEnvironment: map[string]string{PaperclipAllowedHostnamesEnv: "route-host"},
			Volumes: []selectedPaaSRuntimeVolume{
				{ID: "home", Target: "/paperclip", Class: "persistent", Backup: true},
			},
			Health:       selectedPaaSRuntimeHealth{Kind: "http", Path: "/api/health", Port: paperclipPort},
			Resources:    &selectedPaaSRuntimeLimits{MemoryLimit: "4g", MemoryReservation: "512m", CPUs: 2},
			PeerNetworks: []selectedPaaSPeerNetwork{{WorkloadRef: "ai", NetworkRef: "private-ai-internal"}},
		},
		{
			ID: "paperclip-postgres", Role: "database", Lifecycle: "daemon",
			Image:     selectedPaaSRuntimeImage{Ref: paperclipPostgresImageRef, Digest: paperclipPostgresImageDigest},
			DependsOn: []string{}, NetworkRefs: []string{"paperclip-internal"},
			Environment:       map[string]string{"POSTGRES_DB": "paperclip", "POSTGRES_USER": "paperclip"},
			SecretEnvironment: map[string]string{"POSTGRES_PASSWORD": "database-password"},
			Volumes: []selectedPaaSRuntimeVolume{
				{ID: "database", Target: "/var/lib/postgresql/data", Class: "persistent", Backup: true},
			},
			Health:    selectedPaaSRuntimeHealth{Kind: "command", Command: []string{"pg_isready", "-U", "paperclip", "-d", "paperclip"}},
			Resources: &selectedPaaSRuntimeLimits{MemoryLimit: "1g", MemoryReservation: "256m"},
		},
	}, nil
}

// validatePaperclipRuntimeComponents admits exactly the governed graph:
// nothing else reaches the containers, the entry component never runs
// outside the sandbox runtime, and no component has egress or an accelerator.
func validatePaperclipRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) error {
	expected, err := paperclipComponents()
	if err != nil {
		return wrap(ErrInvalidPlan, path, "decode governed Paperclip entrypoint", err)
	}
	if len(components) != len(expected) {
		return fail(ErrInvalidPlan, path, "Paperclip runtime graph differs from the closed "+paperclipRelease+" contract")
	}
	byComponentID := func(a, b selectedPaaSRuntimeComponent) int { return strings.Compare(a.ID, b.ID) }
	byVolumeID := func(a, b selectedPaaSRuntimeVolume) int { return strings.Compare(a.ID, b.ID) }
	actual := slices.SortedFunc(slices.Values(components), byComponentID)
	expected = slices.SortedFunc(slices.Values(expected), byComponentID)
	for index := range expected {
		want, got := expected[index], actual[index]
		if got.ID != want.ID {
			return fail(ErrInvalidPlan, path, "Paperclip runtime graph differs from the closed "+paperclipRelease+" contract")
		}
		if got.Accelerator != nil || got.Egress {
			return fail(ErrInvalidPlan, path+"."+got.ID, "Paperclip components run without egress and without an accelerator grant")
		}
		if err := validateSandboxRuntime(paperclipWorkloadModuleID, got, path+"."+got.ID); err != nil {
			return err
		}
		if err := validatePeerNetworks(paperclipWorkloadModuleID, got, path+"."+got.ID); err != nil {
			return err
		}
		if !sameEnvironment(got.Environment, want.Environment) || !sameEnvironment(got.SecretEnvironment, want.SecretEnvironment) ||
			!sameEnvironment(got.RouteHostEnvironment, want.RouteHostEnvironment) {
			return fail(ErrInvalidPlan, path+"."+got.ID, "Paperclip environment differs from the closed "+paperclipRelease+" contract")
		}
		got.Environment, got.SecretEnvironment = maps.Clone(want.Environment), maps.Clone(want.SecretEnvironment)
		got.RouteHostEnvironment = maps.Clone(want.RouteHostEnvironment)
		got.Volumes = slices.SortedFunc(slices.Values(got.Volumes), byVolumeID)
		want.Volumes = slices.SortedFunc(slices.Values(want.Volumes), byVolumeID)
		if !reflect.DeepEqual(got, want) {
			return fail(ErrInvalidPlan, path+"."+got.ID, "Paperclip runtime graph differs from the closed "+paperclipRelease+" contract")
		}
	}
	return nil
}

func validatePaperclipServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != paperclipWorkloadRef || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != paperclipPort ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "paperclip-http" || endpoint.IngressAuth != "forward-auth" ||
		endpoint.Data.BindingRef != paperclipWorkloadRef || endpoint.Data.Locality != "primary-site" ||
		!exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private"}) {
		return fail(ErrInvalidPlan, path, "route authority differs from the governed private Paperclip endpoint")
	}
	return nil
}

func validatePaperclipRoute(route applicationDeliveryRoute, path string) error {
	if err := validateParsedApplicationDeliveryRoute(route, paperclipWorkloadModuleID, paperclipWorkloadRef, paperclipPort, path); err != nil {
		return err
	}
	if route.Exposure == "public" {
		return fail(ErrInvalidPlan, path, "Paperclip runs the owner's agents and is never published")
	}
	return nil
}

// parsePaperclipRouteHostFields admits the route-host binding for the
// Paperclip entry component alone: no published port, no ACME passthrough, and
// never a public route. Without a route the variable stays unset and
// Paperclip answers only its own loopback health probe.
func parsePaperclipRouteHostFields(component selectedPaaSRuntimeComponent, route *applicationDeliveryRoute, path string) (componentHostBindings, error) {
	if component.ID != paperclipWorkloadUnitID || len(component.PublishedPorts) != 0 || component.AcmeTLSALPNPort != 0 ||
		!reflect.DeepEqual(component.RouteHostEnvironment, map[string]string{PaperclipAllowedHostnamesEnv: "route-host"}) {
		return componentHostBindings{}, fail(ErrInvalidPlan, path, "Paperclip admits only its allowed-hostnames route-host binding")
	}
	if route != nil && route.Exposure == "public" {
		return componentHostBindings{}, fail(ErrInvalidPlan, path, "Paperclip runs the owner's agents and is never published")
	}
	return componentHostBindings{RouteHostEnvironment: []string{PaperclipAllowedHostnamesEnv}}, nil
}
