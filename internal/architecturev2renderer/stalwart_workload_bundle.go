package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
)

const (
	stalwartWorkloadModuleID    = "stackkits-stalwart-runtime"
	stalwartWorkloadUnitID      = "stalwart"
	stalwartWorkloadRef         = "mail-server"
	stalwartWorkloadTemplateRef = "builtin://workloads/stalwart/bundle/v1.json"
	stalwartWorkloadVersion     = "1.0.0"
	stalwartWorkloadOutputRef   = "workloads/stalwart/bundle.json"
	stalwartHTTPPort            = 8080

	// StalwartACMETLSALPNPort is Stalwart's HTTPS listener. The router passes
	// only TLS-ALPN-01 challenges for the route host to it (ADR-0046).
	StalwartACMETLSALPNPort = 443
	// StalwartHostnameEnv carries the route host, which is the mail host name.
	StalwartHostnameEnv = "STALWART_HOSTNAME"
)

// StalwartPublishedPorts are the mail ports the node publishes (ADR-0046):
// SMTP, submissions, submission, IMAPS and ManageSieve.
var StalwartPublishedPorts = []int{25, 465, 587, 993, 4190}

const stalwartWorkloadRendererSchema = `stackkit.workload-bundle/v2|StalwartWorkloadBundle|application-adapter|route:authority-bound-module-route-v1,public-host-required|provider-lifecycle:not-owned|components:stalwart|egress:smtp-acme-updates|entrypoint:governed-seed-v1|published-ports:25,465,587,993,4190|acme:tls-alpn-01-router-passthrough-443|hostname:route-host|health:http-healthz-ready-8080|release:` + stalwartRelease + `|secret-material:not-included`

// StalwartWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the standalone application adapter for the own mail
// server. The administrator password stays an opaque reference.
type StalwartWorkloadBundleDescriptor struct {
	WorkloadRef      string
	ModuleRef        string
	Release          string
	SiteRef          string
	NodeRef          string
	InstanceRef      string
	MailHost         string
	AdminPasswordRef string
	Route            ApplicationDeliveryRouteDescriptor
}

func StalwartWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(stalwartWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: stalwartWorkloadTemplateRef,
		Version: stalwartWorkloadVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type stalwartWorkloadBundleRenderer struct{ contract RendererContract }

func newStalwartWorkloadBundleRenderer() stalwartWorkloadBundleRenderer {
	return stalwartWorkloadBundleRenderer{contract: StalwartWorkloadBundleRendererContract()}
}

func (r stalwartWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateStalwartWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.stalwart-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: stalwartWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseStalwartWorkloadBundle validates the closed artifact before a runtime
// owner or the setup action may consume it.
func ParseStalwartWorkloadBundle(data []byte) (StalwartWorkloadBundleDescriptor, error) {
	path := "stalwartWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return StalwartWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Stalwart workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "StalwartWorkloadBundle" ||
		bundle.Workload.Ref != stalwartWorkloadRef || bundle.Workload.AlternativeRef != "stalwart" ||
		bundle.Workload.ModuleRef != stalwartWorkloadModuleID || bundle.Workload.Release != stalwartRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != stalwartWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return StalwartWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Stalwart contract")
	}
	if len(bundle.SecretRefs) != 1 || !validSecretReference(bundle.SecretRefs["admin-password"]) {
		return StalwartWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires exactly one opaque admin-password reference")
	}
	if len(bundle.ConfigFiles) != 0 {
		return StalwartWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "Stalwart receives no configuration file; its entrypoint is governed")
	}
	if bundle.DeliveryRoute == nil {
		return StalwartWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".deliveryRoute", "Stalwart requires its declared public HTTPS origin")
	}
	if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, stalwartWorkloadModuleID, stalwartWorkloadRef, stalwartHTTPPort, path+".deliveryRoute"); err != nil {
		return StalwartWorkloadBundleDescriptor{}, err
	}
	if err := validateStalwartMailRoute(bundle.DeliveryRoute, path+".deliveryRoute"); err != nil {
		return StalwartWorkloadBundleDescriptor{}, err
	}
	if err := validateStalwartRuntimeComponents(bundle.Components, path+".components"); err != nil {
		return StalwartWorkloadBundleDescriptor{}, err
	}
	if err := validateStalwartServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return StalwartWorkloadBundleDescriptor{}, err
	}
	return StalwartWorkloadBundleDescriptor{
		WorkloadRef: stalwartWorkloadRef, ModuleRef: stalwartWorkloadModuleID, Release: stalwartRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		MailHost: bundle.DeliveryRoute.Host, AdminPasswordRef: bundle.SecretRefs["admin-password"],
		Route: bundle.DeliveryRoute.descriptor(),
	}, nil
}

