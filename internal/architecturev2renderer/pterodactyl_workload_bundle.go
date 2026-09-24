package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
)

const (
	pterodactylWorkloadModuleID    = "stackkits-pterodactyl-runtime"
	pterodactylWorkloadUnitID      = "pterodactyl"
	pterodactylWorkloadTemplateRef = "builtin://workloads/pterodactyl/bundle/v1.json"
	pterodactylWorkloadVersion     = "1.0.0"
	pterodactylWorkloadOutputRef   = "workloads/pterodactyl/bundle.json"
	pterodactylDaemonRef           = "docker-default"
	pterodactylPolicyProfile       = "docker-game-node-lifecycle"
	pterodactylApprovalID          = "approve-pterodactyl-wings-lifecycle-owner"
	pterodactylApprovalEvidence    = "pterodactyl-wings-lifecycle-owner-governance"

	// PterodactylGameDataTarget is where Wings and the bootstrap see the game
	// data volume; the executor additionally mounts it at its own host path.
	PterodactylGameDataTarget = "/stackkit/game-data"
	// PterodactylGameDataHostPathEnv names the host path the executor exports.
	PterodactylGameDataHostPathEnv = "STACKKIT_GAME_DATA_HOST_PATH"
	// PterodactylBedrockEggSHA256 pins the imported official Bedrock Egg
	// (pterodactyl/game-eggs 342628869f5e145b4e99691a6bedacfc06d3ec02).
	PterodactylBedrockEggSHA256 = "7723befb387894afeccc042f25560550a22785bc867e12a2d0254f0259f75e6b"
	// PterodactylTerrariaEggSHA256 and PterodactylValheimEggSHA256 pin the
	// official vanilla Eggs (pterodactyl/game-eggs c637a6d1e0449b167efeff81bb9a0177aa3df6c2).
	PterodactylTerrariaEggSHA256 = "977a25b86d7613b62ca00403528ee41ecd131fee4fe75f99543393b558047ac4"
	PterodactylValheimEggSHA256  = "6b027d661b24523a67630d5f96482eaaa83897af7c446fbb17eb8bf270d10ed7"
)

var (
	//go:embed assets/pterodactyl/egg-minecraft-bedrock.json
	pterodactylBedrockEgg string
	//go:embed assets/pterodactyl/egg-terraria-vanilla.json
	pterodactylTerrariaEgg string
	//go:embed assets/pterodactyl/egg-valheim-vanilla.json
	pterodactylValheimEgg string
)

// pterodactylEggs are the imported Eggs beyond the Panel's seeded catalog,
// each pinned by content digest.
var pterodactylEggs = []struct{ Path, Body, SHA256 string }{
	{Path: "/stackkit/eggs/minecraft-bedrock.json", Body: pterodactylBedrockEgg, SHA256: PterodactylBedrockEggSHA256},
	{Path: "/stackkit/eggs/terraria-vanilla.json", Body: pterodactylTerrariaEgg, SHA256: PterodactylTerrariaEggSHA256},
	{Path: "/stackkit/eggs/valheim-vanilla.json", Body: pterodactylValheimEgg, SHA256: PterodactylValheimEggSHA256},
}

var pterodactylSecretSlots = []string{"database-password", "database-root-password", "app-key", "hashids-salt", "owner-password", "application-api-key", "client-api-key"}

const pterodactylWorkloadRendererSchema = `stackkit.workload-bundle/v2|PterodactylWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:panel,panel-database,panel-cache,panel-bootstrap,wings|wings:docker-socket-direct-v1/lifecycle-owner|game-data:self-path|panel:route-host-loopback,node-tls-local,nginx-node-proxy|egg:minecraft-bedrock/` + PterodactylBedrockEggSHA256 + `,terraria-vanilla/` + PterodactylTerrariaEggSHA256 + `,valheim-vanilla/` + PterodactylValheimEggSHA256 + `|release:` + pterodactylPanelRelease + `+wings-` + pterodactylWingsRelease + `|secret-material:not-included`

// PterodactylWorkloadBundleDescriptor is the closed runtime artifact accepted
// by the standalone application adapter for the Game workload (ADR-0043).
type PterodactylWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Route       ApplicationDeliveryRouteDescriptor
}

func PterodactylWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(pterodactylWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: pterodactylWorkloadTemplateRef,
		Version: pterodactylWorkloadVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type pterodactylWorkloadBundleRenderer struct{ contract RendererContract }

func newPterodactylWorkloadBundleRenderer() pterodactylWorkloadBundleRenderer {
	return pterodactylWorkloadBundleRenderer{contract: PterodactylWorkloadBundleRendererContract()}
}

func (r pterodactylWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validatePterodactylWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.pterodactyl-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: pterodactylWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParsePterodactylWorkloadBundle validates the closed artifact before a
// runtime owner may consume it.
func ParsePterodactylWorkloadBundle(data []byte) (PterodactylWorkloadBundleDescriptor, error) {
	path := "pterodactylWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return PterodactylWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Pterodactyl workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "PterodactylWorkloadBundle" ||
		bundle.Workload.Ref != "game" || bundle.Workload.AlternativeRef != "pterodactyl" ||
		bundle.Workload.ModuleRef != pterodactylWorkloadModuleID || bundle.Workload.Release != pterodactylPanelRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != "panel" ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return PterodactylWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Pterodactyl contract")
	}
	if !validPterodactylSecretRefs(bundle.SecretRefs) || validateDockerSocketPath(bundle.DaemonSocketPath) != nil {
		return PterodactylWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "requires the declared opaque secrets and the approved Docker socket")
	}
	if bundle.DeliveryRoute == nil {
		return PterodactylWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".deliveryRoute", "Pterodactyl requires its declared HTTPS origin")
	}
	if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, pterodactylWorkloadModuleID, "game", 80, path+".deliveryRoute"); err != nil {
		return PterodactylWorkloadBundleDescriptor{}, err
	}
	origin, err := applicationHTTPSRootURL(bundle.DeliveryRoute)
	if err != nil {
		return PterodactylWorkloadBundleDescriptor{}, err
	}
	if _, err := validatePterodactylRuntimeComponents(bundle.Components, path+".components", origin); err != nil {
		return PterodactylWorkloadBundleDescriptor{}, err
	}
	if err := validatePterodactylServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return PterodactylWorkloadBundleDescriptor{}, err
	}
	if !reflect.DeepEqual(bundle.ConfigFiles, pterodactylConfigFiles()) {
		return PterodactylWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "Pterodactyl startup files differ from the governed renderer output")
	}
	return PterodactylWorkloadBundleDescriptor{
		WorkloadRef: "game", ModuleRef: pterodactylWorkloadModuleID, Release: pterodactylPanelRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Route: bundle.DeliveryRoute.descriptor(),
	}, nil
}

//nolint:gocyclo // One boundary validates the complete upstream service graph and its governed privileges.
func validatePterodactylWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + pterodactylWorkloadModuleID + ".renderUnits." + pterodactylWorkloadUnitID
	if unit.ModuleID() != pterodactylWorkloadModuleID || unit.ID() != pterodactylWorkloadUnitID ||
		unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Pterodactyl contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" || !hasEngine || engine != "docker" ||
		!hasImage || imageRef != pterodactylPanelImageRef || !hasDigest || imageDigest != pterodactylPanelImageDigest || !hasEntry || entry != "panel" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity differs from the pinned Pterodactyl Panel image")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !stringListContains(unit.LogicalNodeRefs(), nodeRef) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Pterodactyl requires one exact node-local target")
	}
	daemonRef, hasDaemon := unit.DaemonRef()
	daemonEngine, hasDaemonEngine := unit.DaemonEngine()
	socketPath, hasSocket := unit.DaemonSocketPath()
	if !hasDaemon || daemonRef != pterodactylDaemonRef || !hasDaemonEngine || daemonEngine != "docker" || !hasSocket || validateDockerSocketPath(socketPath) != nil {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Wings requires the exact docker-default daemon and canonical Unix socket")
	}
	var placement struct{ Scope, Cardinality, DaemonRef string }
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-daemon" || placement.DaemonRef != pterodactylDaemonRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-daemon docker-default placement")
	}
	if err := validatePterodactylPrivileges(unit, siteRef, nodeRef, path); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, pterodactylWorkloadModuleID, "game", 80, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute == nil {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Pterodactyl requires a declared delivery route")
	}
	if !sameStringSet(unit.SecretInputRefs(), pterodactylSecretSlots) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Pterodactyl requires its database, key, owner and API secret slots")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || !validPterodactylSecretRefs(secretRefs) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires declared opaque secret references")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".interfaces", "Pterodactyl provides no interface and receives no runtime-network authority")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != pterodactylWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", pterodactylWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode component graph", err)
	}
	if components, err = validatePterodactylRuntimeComponents(components, path+".runtime.components", ""); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact Pterodactyl endpoint")
	}
	if err := validatePterodactylServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	origin, err := applicationHTTPSRootURL(deliveryRoute)
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	for index := range components {
		for key, value := range pterodactylRouteEnvironment(components[index].ID, origin) {
			components[index].Environment[key] = value
		}
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "PterodactylWorkloadBundle", SecretRefs: secretRefs,
		Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
		ConfigFiles: pterodactylConfigFiles(), DaemonSocketPath: socketPath,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "game", "pterodactyl"
	bundle.Workload.ModuleRef, bundle.Workload.Release = pterodactylWorkloadModuleID, pterodactylPanelRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter, bundle.Ownership.ProviderLifecycle, bundle.Ownership.Credentials = "selected-application-adapter", "not-owned", "opaque-references-only"
	return bundle, nil
}

