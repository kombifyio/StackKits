package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const (
	paperlessWorkloadModuleID    = "stackkits-paperless-runtime"
	paperlessWorkloadUnitID      = "paperless"
	paperlessWorkloadTemplateRef = "builtin://workloads/paperless-ngx/bundle/v1.json"
	paperlessWorkloadVersion     = "1.0.0"
	paperlessWorkloadOutputRef   = "workloads/paperless-ngx/bundle.json"
)

const paperlessWorkloadRendererSchema = `stackkit.workload-bundle/v2|PaperlessWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:paperless,postgres,valkey|health:http-root-8000|release:` + paperlessRelease + `|secret-material:not-included`

// PaperlessWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the selected application adapter.
type PaperlessWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func PaperlessWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(paperlessWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: paperlessWorkloadTemplateRef,
		Version: paperlessWorkloadVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type paperlessWorkloadBundleRenderer struct{ contract RendererContract }

func newPaperlessWorkloadBundleRenderer() paperlessWorkloadBundleRenderer {
	return paperlessWorkloadBundleRenderer{contract: PaperlessWorkloadBundleRendererContract()}
}

func (r paperlessWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validatePaperlessWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.paperless-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: paperlessWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParsePaperlessWorkloadBundle validates the closed artifact before a runtime
// owner may consume it. Secret values remain opaque references.
func ParsePaperlessWorkloadBundle(data []byte) (PaperlessWorkloadBundleDescriptor, error) {
	path := "paperlessWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return PaperlessWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Paperless workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "PaperlessWorkloadBundle" ||
		bundle.Workload.Ref != "documents" || bundle.Workload.AlternativeRef != "paperless-ngx" ||
		bundle.Workload.ModuleRef != paperlessWorkloadModuleID || bundle.Workload.Release != paperlessRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != "paperless" ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return PaperlessWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Paperless contract")
	}
	if !validPaperlessSecretRefs(bundle.SecretRefs) || len(bundle.ConfigFiles) != 0 {
		return PaperlessWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "requires only the declared opaque secrets and no startup file override")
	}
	if bundle.DeliveryRoute == nil {
		return PaperlessWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".deliveryRoute", "Paperless requires its declared HTTPS origin")
	}
	if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, paperlessWorkloadModuleID, "documents", 8000, path+".deliveryRoute"); err != nil {
		return PaperlessWorkloadBundleDescriptor{}, err
	}
	origin, err := applicationHTTPSRootURL(bundle.DeliveryRoute)
	if err != nil {
		return PaperlessWorkloadBundleDescriptor{}, err
	}
	components, err := validatePaperlessRuntimeComponents(bundle.Components, path+".components", origin)
	if err != nil {
		return PaperlessWorkloadBundleDescriptor{}, err
	}
	if err := validatePaperlessServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return PaperlessWorkloadBundleDescriptor{}, err
	}
	descriptor := PaperlessWorkloadBundleDescriptor{
		WorkloadRef: "documents", ModuleRef: paperlessWorkloadModuleID, Release: paperlessRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Components: make([]SelectedPaaSWorkloadComponentDescriptor, len(components)), Route: bundle.DeliveryRoute.descriptor(),
	}
	for index, component := range components {
		descriptor.Components[index] = SelectedPaaSWorkloadComponentDescriptor{
			ID: component.ID, Lifecycle: component.Lifecycle, ImageRef: component.Image.Ref, ImageDigest: component.Image.Digest,
		}
	}
	return descriptor, nil
}

