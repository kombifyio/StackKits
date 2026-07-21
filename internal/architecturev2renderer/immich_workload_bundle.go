package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

const (
	immichWorkloadModuleID    = "stackkits-immich-runtime"
	immichWorkloadUnitID      = "immich-server"
	immichWorkloadRendererRef = "stackkit"
	immichWorkloadTemplateRef = "builtin://workloads/immich/bundle/v1.json"
	immichWorkloadVersion     = "2.0.0"
	immichWorkloadOutputRef   = "workloads/immich/bundle.json"
)

// This fixed schema identity binds the renderer semantics. The rendered
// document itself also carries the exact plan-owned target and opaque secret
// references, so its artifact hash remains instance-specific.
const immichWorkloadRendererSchema = `stackkit.workload-bundle/v1|ImmichWorkloadBundle|selected-paas|provider-lifecycle:not-owned|components:server,ml,postgres,postgres-init,valkey|secret-material:not-included`

type immichRuntimeImage struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

type immichRuntimeVolume struct {
	ID     string `json:"id"`
	Target string `json:"target"`
	Class  string `json:"class"`
	Backup bool   `json:"backup"`
}

type immichRuntimeHealth struct {
	Kind    string   `json:"kind"`
	Path    string   `json:"path,omitempty"`
	Port    int      `json:"port,omitempty"`
	Command []string `json:"command,omitempty"`
}

type immichRuntimeComponent struct {
	ID                string                `json:"id"`
	Role              string                `json:"role"`
	Lifecycle         string                `json:"lifecycle"`
	Image             immichRuntimeImage    `json:"image"`
	DependsOn         []string              `json:"dependsOn"`
	NetworkRefs       []string              `json:"networkRefs"`
	Command           []string              `json:"command,omitempty"`
	Environment       map[string]string     `json:"environment,omitempty"`
	SecretEnvironment map[string]string     `json:"secretEnvironment,omitempty"`
	Volumes           []immichRuntimeVolume `json:"volumes,omitempty"`
	Health            immichRuntimeHealth   `json:"health"`
}

type immichServiceEndpoint struct {
	ServiceRef              string   `json:"serviceRef"`
	UpstreamProtocol        string   `json:"upstreamProtocol"`
	TargetPort              int      `json:"targetPort"`
	AllowedIngressProtocols []string `json:"allowedIngressProtocols"`
	AllowedExposures        []string `json:"allowedExposures"`
	OriginSelector          string   `json:"originSelector"`
	HealthRef               string   `json:"healthRef"`
	Data                    struct {
		BindingRef      string   `json:"bindingRef"`
		RequiredClasses []string `json:"requiredClasses"`
		Locality        string   `json:"locality"`
	} `json:"data"`
}

type immichWorkloadBundle struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Workload   struct {
		Ref            string `json:"ref"`
		AlternativeRef string `json:"alternativeRef"`
		ModuleRef      string `json:"moduleRef"`
		Release        string `json:"release"`
		Delivery       string `json:"delivery"`
		EntryComponent string `json:"entryComponentRef"`
	} `json:"workload"`
	Target struct {
		SiteRef     string `json:"siteRef"`
		NodeRef     string `json:"nodeRef"`
		InstanceRef string `json:"instanceRef"`
	} `json:"target"`
	Ownership struct {
		ExecutionAdapter  string `json:"executionAdapter"`
		ProviderLifecycle string `json:"providerLifecycle"`
		Credentials       string `json:"credentials"`
	} `json:"ownership"`
	SecretRefs map[string]string        `json:"secretRefs"`
	Components []immichRuntimeComponent `json:"components"`
	Route      immichServiceEndpoint    `json:"route"`
}

// ImmichWorkloadComponentDescriptor is the immutable component identity an
// external selected-PaaS adapter must observe after applying the bundle.
type ImmichWorkloadComponentDescriptor struct {
	ID          string
	Lifecycle   string
	ImageRef    string
	ImageDigest string
}