// validatePterodactylPrivileges accepts exactly the Wings lifecycle-owner
// socket requirement and its central approval (ADR-0043).
func validatePterodactylPrivileges(unit RenderUnit, siteRef, nodeRef, path string) error {
	var required []socketProxyRequiredInterfaceContract
	if err := decodeStrict(unit.RequiredInterfacesJSON(), &required); err != nil || len(required) != 1 {
		return wrap(ErrInvalidPlan, path+".requiresInterfaces", "decode exact Wings lifecycle interface", errOrCount(err, len(required), 1))
	}
	r := required[0]
	if r.ID != pterodactylPolicyProfile || r.Kind != dockerSocketDirectInterfaceKind || r.Protocol != "docker-engine" || r.Version != "v1" ||
		r.Endpoint.Visibility != "node-local" || r.Endpoint.Transport != "unix-socket" || r.Endpoint.PathSource != dockerSocketPathSourceDaemonBinding ||
		!exactStringList(r.Scopes, []string{"docker-api:full"}) || r.CoLocation != "same-node" || r.DaemonRef != pterodactylDaemonRef || r.PolicyProfile != pterodactylPolicyProfile {
		return fail(ErrInvalidPlan, path+".requiresInterfaces", "Wings Docker interface widens or drifts from its lifecycle-owner approval")
	}
	var approvals []rawPrivilegedInterfaceApproval
	if err := decodeStrict(unit.PrivilegedInterfaceApprovalsJSON(), &approvals); err != nil || len(approvals) != 1 {
		return wrap(ErrInvalidPlan, path+".privilegedInterfaceApprovals", "decode exact Wings lifecycle-owner approval", errOrCount(err, len(approvals), 1))
	}
	a := approvals[0]
	if a.ID != pterodactylApprovalID || a.Kind != dockerSocketDirectInterfaceKind || a.ModuleRef != pterodactylWorkloadModuleID || a.UnitRef != pterodactylWorkloadUnitID ||
		a.ProviderRef != "stackkits-pterodactyl" || a.DaemonRef != pterodactylDaemonRef || a.PolicyProfile != pterodactylPolicyProfile ||
		a.ReasonCode != "lifecycle-owner" || a.EvidenceRef != pterodactylApprovalEvidence || a.EvidenceGateRef == "" ||
		!stringListContains(a.SiteRefs, siteRef) || !stringListContains(a.NodeRefs, nodeRef) {
		return fail(ErrInvalidPlan, path+".privilegedInterfaceApprovals", "lifecycle-owner approval does not exactly cover Wings on this node")
	}
	return nil
}

// pterodactylRouteEnvironment is the route-derived configuration the renderer
// adds; validators compare it against the same function.
func pterodactylRouteEnvironment(componentID, origin string) map[string]string {
	if origin == "" {
		return nil
	}
	// Browsers send Origin without a trailing slash; Wings compares exactly.
	origin = strings.TrimRight(origin, "/")
	host := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	if parsed, err := url.Parse(origin); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	switch componentID {
	case "panel":
		return map[string]string{"APP_URL": origin, "STACKKIT_ROUTE_HOST": host}
	case "panel-bootstrap":
		return map[string]string{"APP_URL": origin, "STACKKIT_ROUTE_HOST": host, "STACKKIT_PANEL_ORIGIN": origin}
	}
	return nil
}

var pterodactylPanelEnvironment = map[string]string{
	"APP_ENV": "production", "APP_ENVIRONMENT_ONLY": "false", "APP_TIMEZONE": "UTC",
	"CACHE_DRIVER": "redis", "SESSION_DRIVER": "redis", "QUEUE_DRIVER": "redis",
	"REDIS_HOST": "panel-cache", "DB_HOST": "panel-database", "DB_PORT": "3306",
	"DB_DATABASE": "panel", "DB_USERNAME": "pterodactyl", "MAIL_DRIVER": "log",
	"TRUSTED_PROXIES": "*", "PTERODACTYL_TELEMETRY_ENABLED": "false",
	// No third-party CAPTCHA on the owner login: it calls Google and blocks
	// offline home networks; the Panel throttles failed logins itself.
	"RECAPTCHA_ENABLED": "false",
}

