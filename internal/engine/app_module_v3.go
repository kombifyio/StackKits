package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	"gopkg.in/yaml.v3"
)

// AppModuleV3APIVersion identifies an #AppModuleV3 declaration.
const AppModuleV3APIVersion = "stackkit.app-module/v3"

// AppModuleV3Field is the module package field holding the declaration.
const AppModuleV3Field = "AppModuleV3"

// AppModuleV3 is the decoded foundation.#AppModuleV3 value
// (foundation/app_module_v3.cue). Field names follow the CUE export.
type AppModuleV3 struct {
	APIVersion     string                           `json:"apiVersion"`
	ModuleRef      string                           `json:"moduleRef"`
	WorkloadRef    string                           `json:"workloadRef"`
	AlternativeRef string                           `json:"alternativeRef"`
	Release        string                           `json:"release"`
	EntryComponent string                           `json:"entryComponent"`
	Delivery       AppModuleV3Delivery              `json:"delivery"`
	Lifecycle      AppModuleV3Lifecycle             `json:"lifecycle"`
	Route          *AppModuleV3Route                `json:"route,omitempty"`
	Secrets        map[string]AppModuleV3SecretSlot `json:"secrets"`
	ConfigFiles    []AppModuleV3ConfigFile          `json:"configFiles"`
	Backup         *AppModuleV3Backup               `json:"backup,omitempty"`
	Networks       map[string]AppModuleV3Network    `json:"networks"`
	Components     []AppModuleV3Component           `json:"components"`
}

// AppModuleV3Delivery is the delivery contract.
type AppModuleV3Delivery struct {
	Contract        string `json:"contract"`
	ManagedBy       string `json:"managedBy"`
	ResourcePrefix  string `json:"resourcePrefix"`
	NoWait          bool   `json:"noWait"`
	AdoptNoRecreate bool   `json:"adoptNoRecreate"`
}

// Delivery contracts.
const (
	DeliveryOpenTofuStage1 = "opentofu-stage1"
	DeliveryCompose        = "compose"
)

// AppModuleV3Lifecycle is the two-tier lifecycle.
type AppModuleV3Lifecycle struct {
	RequiredSafetyInit  []string `json:"requiredSafetyInit"`
	OptionalAppsetupRef string   `json:"optionalAppsetupRef,omitempty"`
}

// AppModuleV3Route is the governed service endpoint of the entry component.
type AppModuleV3Route struct {
	ServiceRef          string   `json:"serviceRef"`
	TargetPort          int      `json:"targetPort"`
	UpstreamProtocol    string   `json:"upstreamProtocol"`
	IngressAuth         string   `json:"ingressAuth"`
	HealthRef           string   `json:"healthRef"`
	RequiredPrivilege   string   `json:"requiredPrivilege"`
	AllowedExposures    []string `json:"allowedExposures"`
	CompanionSubdomains []string `json:"companionSubdomains,omitempty"`
}

// AppModuleV3SecretSlot is one opaque custody secret slot.
type AppModuleV3SecretSlot struct {
	Purpose string `json:"purpose"`
}

// AppModuleV3ConfigFile is one generated, read-only configuration file.
type AppModuleV3ConfigFile struct {
	ComponentRef      string `json:"componentRef"`
	Target            string `json:"target"`
	ConfigRendererRef string `json:"configRendererRef"`
}

// AppModuleV3Backup declares backup coverage, hook and probe.
type AppModuleV3Backup struct {
	Volumes []struct {
		ComponentRef string `json:"componentRef"`
		VolumeRef    string `json:"volumeRef"`
	} `json:"volumes"`
	HookRef  string `json:"hookRef,omitempty"`
	ProbeRef string `json:"probeRef,omitempty"`
}

// AppModuleV3Network is one project network.
type AppModuleV3Network struct {
	Internal bool `json:"internal"`
}

// AppModuleV3Image pins one image.
type AppModuleV3Image struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

// AppModuleV3Volume is one container mount.
type AppModuleV3Volume struct {
	ID       string `json:"id"`
	Target   string `json:"target"`
	Class    string `json:"class"`
	Backup   bool   `json:"backup"`
	ReadOnly bool   `json:"readOnly"`
}

// AppModuleV3Health is the component health contract.
type AppModuleV3Health struct {
	Kind          string   `json:"kind"`
	Path          string   `json:"path,omitempty"`
	Port          int      `json:"port,omitempty"`
	Command       []string `json:"command,omitempty"`
	Interval      string   `json:"interval,omitempty"`
	Timeout       string   `json:"timeout,omitempty"`
	Retries       int      `json:"retries,omitempty"`
	StartPeriod   string   `json:"startPeriod,omitempty"`
	StartInterval string   `json:"startInterval,omitempty"`
}

// AppModuleV3Resources is the measured per-container ceiling.
type AppModuleV3Resources struct {
	MemoryLimit       string  `json:"memoryLimit,omitempty"`
	MemoryReservation string  `json:"memoryReservation,omitempty"`
	CPUs              float64 `json:"cpus,omitempty"`
}

// AppModuleV3Security types every retained container hardening field.
type AppModuleV3Security struct {
	CapDrop      []string          `json:"capDrop,omitempty"`
	CapAdd       []string          `json:"capAdd,omitempty"`
	SecurityOpt  []string          `json:"securityOpt,omitempty"`
	Sysctls      map[string]string `json:"sysctls,omitempty"`
	Tmpfs        []string          `json:"tmpfs,omitempty"`
	User         string            `json:"user,omitempty"`
	ReadOnly     *bool             `json:"readOnly,omitempty"`
	ShmSize      string            `json:"shmSize,omitempty"`
	Ulimits      map[string]any    `json:"ulimits,omitempty"`
	PidsLimit    int               `json:"pidsLimit,omitempty"`
	MemLimit     string            `json:"memLimit,omitempty"`
	MemswapLimit string            `json:"memswapLimit,omitempty"`
	CPUs         float64           `json:"cpus,omitempty"`
	OOMScoreAdj  *int              `json:"oomScoreAdj,omitempty"`
}