//nolint:gocyclo // One boundary validates the complete upstream service graph.
func validatePaperlessWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + paperlessWorkloadModuleID + ".renderUnits." + paperlessWorkloadUnitID
	if unit.ModuleID() != paperlessWorkloadModuleID || unit.ID() != paperlessWorkloadUnitID ||
		unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Paperless contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" || !hasEngine || engine != "docker" ||
		!hasImage || imageRef != paperlessImageRef || !hasDigest || imageDigest != paperlessImageDigest || !hasEntry || entry != "paperless" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity differs from the pinned Paperless image")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Paperless requires one exact node-local target")
	}
	if _, ok := unit.DaemonRef(); ok {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "application adapter receives no daemon authority")
	}
	if _, ok := unit.DaemonInstanceRef(); ok {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "application adapter receives no daemon authority")
	}
	if _, ok := unit.DaemonEngine(); ok {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "application adapter receives no daemon authority")
	}
	if _, ok := unit.DaemonSocketPath(); ok {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "application adapter receives no socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, paperlessWorkloadModuleID, "documents", 8000, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute == nil {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Paperless requires a declared delivery route")
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"database-password", "owner-password", "session-key"}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Paperless requires database, owner and session secret slots")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || !validPaperlessSecretRefs(secretRefs) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires declared opaque secret references")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".interfaces", "Paperless receives no host or runtime-network authority")
	}
	var placement struct{ Scope, Cardinality string }
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != paperlessWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", paperlessWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode component graph", err)
	}
	components, err = validatePaperlessRuntimeComponents(components, path+".runtime.components", "")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact Paperless endpoint")
	}
	if err := validatePaperlessServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	origin, err := applicationHTTPSRootURL(deliveryRoute)
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	for index := range components {
		if components[index].ID == "paperless" {
			components[index].Environment["PAPERLESS_URL"] = origin
			components[index].Environment["PAPERLESS_CSRF_TRUSTED_ORIGINS"] = origin
			break
		}
	}
	bundle := selectedPaaSWorkloadBundle{APIVersion: "stackkit.workload-bundle/v2", Kind: "PaperlessWorkloadBundle", SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "documents", "paperless-ngx"
	bundle.Workload.ModuleRef, bundle.Workload.Release = paperlessWorkloadModuleID, paperlessRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter, bundle.Ownership.ProviderLifecycle, bundle.Ownership.Credentials = "selected-application-adapter", "not-owned", "opaque-references-only"
	return bundle, nil
}