func pterodactylExpectedEnvironment(componentID, origin string) map[string]string {
	expected := map[string]string{}
	switch componentID {
	case "panel", "panel-bootstrap":
		for key, value := range pterodactylPanelEnvironment {
			expected[key] = value
		}
	case "panel-database":
		expected = map[string]string{"MARIADB_DATABASE": "panel", "MARIADB_USER": "pterodactyl"}
	case "wings":
		expected = map[string]string{"TZ": "UTC", "WINGS_UID": "988", "WINGS_GID": "988", "WINGS_USERNAME": "pterodactyl"}
	case "panel-cache":
		return nil
	}
	for key, value := range pterodactylRouteEnvironment(componentID, origin) {
		expected[key] = value
	}
	return expected
}

//nolint:gocyclo // The five-component graph is validated at one boundary.
func validatePterodactylRuntimeComponents(components []selectedPaaSRuntimeComponent, path, origin string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 5 {
		return nil, fail(ErrInvalidPlan, path, "requires panel, database, cache, bootstrap and Wings")
	}
	panelSecrets := map[string]string{"DB_PASSWORD": "database-password", "STACKKIT_APP_KEY": "app-key", "STACKKIT_HASHIDS_SALT": "hashids-salt"}
	bootstrapSecrets := map[string]string{
		"DB_PASSWORD": "database-password", "STACKKIT_APP_KEY": "app-key", "STACKKIT_HASHIDS_SALT": "hashids-salt",
		"STACKKIT_OWNER_PASSWORD": "owner-password", "STACKKIT_APPLICATION_API_KEY": "application-api-key", "STACKKIT_CLIENT_API_KEY": "client-api-key",
	}
	seen := map[string]bool{}
	for _, c := range components {
		if seen[c.ID] || c.Egress || c.HealthFailure != "" || !exactStringList(c.NetworkRefs, []string{"game-internal"}) {
			return nil, fail(ErrInvalidPlan, path, "component identity or network differs")
		}
		seen[c.ID] = true
		env := c.Environment
		if len(env) == 0 {
			env = nil
		}
		expectedEnv := pterodactylExpectedEnvironment(c.ID, origin)
		if len(expectedEnv) == 0 {
			expectedEnv = nil
		}
		if !reflect.DeepEqual(env, expectedEnv) {
			return nil, fail(ErrInvalidPlan, path, "component %q environment differs from the governed configuration", c.ID)
		}
		switch c.ID {
		case "panel":
			if c.Role != "application" || c.Lifecycle != "daemon" || c.Image.Ref != pterodactylPanelImageRef || c.Image.Digest != pterodactylPanelImageDigest ||
				!exactStringList(c.DependsOn, []string{"panel-cache", "panel-database"}) || !exactStringList(c.Entrypoint, []string{"/bin/ash", "/stackkit/panel-entrypoint.sh"}) ||
				!exactStringList(c.Command, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}) ||
				!reflect.DeepEqual(c.OwnerEnvironment, map[string]string{"APP_SERVICE_AUTHOR": "email"}) || !reflect.DeepEqual(c.SecretEnvironment, panelSecrets) ||
				!c.RouteHostLoopback || c.DockerLifecycleOwner != nil ||
				c.Health.Kind != "http" || c.Health.Path != "/auth/login" || c.Health.Port != 80 || len(c.Health.Command) != 0 ||
				!pterodactylVolumes(c.Volumes, []selectedPaaSRuntimeVolume{
					{ID: "var", Target: "/app/var", Class: "persistent", Backup: true},
					{ID: "nginx", Target: "/etc/nginx/http.d", Class: "cache"},
					{ID: "stackkit", Target: "/stackkit", Class: "cache"},
				}) {
				return nil, fail(ErrInvalidPlan, path, "Panel image, bootstrap wiring, owner, secrets or persistence differs")
			}
		case "panel-database":
			if c.Role != "database" || c.Lifecycle != "daemon" || c.Image.Ref != pterodactylDatabaseImageRef || c.Image.Digest != pterodactylDatabaseImageDigest ||
				len(c.DependsOn) != 0 || len(c.Command) != 0 || len(c.Entrypoint) != 0 || len(c.OwnerEnvironment) != 0 ||
				!reflect.DeepEqual(c.SecretEnvironment, map[string]string{"MARIADB_PASSWORD": "database-password", "MARIADB_ROOT_PASSWORD": "database-root-password"}) ||
				c.RouteHostLoopback || c.DockerLifecycleOwner != nil ||
				c.Health.Kind != "command" || !exactStringList(c.Health.Command, []string{"healthcheck.sh", "--connect", "--innodb_initialized"}) ||
				!pterodactylVolumes(c.Volumes, []selectedPaaSRuntimeVolume{{ID: "database", Target: "/var/lib/mysql", Class: "persistent", Backup: true}}) {
				return nil, fail(ErrInvalidPlan, path, "MariaDB image, credentials or persistent data differs")
			}
		case "panel-cache":
			if c.Role != "cache" || c.Lifecycle != "daemon" || c.Image.Ref != pterodactylCacheImageRef || c.Image.Digest != pterodactylCacheImageDigest ||
				len(c.DependsOn) != 0 || !exactStringList(c.Command, []string{"valkey-server"}) || len(c.Entrypoint) != 0 ||
				len(c.OwnerEnvironment) != 0 || len(c.SecretEnvironment) != 0 || c.RouteHostLoopback || c.DockerLifecycleOwner != nil ||
				c.Health.Kind != "command" || !exactStringList(c.Health.Command, []string{"valkey-cli", "ping"}) ||
				!pterodactylVolumes(c.Volumes, []selectedPaaSRuntimeVolume{{ID: "cache", Target: "/data", Class: "cache"}}) {
				return nil, fail(ErrInvalidPlan, path, "Valkey cache contract differs")
			}
		case "panel-bootstrap":
			if c.Role != "database-init" || c.Lifecycle != "one-shot" || c.Image.Ref != pterodactylPanelImageRef || c.Image.Digest != pterodactylPanelImageDigest ||
				!exactStringList(c.DependsOn, []string{"panel"}) || !exactStringList(c.Entrypoint, []string{"/bin/ash"}) ||
				!exactStringList(c.Command, []string{"/stackkit/bootstrap.sh"}) ||
				!reflect.DeepEqual(c.OwnerEnvironment, map[string]string{"STACKKIT_OWNER_EMAIL": "email"}) || !reflect.DeepEqual(c.SecretEnvironment, bootstrapSecrets) ||
				c.RouteHostLoopback || c.DockerLifecycleOwner != nil || c.Health.Kind != "completion" ||
				!pterodactylVolumes(c.Volumes, []selectedPaaSRuntimeVolume{
					{ID: "stackkit", Target: "/stackkit", Class: "cache"},
					{ID: "data", Target: PterodactylGameDataTarget, Class: "persistent", Backup: true, SharedFrom: &selectedPaaSVolumeSource{ComponentRef: "wings", VolumeRef: "data"}},
				}) {
				return nil, fail(ErrInvalidPlan, path, "bootstrap image, custody inputs or shared game data differs")
			}
		case "wings":
			if c.Role != "application" || c.Lifecycle != "daemon" || c.Image.Ref != pterodactylWingsImageRef || c.Image.Digest != pterodactylWingsImageDigest ||
				!exactStringList(c.DependsOn, []string{"panel-bootstrap"}) || len(c.Entrypoint) != 0 ||
				!exactStringList(c.Command, []string{"--config", PterodactylGameDataTarget + "/config.yml"}) ||
				len(c.OwnerEnvironment) != 0 || len(c.SecretEnvironment) != 0 || c.RouteHostLoopback ||
				c.DockerLifecycleOwner == nil || c.DockerLifecycleOwner.DaemonRef != pterodactylDaemonRef || c.DockerLifecycleOwner.PolicyProfile != pterodactylPolicyProfile ||
				c.Health.Kind != "image" ||
				!pterodactylVolumes(c.Volumes, []selectedPaaSRuntimeVolume{
					{ID: "data", Target: PterodactylGameDataTarget, Class: "persistent", Backup: true, SelfPath: true},
					{ID: "logs", Target: "/var/log/pterodactyl", Class: "cache"},
				}) {
				return nil, fail(ErrInvalidPlan, path, "Wings image, lifecycle-owner binding or game data differs")
			}
		default:
			return nil, fail(ErrInvalidPlan, path, "unknown Pterodactyl component")
		}
	}
	return components, nil
}