// AppModuleV3Logging overrides the bounded default log policy.
type AppModuleV3Logging struct {
	Driver  string            `json:"driver"`
	Options map[string]string `json:"options"`
}

// AppModuleV3SecretFile delivers a custody secret as a 0400 file.
type AppModuleV3SecretFile struct {
	Slot            string `json:"slot"`
	Target          string `json:"target"`
	PathEnvironment string `json:"pathEnvironment"`
	UID             int    `json:"uid"`
	GID             int    `json:"gid"`
}

// AppModuleV3Traits are the governed trait families.
type AppModuleV3Traits struct {
	HostNetwork  bool `json:"hostNetwork,omitempty"`
	LANListeners []struct {
		Port       int    `json:"port"`
		Protocol   string `json:"protocol"`
		SettingRef string `json:"settingRef"`
	} `json:"lanListeners,omitempty"`
	PublishedPorts []struct {
		HostPort      int    `json:"hostPort"`
		ContainerPort int    `json:"containerPort"`
		Protocol      string `json:"protocol"`
		HostIP        string `json:"hostIP,omitempty"`
	} `json:"publishedPorts,omitempty"`
	DevicePassthrough []struct {
		HostPath   string `json:"hostPath"`
		Target     string `json:"target"`
		SettingRef string `json:"settingRef"`
	} `json:"devicePassthrough,omitempty"`
	Accelerator *struct {
		Access     string `json:"access"`
		ProfileRef string `json:"profileRef"`
	} `json:"accelerator,omitempty"`
	SandboxRuntime *struct {
		Runtime      string            `json:"runtime"`
		CompanionEnv map[string]string `json:"companionEnv,omitempty"`
	} `json:"sandboxRuntime,omitempty"`
	PeerNetworks []struct {
		WorkloadRef string `json:"workloadRef"`
		NetworkRef  string `json:"networkRef"`
	} `json:"peerNetworks,omitempty"`
	BundledPlugin *struct {
		Kind      string            `json:"kind"`
		Version   string            `json:"version"`
		SHA256    string            `json:"sha256"`
		Artifacts map[string]string `json:"artifacts,omitempty"`
	} `json:"bundledPlugin,omitempty"`
	DockerLifecycleOwner bool `json:"dockerLifecycleOwner,omitempty"`
	OwnHostSSH           bool `json:"ownHostSSH,omitempty"`

	HomeIdentityAccess *struct {
		CABundleTarget      string   `json:"caBundleTarget"`
		CABundleEnvironment []string `json:"caBundleEnvironment"`
	} `json:"homeIdentityAccess,omitempty"`
	PocketIDClient *struct {
		PublicClient  bool              `json:"publicClient"`
		CallbackPath  string            `json:"callbackPath,omitempty"`
		CallbackURLs  []string          `json:"callbackURLs,omitempty"`
		Environment   map[string]string `json:"environment"`
		Configuration *struct {
			Target string `json:"target"`
			Body   string `json:"body"`
		} `json:"configuration,omitempty"`
	} `json:"pocketIDClient,omitempty"`
	HomeAssistantNotify *struct {
		SettingRef string `json:"settingRef"`
		Target     string `json:"target"`
	} `json:"homeAssistantNotify,omitempty"`
}

// AppModuleV3Component is one container.
type AppModuleV3Component struct {
	ID                   string                  `json:"id"`
	Role                 string                  `json:"role"`
	Lifecycle            string                  `json:"lifecycle"`
	HealthFailure        string                  `json:"healthFailure"`
	Image                AppModuleV3Image        `json:"image"`
	PullPolicy           string                  `json:"pullPolicy,omitempty"`
	Restart              string                  `json:"restart,omitempty"`
	Runtime              string                  `json:"runtime,omitempty"`
	Command              []string                `json:"command,omitempty"`
	Entrypoint           []string                `json:"entrypoint,omitempty"`
	Init                 *bool                   `json:"init,omitempty"`
	StopSignal           string                  `json:"stopSignal,omitempty"`
	DependsOn            []string                `json:"dependsOn"`
	DependsOnConditions  map[string]string       `json:"dependsOnConditions,omitempty"`
	Environment          map[string]string       `json:"environment"`
	SecretEnvironment    map[string]string       `json:"secretEnvironment"`
	EnvironmentFromRoute map[string]string       `json:"environmentFromRoute,omitempty"`
	SecretFiles          []AppModuleV3SecretFile `json:"secretFiles"`
	Volumes              []AppModuleV3Volume     `json:"volumes"`
	Networks             []string                `json:"networks"`
	Egress               bool                    `json:"egress"`
	Health               AppModuleV3Health       `json:"health"`
	Resources            *AppModuleV3Resources   `json:"resources,omitempty"`
	Security             *AppModuleV3Security    `json:"security,omitempty"`
	Logging              *AppModuleV3Logging     `json:"logging,omitempty"`
	DNS                  []string                `json:"dns,omitempty"`
	ExtraHosts           []string                `json:"extraHosts,omitempty"`
	MACAddress           string                  `json:"macAddress,omitempty"`
	Labels               map[string]string       `json:"labels"`
	Traits               AppModuleV3Traits       `json:"traits"`
}

