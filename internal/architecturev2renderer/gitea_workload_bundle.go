package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const (
	giteaWorkloadModuleID    = "stackkits-gitea-runtime"
	giteaWorkloadUnitID      = "gitea"
	giteaWorkloadTemplateRef = "builtin://workloads/gitea/bundle/v2.json"
	giteaWorkloadVersion     = "2.0.0"
	giteaWorkloadOutputRef   = "workloads/gitea/bundle.json"
)

// Schema identity is independent of the upstream release. Exact image/release
// validation below uses the generated catalog projection, so an image update
// has one source and does not require a second manual render-contract update.
const giteaWorkloadRendererSchema = `stackkit.workload-bundle/v2|GiteaWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:gitea|secret-material:not-included`

// GiteaWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the selected-PaaS executor. OwnerPasswordRef is opaque.
type GiteaWorkloadBundleDescriptor struct {
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

func GiteaWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(giteaWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: giteaWorkloadTemplateRef, Version: giteaWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type giteaWorkloadBundleRenderer struct{ contract RendererContract }

func newGiteaWorkloadBundleRenderer() giteaWorkloadBundleRenderer {
	return giteaWorkloadBundleRenderer{contract: GiteaWorkloadBundleRendererContract()}
}

func (r giteaWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateGiteaWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.gitea-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: giteaWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseGiteaWorkloadBundle validates the closed generated artifact before
// any selected-PaaS owner may consume it.
func ParseGiteaWorkloadBundle(data []byte) (GiteaWorkloadBundleDescriptor, error) {
	path := "giteaWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return GiteaWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Gitea workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "GiteaWorkloadBundle" ||
		bundle.Workload.Ref != "dev" || bundle.Workload.AlternativeRef != "gitea" ||
		bundle.Workload.ModuleRef != giteaWorkloadModuleID || bundle.Workload.Release != giteaRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != "gitea" ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return GiteaWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Gitea image contract")
	}
	if !validGiteaSecretRefs(bundle.SecretRefs) {
		return GiteaWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires opaque owner-password references")
	}
	if len(bundle.ConfigFiles) != 0 {
		return GiteaWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "Gitea runtime accepts no startup configuration overrides")
	}
	components, err := validateGiteaRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return GiteaWorkloadBundleDescriptor{}, err
	}
	if err := validateGiteaServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return GiteaWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, giteaWorkloadModuleID, "dev", 3000, path+".deliveryRoute"); err != nil {
			return GiteaWorkloadBundleDescriptor{}, err
		}
	}
	rootURL, err := applicationHTTPSRootURL(bundle.DeliveryRoute)
	if err != nil {
		return GiteaWorkloadBundleDescriptor{}, err
	}
	if components[0].Environment["GITEA__server__ROOT_URL"] != rootURL {
		return GiteaWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "Gitea clone URL differs from its route")
	}
	descriptor := GiteaWorkloadBundleDescriptor{
		WorkloadRef: "dev", ModuleRef: giteaWorkloadModuleID, Release: giteaRelease,
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

//nolint:gocyclo // Keep the complete Gitea authority check at one boundary.
func validateGiteaWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + giteaWorkloadModuleID + ".renderUnits." + giteaWorkloadUnitID
	if unit.ModuleID() != giteaWorkloadModuleID || unit.ID() != giteaWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", giteaWorkloadModuleID, giteaWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Gitea workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != giteaImageRef ||
		!hasDigest || imageDigest != giteaImageDigest || !hasEntry || entry != "gitea" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Gitea image contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Gitea requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, giteaWorkloadModuleID, "dev", 3000, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"owner-password"}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Gitea requires owner-password slots")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil ||
		!validGiteaSecretRefs(secretRefs) {
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
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != giteaWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", giteaWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, err = validateGiteaRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact Gitea endpoint")
	}
	if err := validateGiteaServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	rootURL, err := applicationHTTPSRootURL(deliveryRoute)
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if _, provided := components[0].Environment["GITEA__server__ROOT_URL"]; provided {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "ROOT_URL comes only from the delivery route")
	}
	components[0].Environment["GITEA__server__ROOT_URL"] = rootURL
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "GiteaWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "dev", "gitea"
	bundle.Workload.ModuleRef, bundle.Workload.Release = giteaWorkloadModuleID, giteaRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateGiteaRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 {
		return nil, fail(ErrInvalidPlan, path, "requires the single Gitea writer")
	}
	c := components[0]
	var command []string
	if err := json.Unmarshal([]byte(giteaCommandJSON), &command); err != nil {
		return nil, err
	}
	if c.ID != "gitea" || c.Role != "application" || c.Lifecycle != "daemon" || c.Egress || len(c.DependsOn) != 0 || len(c.Entrypoint) != 0 || !exactStringList(c.NetworkRefs, []string{"gitea-internal"}) || !exactStringList(c.Command, command) || c.Image.Ref != giteaImageRef || c.Image.Digest != giteaImageDigest {
		return nil, fail(ErrInvalidPlan, path, "Gitea runtime differs from its catalog authority")
	}
	expected := map[string]string{
		"GITEA__database__DB_TYPE": "sqlite3", "GITEA__database__PATH": "/var/lib/gitea/data/gitea.db",
		"GITEA__security__INSTALL_LOCK": "true", "GITEA__service__DISABLE_REGISTRATION": "true",
		"GITEA__service__REQUIRE_SIGNIN_VIEW": "true", "GITEA__repository__FORCE_PRIVATE": "true",
		"GITEA__repository__DEFAULT_PRIVATE": "private", "GITEA__server__DISABLE_SSH": "true",
		"GITEA__actions__ENABLED": "false", "GITEA__security__REVERSE_PROXY_LIMIT": "0",
	}
	// ROOT_URL is added only from the validated delivery route below.
	actual := make(map[string]string, len(c.Environment))
	for k, v := range c.Environment {
		if k != "GITEA__server__ROOT_URL" {
			actual[k] = v
		}
	}
	if !reflect.DeepEqual(actual, expected) || !reflect.DeepEqual(c.OwnerEnvironment, map[string]string{"STACKKITS_OWNER_EMAIL": "email"}) || !reflect.DeepEqual(c.SecretEnvironment, map[string]string{"STACKKITS_OWNER_PASSWORD": "owner-password"}) || c.Health.Kind != "http" || c.Health.Port != 3000 || c.Health.Path != "/api/healthz" || len(c.Health.Command) != 0 {
		return nil, fail(ErrInvalidPlan, path, "Gitea owner, private repository or SQLite boundary differs")
	}
	if len(c.Volumes) != 2 {
		return nil, fail(ErrInvalidPlan, path, "repository data and configuration must persist together")
	}
	seen := map[string]bool{}
	for _, v := range c.Volumes {
		target := map[string]string{"data": "/var/lib/gitea", "config": "/etc/gitea"}[v.ID]
		if target == "" || seen[v.ID] || v.Target != target || v.Class != "persistent" || !v.Backup || v.ReadOnly || v.HostPath != "" {
			return nil, fail(ErrInvalidPlan, path, "Gitea data and config backup scope differs")
		}
		seen[v.ID] = true
	}
	return components, nil
}

func validateGiteaServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "dev" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 3000 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "gitea-http" || !validBundleIngressAuthNative(endpoint.IngressAuth) || endpoint.Data.BindingRef != "dev" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "Gitea route authority differs from the governed Gitea endpoint")
	}
	return nil
}

func validGiteaSecretRefs(refs map[string]string) bool {
	return len(refs) == 1 && validSecretReference(refs["owner-password"])
}
