package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"reflect"
)

const (
	nextcloudWorkloadModuleID    = "stackkits-nextcloud-runtime"
	nextcloudWorkloadUnitID      = "nextcloud"
	nextcloudWorkloadTemplateRef = "builtin://workloads/nextcloud/bundle/v2.json"
	nextcloudWorkloadVersion     = "2.0.0"
	nextcloudWorkloadOutputRef   = "workloads/nextcloud/bundle.json"
)

// Schema identity is independent of the upstream release; exact images come
// from the generated catalog projection.
const nextcloudWorkloadRendererSchema = `stackkit.workload-bundle/v2|NextcloudWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:nextcloud,postgres,valkey|health:http-status-80|secret-material:not-included`

// NextcloudWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the selected application adapter.
type NextcloudWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func NextcloudWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(nextcloudWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: nextcloudWorkloadTemplateRef,
		Version: nextcloudWorkloadVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type nextcloudWorkloadBundleRenderer struct{ contract RendererContract }

func newNextcloudWorkloadBundleRenderer() nextcloudWorkloadBundleRenderer {
	return nextcloudWorkloadBundleRenderer{contract: NextcloudWorkloadBundleRendererContract()}
}

func (r nextcloudWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateNextcloudWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.nextcloud-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: nextcloudWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseNextcloudWorkloadBundle validates the closed artifact before a runtime
// owner may consume it. The database password stays an opaque reference.
func ParseNextcloudWorkloadBundle(data []byte) (NextcloudWorkloadBundleDescriptor, error) {
	path := "nextcloudWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return NextcloudWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Nextcloud workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "NextcloudWorkloadBundle" ||
		bundle.Workload.Ref != "files" || bundle.Workload.AlternativeRef != "nextcloud" ||
		bundle.Workload.ModuleRef != nextcloudWorkloadModuleID || bundle.Workload.Release != nextcloudRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != nextcloudWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return NextcloudWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Nextcloud contract")
	}
	if !validNextcloudSecretRefs(bundle.SecretRefs) || len(bundle.ConfigFiles) != 0 {
		return NextcloudWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "requires only the database secret references and no startup file override")
	}
	if bundle.DeliveryRoute == nil {
		return NextcloudWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".deliveryRoute", "Nextcloud requires its declared HTTPS origin")
	}
	if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, nextcloudWorkloadModuleID, "files", 80, path+".deliveryRoute"); err != nil {
		return NextcloudWorkloadBundleDescriptor{}, err
	}
	origin, err := applicationHTTPSRootURL(bundle.DeliveryRoute)
	if err != nil {
		return NextcloudWorkloadBundleDescriptor{}, err
	}
	components, err := validateNextcloudRuntimeComponents(bundle.Components, path+".components", origin)
	if err != nil {
		return NextcloudWorkloadBundleDescriptor{}, err
	}
	if err := validateNextcloudServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return NextcloudWorkloadBundleDescriptor{}, err
	}
	descriptor := NextcloudWorkloadBundleDescriptor{
		WorkloadRef: "files", ModuleRef: nextcloudWorkloadModuleID, Release: nextcloudRelease,
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
func validateNextcloudWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + nextcloudWorkloadModuleID + ".renderUnits." + nextcloudWorkloadUnitID
	if unit.ModuleID() != nextcloudWorkloadModuleID || unit.ID() != nextcloudWorkloadUnitID ||
		unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Nextcloud contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" || !hasEngine || engine != "docker" ||
		!hasImage || imageRef != nextcloudImageRef || !hasDigest || imageDigest != nextcloudImageDigest || !hasEntry || entry != nextcloudWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity differs from the pinned Nextcloud image")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Nextcloud requires one exact node-local target")
	}
	if selectedPaaSUnitHasDaemonAuthority(unit) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "application adapter receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, nextcloudWorkloadModuleID, "files", 80, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute == nil {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Nextcloud requires a declared delivery route")
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"database-password", "owner-password"}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Nextcloud requires exactly the database and owner secret slots")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || !validNextcloudSecretRefs(secretRefs) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires a declared opaque secret reference")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".interfaces", "Nextcloud receives no host or runtime-network authority")
	}
	var placement struct{ Scope, Cardinality string }
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != nextcloudWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", nextcloudWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode component graph", err)
	}
	components, err = validateNextcloudRuntimeComponents(components, path+".runtime.components", "")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact Nextcloud endpoint")
	}
	if err := validateNextcloudServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	origin, err := applicationHTTPSRootURL(deliveryRoute)
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	// Trusted domain and overwrite host come only from the validated delivery route.
	host, err := nextcloudRouteHost(origin)
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	for index := range components {
		if components[index].ID == nextcloudWorkloadUnitID {
			components[index].Environment["NEXTCLOUD_TRUSTED_DOMAINS"], components[index].Environment["OVERWRITEHOST"] = host, host
		}
	}
	bundle := selectedPaaSWorkloadBundle{APIVersion: "stackkit.workload-bundle/v2", Kind: "NextcloudWorkloadBundle", SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "files", "nextcloud"
	bundle.Workload.ModuleRef, bundle.Workload.Release = nextcloudWorkloadModuleID, nextcloudRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter, bundle.Ownership.ProviderLifecycle, bundle.Ownership.Credentials = "selected-application-adapter", "not-owned", "opaque-references-only"
	return bundle, nil
}