// LoadAppModuleV3 evaluates the CUE package in moduleDir (relative to
// repoRoot, the CUE module root), validates its AppModuleV3 field concretely
// against foundation.#AppModuleV3 and decodes it strictly.
func LoadAppModuleV3(repoRoot, moduleDir string) (AppModuleV3, error) {
	path := "appModuleV3." + filepath.ToSlash(moduleDir)
	instances := load.Instances([]string{"./" + filepath.ToSlash(filepath.Clean(moduleDir))}, &load.Config{Dir: repoRoot})
	if len(instances) != 1 {
		return AppModuleV3{}, fail(ErrInvalidModule, path, "expected one CUE instance, got %d", len(instances))
	}
	if err := instances[0].Err; err != nil {
		return AppModuleV3{}, wrap(ErrInvalidModule, path, "load module package", err)
	}
	value := cuecontext.New().BuildInstance(instances[0])
	if err := value.Err(); err != nil {
		return AppModuleV3{}, wrap(ErrInvalidModule, path, "build module package", err)
	}
	declaration := value.LookupPath(cue.ParsePath(AppModuleV3Field))
	if !declaration.Exists() {
		return AppModuleV3{}, fail(ErrInvalidModule, path, "module declares no %s", AppModuleV3Field)
	}
	if err := declaration.Validate(cue.Concrete(true)); err != nil {
		return AppModuleV3{}, wrap(ErrInvalidModule, path, "declaration is not concrete and valid", err)
	}
	raw, err := declaration.MarshalJSON()
	if err != nil {
		return AppModuleV3{}, wrap(ErrInvalidModule, path, "export declaration", err)
	}
	return DecodeAppModuleV3(raw)
}

// DecodeAppModuleV3 decodes an exported declaration strictly and validates
// the Go-side rules the CUE schema cannot express (closed registries, trait
// admission, route and lifecycle coherence).
func DecodeAppModuleV3(raw []byte) (AppModuleV3, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var module AppModuleV3
	if err := decoder.Decode(&module); err != nil {
		return AppModuleV3{}, wrap(ErrInvalidModule, "appModuleV3", "decode closed declaration", err)
	}
	if err := ValidateAppModuleV3(module); err != nil {
		return AppModuleV3{}, err
	}
	return module, nil
}

// ValidateAppModuleV3 enforces the engine's admission rules.
//
//nolint:gocyclo // One closed admission boundary; every branch fails closed.
func ValidateAppModuleV3(module AppModuleV3) error {
	path := "appModuleV3." + module.ModuleRef
	if module.APIVersion != AppModuleV3APIVersion {
		return fail(ErrInvalidModule, path+".apiVersion", "must be %s", AppModuleV3APIVersion)
	}
	if module.Delivery.Contract != DeliveryOpenTofuStage1 && module.Delivery.Contract != DeliveryCompose {
		return fail(ErrInvalidModule, path+".delivery.contract", "unsupported delivery contract %q", module.Delivery.Contract)
	}
	if module.Delivery.ManagedBy != module.Delivery.Contract || !composePayloadResourcePattern.MatchString(module.Delivery.ResourcePrefix) {
		return fail(ErrInvalidModule, path+".delivery", "managedBy must equal the contract and resourcePrefix must be an HCL identifier")
	}
	if ref := module.Lifecycle.OptionalAppsetupRef; ref != "" {
		if _, ok := LookupAdapter(AdapterSetup, ref); !ok {
			return fail(ErrUnknownAdapter, path+".lifecycle.optionalAppsetupRef", "%q is not a registered setup adapter", ref)
		}
	}
	if module.Backup != nil {
		if ref := module.Backup.HookRef; ref != "" {
			if _, ok := LookupAdapter(AdapterHook, ref); !ok {
				return fail(ErrUnknownAdapter, path+".backup.hookRef", "%q is not a registered hook adapter", ref)
			}
		}
		if ref := module.Backup.ProbeRef; ref != "" {
			if _, ok := LookupAdapter(AdapterProbe, ref); !ok {
				return fail(ErrUnknownAdapter, path+".backup.probeRef", "%q is not a registered probe adapter", ref)
			}
		}
	}
	components := map[string]AppModuleV3Component{}
	degraded := false
	for _, component := range module.Components {
		if _, duplicate := components[component.ID]; duplicate {
			return fail(ErrInvalidModule, path+".components", "duplicate component %q", component.ID)
		}
		components[component.ID] = component
		degraded = degraded || component.HealthFailure == "degraded"
	}
	if module.Delivery.NoWait != degraded {
		return fail(ErrInvalidModule, path+".delivery.noWait", "noWait must follow the degraded health failures")
	}
	entry, ok := components[module.EntryComponent]
	if !ok || entry.Lifecycle != "daemon" {
		return fail(ErrInvalidModule, path+".entryComponent", "entry must be a declared daemon")
	}
	if entry.Health.Kind != "http" || entry.Health.Port <= 0 {
		return fail(ErrInvalidModule, path+".entryComponent", "entry must declare an HTTP health port for the loopback observation")
	}
	if module.Route != nil && (module.Route.TargetPort != entry.Health.Port) {
		return fail(ErrInvalidModule, path+".route.targetPort", "route target port must be the entry health port")
	}
	for _, ref := range module.Lifecycle.RequiredSafetyInit {
		if component, ok := components[ref]; !ok || component.Lifecycle == "daemon" {
			return fail(ErrInvalidModule, path+".lifecycle.requiredSafetyInit", "%q must be a declared one-shot or init-once component", ref)
		}
	}
	for index, file := range module.ConfigFiles {
		filePath := fmt.Sprintf("%s.configFiles[%d]", path, index)
		if _, ok := LookupConfigRenderer(file.ConfigRendererRef); !ok {
			return fail(ErrUnknownAdapter, filePath+".configRendererRef", "%q is not a registered config renderer", file.ConfigRendererRef)
		}
		owner, ok := components[file.ComponentRef]
		if !ok || !volumeOwnsPath(owner.Volumes, file.Target) {
			return fail(ErrInvalidModule, filePath, "config file target must lie inside a volume of its component")
		}
	}
	for _, component := range module.Components {
		if err := validateAppModuleV3Component(module, component, path+".components."+component.ID); err != nil {
			return err
		}
	}
	return nil
}

