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

// Private AI personal assistant (docs/use-case-expansion/ai-agents.md): Hermes
// Agent on the node's Ollama model, selected with the assistant capability
// module. Experimental and owner-only. StackKits installs its policy as Hermes
// managed scope at every start (local inference, local terminal backend,
// dangerous commands ask, no anonymous web endpoints) and seeds the owner's
// settings once with every grant off; messaging, web, schedules and external
// writes are toolsets the owner adds. The dashboard is reached only behind
// the kit's login on a private route and asks for its own password.
const (
	hermesWorkloadModuleID    = "stackkits-hermes-runtime"
	hermesWorkloadUnitID      = "hermes"
	hermesWorkloadRef         = "ai-assistant"
	hermesWorkloadTemplateRef = "builtin://workloads/hermes/bundle/v2.json"
	hermesWorkloadVersion     = "2.0.0"
	hermesWorkloadOutputRef   = "workloads/hermes/bundle.json"
	hermesDashboardPort       = 9119

	// hermesPolicyDir is the read-only volume that carries the governed
	// files; the start script installs the policy from there.
	hermesPolicyDir = "/opt/stackkit"
)

var (
	//go:embed assets/hermes/start.sh
	hermesStartScript string
	//go:embed assets/hermes/managed-config.yaml
	hermesManagedConfig string
	//go:embed assets/hermes/seed-config.yaml
	hermesSeedConfig string

	hermesSecretSlots = []string{"dashboard-password", "dashboard-session-secret"}
)

// hermesConfigFiles are the governed read-only files: the start script, the
// managed-scope policy and the owner's seed settings. None carries a secret.
func hermesConfigFiles() []selectedPaaSConfigFile {
	return []selectedPaaSConfigFile{
		{Path: hermesPolicyDir + "/start.sh", Body: hermesStartScript},
		{Path: hermesPolicyDir + "/managed/config.yaml", Body: hermesManagedConfig},
		{Path: hermesPolicyDir + "/seed/config.yaml", Body: hermesSeedConfig},
	}
}

