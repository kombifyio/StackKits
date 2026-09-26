package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
)

const (
	passboltWorkloadModuleID    = "stackkits-passbolt-runtime"
	passboltWorkloadUnitID      = "passbolt"
	passboltWorkloadTemplateRef = "builtin://workloads/passbolt/bundle/v2.json"
	passboltWorkloadVersion     = "2.0.0"
	passboltWorkloadOutputRef   = "workloads/passbolt/bundle.json"
)

// Schema identity is independent of the upstream release; exact images come
// from the generated catalog projection.
const passboltWorkloadRendererSchema = `stackkit.workload-bundle/v2|PassboltWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:passbolt,mariadb|health:http-status-8080|secret-material:not-included`

// PassboltWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the selected application adapter.
type PassboltWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func PassboltWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(passboltWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: passboltWorkloadTemplateRef,
		Version: passboltWorkloadVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type passboltWorkloadBundleRenderer struct{ contract RendererContract }

func newPassboltWorkloadBundleRenderer() passboltWorkloadBundleRenderer {
	return passboltWorkloadBundleRenderer{contract: PassboltWorkloadBundleRendererContract()}
}

func (r passboltWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validatePassboltWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.passbolt-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: passboltWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParsePassboltWorkloadBundle validates the closed artifact before a runtime
// owner may consume it. The database password stays an opaque reference.
func ParsePassboltWorkloadBundle(data []byte) (PassboltWorkloadBundleDescriptor, error) {
	path := "passboltWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return PassboltWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Passbolt workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "PassboltWorkloadBundle" ||
		bundle.Workload.Ref != "vault" || bundle.Workload.AlternativeRef != "passbolt" ||
		bundle.Workload.ModuleRef != passboltWorkloadModuleID || bundle.Workload.Release != passboltRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != passboltWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return PassboltWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Passbolt contract")
	}
	if !validPassboltSecretRefs(bundle.SecretRefs) || len(bundle.ConfigFiles) != 0 {
		return PassboltWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "requires only the database secret references and no startup file override")
	}
	if bundle.DeliveryRoute == nil {
		return PassboltWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".deliveryRoute", "Passbolt requires its declared HTTPS origin")
	}
	if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, passboltWorkloadModuleID, "vault", 8080, path+".deliveryRoute"); err != nil {
		return PassboltWorkloadBundleDescriptor{}, err
	}
	origin, err := applicationHTTPSRootURL(bundle.DeliveryRoute)
	if err != nil {
		return PassboltWorkloadBundleDescriptor{}, err
	}
	components, err := validatePassboltRuntimeComponents(bundle.Components, path+".components", origin)
	if err != nil {
		return PassboltWorkloadBundleDescriptor{}, err
	}
	if err := validatePassboltServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return PassboltWorkloadBundleDescriptor{}, err
	}
	descriptor := PassboltWorkloadBundleDescriptor{
		WorkloadRef: "vault", ModuleRef: passboltWorkloadModuleID, Release: passboltRelease,
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
func validatePassboltWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + passboltWorkloadModuleID + ".renderUnits." + passboltWorkloadUnitID
	if unit.ModuleID() != passboltWorkloadModuleID || unit.ID() != passboltWorkloadUnitID ||
		unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Passbolt contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" || !hasEngine || engine != "docker" ||
		!hasImage || imageRef != passboltImageRef || !hasDigest || imageDigest != passboltImageDigest || !hasEntry || entry != passboltWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity differs from the pinned Passbolt image")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Passbolt requires one exact node-local target")
	}
	if selectedPaaSUnitHasDaemonAuthority(unit) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "application adapter receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, passboltWorkloadModuleID, "vault", 8080, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute == nil {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Passbolt requires a declared delivery route")
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"database-password", "database-root-password"}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Passbolt requires exactly the database and database-root secret slots")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || !validPassboltSecretRefs(secretRefs) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires a declared opaque secret reference")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".interfaces", "Passbolt receives no host or runtime-network authority")
	}
	var placement struct{ Scope, Cardinality string }
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != passboltWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", passboltWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode component graph", err)
	}
	components, err = validatePassboltRuntimeComponents(components, path+".runtime.components", "")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact Passbolt endpoint")
	}
	if err := validatePassboltServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	origin, err := applicationHTTPSRootURL(deliveryRoute)
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	// The public origin comes only from the validated delivery route; Passbolt
	// builds links by appending paths, so it takes the origin without a slash.
	for index := range components {
		if components[index].ID == passboltWorkloadUnitID {
			components[index].Environment["APP_FULL_BASE_URL"] = strings.TrimSuffix(origin, "/")
		}
	}
	bundle := selectedPaaSWorkloadBundle{APIVersion: "stackkit.workload-bundle/v2", Kind: "PassboltWorkloadBundle", SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "vault", "passbolt"
	bundle.Workload.ModuleRef, bundle.Workload.Release = passboltWorkloadModuleID, passboltRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter, bundle.Ownership.ProviderLifecycle, bundle.Ownership.Credentials = "selected-application-adapter", "not-owned", "opaque-references-only"
	return bundle, nil
}