func validateAppModuleV3Component(module AppModuleV3, component AppModuleV3Component, path string) error {
	for _, slot := range component.SecretEnvironment {
		if _, ok := module.Secrets[slot]; !ok {
			return fail(ErrInvalidModule, path+".secretEnvironment", "references undeclared secret slot %q", slot)
		}
	}
	for _, file := range component.SecretFiles {
		if _, ok := module.Secrets[file.Slot]; !ok {
			return fail(ErrInvalidModule, path+".secretFiles", "references undeclared secret slot %q", file.Slot)
		}
		if volumeOwnsPath(component.Volumes, file.Target) {
			return fail(ErrInvalidModule, path+".secretFiles", "a secret file must stay outside the workload volumes")
		}
	}
	for name := range component.Labels {
		if strings.HasPrefix(name, "io.stackkit.") || strings.HasPrefix(name, "traefik.") {
			return fail(ErrInvalidModule, path+".labels", "label %q is owned by the renderer", name)
		}
	}
	if len(component.EnvironmentFromRoute) != 0 && (component.ID != module.EntryComponent || module.Route == nil) {
		return fail(ErrInvalidModule, path+".environmentFromRoute", "only the routed entry component binds route values")
	}
	if security := component.Security; security != nil {
		// The workload Compose model has no tmpfs, shm_size, ulimits or
		// read_only keys yet: the Stage 2 native digest marshals the whole
		// service model, so a new model key would replace every native
		// container. A declaration that needs one fails closed until the
		// digest is decoupled (Phase 2).
		if len(security.Tmpfs) != 0 || security.ShmSize != "" || len(security.Ulimits) != 0 || security.ReadOnly != nil {
			return fail(ErrInvalidModule, path+".security", "tmpfs, shmSize, ulimits and readOnly have no workload Compose model key yet")
		}
	}
	traits := component.Traits
	checks := []struct {
		trait    string
		declared bool
	}{
		{TraitHostNetwork, traits.HostNetwork},
		{TraitLANListeners, len(traits.LANListeners) != 0},
		{TraitPublishedPorts, len(traits.PublishedPorts) != 0},
		{TraitDevicePassthrough, len(traits.DevicePassthrough) != 0},
		{TraitAccelerator, traits.Accelerator != nil},
		{TraitSandboxRuntime, traits.SandboxRuntime != nil},
		{TraitPeerNetworks, len(traits.PeerNetworks) != 0},
		{TraitBundledPlugin, traits.BundledPlugin != nil},
		{TraitDockerLifecycleOwner, traits.DockerLifecycleOwner},
		{TraitOwnHostSSH, traits.OwnHostSSH},
		{TraitStopSignal, component.StopSignal != ""},
		{TraitInit, component.Init != nil && *component.Init},
	}
	for _, check := range checks {
		if check.declared && !TraitAdmitted(check.trait, module.ModuleRef, component.ID) {
			return fail(ErrInvalidModule, path+".traits."+check.trait, "trait is not admitted for %s/%s", module.ModuleRef, component.ID)
		}
	}
	if traits.BundledPlugin != nil {
		want := map[string]string{"stackkits-home-assistant-runtime": "hass-oidc", "stackkits-jellyfin-runtime": "jellyfin-sso"}[module.ModuleRef]
		if traits.BundledPlugin.Kind != want {
			return fail(ErrInvalidModule, path+".traits.bundledPlugin", "plugin %q is not the module's governed plugin", traits.BundledPlugin.Kind)
		}
	}
	if traits.HostNetwork && (len(component.Networks) != 0 || component.Egress || len(traits.PeerNetworks) != 0 ||
		len(traits.PublishedPorts) != 0 || len(traits.LANListeners) != 0 || component.ID == module.EntryComponent) {
		return fail(ErrInvalidModule, path+".traits.hostNetwork", "a host-network announcer joins no network and publishes nothing")
	}
	if !traits.HostNetwork && len(component.Networks) == 0 {
		return fail(ErrInvalidModule, path+".networks", "a component joins at least one project network")
	}
	return nil
}

// AppModuleRoute is the plan's delivery route of the entry component, as
// the standalone Compose owner receives it.
type AppModuleRoute struct {
	CoreModuleRef         string
	ID                    string
	Exposure              string
	Protocol              string
	HealthGateRef         string
	BackendPoolRef        string
	Host                  string
	Path                  string
	Port                  int
	TLSRequired           bool
	TLSMode               string
	TLSMinVersion         string
	TLSProfileRef         string
	TLSIssuerRef          string
	TLSOwnerCapabilityRef string
}

// AppModuleRenderContext is the plan-bound input of one render. It carries
// no workspace path and no secret material.
type AppModuleRenderContext struct {
	NodeRef string
	Route   *AppModuleRoute
	// EnabledSettings are the owner-enabled workload settings (LAN rights,
	// device passthrough, notify).
	EnabledSettings map[string]bool
	// OriginServed is the host fact that a delivered origin or public-names
	// certificate already covers the route host (no ACME resolver label).
	OriginServed bool
}

// PendingHostTrait is a declared trait the node adapter binds from workspace
// custody at Apply; the engine never renders it.
type PendingHostTrait struct {
	ComponentID string
	Trait       string
}

// AppModuleRender is the deterministic output of one render.
type AppModuleRender struct {
	ProjectName string
	Document    WorkloadComposeDocument
	Compose     []byte
	// ConfigFiles are project-relative generated files.
	ConfigFiles map[string][]byte
	// SecretVariables maps each .env interpolation variable to its slot.
	SecretVariables   map[string]string
	PendingHostTraits []PendingHostTrait
}

// Container defaults every daemon gets unless its declaration overrides
// them. They match the standalone Compose owner.
var appModuleRoleOOMScoreAdj = map[string]int{
	"database":         -500,
	"database-init":    -500,
	"cache":            -250,
	"application":      -100,
	"machine-learning": 500,
}

const (
	appModuleHealthNetwork  = "stackkit-workload-health"
	appModuleEgressNetwork  = "stackkit-workload-egress"
	appModuleRoutingNetwork = "stackkit-routing"
	appModuleRemoteListener = "io.stackkit.remote-listener"
)

// AppModuleComposeProjectName is the Compose project of a workload on a node.
func AppModuleComposeProjectName(workloadRef, nodeRef string) string {
	return "stackkit-" + workloadRef + "-" + nodeRef
}

// AppModuleSecretVariable is the .env interpolation variable of one secret.
func AppModuleSecretVariable(componentID, environmentName string) string {
	value := strings.ToUpper(componentID + "_" + environmentName)
	return "STACKKIT_SECRET_" + strings.NewReplacer("-", "_", ".", "_").Replace(value)
}