// hermesAssetsDigest binds the renderer contract to the exact governed files,
// so any edit to them changes the CUE-declared contract hash.
func hermesAssetsDigest() string {
	digest := sha256.New()
	for _, file := range hermesConfigFiles() {
		digest.Write([]byte(file.Path + "\x00" + file.Body + "\x00"))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func hermesWorkloadRendererSchema() string {
	return `stackkit.workload-bundle/v2|HermesWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:hermes|egress:owner-grants|volumes:data,policy|managed-scope:stackkit|governed-files:sha256:` +
		hermesAssetsDigest() + `|release:` + hermesRelease + `|secret-material:references-only|peer:ai/private-ai-internal`
}

// HermesWorkloadBundleDescriptor is the closed runtime artifact accepted by
// the standalone application adapter. Secret references stay opaque.
type HermesWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func HermesWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(hermesWorkloadRendererSchema()))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: hermesWorkloadTemplateRef,
		Version: hermesWorkloadVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type hermesWorkloadBundleRenderer struct{ contract RendererContract }

func newHermesWorkloadBundleRenderer() hermesWorkloadBundleRenderer {
	return hermesWorkloadBundleRenderer{contract: HermesWorkloadBundleRendererContract()}
}

func (r hermesWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateHermesWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.hermes-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: hermesWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseHermesWorkloadBundle validates the closed artifact before a runtime
// owner may consume it.
func ParseHermesWorkloadBundle(data []byte) (HermesWorkloadBundleDescriptor, error) {
	path := "hermesWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return HermesWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Hermes workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "HermesWorkloadBundle" ||
		bundle.Workload.Ref != hermesWorkloadRef || bundle.Workload.AlternativeRef != "hermes" ||
		bundle.Workload.ModuleRef != hermesWorkloadModuleID || bundle.Workload.Release != hermesRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != hermesWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return HermesWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Hermes "+hermesRelease+" contract")
	}
	if !validHermesSecretRefs(bundle.SecretRefs) {
		return HermesWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires opaque dashboard-password and dashboard-session-secret references")
	}
	if !reflect.DeepEqual(bundle.ConfigFiles, hermesConfigFiles()) {
		return HermesWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "Hermes policy and start script differ from the governed renderer output")
	}
	if err := validateHermesRuntimeComponents(bundle.Components, path+".components"); err != nil {
		return HermesWorkloadBundleDescriptor{}, err
	}
	if err := validateHermesServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return HermesWorkloadBundleDescriptor{}, err
	}
	descriptor := HermesWorkloadBundleDescriptor{
		WorkloadRef: hermesWorkloadRef, ModuleRef: hermesWorkloadModuleID, Release: hermesRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Components: make([]SelectedPaaSWorkloadComponentDescriptor, len(bundle.Components)),
	}
	if bundle.DeliveryRoute != nil {
		if err := validateHermesRoute(*bundle.DeliveryRoute, path+".deliveryRoute"); err != nil {
			return HermesWorkloadBundleDescriptor{}, err
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
func validateHermesWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + hermesWorkloadModuleID + ".renderUnits." + hermesWorkloadUnitID
	if unit.ModuleID() != hermesWorkloadModuleID || unit.ID() != hermesWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", hermesWorkloadModuleID, hermesWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Hermes workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != hermesImageRef ||
		!hasDigest || imageDigest != hermesImageDigest || !hasEntry || entry != hermesWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Hermes "+hermesRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Hermes requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, hermesWorkloadModuleID, hermesWorkloadRef, hermesDashboardPort, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute != nil && deliveryRoute.Exposure == "public" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "the Hermes dashboard runs the owner's agent and is never published")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || !validHermesSecretRefs(secretRefs) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires opaque dashboard-password and dashboard-session-secret references and no secret material")
	}
	if !sameStringSet(unit.SecretInputRefs(), hermesSecretSlots) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Hermes bundle accepts no free input or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != hermesWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", hermesWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	if err := validateHermesRuntimeComponents(components, path+".runtime.components"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact Hermes dashboard endpoint")
	}
	if err := validateHermesServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "HermesWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
		ConfigFiles: hermesConfigFiles(),
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = hermesWorkloadRef, "hermes"
	bundle.Workload.ModuleRef, bundle.Workload.Release = hermesWorkloadModuleID, hermesRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validHermesSecretRefs(refs map[string]string) bool {
	if len(refs) != len(hermesSecretSlots) {
		return false
	}
	for _, slot := range hermesSecretSlots {
		if !validSecretReference(refs[slot]) {
			return false
		}
	}
	return true
}

// hermesComponent is the one governed component. The container starts as root
// only for the governed start script and the upstream s6 bootstrap; Hermes
// itself runs as UID 10000. HERMES_HOME (memory, skills, sessions, schedules,
// the owner's settings) is the backup source; the policy volume is not.
func hermesComponent() selectedPaaSRuntimeComponent {
	return selectedPaaSRuntimeComponent{
		ID: "hermes", Role: "application", Lifecycle: "daemon", Egress: true,
		Image:     selectedPaaSRuntimeImage{Ref: hermesImageRef, Digest: hermesImageDigest},
		DependsOn: []string{}, NetworkRefs: []string{"hermes-internal"},
		Entrypoint: []string{"/bin/sh", hermesPolicyDir + "/start.sh"},
		Command:    []string{"gateway", "run"},
		Environment: map[string]string{
			"HERMES_DASHBOARD":                     "1",
			"HERMES_DASHBOARD_HOST":                "0.0.0.0",
			"HERMES_DASHBOARD_PORT":                "9119",
			"HERMES_DASHBOARD_BASIC_AUTH_USERNAME": "owner",
			"SEARXNG_URL":                          "http://searxng:8080",
		},
		// start.sh reads both into HERMES_DASHBOARD_BASIC_AUTH_PASSWORD and
		// HERMES_DASHBOARD_BASIC_AUTH_SECRET of the Hermes process.
		SecretFiles:                  slices.Clone(hermesSecretFiles),
		RestoreActivationEnvironment: maps.Clone(hermesRestoreActivationEnvironment),
		Volumes: []selectedPaaSRuntimeVolume{
			{ID: "data", Target: "/opt/data", Class: "persistent", Backup: true},
			{ID: "policy", Target: hermesPolicyDir, Class: "cache"},
		},
		Health:       selectedPaaSRuntimeHealth{Kind: "http", Path: "/api/status", Port: hermesDashboardPort},
		Resources:    &selectedPaaSRuntimeLimits{MemoryLimit: "3g", MemoryReservation: "512m"},
		PeerNetworks: []selectedPaaSPeerNetwork{{WorkloadRef: "ai", NetworkRef: "private-ai-internal"}},
	}
}

