package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// gamePlatform is the closed contract of one ADR-0048 Game platform workload:
// a Panel, its bootstrap steps and the Wings node daemon, which alone owns the
// game containers through the approved Docker socket (ADR-0043).
type gamePlatform struct {
	name, alternative, moduleID, unitID, providerRef string
	kind, templateRef, outputRef, healthRef          string
	approvalID, approvalEvidence                     string
	release, schema                                  string
	routePort                                        int
	entryImage                                       selectedPaaSRuntimeImage
	secretSlots                                      []string
	// components returns the governed component graph; a non-empty origin
	// adds the route-derived environment the renderer binds.
	components  func(origin string) []selectedPaaSRuntimeComponent
	configFiles func() []selectedPaaSConfigFile
}

const (
	gamePlatformDaemonRef     = "docker-default"
	gamePlatformPolicyProfile = "docker-game-node-lifecycle"
	gamePlatformNetwork       = "game-internal"
)

// GamePlatformWorkloadBundleDescriptor is the closed runtime artifact accepted
// by the standalone application adapter for a Game platform workload.
type GamePlatformWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Route       ApplicationDeliveryRouteDescriptor
}

func (p gamePlatform) rendererContract() RendererContract {
	sum := sha256.Sum256([]byte(p.schema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: p.templateRef,
		Version: "1.0.0", ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type gamePlatformWorkloadBundleRenderer struct {
	platform gamePlatform
	contract RendererContract
}

func newGamePlatformWorkloadBundleRenderer(platform gamePlatform) gamePlatformWorkloadBundleRenderer {
	return gamePlatformWorkloadBundleRenderer{platform: platform, contract: platform.rendererContract()}
}

func (r gamePlatformWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := r.platform.validateUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer."+r.platform.unitID+"-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: r.platform.outputRef, Bytes: append(data, '\n')}}, nil
}

// parse validates the closed artifact before a runtime owner may consume it.
func (p gamePlatform) parse(data []byte) (GamePlatformWorkloadBundleDescriptor, error) {
	path := p.unitID + "WorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return GamePlatformWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed "+p.name+" workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != p.kind ||
		bundle.Workload.Ref != "game" || bundle.Workload.AlternativeRef != p.alternative ||
		bundle.Workload.ModuleRef != p.moduleID || bundle.Workload.Release != p.release ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != "panel" ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return GamePlatformWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed %s contract", p.name)
	}
	if !p.validSecretRefs(bundle.SecretRefs) || validateDockerSocketPath(bundle.DaemonSocketPath) != nil {
		return GamePlatformWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "requires the declared opaque secrets and the approved Docker socket")
	}
	if bundle.DeliveryRoute == nil {
		return GamePlatformWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".deliveryRoute", "%s requires its declared HTTPS origin", p.name)
	}
	if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, p.moduleID, "game", p.routePort, path+".deliveryRoute"); err != nil {
		return GamePlatformWorkloadBundleDescriptor{}, err
	}
	origin, err := applicationHTTPSRootURL(bundle.DeliveryRoute)
	if err != nil {
		return GamePlatformWorkloadBundleDescriptor{}, err
	}
	if err := p.validateComponents(bundle.Components, path+".components", origin); err != nil {
		return GamePlatformWorkloadBundleDescriptor{}, err
	}
	if err := p.validateServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return GamePlatformWorkloadBundleDescriptor{}, err
	}
	if !reflect.DeepEqual(bundle.ConfigFiles, p.configFiles()) {
		return GamePlatformWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "%s startup files differ from the governed renderer output", p.name)
	}
	return GamePlatformWorkloadBundleDescriptor{
		WorkloadRef: "game", ModuleRef: p.moduleID, Release: p.release,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Route: bundle.DeliveryRoute.descriptor(),
	}, nil
}