// AppModuleConfigRelPath is the project-relative path of a generated
// configuration file (architecturev2renderer.StandaloneComposeConfigRelPath).
func AppModuleConfigRelPath(containerPath string) string {
	return "files/" + strings.ReplaceAll(strings.TrimPrefix(containerPath, "/"), "/", "_")
}

// RenderAppModuleV3 renders a validated declaration into the workload
// Compose model and its YAML. The output is a pure function of the
// declaration and the context.
//
//nolint:gocyclo // One renderer keeps every admitted mount and exception visible together.
func RenderAppModuleV3(module AppModuleV3, ctx AppModuleRenderContext) (AppModuleRender, error) {
	path := "render." + module.ModuleRef
	if err := ValidateAppModuleV3(module); err != nil {
		return AppModuleRender{}, err
	}
	if !ComposePayloadProjectPattern.MatchString(AppModuleComposeProjectName(module.WorkloadRef, ctx.NodeRef)) || ctx.NodeRef == "" {
		return AppModuleRender{}, fail(ErrRendererFailure, path+".nodeRef", "node reference does not form a Compose project name")
	}
	routingNetwork := ""
	if ctx.Route != nil {
		if module.Route == nil {
			return AppModuleRender{}, fail(ErrRendererFailure, path+".route", "module declares no route endpoint")
		}
		var err error
		if routingNetwork, err = CoreRoutingNetwork(ctx.Route.CoreModuleRef); err != nil {
			return AppModuleRender{}, wrap(ErrRendererFailure, path+".route.coreModuleRef", "resolve routing network", err)
		}
	}
	document := WorkloadComposeDocument{
		Name:     AppModuleComposeProjectName(module.WorkloadRef, ctx.NodeRef),
		Services: map[string]WorkloadComposeService{},
		Networks: map[string]WorkloadComposeNetwork{},
		Volumes:  map[string]map[string]any{},
	}
	result := AppModuleRender{ProjectName: document.Name, ConfigFiles: map[string][]byte{}, SecretVariables: map[string]string{}}
	lifecycle := make(map[string]string, len(module.Components))
	for _, component := range module.Components {
		lifecycle[component.ID] = component.Lifecycle
		for _, networkRef := range component.Networks {
			document.Networks[networkRef] = WorkloadComposeNetwork{Internal: module.Networks[networkRef].Internal}
		}
	}
	routeHost := ""
	if ctx.Route != nil {
		routeHost = ctx.Route.Host
	}
	configInput := ConfigRenderInput{RouteHost: routeHost, EnabledSettings: ctx.EnabledSettings}
	for _, component := range module.Components {
		componentPath := path + ".components." + component.ID
		service := WorkloadComposeService{
			Image:       component.Image.Ref + "@" + component.Image.Digest,
			PullPolicy:  component.PullPolicy,
			Command:     composeLiteralArguments(component.Command),
			Entrypoint:  composeLiteralArguments(component.Entrypoint),
			DependsOn:   map[string]WorkloadComposeDependency{},
			Environment: map[string]string{},
			Networks:    append([]string(nil), component.Networks...),
			DNS:         append([]string(nil), component.DNS...),
			ExtraHosts:  append([]string(nil), component.ExtraHosts...),
			MACAddress:  component.MACAddress,
			Labels: map[string]string{
				"io.stackkit.health-failure":  component.HealthFailure,
				WorkloadComposeLifecycleLabel: component.Lifecycle,
			},
		}
		if len(service.DNS) == 0 {
			service.DNS = nil
		}
		if len(service.ExtraHosts) == 0 {
			service.ExtraHosts = nil
		}
		for name, value := range component.Labels {
			service.Labels[name] = value
		}
		service.Deploy = appModuleDeploy(component.Resources)
		if component.Egress {
			document.Networks[appModuleEgressNetwork] = WorkloadComposeNetwork{}
			service.Networks = append(service.Networks, appModuleEgressNetwork)
		}
		if component.Lifecycle == "daemon" {
			service.Restart = "unless-stopped"
			service.Logging = &WorkloadComposeLogging{Driver: "json-file", Options: map[string]string{"max-size": "10m", "max-file": "3"}}
			if score, ok := appModuleRoleOOMScoreAdj[component.Role]; ok {
				service.OOMScoreAdj = &score
			}
		}
		if component.Restart != "" {
			service.Restart = component.Restart
		}
		if component.Logging != nil {
			service.Logging = &WorkloadComposeLogging{Driver: component.Logging.Driver, Options: component.Logging.Options}
		}
		applyAppModuleSecurity(component.Security, &service)
		for _, dependency := range component.DependsOn {
			condition := "service_started"
			if lifecycle[dependency] != "daemon" {
				condition = "service_completed_successfully"
			}
			if override, ok := component.DependsOnConditions[dependency]; ok {
				condition = override
			}
			service.DependsOn[dependency] = WorkloadComposeDependency{Condition: condition}
		}
		for key, value := range component.Environment {
			service.Environment[key] = composeLiteral(value)
		}
		for key, source := range component.EnvironmentFromRoute {
			if _, conflict := service.Environment[key]; conflict {
				return AppModuleRender{}, fail(ErrRendererFailure, componentPath+".environmentFromRoute", "%s conflicts with a declared variable", key)
			}
			if ctx.Route == nil {
				// Without a delivery route the legacy renderer binds nothing.
				continue
			}
			value, err := appModuleRouteValue(source, *ctx.Route)
			if err != nil {
				return AppModuleRender{}, wrap(ErrRendererFailure, componentPath+".environmentFromRoute."+key, "derive route value", err)
			}
			service.Environment[key] = composeLiteral(value)
		}
		if sandbox := component.Traits.SandboxRuntime; sandbox != nil {
			service.Runtime = sandbox.Runtime
			for key, value := range sandbox.CompanionEnv {
				if _, conflict := service.Environment[key]; conflict {
					return AppModuleRender{}, fail(ErrRendererFailure, componentPath+".traits.sandboxRuntime", "companion variable %s conflicts", key)
				}
				service.Environment[key] = composeLiteral(value)
			}
		}
		if component.Runtime != "" {
			service.Runtime = component.Runtime
		}
		for environmentName, slot := range component.SecretEnvironment {
			variable := AppModuleSecretVariable(component.ID, environmentName)
			result.SecretVariables[variable] = slot
			service.Environment[environmentName] = "${" + variable + ":?required}"
		}
		for _, file := range component.SecretFiles {
			variable := AppModuleSecretVariable(component.ID, "FILE_"+file.Slot)
			result.SecretVariables[variable] = file.Slot
			if document.Secrets == nil {
				document.Secrets = map[string]WorkloadComposeSecret{}
			}
			name := component.ID + "-" + file.Slot
			document.Secrets[name] = WorkloadComposeSecret{Environment: variable}
			service.Secrets = append(service.Secrets, WorkloadComposeServiceSecret{
				Source: name, Target: file.Target, UID: strconv.Itoa(file.UID), GID: strconv.Itoa(file.GID), Mode: 0o400,
			})
			if _, conflict := service.Environment[file.PathEnvironment]; conflict {
				return AppModuleRender{}, fail(ErrRendererFailure, componentPath+".secretFiles", "path variable %s conflicts", file.PathEnvironment)
			}
			service.Environment[file.PathEnvironment] = file.Target
		}
		for _, volume := range component.Volumes {
			ref := component.ID + "-" + volume.ID
			document.Volumes[ref] = map[string]any{}
			mount := ref + ":" + volume.Target
			if volume.ReadOnly {
				mount += ":ro"
			}
			service.Volumes = append(service.Volumes, mount)
		}
		configDigest, mountedConfig := sha256.New(), 0
		for _, file := range module.ConfigFiles {
			if file.ComponentRef != component.ID {
				continue
			}
			renderer, _ := LookupConfigRenderer(file.ConfigRendererRef)
			body, err := renderer.Render(configInput)
			if err != nil {
				return AppModuleRender{}, wrap(ErrRendererFailure, componentPath+".configFiles", "render "+file.ConfigRendererRef, err)
			}
			rel := AppModuleConfigRelPath(file.Target)
			service.Volumes = append(service.Volumes, "./"+rel+":"+file.Target+":ro")
			result.ConfigFiles[rel] = []byte(body)
			fmt.Fprintf(configDigest, "%s\x00%d\x00%s", file.Target, len(body), body)
			mountedConfig++
		}
		if mountedConfig > 0 {
			// A changed config file keeps compose.yaml identical; the digest
			// label makes Compose recreate exactly the affected service.
			service.Labels["io.stackkit.config-digest"] = "sha256:" + hex.EncodeToString(configDigest.Sum(nil))
		}
		if component.StopSignal != "" {
			service.StopSignal = component.StopSignal
		}
		if component.Init != nil && *component.Init {
			enabled := true
			service.Init = &enabled
		}
		if component.Health.Kind == "command" {
			service.Healthcheck = &WorkloadComposeHealthcheck{
				Test:     append([]string{"CMD"}, composeLiteralArguments(component.Health.Command)...),
				Interval: component.Health.Interval, Timeout: component.Health.Timeout,
				Retries: component.Health.Retries, StartPeriod: component.Health.StartPeriod,
				StartInterval: component.Health.StartInterval,
			}
		}
		if component.ID == module.EntryComponent {
			service.Ports = []string{fmt.Sprintf("127.0.0.1::%d", component.Health.Port)}
			if ctx.Route != nil {
				document.Networks[appModuleRoutingNetwork] = WorkloadComposeNetwork{Name: routingNetwork, External: true}
				service.Networks = append(service.Networks, appModuleRoutingNetwork)
				labels, err := appModuleRouteLabels(module, *ctx.Route, routingNetwork, ctx.OriginServed)
				if err != nil {
					return AppModuleRender{}, wrap(ErrRendererFailure, componentPath+".route", "render route labels", err)
				}
				for key, value := range labels {
					service.Labels[key] = value
				}
			} else {
				// Docker binds no published port for a container attached only
				// to internal networks; the entry gets one workload-local bridge.
				document.Networks[appModuleHealthNetwork] = WorkloadComposeNetwork{}
				service.Networks = append(service.Networks, appModuleHealthNetwork)
			}
		}
		traits := component.Traits
		for _, port := range traits.PublishedPorts {
			binding := fmt.Sprintf("%d:%d/%s", port.HostPort, port.ContainerPort, port.Protocol)
			if port.HostIP != "" {
				binding = port.HostIP + ":" + binding
			}
			service.Ports = append(service.Ports, binding)
		}
		for _, listener := range traits.LANListeners {
			if listener.SettingRef == "" || ctx.EnabledSettings[listener.SettingRef] {
				service.Ports = append(service.Ports, fmt.Sprintf("%d:%d/%s", listener.Port, listener.Port, listener.Protocol))
			}
		}
		for _, device := range traits.DevicePassthrough {
			if ctx.EnabledSettings[device.SettingRef] {
				service.Devices = append(service.Devices, device.HostPath+":"+device.Target)
			}
		}
		if accelerator := traits.Accelerator; accelerator != nil {
			switch accelerator.Access {
			case "cdi":
				if service.Deploy == nil {
					service.Deploy = &WorkloadComposeDeploy{}
				}
				if service.Deploy.Resources.Reservations == nil {
					service.Deploy.Resources.Reservations = &WorkloadComposeResourceBounds{}
				}
				service.Deploy.Resources.Reservations.Devices = append(service.Deploy.Resources.Reservations.Devices, WorkloadComposeDeviceRequest{
					Driver: "cdi", DeviceIDs: []string{"nvidia.com/gpu=all"}, Capabilities: []string{"gpu"},
				})
			case "rocm-device-nodes":
				service.Devices = append(service.Devices, "/dev/kfd:/dev/kfd", "/dev/dri:/dev/dri")
			default:
				return AppModuleRender{}, fail(ErrRendererFailure, componentPath+".traits.accelerator", "accelerator access %q is not governed", accelerator.Access)
			}
		}
		for _, peer := range traits.PeerNetworks {
			key := "peer-" + peer.WorkloadRef + "-" + peer.NetworkRef
			document.Networks[key] = WorkloadComposeNetwork{Name: AppModuleComposeProjectName(peer.WorkloadRef, ctx.NodeRef) + "_" + peer.NetworkRef, External: true}
			service.Networks = append(service.Networks, key)
		}
		for _, pending := range []struct {
			trait    string
			declared bool
		}{
			{TraitBundledPlugin, traits.BundledPlugin != nil},
			{"homeIdentityAccess", traits.HomeIdentityAccess != nil},
			{"pocketIDClient", traits.PocketIDClient != nil},
			{"homeAssistantNotify", traits.HomeAssistantNotify != nil && ctx.EnabledSettings[traits.HomeAssistantNotify.SettingRef]},
			{TraitDockerLifecycleOwner, traits.DockerLifecycleOwner},
			{TraitOwnHostSSH, traits.OwnHostSSH},
		} {
			if pending.declared {
				result.PendingHostTraits = append(result.PendingHostTraits, PendingHostTrait{ComponentID: component.ID, Trait: pending.trait})
			}
		}
		if traits.HostNetwork {
			service.NetworkMode = "host"
			service.Networks = nil
		}
		sort.Strings(service.Networks)
		sort.SliceStable(service.Volumes, func(i, j int) bool {
			return fmt.Sprint(service.Volumes[i]) < fmt.Sprint(service.Volumes[j])
		})
		if len(service.Volumes) == 0 {
			service.Volumes = nil
		}
		document.Services[component.ID] = service
	}
	compose, err := yaml.Marshal(document)
	if err != nil {
		return AppModuleRender{}, wrap(ErrRendererFailure, path, "marshal workload Compose definition", err)
	}
	result.Document, result.Compose = document, compose
	return result, nil
}