func pterodactylVolumes(actual, expected []selectedPaaSRuntimeVolume) bool {
	if len(actual) != len(expected) {
		return false
	}
	byID := map[string]selectedPaaSRuntimeVolume{}
	for _, volume := range actual {
		if _, dup := byID[volume.ID]; dup {
			return false
		}
		byID[volume.ID] = volume
	}
	for _, want := range expected {
		if got, ok := byID[want.ID]; !ok || !reflect.DeepEqual(got, want) {
			return false
		}
	}
	return true
}

func validatePterodactylServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "game" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 80 || endpoint.RequiredPrivilege != "user" ||
		endpoint.OriginSelector != "control-authority-site" || endpoint.HealthRef != "pterodactyl-panel-http" || endpoint.IngressAuth != "native" ||
		endpoint.Data.BindingRef != "game" || endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) || !sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "Pterodactyl route authority differs")
	}
	return nil
}

func validPterodactylSecretRefs(refs map[string]string) bool {
	if len(refs) != len(pterodactylSecretSlots) {
		return false
	}
	for _, slot := range pterodactylSecretSlots {
		if !validSecretReference(refs[slot]) {
			return false
		}
	}
	return true
}

// pterodactylConfigFiles are the governed startup files. They carry no secret
// material; custody values reach the containers only as secret environment.
func pterodactylConfigFiles() []selectedPaaSConfigFile {
	files := []selectedPaaSConfigFile{
		{Path: "/stackkit/panel-entrypoint.sh", Body: pterodactylPanelEntrypoint},
		{Path: "/stackkit/nginx-panel.conf", Body: pterodactylNginxTemplate},
		{Path: "/stackkit/bootstrap.sh", Body: pterodactylBootstrapShell},
		{Path: "/stackkit/bootstrap.php", Body: pterodactylBootstrapPHP},
	}
	for _, egg := range pterodactylEggs {
		files = append(files, selectedPaaSConfigFile{Path: egg.Path, Body: egg.Body})
	}
	return files
}

