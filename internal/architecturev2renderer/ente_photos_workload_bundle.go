package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"
)

const (
	entePhotosWorkloadModuleID    = "stackkits-ente-photos-runtime"
	entePhotosWorkloadRef         = "photos"
	entePhotosWorkloadUnitID      = "ente-gateway"
	entePhotosWorkloadTemplateRef = "builtin://workloads/ente-photos/bundle/v1.json"
	entePhotosWorkloadVersion     = "1.0.0"
	entePhotosWorkloadOutputRef   = "workloads/ente-photos/bundle.json"
	entePhotosHTTPPort            = 8080
	entePhotosGatewayConfigPath   = "/etc/nginx/conf.d/default.conf"
)

const entePhotosWorkloadRendererSchema = `stackkit.workload-bundle/v2|EntePhotosWorkloadBundle|application-adapter|route:authority-bound-module-route-v1:multi-endpoint:web,accounts,api,s3|provider-lifecycle:not-owned|components:ente-gateway,ente-web,ente-museum,ente-postgres,ente-s3|health:http-stackkit-healthz-8080|secret-material:not-included|custody-files:museum,s3|cors:same-domain`

// EntePhotosWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the selected application adapter.
type EntePhotosWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

// EntePhotosEndpoints holds the authoritative hosts derived from the primary
// Photos delivery route for Web Photos, Web Accounts, Museum API, and S3.
type EntePhotosEndpoints struct {
	PhotosHost   string
	AccountsHost string
	APIHost      string
	S3Host       string
}

// EntePhotosEndpointsForHost derives the sibling hosts on the same authoritative
// base domain from the primary Photos route host (e.g. photos.example.test ->
// photos-accounts.example.test, photos-api.example.test, photos-s3.example.test).
func EntePhotosEndpointsForHost(host string) (EntePhotosEndpoints, error) {
	trimmed := strings.TrimSpace(host)
	label, rest, ok := strings.Cut(trimmed, ".")
	if !ok || label == "" || rest == "" || strings.ContainsAny(trimmed, " \t\r\n/\\$;\"'`") {
		return EntePhotosEndpoints{}, fail(ErrInvalidPlan, "ente-photos.deliveryRoute.host", "Ente Photos requires a valid multi-label route host")
	}
	return EntePhotosEndpoints{
		PhotosHost:   trimmed,
		AccountsHost: label + "-accounts." + rest,
		APIHost:      label + "-api." + rest,
		S3Host:       label + "-s3." + rest,
	}, nil
}

var (
	entePhotosSecretSlots = []string{
		"database-password",
		"encryption-key",
		"hash-key",
		"jwt-secret",
		"s3-access-key",
		"s3-secret-key",
	}
	enteMuseumEntrypoint = []string{"/bin/sh", "-ec"}
	enteMuseumCommand    = []string{
		`export ENTE_DB_PASSWORD="$(cat "$STACKKIT_DB_PASSWORD_FILE")"; export ENTE_KEY_ENCRYPTION="$(cat "$STACKKIT_ENCRYPTION_KEY_FILE")"; export ENTE_KEY_HASH="$(cat "$STACKKIT_HASH_KEY_FILE")"; export ENTE_JWT_SECRET="$(cat "$STACKKIT_JWT_SECRET_FILE")"; export ENTE_S3_B2_EU_CEN_KEY="$(cat "$STACKKIT_S3_ACCESS_KEY_FILE")"; export ENTE_S3_B2_EU_CEN_SECRET="$(cat "$STACKKIT_S3_SECRET_KEY_FILE")"; exec ./museum`,
	}
	enteMuseumSecretFiles = []selectedPaaSSecretFile{
		custodyFile("database-password", "STACKKIT_DB_PASSWORD_FILE"),
		custodyFile("encryption-key", "STACKKIT_ENCRYPTION_KEY_FILE"),
		custodyFile("hash-key", "STACKKIT_HASH_KEY_FILE"),
		custodyFile("jwt-secret", "STACKKIT_JWT_SECRET_FILE"),
		custodyFile("s3-access-key", "STACKKIT_S3_ACCESS_KEY_FILE"),
		custodyFile("s3-secret-key", "STACKKIT_S3_SECRET_KEY_FILE"),
	}
	enteS3Entrypoint = []string{"/bin/sh", "-ec"}
	enteS3Command    = []string{
		`mkdir -p /data/s3/ente-photos; export ROOT_ACCESS_KEY="$(cat "$STACKKIT_S3_ACCESS_KEY_FILE")"; export ROOT_SECRET_KEY="$(cat "$STACKKIT_S3_SECRET_KEY_FILE")"; exec /app/versitygw --port :7070 --region eu-central-1 posix /data/s3`,
	}
	enteS3SecretFiles = []selectedPaaSSecretFile{
		custodyFile("s3-access-key", "STACKKIT_S3_ACCESS_KEY_FILE"),
		custodyFile("s3-secret-key", "STACKKIT_S3_SECRET_KEY_FILE"),
	}
)

func EntePhotosWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(entePhotosWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: entePhotosWorkloadTemplateRef,
		Version: entePhotosWorkloadVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type entePhotosWorkloadBundleRenderer struct{ contract RendererContract }

func newEntePhotosWorkloadBundleRenderer() entePhotosWorkloadBundleRenderer {
	return entePhotosWorkloadBundleRenderer{contract: EntePhotosWorkloadBundleRendererContract()}
}

func (r entePhotosWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateEntePhotosWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.ente-photos-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: entePhotosWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseEntePhotosWorkloadBundle validates the closed artifact before a runtime
// owner may consume it. Secret values remain opaque references.
func ParseEntePhotosWorkloadBundle(data []byte) (EntePhotosWorkloadBundleDescriptor, error) {
	path := "entePhotosWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return EntePhotosWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Ente Photos workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "EntePhotosWorkloadBundle" ||
		bundle.Workload.Ref != entePhotosWorkloadRef || bundle.Workload.AlternativeRef != "ente-photos" ||
		bundle.Workload.ModuleRef != entePhotosWorkloadModuleID || bundle.Workload.Release != enteMuseumRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != entePhotosWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return EntePhotosWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Ente Photos contract")
	}
	if !validEntePhotosSecretRefs(bundle.SecretRefs) {
		return EntePhotosWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires exact opaque Ente Photos secret references")
	}
	if bundle.DeliveryRoute == nil {
		return EntePhotosWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".deliveryRoute", "Ente Photos requires its declared HTTPS route")
	}
	if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, entePhotosWorkloadModuleID, entePhotosWorkloadRef, entePhotosHTTPPort, path+".deliveryRoute"); err != nil {
		return EntePhotosWorkloadBundleDescriptor{}, err
	}
	endpoints, err := entePhotosRouteEndpoints(bundle.DeliveryRoute)
	if err != nil {
		return EntePhotosWorkloadBundleDescriptor{}, err
	}
	expectedConfig := entePhotosGatewayConfigFiles(endpoints)
	if !reflect.DeepEqual(bundle.ConfigFiles, expectedConfig) {
		return EntePhotosWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "Ente Photos gateway configuration differs from the governed route projection")
	}
	components, err := validateEntePhotosRuntimeComponents(bundle.Components, &endpoints, path+".components")
	if err != nil {
		return EntePhotosWorkloadBundleDescriptor{}, err
	}
	if err := validateEntePhotosServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return EntePhotosWorkloadBundleDescriptor{}, err
	}
	descriptor := EntePhotosWorkloadBundleDescriptor{
		WorkloadRef: entePhotosWorkloadRef, ModuleRef: entePhotosWorkloadModuleID, Release: enteMuseumRelease,
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

//nolint:gocyclo // One boundary validates the complete governed service graph.
func validateEntePhotosWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + entePhotosWorkloadModuleID + ".renderUnits." + entePhotosWorkloadUnitID
	if unit.ModuleID() != entePhotosWorkloadModuleID || unit.ID() != entePhotosWorkloadUnitID ||
		unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Ente Photos contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" || !hasEngine || engine != "docker" ||
		!hasImage || imageRef != enteGatewayImageRef || !hasDigest || imageDigest != enteGatewayImageDigest || !hasEntry || entry != entePhotosWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity differs from the pinned Ente Photos gateway image")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Ente Photos requires one exact node-local target")
	}
	if selectedPaaSUnitHasDaemonAuthority(unit) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "application adapter receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, entePhotosWorkloadModuleID, entePhotosWorkloadRef, entePhotosHTTPPort, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute == nil {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Ente Photos requires a declared delivery route")
	}
	if !sameStringSet(unit.SecretInputRefs(), entePhotosSecretSlots) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Ente Photos requires exact database, encryption, hash, JWT, and S3 secret slots")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || !validEntePhotosSecretRefs(secretRefs) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires declared opaque Ente Photos secret references")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".interfaces", "Ente Photos receives no host or runtime-network authority")
	}
	var placement struct{ Scope, Cardinality string }
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != entePhotosWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", entePhotosWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode component graph", err)
	}
	components, err = validateEntePhotosRuntimeComponents(components, nil, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact Ente Photos endpoint")
	}
	if err := validateEntePhotosServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	routeEndpoints, err := entePhotosRouteEndpoints(deliveryRoute)
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	for index := range components {
		switch components[index].ID {
		case "ente-web":
			if components[index].Environment == nil {
				components[index].Environment = map[string]string{}
			}
			components[index].Environment["ENTE_API_ORIGIN"] = "https://" + routeEndpoints.APIHost
		case "ente-museum":
			if components[index].Environment == nil {
				components[index].Environment = map[string]string{}
			}
			components[index].Environment["ENTE_S3_B2_EU_CEN_ENDPOINT"] = "https://" + routeEndpoints.S3Host
			components[index].Environment["ENTE_APPS_ACCOUNTS"] = "https://" + routeEndpoints.AccountsHost
			components[index].Environment["ENTE_WEBAUTHN_RPID"] = routeEndpoints.AccountsHost
			components[index].Environment["ENTE_WEBAUTHN_RPORIGINS"] = "https://" + routeEndpoints.AccountsHost
		}
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion:    "stackkit.workload-bundle/v2",
		Kind:          "EntePhotosWorkloadBundle",
		SecretRefs:    secretRefs,
		Components:    components,
		ConfigFiles:   entePhotosGatewayConfigFiles(routeEndpoints),
		Route:         endpoints[0],
		DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = entePhotosWorkloadRef, "ente-photos"
	bundle.Workload.ModuleRef, bundle.Workload.Release = entePhotosWorkloadModuleID, enteMuseumRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter, bundle.Ownership.ProviderLifecycle, bundle.Ownership.Credentials = "selected-application-adapter", "not-owned", "opaque-references-only"
	return bundle, nil
}