// RenderAppModuleV3OpenTofu wraps a render in the Stage 1 OpenTofu root of
// the module's delivery contract. The root references the private .env the
// node adapter writes beside compose.yaml, like the native executor.
func RenderAppModuleV3OpenTofu(module AppModuleV3, render AppModuleRender) ([]byte, error) {
	if module.Delivery.Contract != DeliveryOpenTofuStage1 {
		return nil, fail(ErrRendererFailure, "render."+module.ModuleRef+".delivery", "module delivery contract is %q", module.Delivery.Contract)
	}
	return RenderComposePayloadOpenTofu(ComposePayloadSpec{
		ResourcePrefix: module.Delivery.ResourcePrefix, ProjectName: render.ProjectName,
		Compose: render.Compose, EnvFile: ".env",
		NoWait: module.Delivery.NoWait, AdoptNoRecreate: module.Delivery.AdoptNoRecreate,
	})
}

func applyAppModuleSecurity(security *AppModuleV3Security, service *WorkloadComposeService) {
	if security == nil {
		return
	}
	service.CapDrop = append([]string(nil), security.CapDrop...)
	service.CapAdd = append([]string(nil), security.CapAdd...)
	service.SecurityOpt = append([]string(nil), security.SecurityOpt...)
	if len(security.Sysctls) != 0 {
		service.Sysctls = security.Sysctls
	}
	service.User = security.User
	service.PidsLimit = security.PidsLimit
	service.MemLimit = security.MemLimit
	service.MemSwapLimit = security.MemswapLimit
	if security.CPUs > 0 {
		service.CPUs = strconv.FormatFloat(security.CPUs, 'f', -1, 64)
	}
	if security.OOMScoreAdj != nil {
		score := *security.OOMScoreAdj
		service.OOMScoreAdj = &score
	}
	for _, list := range []*[]string{&service.CapDrop, &service.CapAdd, &service.SecurityOpt} {
		if len(*list) == 0 {
			*list = nil
		}
	}
}