// ImmichWorkloadBundleDescriptor is the safe, credential-free projection of
// one validated Immich workload artifact. SecretRef is an opaque reference;
// this package never resolves or accepts secret material.
type ImmichWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	SecretRef   string
	Components  []ImmichWorkloadComponentDescriptor
}

// ParseImmichWorkloadBundle validates the closed generated artifact before a
// runtime adapter can consume it. No provider, endpoint, credential, daemon,
// socket, lease, generation, or lifecycle authority exists in this schema.
func ParseImmichWorkloadBundle(data []byte) (ImmichWorkloadBundleDescriptor, error) {
	path := "immichWorkloadBundle"
	var bundle immichWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return ImmichWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Immich workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v1" || bundle.Kind != "ImmichWorkloadBundle" ||
		bundle.Workload.Ref != "photos" || bundle.Workload.AlternativeRef != "immich" || bundle.Workload.ModuleRef != immichWorkloadModuleID ||
		bundle.Workload.Release != "v2.7.0" || bundle.Workload.Delivery != "selected-paas" || bundle.Workload.EntryComponent != "immich-server" ||
		bundle.Ownership.ExecutionAdapter != "external-selected-paas-adapter" || bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return ImmichWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Immich v2.7.0 contract")
	}
	for field, value := range map[string]string{"siteRef": bundle.Target.SiteRef, "nodeRef": bundle.Target.NodeRef, "instanceRef": bundle.Target.InstanceRef} {
		if err := requireContractID(value, path+".target."+field); err != nil {
			return ImmichWorkloadBundleDescriptor{}, err
		}
	}
	if len(bundle.SecretRefs) != 1 || !validSecretReference(bundle.SecretRefs["database-password"]) {
		return ImmichWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires exactly one opaque database-password reference")
	}
	componentsJSON, err := json.Marshal(bundle.Components)
	if err != nil {
		return ImmichWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path+".components", "canonicalize component graph", err)
	}
	components, err := validateImmichRuntimeComponents(componentsJSON, path+".components")
	if err != nil {
		return ImmichWorkloadBundleDescriptor{}, err
	}
	if err := validateImmichServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return ImmichWorkloadBundleDescriptor{}, err
	}
	descriptor := ImmichWorkloadBundleDescriptor{
		WorkloadRef: bundle.Workload.Ref, ModuleRef: bundle.Workload.ModuleRef, Release: bundle.Workload.Release,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		SecretRef: bundle.SecretRefs["database-password"], Components: make([]ImmichWorkloadComponentDescriptor, len(components)),
	}
	for index, component := range components {
		descriptor.Components[index] = ImmichWorkloadComponentDescriptor{ID: component.ID, Lifecycle: component.Lifecycle, ImageRef: component.Image.Ref, ImageDigest: component.Image.Digest}
	}
	return descriptor, nil
}

func ImmichWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(immichWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: immichWorkloadRendererRef,
		TemplateRef: immichWorkloadTemplateRef, Version: immichWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type immichWorkloadBundleRenderer struct{ contract RendererContract }

func newImmichWorkloadBundleRenderer() immichWorkloadBundleRenderer {
	return immichWorkloadBundleRenderer{contract: ImmichWorkloadBundleRendererContract()}
}

func (r immichWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateImmichWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.immich-workload", "marshal governed workload bundle", err)
	}
	data = append(data, '\n')
	return []UnitOutput{{Ref: immichWorkloadOutputRef, Bytes: data}}, nil
}