//nolint:gocyclo // One boundary validates the complete upstream service graph and its governed privileges.
func (p gamePlatform) validateUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + p.moduleID + ".renderUnits." + p.unitID
	if unit.ModuleID() != p.moduleID || unit.ID() != p.unitID ||
		unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered %s contract", p.name)
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" || !hasEngine || engine != "docker" ||
		!hasImage || imageRef != p.entryImage.Ref || !hasDigest || imageDigest != p.entryImage.Digest || !hasEntry || entry != "panel" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity differs from the pinned %s Panel image", p.name)
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !stringListContains(unit.LogicalNodeRefs(), nodeRef) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "%s requires one exact node-local target", p.name)
	}
	daemonRef, hasDaemon := unit.DaemonRef()
	daemonEngine, hasDaemonEngine := unit.DaemonEngine()
	socketPath, hasSocket := unit.DaemonSocketPath()
	if !hasDaemon || daemonRef != gamePlatformDaemonRef || !hasDaemonEngine || daemonEngine != "docker" || !hasSocket || validateDockerSocketPath(socketPath) != nil {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Wings requires the exact docker-default daemon and canonical Unix socket")
	}
	var placement struct{ Scope, Cardinality, DaemonRef string }
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-daemon" || placement.DaemonRef != gamePlatformDaemonRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-daemon docker-default placement")
	}
	if err := p.validatePrivileges(unit, siteRef, nodeRef, path); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, p.moduleID, "game", p.routePort, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute == nil {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "%s requires a declared delivery route", p.name)
	}
	if !sameStringSet(unit.SecretInputRefs(), p.secretSlots) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "%s requires its declared secret slots", p.name)
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || !p.validSecretRefs(secretRefs) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires declared opaque secret references")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".interfaces", "%s provides no interface and receives no runtime-network authority", p.name)
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != p.outputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", p.outputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode component graph", err)
	}
	if err := p.validateComponents(components, path+".runtime.components", ""); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact %s endpoint", p.name)
	}
	if err := p.validateServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	origin, err := applicationHTTPSRootURL(deliveryRoute)
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: p.kind, SecretRefs: secretRefs,
		Components: p.components(origin), Route: endpoints[0], DeliveryRoute: deliveryRoute,
		ConfigFiles: p.configFiles(), DaemonSocketPath: socketPath,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "game", p.alternative
	bundle.Workload.ModuleRef, bundle.Workload.Release = p.moduleID, p.release
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter, bundle.Ownership.ProviderLifecycle, bundle.Ownership.Credentials = "selected-application-adapter", "not-owned", "opaque-references-only"
	return bundle, nil
}

// validatePrivileges accepts exactly the Wings lifecycle-owner socket
// requirement and its central approval (ADR-0043, ADR-0048).
func (p gamePlatform) validatePrivileges(unit RenderUnit, siteRef, nodeRef, path string) error {
	var required []socketProxyRequiredInterfaceContract
	if err := decodeStrict(unit.RequiredInterfacesJSON(), &required); err != nil || len(required) != 1 {
		return wrap(ErrInvalidPlan, path+".requiresInterfaces", "decode exact Wings lifecycle interface", errOrCount(err, len(required), 1))
	}
	r := required[0]
	if r.ID != gamePlatformPolicyProfile || r.Kind != dockerSocketDirectInterfaceKind || r.Protocol != "docker-engine" || r.Version != "v1" ||
		r.Endpoint.Visibility != "node-local" || r.Endpoint.Transport != "unix-socket" || r.Endpoint.PathSource != dockerSocketPathSourceDaemonBinding ||
		!exactStringList(r.Scopes, []string{"docker-api:full"}) || r.CoLocation != "same-node" || r.DaemonRef != gamePlatformDaemonRef || r.PolicyProfile != gamePlatformPolicyProfile {
		return fail(ErrInvalidPlan, path+".requiresInterfaces", "Wings Docker interface widens or drifts from its lifecycle-owner approval")
	}
	var approvals []rawPrivilegedInterfaceApproval
	if err := decodeStrict(unit.PrivilegedInterfaceApprovalsJSON(), &approvals); err != nil || len(approvals) != 1 {
		return wrap(ErrInvalidPlan, path+".privilegedInterfaceApprovals", "decode exact Wings lifecycle-owner approval", errOrCount(err, len(approvals), 1))
	}
	a := approvals[0]
	if a.ID != p.approvalID || a.Kind != dockerSocketDirectInterfaceKind || a.ModuleRef != p.moduleID || a.UnitRef != p.unitID ||
		a.ProviderRef != p.providerRef || a.DaemonRef != gamePlatformDaemonRef || a.PolicyProfile != gamePlatformPolicyProfile ||
		a.ReasonCode != "lifecycle-owner" || a.EvidenceRef != p.approvalEvidence || a.EvidenceGateRef == "" ||
		!stringListContains(a.SiteRefs, siteRef) || !stringListContains(a.NodeRefs, nodeRef) {
		return fail(ErrInvalidPlan, path+".privilegedInterfaceApprovals", "lifecycle-owner approval does not exactly cover Wings on this node")
	}
	return nil
}