func appModuleDeploy(declared *AppModuleV3Resources) *WorkloadComposeDeploy {
	if declared == nil {
		return nil
	}
	deploy := &WorkloadComposeDeploy{}
	if declared.MemoryLimit != "" || declared.CPUs > 0 {
		deploy.Resources.Limits = &WorkloadComposeResourceBounds{Memory: declared.MemoryLimit}
		if declared.CPUs > 0 {
			deploy.Resources.Limits.CPUs = strconv.FormatFloat(declared.CPUs, 'f', -1, 64)
		}
	}
	if declared.MemoryReservation != "" {
		deploy.Resources.Reservations = &WorkloadComposeResourceBounds{Memory: declared.MemoryReservation}
	}
	if deploy.Resources.Limits == nil && deploy.Resources.Reservations == nil {
		return nil
	}
	return deploy
}

// CoreRoutingNetwork is the external routing network a plan-bound Core owner
// provides to workload routes.
func CoreRoutingNetwork(coreModuleRef string) (string, error) {
	switch coreModuleRef {
	case "stackkits-basement-core-runtime", "stackkits-basement-core-lite-runtime":
		return "stackkit-basement-core", nil
	case "stackkits-cloud-core-runtime":
		return "stackkit-cloud-core", nil
	case "stackkits-cloud-core-standalone-runtime":
		return "stackkit-cloud-core-standalone", nil
	default:
		return "", fmt.Errorf("route requires a supported plan-bound core owner, got %q", coreModuleRef)
	}
}

// HomeCore reports a Core with separate LAN and remote listeners. A Cloud
// Core's only listener is public and therefore remote.
func HomeCore(coreModuleRef string) bool {
	return coreModuleRef == "stackkits-basement-core-runtime" || coreModuleRef == "stackkits-basement-core-lite-runtime"
}