//nolint:gocyclo // Keep the complete workload authority check at one auditable boundary.
func validateImmichWorkloadUnit(unit RenderUnit, contract RendererContract) (immichWorkloadBundle, error) {
	path := "resolvedPlan.modules." + immichWorkloadModuleID + ".renderUnits." + immichWorkloadUnitID
	if unit.ModuleID() != immichWorkloadModuleID || unit.ID() != immichWorkloadUnitID {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", immichWorkloadModuleID, immichWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return immichWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Immich workload contract")
	}
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "selected-paas" {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "Immich requires exact container/selected-paas delivery")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entryComponent, hasEntry := unit.RuntimeEntryComponentRef()
	if !hasEngine || engine != "docker" || !hasImage || imageRef != "ghcr.io/immich-app/immich-server:v2.7.0" || !hasDigest || imageDigest != "sha256:ee60b98e7fcc836d61d7f5e7689514f3de7a9480f31ec6ca62d6221056b46ae1" || !hasEntry || entryComponent != "immich-server" {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact governed Immich v2.7.0 entry component")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Immich requires one exact node-local target")
	}
	if !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "logical placement must close over the exact rendered target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workloads do not receive daemon or socket authority")
	}
	if len(unit.PublicInputRefs()) != 0 || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.PlanInputsJSON()) || !emptyJSONArray(unit.InputBindingsJSON()) {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Immich v1 bundle has no free public or plan input")
	}
	if !exactStringList(unit.SecretInputRefs(), []string{"database-password"}) {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretInputRefs", "requires exactly the database-password secret slot")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || len(secretRefs) != 1 || !validSecretReference(secretRefs["database-password"]) {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires one opaque database-password reference and no secret material")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path+".interfaces", "selected-PaaS bundle receives no host, socket, or runtime-network authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != immichWorkloadOutputRef {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", immichWorkloadOutputRef)
	}
	components, err := validateImmichRuntimeComponents(unit.RuntimeComponentsJSON(), path+".runtime.components")
	if err != nil {
		return immichWorkloadBundle{}, err
	}
	var endpoints []immichServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return immichWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact photos endpoint")
	}
	endpoint := endpoints[0]
	if err := validateImmichServiceEndpoint(endpoint, path+".serviceEndpoints"); err != nil {
		return immichWorkloadBundle{}, err
	}
	bundle := immichWorkloadBundle{APIVersion: "stackkit.workload-bundle/v1", Kind: "ImmichWorkloadBundle", SecretRefs: secretRefs, Components: components, Route: endpoint}
	bundle.Workload.Ref = "photos"
	bundle.Workload.AlternativeRef = "immich"
	bundle.Workload.ModuleRef = immichWorkloadModuleID
	bundle.Workload.Release = "v2.7.0"
	bundle.Workload.Delivery = "selected-paas"
	bundle.Workload.EntryComponent = entryComponent
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "external-selected-paas-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateImmichServiceEndpoint(endpoint immichServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "photos" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 2283 || endpoint.OriginSelector != "control-authority-site" || endpoint.HealthRef != "immich-http" || endpoint.Data.BindingRef != "photos" || endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) || !exactStringList(endpoint.AllowedIngressProtocols, []string{"http", "https"}) || !sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "photos route authority differs from the governed Immich endpoint")
	}
	return nil
}

func validateImmichRuntimeComponents(raw []byte, path string) ([]immichRuntimeComponent, error) {
	var components []immichRuntimeComponent
	if err := decodeStrict(raw, &components); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode closed component graph", err)
	}
	sort.Slice(components, func(i, j int) bool { return components[i].ID < components[j].ID })
	expected := expectedImmichRuntimeComponents()
	if !reflect.DeepEqual(components, expected) {
		return nil, fail(ErrInvalidPlan, path, "component graph differs from the exact Immich v2.7.0 workload contract")
	}
	for _, component := range components {
		for key := range component.Environment {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "key") {
				return nil, fail(ErrInvalidPlan, path+"["+component.ID+"].environment."+key, "credential-like values must use secretEnvironment")
			}
		}
		if !validSHA256(component.Image.Digest) || component.ID == "" {
			return nil, fail(ErrInvalidPlan, path, "components must have exact immutable identities in canonical ID order")
		}
	}
	return components, nil
}