// validateComponents compares the graph with the governed one component by
// component; volumes compare as sets.
func (p gamePlatform) validateComponents(components []selectedPaaSRuntimeComponent, path, origin string) error {
	expected := p.components(origin)
	if len(components) != len(expected) {
		return fail(ErrInvalidPlan, path, "requires exactly the %d governed %s components", len(expected), p.name)
	}
	actual := make(map[string]selectedPaaSRuntimeComponent, len(components))
	for _, component := range components {
		actual[component.ID] = component
	}
	for _, component := range expected {
		found, ok := actual[component.ID]
		if !ok {
			return fail(ErrInvalidPlan, path, "%s requires its governed component %q", p.name, component.ID)
		}
		got, want := normalizedGameComponent(found), normalizedGameComponent(component)
		if !reflect.DeepEqual(got, want) {
			return fail(ErrInvalidPlan, path, "%s component %q differs from the governed configuration in %s", p.name, component.ID, firstDifferingField(got, want))
		}
	}
	return nil
}

// firstDifferingField names the first component field that differs, so a
// catalog drift is located without printing configuration values.
func firstDifferingField(got, want selectedPaaSRuntimeComponent) string {
	gotValue, wantValue := reflect.ValueOf(got), reflect.ValueOf(want)
	for index := 0; index < gotValue.NumField(); index++ {
		if !reflect.DeepEqual(gotValue.Field(index).Interface(), wantValue.Field(index).Interface()) {
			return gotValue.Type().Field(index).Name
		}
	}
	return "an unknown field"
}

func normalizedGameComponent(component selectedPaaSRuntimeComponent) selectedPaaSRuntimeComponent {
	component.Volumes = slices.Clone(component.Volumes)
	sort.Slice(component.Volumes, func(i, j int) bool { return component.Volumes[i].ID < component.Volumes[j].ID })
	value := reflect.ValueOf(&component).Elem()
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		if (field.Kind() == reflect.Slice || field.Kind() == reflect.Map) && field.Len() == 0 {
			field.Set(reflect.Zero(field.Type()))
		}
	}
	return component
}

func (p gamePlatform) validateServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "game" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != p.routePort || endpoint.RequiredPrivilege != "user" ||
		endpoint.OriginSelector != "control-authority-site" || endpoint.HealthRef != p.healthRef || endpoint.IngressAuth != "native" ||
		endpoint.Data.BindingRef != "game" || endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) || !sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "%s route authority differs", p.name)
	}
	return nil
}

func (p gamePlatform) validSecretRefs(refs map[string]string) bool {
	if len(refs) != len(p.secretSlots) {
		return false
	}
	for _, slot := range p.secretSlots {
		if !validSecretReference(refs[slot]) {
			return false
		}
	}
	return true
}

// gameRouteHost is the host name of a route origin.
func gameRouteHost(origin string) string {
	origin = strings.TrimRight(origin, "/")
	if parsed, err := url.Parse(origin); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
}

// withGameEnvironment returns base plus the entries of extra.
func withGameEnvironment(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

// gameEggFiles are the curated Eggs, pinned by content digest, imported by
// the Calagopus and Pelican bootstraps (neither seeds Minecraft).
func gameEggFiles() []selectedPaaSConfigFile {
	files := make([]selectedPaaSConfigFile, 0, len(gameEggs))
	for _, egg := range gameEggs {
		files = append(files, selectedPaaSConfigFile{Path: egg.Path, Body: egg.Body})
	}
	return files
}

func gameVolume(id, target string, persistent bool) selectedPaaSRuntimeVolume {
	class := "cache"
	if persistent {
		class = "persistent"
	}
	return selectedPaaSRuntimeVolume{ID: id, Target: target, Class: class, Backup: persistent}
}