// appModuleRouteValue derives a route-bound variable value.
func appModuleRouteValue(source string, route AppModuleRoute) (string, error) {
	switch source {
	case "https-root-url":
		if route.Host == "" || route.Protocol != "https" || !route.TLSRequired {
			return "", fmt.Errorf("an HTTPS root URL requires a declared HTTPS route")
		}
		host := route.Host
		if route.Port != 0 && route.Port != 443 {
			host = net.JoinHostPort(host, strconv.Itoa(route.Port))
		}
		return (&url.URL{Scheme: "https", Host: host, Path: strings.TrimRight(route.Path, "/") + "/"}).String(), nil
	case "route-host":
		if route.Host == "" {
			return "", fmt.Errorf("route has no host")
		}
		return route.Host, nil
	default:
		return "", fmt.Errorf("route value source %q is not governed", source)
	}
}

// appModuleRouteLabels renders the Traefik and StackKits route labels of the
// entry component. It covers the generic application route; the own
// application and multi-host companion routes stay with the standalone
// Compose owner until they migrate.
func appModuleRouteLabels(module AppModuleV3, route AppModuleRoute, routingNetwork string, originServed bool) (map[string]string, error) {
	endpoint := module.Route
	router := "stackkit-" + endpoint.ServiceRef
	rule := "PathPrefix(`" + route.Path + "`)"
	if route.Host != "" {
		rule = "Host(`" + route.Host + "`)"
		if route.Path != "" && route.Path != "/" {
			rule += " && PathPrefix(`" + route.Path + "`)"
		}
	}
	entrypoint := "web"
	if route.TLSRequired {
		entrypoint = "websecure"
	}
	labels := map[string]string{
		"traefik.enable":                                                  "true",
		"traefik.docker.network":                                          routingNetwork,
		"traefik.http.routers." + router + ".entrypoints":                 entrypoint,
		"traefik.http.routers." + router + ".rule":                        rule,
		"traefik.http.services." + router + ".loadbalancer.server.port":   strconv.Itoa(endpoint.TargetPort),
		"traefik.http.services." + router + ".loadbalancer.server.scheme": endpoint.UpstreamProtocol,
		"io.stackkit.route.id":                                            route.ID,
		"io.stackkit.route.core-module-ref":                               route.CoreModuleRef,
		"io.stackkit.route.exposure":                                      route.Exposure,
		"io.stackkit.route.protocol":                                      route.Protocol,
		"io.stackkit.route.upstream-protocol":                             endpoint.UpstreamProtocol,
		"io.stackkit.route.health-gate-ref":                               route.HealthGateRef,
		"io.stackkit.route.backend-pool-ref":                              route.BackendPoolRef,
		"io.stackkit.route.path":                                          route.Path,
		"io.stackkit.route.port":                                          strconv.Itoa(route.Port),
		"io.stackkit.route.target-port":                                   strconv.Itoa(endpoint.TargetPort),
		"io.stackkit.route.tls.required":                                  strconv.FormatBool(route.TLSRequired),
		"io.stackkit.route.tls.mode":                                      route.TLSMode,
		"io.stackkit.route.tls.min-version":                               route.TLSMinVersion,
		"io.stackkit.route.tls.profile-ref":                               route.TLSProfileRef,
		"io.stackkit.route.tls.issuer-ref":                                route.TLSIssuerRef,
		"io.stackkit.route.tls.owner-capability-ref":                      route.TLSOwnerCapabilityRef,
	}
	if route.TLSRequired {
		labels["traefik.http.routers."+router+".tls"] = "true"
		if route.TLSIssuerRef != "" && !originServed {
			labels["traefik.http.routers."+router+".tls.certresolver"] = "stackkits"
		}
	}
	if !HomeCore(route.CoreModuleRef) {
		labels["traefik.http.routers."+router+".middlewares"] = "stackkit-access-gate-remote@docker"
	} else {
		if endpoint.IngressAuth == "forward-auth" {
			labels["traefik.http.routers."+router+".middlewares"] = "stackkit-access-gate-lan@docker,stackkit-forward-auth@docker"
		}
		if route.Host != "" && (route.Exposure == "remote-private" || route.Exposure == "public") {
			remote := router + "-remote"
			labels[appModuleRemoteListener] = "true"
			labels["traefik.http.routers."+remote+".entrypoints"] = "remote"
			labels["traefik.http.routers."+remote+".rule"] = rule
			labels["traefik.http.routers."+remote+".service"] = router
		}
	}
	if len(endpoint.CompanionSubdomains) > 0 && route.Host != "" {
		prefix, rest, hasDot := strings.Cut(route.Host, ".")
		if hasDot && prefix != "" && rest != "" {
			for _, suffix := range endpoint.CompanionSubdomains {
				subHost := prefix + "-" + suffix + "." + rest
				subRouter := router + "-" + suffix
				subRule := "Host(`" + subHost + "`)"
				labels["traefik.http.routers."+subRouter+".entrypoints"] = entrypoint
				labels["traefik.http.routers."+subRouter+".rule"] = subRule
				labels["traefik.http.routers."+subRouter+".service"] = router
				if route.TLSRequired {
					labels["traefik.http.routers."+subRouter+".tls"] = "true"
					if route.TLSIssuerRef != "" && !originServed {
						labels["traefik.http.routers."+subRouter+".tls.certresolver"] = "stackkits"
					}
				}
				if !HomeCore(route.CoreModuleRef) {
					labels["traefik.http.routers."+subRouter+".middlewares"] = "stackkit-access-gate-remote@docker"
				} else if route.Exposure == "remote-private" || route.Exposure == "public" {
					subRemote := subRouter + "-remote"
					labels["traefik.http.routers."+subRemote+".entrypoints"] = "remote"
					labels["traefik.http.routers."+subRemote+".rule"] = subRule
					labels["traefik.http.routers."+subRemote+".service"] = router
				}
			}
		}
	}
	return labels, nil
}

func composeLiteral(value string) string { return strings.ReplaceAll(value, "$", "$$") }

func composeLiteralArguments(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = composeLiteral(value)
	}
	return result
}

func volumeOwnsPath(volumes []AppModuleV3Volume, path string) bool {
	for _, volume := range volumes {
		prefix := strings.TrimSuffix(volume.Target, "/")
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}