func validatePassboltRuntimeComponents(components []selectedPaaSRuntimeComponent, path, routeOrigin string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 2 {
		return nil, fail(ErrInvalidPlan, path, "requires Passbolt and MariaDB")
	}
	seen := map[string]bool{}
	for _, c := range components {
		if seen[c.ID] || c.Lifecycle != "daemon" || c.Egress || len(c.Entrypoint) != 0 || len(c.OwnerEnvironment) != 0 || !exactStringList(c.NetworkRefs, []string{"passbolt-internal"}) {
			return nil, fail(ErrInvalidPlan, path, "component identity, lifecycle or network differs")
		}
		seen[c.ID] = true
		switch c.ID {
		case "passbolt":
			expected := map[string]string{
				"DATASOURCES_DEFAULT_HOST": "passbolt-mariadb", "DATASOURCES_DEFAULT_USERNAME": "passbolt",
				"DATASOURCES_DEFAULT_DATABASE": "passbolt", "PASSBOLT_SSL_FORCE": "false", "PASSBOLT_REGISTRATION_PUBLIC": "false",
			}
			if routeOrigin != "" {
				expected["APP_FULL_BASE_URL"] = strings.TrimSuffix(routeOrigin, "/")
			}
			if c.Role != "application" || c.Image.Ref != passboltImageRef || c.Image.Digest != passboltImageDigest ||
				!exactStringList(c.DependsOn, []string{"passbolt-mariadb"}) || !exactStringList(c.Command, passboltCommand) || !reflect.DeepEqual(c.Environment, expected) ||
				!reflect.DeepEqual(c.SecretEnvironment, map[string]string{"DATASOURCES_DEFAULT_PASSWORD": "database-password"}) ||
				c.Health.Kind != "http" || c.Health.Path != "/healthcheck/status.json" || c.Health.Port != 8080 || len(c.Health.Command) != 0 ||
				len(c.Volumes) != 2 || !volumeMatches(c.Volumes[0], "gpg", "/etc/passbolt/gpg", true) || !volumeMatches(c.Volumes[1], "jwt", "/etc/passbolt/jwt", true) {
				return nil, fail(ErrInvalidPlan, path, "Passbolt image, database, origin or key persistence differs")
			}
		case "passbolt-mariadb":
			if c.Role != "database" || len(c.Command) != 0 || c.Image.Ref != passboltMariaDBImageRef || c.Image.Digest != passboltMariaDBImageDigest || len(c.DependsOn) != 0 ||
				!reflect.DeepEqual(c.Environment, map[string]string{"MARIADB_DATABASE": "passbolt", "MARIADB_USER": "passbolt"}) ||
				!reflect.DeepEqual(c.SecretEnvironment, map[string]string{"MARIADB_PASSWORD": "database-password", "MARIADB_ROOT_PASSWORD": "database-root-password"}) ||
				c.Health.Kind != "command" || !exactStringList(c.Health.Command, []string{"healthcheck.sh", "--connect", "--innodb_initialized"}) ||
				len(c.Volumes) != 1 || !volumeMatches(c.Volumes[0], "database", "/var/lib/mysql", true) {
				return nil, fail(ErrInvalidPlan, path, "MariaDB image, credentials or persistent data differs")
			}
		default:
			return nil, fail(ErrInvalidPlan, path, "unknown Passbolt component")
		}
	}
	return components, nil
}

func validatePassboltServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "vault" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 8080 || endpoint.RequiredPrivilege != "user" ||
		endpoint.OriginSelector != "control-authority-site" || endpoint.HealthRef != "passbolt-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) ||
		endpoint.Data.BindingRef != "vault" || endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"secret"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) || !sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "Passbolt route authority differs")
	}
	return nil
}

func validPassboltSecretRefs(refs map[string]string) bool {
	return len(refs) == 2 && validSecretReference(refs["database-password"]) && validSecretReference(refs["database-root-password"])
}

// selectedPaaSUnitHasDaemonAuthority reports whether a render unit carries any
// daemon or socket authority, which selected application workloads never get.
func selectedPaaSUnitHasDaemonAuthority(unit RenderUnit) bool {
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	return hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket
}

// passboltCommand waits for MariaDB before Passbolt's install and migrations.
var passboltCommand = []string{"/usr/bin/wait-for.sh", "-t", "0", "passbolt-mariadb:3306", "--", "/docker-entrypoint.sh"}