//nolint:gocyclo // One boundary validates the complete render-unit authority.
func validateStalwartWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + stalwartWorkloadModuleID + ".renderUnits." + stalwartWorkloadUnitID
	if unit.ModuleID() != stalwartWorkloadModuleID || unit.ID() != stalwartWorkloadUnitID ||
		unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Stalwart contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" || !hasEngine || engine != "docker" ||
		!hasImage || imageRef != stalwartImageRef || !hasDigest || imageDigest != stalwartImageDigest || !hasEntry || entry != stalwartWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity differs from the pinned Stalwart image")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Stalwart requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "application adapter receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, stalwartWorkloadModuleID, stalwartWorkloadRef, stalwartHTTPPort, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if err := validateStalwartMailRoute(deliveryRoute, path+".inputs"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if !exactStringList(unit.SecretInputRefs(), []string{"admin-password"}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Stalwart requires only its admin-password slot")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || len(secretRefs) != 1 || !validSecretReference(secretRefs["admin-password"]) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires one opaque admin-password reference and no secret material")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".interfaces", "Stalwart receives no host, socket or runtime-network authority")
	}
	var placement struct{ Scope, Cardinality string }
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != stalwartWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", stalwartWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode component graph", err)
	}
	if err := validateStalwartRuntimeComponents(components, path+".runtime.components"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact mail-server endpoint")
	}
	if err := validateStalwartServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "StalwartWorkloadBundle", SecretRefs: secretRefs,
		Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = stalwartWorkloadRef, "stalwart"
	bundle.Workload.ModuleRef, bundle.Workload.Release = stalwartWorkloadModuleID, stalwartRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter, bundle.Ownership.ProviderLifecycle, bundle.Ownership.Credentials = "selected-application-adapter", "not-owned", "opaque-references-only"
	return bundle, nil
}

// validateStalwartMailRoute requires the public HTTPS route whose host becomes
// the mail host name, the MX target and the ACME certificate name.
func validateStalwartMailRoute(route *applicationDeliveryRoute, path string) error {
	if route == nil || route.Host == "" || route.Exposure != "public" || route.Protocol != "https" || !route.TLS.Required {
		return fail(ErrInvalidPlan, path, "the own mail server requires a public HTTPS route with a host name")
	}
	return nil
}

var stalwartVolumes = map[string]selectedPaaSRuntimeVolume{
	"data":   {ID: "data", Target: "/var/lib/stalwart", Class: "persistent", Backup: true},
	"config": {ID: "config", Target: "/etc/stalwart", Class: "cache"},
}

func stalwartPublishedPorts() []selectedPaaSPublishedPort {
	ports := make([]selectedPaaSPublishedPort, 0, len(StalwartPublishedPorts))
	for _, port := range StalwartPublishedPorts {
		ports = append(ports, selectedPaaSPublishedPort{Port: port, Protocol: "tcp"})
	}
	return ports
}

func validateStalwartRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) error {
	if len(components) != 1 {
		return fail(ErrInvalidPlan, path, "requires exactly the Stalwart component")
	}
	c := components[0]
	var entrypoint []string
	if err := json.Unmarshal([]byte(stalwartEntrypointJSON), &entrypoint); err != nil {
		return wrap(ErrInvalidPlan, path, "decode the governed Stalwart entrypoint", err)
	}
	if c.ID != stalwartWorkloadUnitID || c.Role != "application" || c.Lifecycle != "daemon" || c.HealthFailure != "" ||
		c.Image.Ref != stalwartImageRef || c.Image.Digest != stalwartImageDigest || len(c.DependsOn) != 0 ||
		!exactStringList(c.NetworkRefs, []string{"mail-server-internal"}) || !c.Egress || !slices.Equal(c.Entrypoint, entrypoint) ||
		len(c.Command) != 0 || c.RouteHostLoopback || c.DockerLifecycleOwner != nil || len(c.OwnerEnvironment) != 0 ||
		len(c.Environment) != 0 ||
		!reflect.DeepEqual(c.SecretEnvironment, map[string]string{"STACKKIT_ADMIN_SECRET": "admin-password"}) ||
		!reflect.DeepEqual(c.RouteHostEnvironment, map[string]string{StalwartHostnameEnv: "route-host"}) ||
		!reflect.DeepEqual(c.PublishedPorts, stalwartPublishedPorts()) || c.AcmeTLSALPNPort != StalwartACMETLSALPNPort ||
		c.Health.Kind != "http" || c.Health.Path != "/healthz/ready" || c.Health.Port != stalwartHTTPPort || len(c.Health.Command) != 0 {
		return fail(ErrInvalidPlan, path, "Stalwart image, entrypoint, egress, ports, host binding, password binding or health differs from the closed contract")
	}
	if len(c.Volumes) != len(stalwartVolumes) {
		return fail(ErrInvalidPlan, path+".volumes", "Stalwart requires its data and config volumes")
	}
	for _, volume := range c.Volumes {
		if want, ok := stalwartVolumes[volume.ID]; !ok || !reflect.DeepEqual(volume, want) {
			return fail(ErrInvalidPlan, path+".volumes", "Stalwart volume %q differs from the closed contract", volume.ID)
		}
	}
	return nil
}

type mailNodeComponentFields struct {
	PublishedTCPPorts    []int
	RouteHostEnvironment []string
	ACMETLSALPNPort      int
}

// parseMailNodeComponentFields admits the ADR-0046 mail-node fields only for
// the Stalwart entry component with its exact port set and a public route
// host. Every other workload keeps the loopback-only publication rule.
func parseMailNodeComponentFields(component selectedPaaSRuntimeComponent, moduleRef string, route *applicationDeliveryRoute, path string) (mailNodeComponentFields, error) {
	if len(component.PublishedPorts) == 0 && len(component.RouteHostEnvironment) == 0 && component.AcmeTLSALPNPort == 0 {
		return mailNodeComponentFields{}, nil
	}
	if moduleRef != stalwartWorkloadModuleID || component.ID != stalwartWorkloadUnitID {
		return mailNodeComponentFields{}, fail(ErrInvalidPlan, path, "published mail ports, route-host binding and ACME passthrough are admitted only for the governed mail node")
	}
	if !reflect.DeepEqual(component.PublishedPorts, stalwartPublishedPorts()) ||
		!reflect.DeepEqual(component.RouteHostEnvironment, map[string]string{StalwartHostnameEnv: "route-host"}) ||
		component.AcmeTLSALPNPort != StalwartACMETLSALPNPort {
		return mailNodeComponentFields{}, fail(ErrInvalidPlan, path, "mail-node ports, host binding or ACME passthrough differ from the closed Stalwart contract")
	}
	if err := validateStalwartMailRoute(route, path); err != nil {
		return mailNodeComponentFields{}, err
	}
	return mailNodeComponentFields{
		PublishedTCPPorts:    slices.Clone(StalwartPublishedPorts),
		RouteHostEnvironment: []string{StalwartHostnameEnv},
		ACMETLSALPNPort:      StalwartACMETLSALPNPort,
	}, nil
}

func validateStalwartServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != stalwartWorkloadRef || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != stalwartHTTPPort || endpoint.RequiredPrivilege != "user" ||
		endpoint.OriginSelector != "control-authority-site" || endpoint.HealthRef != "stalwart-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) ||
		endpoint.Data.BindingRef != stalwartWorkloadRef || endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) || !exactStringList(endpoint.AllowedExposures, []string{"public"}) {
		return fail(ErrInvalidPlan, path, "mail-server route authority differs from the governed Stalwart endpoint")
	}
	return nil
}