func validateNextcloudRuntimeComponents(components []selectedPaaSRuntimeComponent, path, routeOrigin string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 3 {
		return nil, fail(ErrInvalidPlan, path, "requires Nextcloud, PostgreSQL and Valkey")
	}
	seen := map[string]bool{}
	for _, c := range components {
		if seen[c.ID] || c.Lifecycle != "daemon" || c.Egress || len(c.Entrypoint) != 0 || len(c.OwnerEnvironment) != 0 || !exactStringList(c.NetworkRefs, []string{"nextcloud-internal"}) {
			return nil, fail(ErrInvalidPlan, path, "component identity, lifecycle or network differs")
		}
		seen[c.ID] = true
		switch c.ID {
		case "nextcloud":
			expected := map[string]string{
				"POSTGRES_HOST": "nextcloud-postgres", "POSTGRES_DB": "nextcloud", "POSTGRES_USER": "nextcloud",
				"REDIS_HOST": "nextcloud-valkey", "NEXTCLOUD_ADMIN_USER": "owner", "OVERWRITEPROTOCOL": "https",
				"TRUSTED_PROXIES": "10.0.0.0/8 172.16.0.0/12 192.168.0.0/16",
			}
			if routeOrigin != "" {
				host, err := nextcloudRouteHost(routeOrigin)
				if err != nil {
					return nil, err
				}
				expected["NEXTCLOUD_TRUSTED_DOMAINS"], expected["OVERWRITEHOST"] = host, host
			}
			if c.Role != "application" || len(c.Command) != 0 || c.Image.Ref != nextcloudImageRef || c.Image.Digest != nextcloudImageDigest ||
				!exactStringList(c.DependsOn, []string{"nextcloud-postgres", "nextcloud-valkey"}) || !reflect.DeepEqual(c.Environment, expected) ||
				!reflect.DeepEqual(c.SecretEnvironment, map[string]string{"POSTGRES_PASSWORD": "database-password", "NEXTCLOUD_ADMIN_PASSWORD": "owner-password"}) ||
				c.Health.Kind != "http" || c.Health.Path != "/status.php" || c.Health.Port != 80 || len(c.Health.Command) != 0 ||
				len(c.Volumes) != 1 || !volumeMatches(c.Volumes[0], "html", "/var/www/html", true) {
				return nil, fail(ErrInvalidPlan, path, "Nextcloud image, owner bootstrap, database, domain or persistence differs")
			}
		case "nextcloud-postgres":
			if c.Role != "database" || len(c.Command) != 0 || c.Image.Ref != nextcloudPostgresImageRef || c.Image.Digest != nextcloudPostgresImageDigest || len(c.DependsOn) != 0 ||
				!reflect.DeepEqual(c.Environment, map[string]string{"POSTGRES_DB": "nextcloud", "POSTGRES_USER": "nextcloud"}) ||
				!reflect.DeepEqual(c.SecretEnvironment, map[string]string{"POSTGRES_PASSWORD": "database-password"}) ||
				c.Health.Kind != "command" || !exactStringList(c.Health.Command, []string{"pg_isready", "-U", "nextcloud", "-d", "nextcloud"}) ||
				len(c.Volumes) != 1 || !volumeMatches(c.Volumes[0], "database", "/var/lib/postgresql", true) {
				return nil, fail(ErrInvalidPlan, path, "PostgreSQL image, credentials or persistent data differs")
			}
		case "nextcloud-valkey":
			if c.Role != "cache" || c.Image.Ref != nextcloudValkeyImageRef || c.Image.Digest != nextcloudValkeyImageDigest || len(c.DependsOn) != 0 ||
				!exactStringList(c.Command, []string{"valkey-server"}) || len(c.Environment) != 0 || len(c.SecretEnvironment) != 0 ||
				c.Health.Kind != "command" || !exactStringList(c.Health.Command, []string{"valkey-cli", "ping"}) ||
				len(c.Volumes) != 1 || !volumeMatches(c.Volumes[0], "cache", "/data", false) {
				return nil, fail(ErrInvalidPlan, path, "Valkey cache contract differs")
			}
		default:
			return nil, fail(ErrInvalidPlan, path, "unknown Nextcloud component")
		}
	}
	return components, nil
}

// nextcloudRouteHost is the host[:port] Nextcloud trusts and writes into its
// generated links; it comes only from the validated delivery route.
func nextcloudRouteHost(origin string) (string, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return "", fail(ErrInvalidPlan, "nextcloud.deliveryRoute", "route origin has no host")
	}
	return parsed.Host, nil
}

func validateNextcloudServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "files" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 80 || endpoint.RequiredPrivilege != "user" ||
		endpoint.OriginSelector != "control-authority-site" || endpoint.HealthRef != "nextcloud-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) ||
		endpoint.Data.BindingRef != "files" || endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) || !sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "Nextcloud route authority differs")
	}
	return nil
}

func validNextcloudSecretRefs(refs map[string]string) bool {
	return len(refs) == 2 && validSecretReference(refs["database-password"]) && validSecretReference(refs["owner-password"])
}