func expectedImmichRuntimeComponents() []immichRuntimeComponent {
	components := []immichRuntimeComponent{
		{ID: "immich-machine-learning", Role: "machine-learning", Lifecycle: "daemon", Image: immichRuntimeImage{Ref: "ghcr.io/immich-app/immich-machine-learning:v2.7.0", Digest: "sha256:aff861526d690bb720130a46bd48ee2827c44d2f601a194e61f31e979a591952"}, DependsOn: []string{}, NetworkRefs: []string{"immich-internal"}, Volumes: []immichRuntimeVolume{{ID: "model-cache", Target: "/cache", Class: "cache", Backup: false}}, Health: immichRuntimeHealth{Kind: "image"}},
		{ID: "immich-postgres", Role: "database", Lifecycle: "daemon", Image: immichRuntimeImage{Ref: "ghcr.io/immich-app/postgres:14-vectorchord0.4.3-pgvectors0.2.0", Digest: "sha256:bcf63357191b76a916ae5eb93464d65c07511da41e3bf7a8416db519b40b1c23"}, DependsOn: []string{}, NetworkRefs: []string{"immich-internal"}, Environment: map[string]string{"POSTGRES_DB": "immich", "POSTGRES_INITDB_ARGS": "--data-checksums", "POSTGRES_USER": "immich"}, SecretEnvironment: map[string]string{"POSTGRES_PASSWORD": "database-password"}, Volumes: []immichRuntimeVolume{{ID: "database", Target: "/var/lib/postgresql/data", Class: "persistent", Backup: true}}, Health: immichRuntimeHealth{Kind: "command", Command: []string{"pg_isready", "-U", "immich", "-d", "postgres"}}},
		{ID: "immich-postgres-init", Role: "database-init", Lifecycle: "one-shot", Image: immichRuntimeImage{Ref: "ghcr.io/immich-app/postgres:14-vectorchord0.4.3-pgvectors0.2.0", Digest: "sha256:bcf63357191b76a916ae5eb93464d65c07511da41e3bf7a8416db519b40b1c23"}, DependsOn: []string{"immich-postgres"}, NetworkRefs: []string{"immich-internal"}, Command: []string{"sh", "-c", "until pg_isready -h immich-postgres -U immich -d postgres; do sleep 1; done; psql -h immich-postgres -U immich -d postgres -tAc \"SELECT 1 FROM pg_database WHERE datname = 'immich'\" | grep -q 1 || createdb -h immich-postgres -U immich immich"}, Environment: map[string]string{"PGUSER": "immich"}, SecretEnvironment: map[string]string{"PGPASSWORD": "database-password"}, Health: immichRuntimeHealth{Kind: "completion"}},
		{ID: "immich-server", Role: "application", Lifecycle: "daemon", Image: immichRuntimeImage{Ref: "ghcr.io/immich-app/immich-server:v2.7.0", Digest: "sha256:ee60b98e7fcc836d61d7f5e7689514f3de7a9480f31ec6ca62d6221056b46ae1"}, DependsOn: []string{"immich-machine-learning", "immich-postgres-init", "immich-valkey"}, NetworkRefs: []string{"immich-internal"}, Environment: map[string]string{"DB_DATABASE_NAME": "immich", "DB_HOSTNAME": "immich-postgres", "DB_PORT": "5432", "DB_USERNAME": "immich", "IMMICH_MACHINE_LEARNING_URL": "http://immich-machine-learning:3003", "REDIS_HOSTNAME": "immich-valkey", "REDIS_PORT": "6379"}, SecretEnvironment: map[string]string{"DB_PASSWORD": "database-password"}, Volumes: []immichRuntimeVolume{{ID: "library", Target: "/data", Class: "persistent", Backup: true}}, Health: immichRuntimeHealth{Kind: "http", Path: "/api/server/ping", Port: 2283}},
		{ID: "immich-valkey", Role: "cache", Lifecycle: "daemon", Image: immichRuntimeImage{Ref: "docker.io/valkey/valkey:9", Digest: "sha256:3b55fbaa0cd93cf0d9d961f405e4dfcc70efe325e2d84da207a0a8e6d8fde4f9"}, DependsOn: []string{}, NetworkRefs: []string{"immich-internal"}, Command: []string{"valkey-server"}, Health: immichRuntimeHealth{Kind: "command", Command: []string{"redis-cli", "ping"}}},
	}
	sort.Slice(components, func(i, j int) bool { return components[i].ID < components[j].ID })
	return components
}

func sameStringSet(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	return reflect.DeepEqual(left, right)
}