// hermesRestoreActivationEnvironment keeps a restored Hermes from resuming
// its gateway schedules until the owner has checked the restored state.
var hermesRestoreActivationEnvironment = map[string]string{"STACKKIT_HERMES_RESTORE_ACTIVATION": "restore-activation"}

func init() {
	governedCustodyNodeRights[hermesWorkloadModuleID] = parseHermesNodeFields
}

// parseHermesNodeFields admits the restore-activation variable and the
// dashboard custody files only for the governed Hermes component.
func parseHermesNodeFields(component selectedPaaSRuntimeComponent, secretRefs map[string]string, path string) ([]ApplicationDeliverySecretFile, []string, error) {
	if component.ID != hermesWorkloadUnitID || !slices.Equal(component.SecretFiles, hermesSecretFiles) ||
		!reflect.DeepEqual(component.RestoreActivationEnvironment, hermesRestoreActivationEnvironment) {
		return nil, nil, fail(ErrInvalidPlan, path, "custody files and a restore activation variable are admitted only for the governed Hermes component")
	}
	files, _, err := governedSecretFileDescriptors(hermesSecretFiles, secretRefs, path)
	if err != nil {
		return nil, nil, err
	}
	return files, slices.Sorted(maps.Keys(hermesRestoreActivationEnvironment)), nil
}

// validateHermesRuntimeComponents admits exactly the governed component:
// nothing else reaches the container, no accelerator, no host rights.
func validateHermesRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) error {
	if len(components) != 1 {
		return fail(ErrInvalidPlan, path, "Hermes runtime graph differs from the closed "+hermesRelease+" contract")
	}
	actual, expected := components[0], hermesComponent()
	if actual.Accelerator != nil {
		return fail(ErrInvalidPlan, path+".accelerator", "Hermes runs no model of its own and admits no accelerator grant")
	}
	if err := validatePeerNetworks(hermesWorkloadModuleID, actual, path); err != nil {
		return err
	}
	if !sameEnvironment(actual.Environment, expected.Environment) || !sameEnvironment(actual.SecretEnvironment, expected.SecretEnvironment) ||
		!sameEnvironment(actual.RestoreActivationEnvironment, expected.RestoreActivationEnvironment) {
		return fail(ErrInvalidPlan, path, "Hermes environment differs from the closed "+hermesRelease+" contract")
	}
	actual.Environment, actual.SecretEnvironment = maps.Clone(expected.Environment), maps.Clone(expected.SecretEnvironment)
	actual.RestoreActivationEnvironment = maps.Clone(expected.RestoreActivationEnvironment)
	// The plan orders volumes by its own rule; the set is what is governed.
	byID := func(a, b selectedPaaSRuntimeVolume) int { return strings.Compare(a.ID, b.ID) }
	actual.Volumes = slices.SortedFunc(slices.Values(actual.Volumes), byID)
	expected.Volumes = slices.SortedFunc(slices.Values(expected.Volumes), byID)
	if !reflect.DeepEqual(actual, expected) {
		return fail(ErrInvalidPlan, path, "Hermes runtime graph differs from the closed "+hermesRelease+" contract")
	}
	return nil
}

func validateHermesServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != hermesWorkloadRef || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != hermesDashboardPort ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "hermes-http" || endpoint.IngressAuth != "forward-auth" ||
		endpoint.Data.BindingRef != hermesWorkloadRef || endpoint.Data.Locality != "primary-site" ||
		!exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private"}) {
		return fail(ErrInvalidPlan, path, "route authority differs from the governed private Hermes dashboard endpoint")
	}
	return nil
}

func validateHermesRoute(route applicationDeliveryRoute, path string) error {
	if err := validateParsedApplicationDeliveryRoute(route, hermesWorkloadModuleID, hermesWorkloadRef, hermesDashboardPort, path); err != nil {
		return err
	}
	if route.Exposure == "public" {
		return fail(ErrInvalidPlan, path, "the Hermes dashboard runs the owner's agent and is never published")
	}
	return nil
}