// The Panel derives Laravel's key from custody material, serves its node paths
// to Wings, and trusts a node certificate that exists only inside the Panel.
const pterodactylPanelEntrypoint = `#!/bin/ash
set -eu
export APP_KEY="base64:${STACKKIT_APP_KEY}="
export HASHIDS_SALT="$(printf %s "$STACKKIT_HASHIDS_SALT" | tr -dc 'A-Za-z0-9' | cut -c1-20)"
unset STACKKIT_APP_KEY STACKKIT_HASHIDS_SALT
mkdir -p /stackkit-local/tls
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=${STACKKIT_ROUTE_HOST}" \
  -addext "subjectAltName=DNS:${STACKKIT_ROUTE_HOST}" \
  -keyout /stackkit-local/tls/node.key -out /stackkit-local/tls/node.crt >/dev/null 2>&1
cat /etc/ssl/certs/ca-certificates.crt /stackkit-local/tls/node.crt > /stackkit-local/ca-bundle.pem
printf 'curl.cainfo=/stackkit-local/ca-bundle.pem\nopenssl.cafile=/stackkit-local/ca-bundle.pem\n' > /usr/local/etc/php/conf.d/zz-stackkit.ini
cp /stackkit/nginx-panel.conf /etc/nginx/http.d/panel.conf
# The upstream entrypoint removes the stock site only when it writes its own
# config; with ours in place the stock default server would answer port 80
# and hide the node proxy (and the live console) from the router.
rm -f /etc/nginx/http.d/default.conf
exec /bin/ash .github/docker/entrypoint.sh "$@"
`

const pterodactylNginxTemplate = `server {
    listen 80;
    listen 443 ssl;
    server_name _;
    ssl_certificate /stackkit-local/tls/node.crt;
    ssl_certificate_key /stackkit-local/tls/node.key;

    root /app/public;
    index index.html index.htm index.php;
    charset utf-8;
    access_log off;
    error_log /var/log/nginx/pterodactyl.app-error.log error;
    client_max_body_size 100m;
    client_body_timeout 120s;
    sendfile off;

    # Wings node API and console on the Panel origin (ADR-0043).
    location ~ ^/(api/servers|api/system|api/update|api/transfers|download|upload)(/|$) {
        proxy_pass http://wings:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_read_timeout 3600s;
        proxy_request_buffering off;
        proxy_buffering off;
    }

    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }

    location = /favicon.ico { access_log off; log_not_found off; }
    location = /robots.txt  { access_log off; log_not_found off; }

    location ~ \.php$ {
        fastcgi_split_path_info ^(.+\.php)(/.+)$;
        fastcgi_pass 127.0.0.1:9000;
        fastcgi_index index.php;
        include fastcgi_params;
        fastcgi_param PHP_VALUE "upload_max_filesize = 100M \n post_max_size=100M";
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        fastcgi_param HTTP_PROXY "";
        fastcgi_intercept_errors off;
        fastcgi_buffer_size 16k;
        fastcgi_buffers 4 16k;
        fastcgi_connect_timeout 300;
        fastcgi_send_timeout 300;
        fastcgi_read_timeout 300;
    }

    location ~ /\.ht {
        deny all;
    }
}
`

