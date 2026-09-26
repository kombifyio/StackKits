package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const (
	forgejoWorkloadModuleID    = "stackkits-forgejo-runtime"
	forgejoWorkloadUnitID      = "forgejo"
	forgejoWorkloadTemplateRef = "builtin://workloads/forgejo/bundle/v2.json"
	forgejoWorkloadVersion     = "2.0.0"
	forgejoWorkloadOutputRef   = "workloads/forgejo/bundle.json"
)

// Schema identity is independent of the upstream release. Exact image/release
// validation below uses the generated catalog projection, so an image update
// has one source and does not require a second manual render-contract update.
const forgejoWorkloadRendererSchema = `stackkit.workload-bundle/v2|ForgejoWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:forgejo|secret-material:not-included`

// ForgejoWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the selected-PaaS executor. OwnerPasswordRef is opaque.
type ForgejoWorkloadBundleDescriptor struct {
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

func ForgejoWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(forgejoWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: forgejoWorkloadTemplateRef, Version: forgejoWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type forgejoWorkloadBundleRenderer struct{ contract RendererContract }

func newForgejoWorkloadBundleRenderer() forgejoWorkloadBundleRenderer {
	return forgejoWorkloadBundleRenderer{contract: ForgejoWorkloadBundleRendererContract()}
}

func (r forgejoWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateForgejoWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.forgejo-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: forgejoWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseForgejoWorkloadBundle validates the closed generated artifact before
// any selected-PaaS owner may consume it.
func ParseForgejoWorkloadBundle(data []byte) (ForgejoWorkloadBundleDescriptor, error) {
	path := "forgejoWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return ForgejoWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Forgejo workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "ForgejoWorkloadBundle" ||
		bundle.Workload.Ref != "dev" || bundle.Workload.AlternativeRef != "forgejo" ||
		bundle.Workload.ModuleRef != forgejoWorkloadModuleID || bundle.Workload.Release != forgejoRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != "forgejo" ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return ForgejoWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Forgejo image contract")
	}
	if !validForgejoSecretRefs(bundle.SecretRefs) {
		return ForgejoWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires opaque owner-password references")
	}
	if len(bundle.ConfigFiles) != 0 {
		return ForgejoWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "Forgejo runtime accepts no startup configuration overrides")
	}
	components, err := validateForgejoRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return ForgejoWorkloadBundleDescriptor{}, err
	}
	if err := validateForgejoServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return ForgejoWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, forgejoWorkloadModuleID, "dev", 3000, path+".deliveryRoute"); err != nil {
			return ForgejoWorkloadBundleDescriptor{}, err
		}
	}
	rootURL, err := applicationHTTPSRootURL(bundle.DeliveryRoute)
	if err != nil {
		return ForgejoWorkloadBundleDescriptor{}, err
	}
	if components[0].Environment["FORGEJO__server__ROOT_URL"] != rootURL {
		return ForgejoWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "Forgejo clone URL differs from its route")
	}
	descriptor := ForgejoWorkloadBundleDescriptor{
		WorkloadRef: "dev", ModuleRef: forgejoWorkloadModuleID, Release: forgejoRelease,
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

//nolint:gocyclo // Keep the complete Forgejo authority check at one boundary.
func validateForgejoWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + forgejoWorkloadModuleID + ".renderUnits." + forgejoWorkloadUnitID
	if unit.ModuleID() != forgejoWorkloadModuleID || unit.ID() != forgejoWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", forgejoWorkloadModuleID, forgejoWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Forgejo workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != forgejoImageRef ||
		!hasDigest || imageDigest != forgejoImageDigest || !hasEntry || entry != "forgejo" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Forgejo image contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Forgejo requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, forgejoWorkloadModuleID, "dev", 3000, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"owner-password"}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Forgejo requires owner-password slots")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil ||
		!validForgejoSecretRefs(secretRefs) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires opaque owner-password references and no secret material")
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
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != forgejoWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", forgejoWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, err = validateForgejoRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact Forgejo endpoint")
	}
	if err := validateForgejoServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	rootURL, err := applicationHTTPSRootURL(deliveryRoute)
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if _, provided := components[0].Environment["FORGEJO__server__ROOT_URL"]; provided {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "ROOT_URL comes only from the delivery route")
	}
	components[0].Environment["FORGEJO__server__ROOT_URL"] = rootURL
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "ForgejoWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "dev", "forgejo"
	bundle.Workload.ModuleRef, bundle.Workload.Release = forgejoWorkloadModuleID, forgejoRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateForgejoRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 {
		return nil, fail(ErrInvalidPlan, path, "requires the single Forgejo writer")
	}
	c := components[0]
	var command []string
	if err := json.Unmarshal([]byte(forgejoCommandJSON), &command); err != nil {
		return nil, err
	}
	if c.ID != "forgejo" || c.Role != "application" || c.Lifecycle != "daemon" || c.Egress || len(c.DependsOn) != 0 || len(c.Entrypoint) != 0 || !exactStringList(c.NetworkRefs, []string{"forgejo-internal"}) || !exactStringList(c.Command, command) || c.Image.Ref != forgejoImageRef || c.Image.Digest != forgejoImageDigest {
		return nil, fail(ErrInvalidPlan, path, "Forgejo runtime differs from its catalog authority")
	}
	expected := map[string]string{
		"FORGEJO__database__DB_TYPE": "sqlite3", "FORGEJO__database__PATH": "/var/lib/gitea/data/forgejo.db",
		"FORGEJO__security__INSTALL_LOCK": "true", "FORGEJO__service__DISABLE_REGISTRATION": "true",
		"FORGEJO__service__REQUIRE_SIGNIN_VIEW": "true", "FORGEJO__repository__FORCE_PRIVATE": "true",
		"FORGEJO__repository__DEFAULT_PRIVATE": "private", "FORGEJO__server__DISABLE_SSH": "true",
		"FORGEJO__actions__ENABLED": "false", "FORGEJO__security__REVERSE_PROXY_LIMIT": "0",
	}
	// ROOT_URL is added only from the validated delivery route below.
	actual := make(map[string]string, len(c.Environment))
	for k, v := range c.Environment {
		if k != "FORGEJO__server__ROOT_URL" {
			actual[k] = v
		}
	}
	if !reflect.DeepEqual(actual, expected) || !reflect.DeepEqual(c.OwnerEnvironment, map[string]string{"STACKKITS_OWNER_EMAIL": "email"}) || !reflect.DeepEqual(c.SecretEnvironment, map[string]string{"STACKKITS_OWNER_PASSWORD": "owner-password"}) || c.Health.Kind != "http" || c.Health.Port != 3000 || c.Health.Path != "/api/healthz" || len(c.Health.Command) != 0 {
		return nil, fail(ErrInvalidPlan, path, "Forgejo owner, private repository or SQLite boundary differs")
	}
	if len(c.Volumes) != 1 {
		return nil, fail(ErrInvalidPlan, path, "repositories, SQLite and app.ini persist in the single data volume")
	}
	seen := map[string]bool{}
	for _, v := range c.Volumes {
		target := map[string]string{"data": "/var/lib/gitea"}[v.ID]
		if target == "" || seen[v.ID] || v.Target != target || v.Class != "persistent" || !v.Backup || v.ReadOnly || v.HostPath != "" {
			return nil, fail(ErrInvalidPlan, path, "Forgejo data backup scope differs")
		}
		seen[v.ID] = true
	}
	return components, nil
}

func validateForgejoServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "dev" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 3000 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "forgejo-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "dev" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "Forgejo route authority differs from the governed Forgejo endpoint")
	}
	return nil
}

func validForgejoSecretRefs(refs map[string]string) bool {
	return len(refs) == 1 && validSecretReference(refs["owner-password"])
}