func validatePaperlessRuntimeComponents(components []selectedPaaSRuntimeComponent, path, routeOrigin string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 3 {
		return nil, fail(ErrInvalidPlan, path, "requires Paperless, PostgreSQL and Valkey")
	}
	seen := map[string]bool{}
	for _, c := range components {
		if seen[c.ID] || c.Lifecycle != "daemon" || c.Egress || len(c.Entrypoint) != 0 || !exactStringList(c.NetworkRefs, []string{"paperless-internal"}) {
			return nil, fail(ErrInvalidPlan, path, "component identity, lifecycle or network differs")
		}
		seen[c.ID] = true
		switch c.ID {
		case "paperless":
			expected := map[string]string{"PAPERLESS_REDIS": "redis://paperless-valkey:6379", "PAPERLESS_DBHOST": "paperless-postgres", "PAPERLESS_DBENGINE": "postgresql", "PAPERLESS_DBNAME": "paperless", "PAPERLESS_DBUSER": "paperless", "PAPERLESS_ADMIN_USER": "owner"}
			if routeOrigin != "" {
				expected["PAPERLESS_URL"], expected["PAPERLESS_CSRF_TRUSTED_ORIGINS"] = routeOrigin, routeOrigin
			}
			if c.Role != "application" || c.Image.Ref != paperlessImageRef || c.Image.Digest != paperlessImageDigest || !exactStringList(c.DependsOn, []string{"paperless-postgres", "paperless-valkey"}) || len(c.Command) != 0 || !reflect.DeepEqual(c.Environment, expected) || !reflect.DeepEqual(c.OwnerEnvironment, map[string]string{"PAPERLESS_ADMIN_MAIL": "email"}) || !reflect.DeepEqual(c.SecretEnvironment, map[string]string{"PAPERLESS_DBPASS": "database-password", "PAPERLESS_ADMIN_PASSWORD": "owner-password", "PAPERLESS_SECRET_KEY": "session-key"}) || c.Health.Kind != "http" || c.Health.Path != "/" || c.Health.Port != 8000 || len(c.Health.Command) != 0 {
				return nil, fail(ErrInvalidPlan, path, "Paperless image, owner bootstrap, database or route configuration differs")
			}
			if !paperlessVolumesValid(c.Volumes) {
				return nil, fail(ErrInvalidPlan, path, "Paperless data, media, consume and export persistence differs")
			}
		case "paperless-postgres":
			if c.Role != "database" || c.Image.Ref != paperlessPostgresImageRef || c.Image.Digest != paperlessPostgresImageDigest || len(c.DependsOn) != 0 || len(c.Command) != 0 || !reflect.DeepEqual(c.Environment, map[string]string{"POSTGRES_DB": "paperless", "POSTGRES_USER": "paperless"}) || len(c.OwnerEnvironment) != 0 || !reflect.DeepEqual(c.SecretEnvironment, map[string]string{"POSTGRES_PASSWORD": "database-password"}) || c.Health.Kind != "command" || !exactStringList(c.Health.Command, []string{"pg_isready", "-U", "paperless", "-d", "paperless"}) || len(c.Volumes) != 1 || !volumeMatches(c.Volumes[0], "database", "/var/lib/postgresql", true) {
				return nil, fail(ErrInvalidPlan, path, "PostgreSQL image, credentials or persistent data differs")
			}
		case "paperless-valkey":
			if c.Role != "cache" || c.Image.Ref != paperlessValkeyImageRef || c.Image.Digest != paperlessValkeyImageDigest || len(c.DependsOn) != 0 || !exactStringList(c.Command, []string{"valkey-server"}) || len(c.Environment) != 0 || len(c.OwnerEnvironment) != 0 || len(c.SecretEnvironment) != 0 || c.Health.Kind != "command" || !exactStringList(c.Health.Command, []string{"valkey-cli", "ping"}) || len(c.Volumes) != 1 || !volumeMatches(c.Volumes[0], "cache", "/data", false) {
				return nil, fail(ErrInvalidPlan, path, "Valkey cache contract differs")
			}
		default:
			return nil, fail(ErrInvalidPlan, path, "unknown Paperless component")
		}
	}
	return components, nil
}

func paperlessVolumesValid(volumes []selectedPaaSRuntimeVolume) bool {
	if len(volumes) != 4 {
		return false
	}
	expected := map[string]struct {
		target string
		backup bool
	}{"data": {"/usr/src/paperless/data", true}, "media": {"/usr/src/paperless/media", true}, "consume": {"/usr/src/paperless/consume", true}, "export": {"/usr/src/paperless/export", false}}
	seen := map[string]bool{}
	for _, volume := range volumes {
		want, ok := expected[volume.ID]
		if !ok || seen[volume.ID] || !volumeMatches(volume, volume.ID, want.target, want.backup) {
			return false
		}
		seen[volume.ID] = true
	}
	return true
}

func volumeMatches(volume selectedPaaSRuntimeVolume, id, target string, backup bool) bool {
	class := "persistent"
	if id == "cache" {
		class = "cache"
	}
	return volume.ID == id && volume.Target == target && volume.Class == class && volume.Backup == backup && !volume.ReadOnly && volume.HostPath == ""
}

func validatePaperlessServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "documents" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 8000 || endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" || endpoint.HealthRef != "paperless-http" || !validBundleIngressAuthNative(endpoint.IngressAuth) || endpoint.Data.BindingRef != "documents" || endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) || !exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) || !sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "Paperless route authority differs")
	}
	return nil
}

func validPaperlessSecretRefs(refs map[string]string) bool {
	return len(refs) == 3 && validSecretReference(refs["database-password"]) && validSecretReference(refs["owner-password"]) && validSecretReference(refs["session-key"])
}