func entePhotosRouteEndpoints(route *applicationDeliveryRoute) (EntePhotosEndpoints, error) {
	origin, err := applicationHTTPSRootURL(route)
	if err != nil {
		return EntePhotosEndpoints{}, err
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Hostname() == "" {
		return EntePhotosEndpoints{}, fail(ErrInvalidPlan, "ente-photos.deliveryRoute", "route origin has no host")
	}
	return EntePhotosEndpointsForHost(parsed.Hostname())
}

func entePhotosGatewayConfigFiles(endpoints EntePhotosEndpoints) []selectedPaaSConfigFile {
	return []selectedPaaSConfigFile{{
		Path: entePhotosGatewayConfigPath,
		Body: renderEntePhotosGatewayNginxConf(endpoints),
	}}
}

func renderEntePhotosGatewayNginxConf(endpoints EntePhotosEndpoints) string {
	return fmt.Sprintf(`map $http_origin $ente_cors_origin {
    default "";
    "https://%s" $http_origin;
    "https://%s" $http_origin;
}

server {
    listen 8080 default_server;
    location = /stackkit-healthz {
        access_log off;
        default_type text/plain;
        return 200 "ok\n";
    }
    location / {
        return 404;
    }
}

server {
    listen 8080;
    server_name %s;
    client_max_body_size 0;
    location / {
        proxy_pass http://ente-web:3000;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
    }
}

server {
    listen 8080;
    server_name %s;
    client_max_body_size 0;
    location / {
        proxy_pass http://ente-web:3001;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
    }
}

server {
    listen 8080;
    server_name %s;
    client_max_body_size 0;
    location / {
        proxy_pass http://ente-museum:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
    }
}

server {
    listen 8080;
    server_name %s;
    client_max_body_size 0;
    proxy_buffering off;
    proxy_request_buffering off;
    location / {
        if ($request_method = OPTIONS) {
            add_header Access-Control-Allow-Origin $ente_cors_origin always;
            add_header Access-Control-Allow-Methods "GET, PUT, HEAD, POST, DELETE, OPTIONS" always;
            add_header Access-Control-Allow-Headers "*" always;
            add_header Access-Control-Expose-Headers "ETag" always;
            add_header Access-Control-Max-Age 3600 always;
            return 204;
        }
        add_header Access-Control-Allow-Origin $ente_cors_origin always;
        add_header Access-Control-Expose-Headers "ETag" always;
        proxy_pass http://ente-s3:7070;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
    }
}
`, endpoints.PhotosHost, endpoints.AccountsHost, endpoints.PhotosHost, endpoints.AccountsHost, endpoints.APIHost, endpoints.S3Host)
}

func validateEntePhotosRuntimeComponents(components []selectedPaaSRuntimeComponent, endpoints *EntePhotosEndpoints, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 5 {
		return nil, fail(ErrInvalidPlan, path, "requires ente-gateway, ente-web, ente-museum, ente-postgres and ente-s3")
	}
	seen := map[string]bool{}
	for _, c := range components {
		if seen[c.ID] || c.Lifecycle != "daemon" || len(c.OwnerEnvironment) != 0 ||
			!exactStringList(c.NetworkRefs, []string{"ente-internal"}) || c.HomeIdentityAccess != nil || c.PocketIDClient != nil {
			return nil, fail(ErrInvalidPlan, path, "component identity, lifecycle or network differs")
		}
		seen[c.ID] = true
		switch c.ID {
		case "ente-gateway":
			if c.Role != "application" || c.Egress || len(c.Entrypoint) != 0 || len(c.Command) != 0 ||
				c.Image.Ref != enteGatewayImageRef || c.Image.Digest != enteGatewayImageDigest ||
				!exactStringList(c.DependsOn, []string{"ente-web", "ente-museum", "ente-s3"}) ||
				len(c.Environment) != 0 || len(c.SecretEnvironment) != 0 || len(c.SecretFiles) != 0 ||
				c.Health.Kind != "http" || c.Health.Path != "/stackkit-healthz" || c.Health.Port != entePhotosHTTPPort || len(c.Health.Command) != 0 ||
				len(c.Volumes) != 1 || c.Volumes[0].ID != "config" || c.Volumes[0].Target != "/etc/nginx/conf.d" || c.Volumes[0].Class != "cache" || c.Volumes[0].Backup || c.Volumes[0].ReadOnly || c.Volumes[0].HostPath != "" || c.Volumes[0].SelfPath || c.Volumes[0].SharedFrom != nil {
				return nil, fail(ErrInvalidPlan, path, "Ente Photos gateway image, dependencies, health or config mount differs")
			}
		case "ente-web":
			expectedEnv := map[string]string{}
			if endpoints != nil {
				expectedEnv["ENTE_API_ORIGIN"] = "https://" + endpoints.APIHost
			}
			if c.Role != "application" || c.Egress || len(c.Entrypoint) != 0 || len(c.Command) != 0 ||
				c.Image.Ref != enteWebImageRef || c.Image.Digest != enteWebImageDigest ||
				!exactStringList(c.DependsOn, []string{"ente-museum"}) ||
				!equalStringMaps(c.Environment, expectedEnv) || len(c.SecretEnvironment) != 0 || len(c.SecretFiles) != 0 ||
				c.Health.Kind != "http" || c.Health.Path != "/" || c.Health.Port != 3000 || len(c.Health.Command) != 0 ||
				len(c.Volumes) != 0 {
				return nil, fail(ErrInvalidPlan, path, "Ente Web image, API origin or health differs")
			}
		case "ente-museum":
			expectedEnv := map[string]string{
				"ENTE_DB_HOST":                "ente-postgres",
				"ENTE_DB_PORT":                "5432",
				"ENTE_DB_NAME":                "ente_db",
				"ENTE_DB_USER":                "pguser",
				"ENTE_DB_SSLMODE":             "disable",
				"ENTE_S3_ARE_LOCAL_BUCKETS":   "false",
				"ENTE_S3_USE_PATH_STYLE_URLS": "true",
				"ENTE_S3_B2_EU_CEN_REGION":    "eu-central-1",
				"ENTE_S3_B2_EU_CEN_BUCKET":    "ente-photos",
			}
			if endpoints != nil {
				expectedEnv["ENTE_S3_B2_EU_CEN_ENDPOINT"] = "https://" + endpoints.S3Host
				expectedEnv["ENTE_APPS_ACCOUNTS"] = "https://" + endpoints.AccountsHost
				expectedEnv["ENTE_WEBAUTHN_RPID"] = endpoints.AccountsHost
				expectedEnv["ENTE_WEBAUTHN_RPORIGINS"] = "https://" + endpoints.AccountsHost
			}
			if c.Role != "application" || !c.Egress || !slices.Equal(c.Entrypoint, enteMuseumEntrypoint) || !slices.Equal(c.Command, enteMuseumCommand) ||
				c.Image.Ref != enteMuseumImageRef || c.Image.Digest != enteMuseumImageDigest ||
				!exactStringList(c.DependsOn, []string{"ente-postgres", "ente-s3"}) ||
				!equalStringMaps(c.Environment, expectedEnv) || len(c.SecretEnvironment) != 0 ||
				!slices.Equal(c.SecretFiles, enteMuseumSecretFiles) ||
				c.Health.Kind != "http" || c.Health.Path != "/ping" || c.Health.Port != 8080 || len(c.Health.Command) != 0 ||
				len(c.Volumes) != 1 || !volumeMatches(c.Volumes[0], "data", "/data", true) {
				return nil, fail(ErrInvalidPlan, path, "Ente Museum image, secrets, database, S3 or persistence differs")
			}
		case "ente-postgres":
			if c.Role != "database" || c.Egress || len(c.Entrypoint) != 0 || len(c.Command) != 0 ||
				c.Image.Ref != entePostgresImageRef || c.Image.Digest != entePostgresImageDigest || len(c.DependsOn) != 0 ||
				!reflect.DeepEqual(c.Environment, map[string]string{"POSTGRES_DB": "ente_db", "POSTGRES_USER": "pguser"}) ||
				!reflect.DeepEqual(c.SecretEnvironment, map[string]string{"POSTGRES_PASSWORD": "database-password"}) ||
				len(c.SecretFiles) != 0 ||
				c.Health.Kind != "command" || !exactStringList(c.Health.Command, []string{"pg_isready", "-U", "pguser", "-d", "ente_db"}) ||
				len(c.Volumes) != 1 || !volumeMatches(c.Volumes[0], "database", "/var/lib/postgresql/data", true) {
				return nil, fail(ErrInvalidPlan, path, "Ente PostgreSQL image, credentials or persistent data differs")
			}
		case "ente-s3":
			if c.Role != "application" || c.Egress || !slices.Equal(c.Entrypoint, enteS3Entrypoint) || !slices.Equal(c.Command, enteS3Command) ||
				c.Image.Ref != enteS3ImageRef || c.Image.Digest != enteS3ImageDigest || len(c.DependsOn) != 0 ||
				len(c.Environment) != 0 || len(c.SecretEnvironment) != 0 ||
				!slices.Equal(c.SecretFiles, enteS3SecretFiles) ||
				c.Health.Kind != "command" || !exactStringList(c.Health.Command, []string{"sh", "-c", "nc -z 127.0.0.1 7070"}) ||
				len(c.Volumes) != 1 || !volumeMatches(c.Volumes[0], "objects", "/data/s3", true) {
				return nil, fail(ErrInvalidPlan, path, "Ente S3 object storage image, credentials or persistence differs")
			}
		default:
			return nil, fail(ErrInvalidPlan, path, "unknown Ente Photos component")
		}
	}
	return components, nil
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func validateEntePhotosServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != entePhotosWorkloadRef || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != entePhotosHTTPPort ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" || endpoint.HealthRef != "ente-photos-http" ||
		endpoint.IngressAuth != "native" || endpoint.Data.BindingRef != entePhotosWorkloadRef || endpoint.Data.Locality != "primary-site" ||
		!exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) || !exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "Ente Photos route authority differs")
	}
	return nil
}

func validEntePhotosSecretRefs(refs map[string]string) bool {
	if len(refs) != len(entePhotosSecretSlots) {
		return false
	}
	for _, slot := range entePhotosSecretSlots {
		if !validSecretReference(refs[slot]) {
			return false
		}
	}
	return true
}

func init() {
	governedCustodyNodeRights[entePhotosWorkloadModuleID] = governedEntrypointSecretFiles(map[string][]selectedPaaSSecretFile{
		"ente-museum": enteMuseumSecretFiles,
		"ente-s3":     enteS3SecretFiles,
	})
	nativeDockerSecretReaders[entePhotosWorkloadModuleID] = map[string]nativeDockerSecretReader{
		"ente-postgres": {FileSuffix: true},
	}
}