const pterodactylBootstrapShell = `#!/bin/ash
set -eu
cd /app
export APP_KEY="base64:${STACKKIT_APP_KEY}="
export HASHIDS_SALT="$(printf %s "$STACKKIT_HASHIDS_SALT" | tr -dc 'A-Za-z0-9' | cut -c1-20)"
exec php /stackkit/bootstrap.php
`

// The bootstrap is idempotent: every run converges the owner, location, node,
// API keys, allocations, curated Eggs and the Wings configuration.
const pterodactylBootstrapPHP = `<?php
declare(strict_types=1);

require '/app/vendor/autoload.php';
$app = require '/app/bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();

use Illuminate\Http\UploadedFile;
use Illuminate\Support\Facades\Schema;
use Pterodactyl\Models\Allocation;
use Pterodactyl\Models\ApiKey;
use Pterodactyl\Models\Egg;
use Pterodactyl\Models\Location;
use Pterodactyl\Models\Node;
use Pterodactyl\Models\User;
use Symfony\Component\Yaml\Yaml;

function envOrFail(string $name): string {
    $value = getenv($name);
    if ($value === false || $value === '') {
        fwrite(STDERR, "missing required environment {$name}\n");
        exit(2);
    }
    return $value;
}

$deadline = time() + 900;
while (true) {
    try {
        if (Schema::hasTable('api_keys') && Schema::hasTable('settings') && Egg::query()->count() > 0) {
            break;
        }
    } catch (Throwable $e) {
    }
    if (time() > $deadline) {
        fwrite(STDERR, "Panel database was not migrated and seeded in time\n");
        exit(3);
    }
    sleep(3);
}

$email = envOrFail('STACKKIT_OWNER_EMAIL');
$routeHost = envOrFail('STACKKIT_ROUTE_HOST');
$origin = envOrFail('STACKKIT_PANEL_ORIGIN');
$dataHost = rtrim(envOrFail('STACKKIT_GAME_DATA_HOST_PATH'), '/');

$user = User::query()->where('email', $email)->first();
if ($user === null) {
    $user = app(Pterodactyl\Services\Users\UserCreationService::class)->handle([
        'email' => $email, 'username' => 'owner', 'name_first' => 'Home', 'name_last' => 'Owner',
        'password' => envOrFail('STACKKIT_OWNER_PASSWORD'), 'root_admin' => true,
    ]);
}

$location = Location::query()->firstOrCreate(['short' => 'home'], ['long' => 'StackKits game node']);

$memoryMb = 4096;
foreach (file('/proc/meminfo') ?: [] as $line) {
    if (preg_match('/^MemTotal:\s+(\d+)\s+kB/', $line, $m)) {
        // The platform (Panel, database, cache, Wings) and the Basement core
        // keep 3 GB; the rest is game memory the Panel may allocate.
        $memoryMb = max(2048, intdiv((int) $m[1], 1024) - 3072);
    }
}
$diskMb = max(10240, (int) floor(((float) disk_total_space('/stackkit/game-data')) / 1048576 * 0.8));

$nodeAttributes = [
    'name' => 'stackkit-node', 'description' => 'StackKits game node', 'location_id' => $location->id,
    'fqdn' => $routeHost, 'scheme' => 'https', 'behind_proxy' => true, 'public' => false,
    'memory' => $memoryMb, 'memory_overallocate' => 0, 'disk' => $diskMb, 'disk_overallocate' => 0,
    'upload_size' => 100, 'daemonListen' => 443, 'daemonSFTP' => 2022,
    'daemonBase' => $dataHost . '/volumes', 'maintenance_mode' => false,
];
$node = Node::query()->where('name', 'stackkit-node')->first();
if ($node === null) {
    $node = app(Pterodactyl\Services\Nodes\NodeCreationService::class)->handle($nodeAttributes);
} else {
    $node->forceFill(['fqdn' => $routeHost, 'scheme' => 'https', 'behind_proxy' => true, 'daemonListen' => 443,
        'daemonBase' => $dataHost . '/volumes', 'memory' => $memoryMb, 'disk' => $diskMb])->save();
}

$ensureKey = function (string $material, string $prefix, int $type, array $permissions) use ($user): void {
    if (strlen($material) < 43) {
        fwrite(STDERR, "API key material is too short\n");
        exit(4);
    }
    $identifier = $prefix . substr($material, 0, 11);
    $token = substr($material, 11, 32);
    $key = ApiKey::query()->where('identifier', $identifier)->first();
    if ($key === null) {
        $key = new ApiKey();
        $key->forceFill(array_merge([
            'user_id' => $user->id, 'key_type' => $type, 'identifier' => $identifier,
            'token' => encrypt($token), 'memo' => 'StackKits custody', 'allowed_ips' => [],
        ], $permissions))->save();
    }
};
$full = ['r_servers' => 3, 'r_nodes' => 3, 'r_allocations' => 3, 'r_users' => 3, 'r_locations' => 3,
    'r_nests' => 3, 'r_eggs' => 3, 'r_database_hosts' => 3, 'r_server_databases' => 3];
$ensureKey(envOrFail('STACKKIT_APPLICATION_API_KEY'), 'ptla_', ApiKey::TYPE_APPLICATION, $full);
$ensureKey(envOrFail('STACKKIT_CLIENT_API_KEY'), 'ptlc_', ApiKey::TYPE_ACCOUNT, []);

// Minecraft Bedrock and Java, Terraria, and Valheim (game plus query port).
$ports = ['19132', '25565', '25566', '25567', '25568', '25569', '25570', '7777', '2456', '2457'];
$existing = Allocation::query()->where('node_id', $node->id)->pluck('port')->map(fn ($p) => (string) $p)->all();
$missing = array_values(array_diff($ports, $existing));
if ($missing !== []) {
    app(Pterodactyl\Services\Allocations\AssignmentService::class)->handle($node, ['allocation_ip' => '0.0.0.0', 'allocation_ports' => $missing]);
}

foreach (glob('/stackkit/eggs/*.json') ?: [] as $eggFile) {
    $definition = json_decode((string) file_get_contents($eggFile), true);
    if (Egg::query()->where('name', $definition['name'])->exists()) {
        continue;
    }
    $nestName = ['Vanilla Bedrock' => 'Minecraft', 'Terraria Vanilla' => 'Terraria', 'Valheim' => 'Valheim'][$definition['name']] ?? null;
    if ($nestName === null) {
        fwrite(STDERR, "stackkit: Egg {$definition['name']} has no curated nest\n");
        exit(1);
    }
    $nest = Pterodactyl\Models\Nest::query()->where('name', $nestName)->first()
        ?? app(Pterodactyl\Services\Nests\NestCreationService::class)->handle(['name' => $nestName, 'description' => 'Curated by StackKits'], 'stackkits@kombify.io');
    app(Pterodactyl\Services\Eggs\Sharing\EggImporterService::class)
        ->handle(new UploadedFile($eggFile, basename($eggFile), 'application/json', null, true), $nest->id);
}

$config = Yaml::parse($node->fresh()->getYamlConfiguration());
$config['remote'] = 'http://panel';
$config['allowed_origins'] = [$origin];
$config['api']['host'] = '0.0.0.0';
$config['api']['port'] = 8080;
$config['api']['ssl']['enabled'] = false;
$config['system']['root_directory'] = $dataHost;
$config['system']['data'] = $dataHost . '/volumes';
$config['system']['archive_directory'] = $dataHost . '/archives';
$config['system']['backup_directory'] = $dataHost . '/backups';
$config['system']['tmp_directory'] = $dataHost . '/tmp';
$config['system']['log_directory'] = '/var/log/pterodactyl';
$config['system']['passwd']['enable'] = true;
$config['system']['passwd']['directory'] = $dataHost . '/passwd';
$config['system']['machine_id']['enabled'] = true;
$config['system']['machine_id']['directory'] = $dataHost . '/machine-id';
$config['docker']['network']['name'] = 'stackkit_game_nw';
$config['docker']['network']['network_mode'] = 'stackkit_game_nw';
$config['docker']['network']['interface'] = '10.213.0.1';
$config['docker']['network']['interfaces']['v4'] = ['subnet' => '10.213.0.0/24', 'gateway' => '10.213.0.1'];
$config['docker']['network']['interfaces']['v6'] = ['subnet' => 'fdba:17c8:6c94::/64', 'gateway' => 'fdba:17c8:6c94::1011'];
file_put_contents('/stackkit/game-data/config.yml', Yaml::dump($config, 8, 2, Yaml::DUMP_EMPTY_ARRAY_AS_SEQUENCE));
chmod('/stackkit/game-data/config.yml', 0600);
fwrite(STDOUT, "Pterodactyl bootstrap converged\n");
`
