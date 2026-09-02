package architecturev2renderer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	applicationDeliveryRouteInputRef    = "delivery-route"
	applicationDeliveryRouteSourceRef   = "network.moduleRoute"
	applicationDeliveryRouteValueType   = "authority-bound-module-route-v1"
	applicationDeliveryRouteCardinality = "single"
)

type applicationDeliveryTLS struct {
	Required           bool   `json:"required"`
	Mode               string `json:"mode"`
	MinVersion         string `json:"minVersion,omitempty"`
	ProfileRef         string `json:"profileRef,omitempty"`
	IssuerRef          string `json:"issuerRef,omitempty"`
	OwnerCapabilityRef string `json:"ownerCapabilityRef,omitempty"`
}

// applicationDeliveryRoute preserves the complete compiler-owned, secret-safe
// route projection in the workload artifact. Complex backend, health, access,
// and capability bodies remain opaque to the renderer but are checked for
// forbidden authority and remain covered by the artifact digest.
type applicationDeliveryRoute struct {
	ID                    string                 `json:"id"`
	ServiceRef            string                 `json:"serviceRef"`
	ModuleRef             string                 `json:"moduleRef"`
	Exposure              string                 `json:"exposure"`
	Protocol              string                 `json:"protocol"`
	UpstreamProtocol      string                 `json:"upstreamProtocol"`
	HealthGateRef         string                 `json:"healthGateRef"`
	BackendPoolRef        string                 `json:"backendPoolRef"`
	OriginSelector        string                 `json:"originSelector"`
	OriginSiteRefs        []string               `json:"originSiteRefs"`
	OriginSiteRef         string                 `json:"originSiteRef,omitempty"`
	OriginSelection       json.RawMessage        `json:"originSelection,omitempty"`
	Port                  int                    `json:"port"`
	TargetPort            int                    `json:"targetPort"`
	Host                  string                 `json:"host,omitempty"`
	Path                  string                 `json:"path,omitempty"`
	OriginNodeRefs        []string               `json:"originNodeRefs"`
	BackendPool           json.RawMessage        `json:"backendPool"`
	HealthProbe           json.RawMessage        `json:"healthProbe"`
	CapabilityAuthorities json.RawMessage        `json:"capabilityAuthorities"`
	Access                json.RawMessage        `json:"access"`
	TLS                   applicationDeliveryTLS `json:"tls"`
}

// ApplicationDeliveryRouteDescriptor is the non-secret route identity every
// Coolify, Komodo, or standalone adapter must observe after Apply.
type ApplicationDeliveryRouteDescriptor struct {
	ID                    string
	ServiceRef            string
	ModuleRef             string
	Exposure              string
	Protocol              string
	UpstreamProtocol      string
	HealthGateRef         string
	BackendPoolRef        string
	Host                  string
	Path                  string
	Port                  int
	TargetPort            int
	TLSRequired           bool
	TLSMode               string
	TLSMinVersion         string
	TLSProfileRef         string
	TLSIssuerRef          string
	TLSOwnerCapabilityRef string
}

// ApplicationDeliveryComponentDescriptor is the complete non-secret
// container contract consumed by the standalone Compose adapter.
type ApplicationDeliveryComponentDescriptor struct {
	ID                string
	Role              string
	Lifecycle         string
	ImageRef          string
	ImageDigest       string
	DependsOn         []string
	NetworkRefs       []string
	Command           []string
	Environment       map[string]string
	SecretEnvironment map[string]string
	Volumes           []ApplicationDeliveryVolumeDescriptor
	HealthKind        string
	HealthPath        string
	HealthPort        int
	HealthCommand     []string
	Resources         *ApplicationDeliveryResourcesDescriptor
}

// ApplicationDeliveryResourcesDescriptor is the declared per-container ceiling
// carried through to the adapter. Nil means the component declared none.
type ApplicationDeliveryResourcesDescriptor struct {
	MemoryLimit       string
	MemoryReservation string
	CPUs              float64
}

func resourcesDescriptor(limits *selectedPaaSRuntimeLimits) *ApplicationDeliveryResourcesDescriptor {
	if limits == nil {
		return nil
	}
	return &ApplicationDeliveryResourcesDescriptor{
		MemoryLimit: limits.MemoryLimit, MemoryReservation: limits.MemoryReservation, CPUs: limits.CPUs,
	}
}

type ApplicationDeliveryVolumeDescriptor struct {
	ID     string
	Target string
	Class  string
	Backup bool
}

// ApplicationDeliveryBundleDescriptor is the validated provider-neutral
// workload graph used by the StackKits-owned standalone adapter.
type ApplicationDeliveryBundleDescriptor struct {
	WorkloadRef    string
	ModuleRef      string
	Release        string
	EntryComponent string
	SiteRef        string
	NodeRef        string
	InstanceRef    string
	SecretRefs     map[string]string
	Components     []ApplicationDeliveryComponentDescriptor
	ConfigFiles    []ApplicationDeliveryConfigFileDescriptor
	Route          ApplicationDeliveryRouteDescriptor
}

type ApplicationDeliveryConfigFileDescriptor struct {
	Path string
	Body string
}

// StandaloneComposeConfigRelPath is the portable owner-workspace path used by
// the standalone Compose adapter for one container-visible configuration file.
// The naming rule belongs to the delivery contract so custody and execution
// cannot derive different paths for the same generated file.
func StandaloneComposeConfigRelPath(containerPath string) string {
	return "files/" + strings.ReplaceAll(strings.TrimPrefix(containerPath, "/"), "/", "_")
}

// ParseApplicationDeliveryWorkloadBundle validates the common v2 envelope and
// returns no secret material. Product executors perform their stricter
// product/version graph validation before invoking an operations owner.
func ParseApplicationDeliveryWorkloadBundle(data []byte) (ApplicationDeliveryBundleDescriptor, error) {
	path := "applicationDeliveryWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return ApplicationDeliveryBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" ||
		bundle.Workload.Delivery != "application-adapter" ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" ||
		bundle.Ownership.Credentials != "opaque-references-only" {
		return ApplicationDeliveryBundleDescriptor{}, fail(ErrInvalidPlan, path, "common workload delivery identity differs from v2")
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(
			*bundle.DeliveryRoute, bundle.Workload.ModuleRef, bundle.Workload.Ref,
			bundle.Route.TargetPort, path+".deliveryRoute",
		); err != nil {
			return ApplicationDeliveryBundleDescriptor{}, err
		}
	}
	secretRefs := make(map[string]string, len(bundle.SecretRefs))
	for slot, ref := range bundle.SecretRefs {
		if !validSecretReference(ref) {
			return ApplicationDeliveryBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "contains an invalid opaque secret reference")
		}
		secretRefs[slot] = ref
	}
	components := make([]ApplicationDeliveryComponentDescriptor, len(bundle.Components))
	seen := make(map[string]struct{}, len(bundle.Components))
	entryFound := false
	for index, component := range bundle.Components {
		componentPath := fmt.Sprintf("%s.components[%d]", path, index)
		seen[component.ID] = struct{}{}
		entryFound = entryFound || component.ID == bundle.Workload.EntryComponent
		for envName, slot := range component.SecretEnvironment {
			if _, exists := secretRefs[slot]; !exists || strings.TrimSpace(envName) == "" {
				return ApplicationDeliveryBundleDescriptor{}, fail(ErrInvalidPlan, componentPath+".secretEnvironment", "references an undeclared secret slot")
			}
		}
		volumes := make([]ApplicationDeliveryVolumeDescriptor, len(component.Volumes))
		for volumeIndex, volume := range component.Volumes {
			if !strings.HasPrefix(volume.Target, "/") {
				return ApplicationDeliveryBundleDescriptor{}, fail(ErrInvalidPlan, componentPath+".volumes", "volume target is invalid")
			}
			volumes[volumeIndex] = ApplicationDeliveryVolumeDescriptor{
				ID: volume.ID, Target: volume.Target, Class: volume.Class, Backup: volume.Backup,
			}
		}
		components[index] = ApplicationDeliveryComponentDescriptor{
			ID: component.ID, Role: component.Role, Lifecycle: component.Lifecycle,
			ImageRef: component.Image.Ref, ImageDigest: component.Image.Digest,
			DependsOn:         append([]string(nil), component.DependsOn...),
			NetworkRefs:       append([]string(nil), component.NetworkRefs...),
			Command:           append([]string(nil), component.Command...),
			Environment:       cloneStringMap(component.Environment),
			SecretEnvironment: cloneStringMap(component.SecretEnvironment),
			Volumes:           volumes, HealthKind: component.Health.Kind,
			HealthPath: component.Health.Path, HealthPort: component.Health.Port,
			HealthCommand: append([]string(nil), component.Health.Command...),
			Resources:     resourcesDescriptor(component.Resources),
		}
	}
	if !entryFound || len(components) == 0 {
		return ApplicationDeliveryBundleDescriptor{}, fail(ErrInvalidPlan, path+".components", "entry component is absent")
	}
	for index, component := range components {
		for _, dependency := range component.DependsOn {
			if _, exists := seen[dependency]; !exists {
				return ApplicationDeliveryBundleDescriptor{}, fail(ErrInvalidPlan, fmt.Sprintf("%s.components[%d].dependsOn", path, index), "dependency is outside the closed component graph")
			}
		}
	}
	sort.Slice(components, func(i, j int) bool { return components[i].ID < components[j].ID })
	configFiles, err := validateApplicationDeliveryConfigFiles(bundle.ConfigFiles, path+".configFiles")
	if err != nil {
		return ApplicationDeliveryBundleDescriptor{}, err
	}
	descriptor := ApplicationDeliveryBundleDescriptor{
		WorkloadRef: bundle.Workload.Ref, ModuleRef: bundle.Workload.ModuleRef,
		Release: bundle.Workload.Release, EntryComponent: bundle.Workload.EntryComponent,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		SecretRefs: secretRefs, Components: components, ConfigFiles: configFiles,
	}
	if bundle.DeliveryRoute != nil {
		descriptor.Route = bundle.DeliveryRoute.descriptor()
	}
	return descriptor, nil
}

func cloneStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func validateApplicationDeliveryConfigFiles(files []selectedPaaSConfigFile, path string) ([]ApplicationDeliveryConfigFileDescriptor, error) {
	out := make([]ApplicationDeliveryConfigFileDescriptor, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if !strings.HasPrefix(file.Path, "/") || strings.Contains(file.Path, "\\") || strings.Contains(file.Path, "..") || strings.TrimSpace(file.Body) == "" {
			return nil, fail(ErrInvalidPlan, path, "config file path or body is invalid")
		}
		if _, dup := seen[file.Path]; dup {
			return nil, fail(ErrInvalidPlan, path, "config file path is duplicated")
		}
		seen[file.Path] = struct{}{}
		out = append(out, ApplicationDeliveryConfigFileDescriptor{Path: file.Path, Body: file.Body})
	}
	return out, nil
}

func (route applicationDeliveryRoute) descriptor() ApplicationDeliveryRouteDescriptor {
	return ApplicationDeliveryRouteDescriptor{
		ID: route.ID, ServiceRef: route.ServiceRef, ModuleRef: route.ModuleRef,
		Exposure: route.Exposure, Protocol: route.Protocol, UpstreamProtocol: route.UpstreamProtocol,
		HealthGateRef: route.HealthGateRef, BackendPoolRef: route.BackendPoolRef,
		Host: route.Host, Path: route.Path, Port: route.Port, TargetPort: route.TargetPort,
		TLSRequired: route.TLS.Required, TLSMode: route.TLS.Mode, TLSMinVersion: route.TLS.MinVersion,
		TLSProfileRef: route.TLS.ProfileRef, TLSIssuerRef: route.TLS.IssuerRef,
		TLSOwnerCapabilityRef: route.TLS.OwnerCapabilityRef,
	}
}

func validateApplicationDeliveryRouteInput(
	unit RenderUnit,
	moduleRef, serviceRef string,
	targetPort int,
	path string,
) (*applicationDeliveryRoute, error) {
	if !exactStringList(unit.PublicInputRefs(), []string{applicationDeliveryRouteInputRef}) ||
		len(unit.PlanInputRefs()) != 0 || !emptyJSONObject(unit.PlanInputsJSON()) {
		return nil, fail(ErrInvalidPlan, path, "requires only the exact compiler-owned delivery route input")
	}
	var bindings []struct {
		TargetRef    string          `json:"targetRef"`
		SourceRef    string          `json:"sourceRef"`
		ValueType    string          `json:"valueType"`
		Cardinality  string          `json:"cardinality"`
		Required     bool            `json:"required"`
		DefaultValue json.RawMessage `json:"defaultValue"`
	}
	if err := decodeStrict(unit.InputBindingsJSON(), &bindings); err != nil || len(bindings) != 1 ||
		bindings[0].TargetRef != applicationDeliveryRouteInputRef ||
		bindings[0].SourceRef != applicationDeliveryRouteSourceRef ||
		bindings[0].ValueType != applicationDeliveryRouteValueType ||
		bindings[0].Cardinality != applicationDeliveryRouteCardinality || bindings[0].Required ||
		!bytes.Equal(bytes.TrimSpace(bindings[0].DefaultValue), []byte("null")) {
		return nil, fail(ErrInvalidPlan, path+".inputBindings", "delivery route binding identity differs from the compiler-owned contract")
	}
	var values struct {
		Route *applicationDeliveryRoute `json:"delivery-route"`
	}
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return nil, wrap(ErrInvalidPlan, path+".values", "decode exact delivery route", err)
	}
	if values.Route == nil {
		return nil, nil
	}
	route := values.Route
	if err := validateParsedApplicationDeliveryRoute(*route, moduleRef, serviceRef, targetPort, path+".values.delivery-route"); err != nil {
		return nil, err
	}
	return route, nil
}

func validateParsedApplicationDeliveryRoute(
	route applicationDeliveryRoute,
	moduleRef, serviceRef string,
	targetPort int,
	path string,
) error {
	if route.ModuleRef != moduleRef || route.ServiceRef != serviceRef || route.TargetPort != targetPort ||
		route.ID == "" || route.HealthGateRef == "" || route.BackendPoolRef == "" ||
		route.Port < 1 || route.Port > 65535 || route.TargetPort < 1 || route.TargetPort > 65535 ||
		!containsStringValue([]string{"local", "remote-private", "public"}, route.Exposure) ||
		!containsStringValue([]string{"http", "https", "tcp"}, route.Protocol) ||
		!containsStringValue([]string{"http", "https", "tcp"}, route.UpstreamProtocol) {
		return fail(ErrInvalidPlan, path, "route identity differs from the exact workload endpoint")
	}
	if route.Exposure != "local" && strings.TrimSpace(route.Host) == "" {
		return fail(ErrInvalidPlan, path+".host", "non-local delivery route requires an exact host")
	}
	if route.Protocol == "https" && (!route.TLS.Required || route.TLS.Mode == "disabled") {
		return fail(ErrInvalidPlan, path+".tls", "HTTPS route requires an enabled TLS contract")
	}
	for field, raw := range map[string]json.RawMessage{
		"backendPool": route.BackendPool, "healthProbe": route.HealthProbe,
		"capabilityAuthorities": route.CapabilityAuthorities, "access": route.Access,
	} {
		if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fail(ErrInvalidPlan, path+"."+field, "required route projection is missing")
		}
		if err := rejectForbiddenApplicationRouteAuthority(raw, path+"."+field); err != nil {
			return err
		}
	}
	if len(route.OriginSelection) > 0 {
		if err := rejectForbiddenApplicationRouteAuthority(route.OriginSelection, path+".originSelection"); err != nil {
			return err
		}
	}
	return nil
}

func rejectForbiddenApplicationRouteAuthority(raw []byte, path string) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return wrap(ErrInvalidPlan, path, "decode route projection", err)
	}
	forbidden := map[string]struct{}{
		"credential": {}, "credentials": {}, "credentialref": {}, "credentialrefs": {},
		"secret": {}, "secrets": {}, "secretref": {}, "secretrefs": {},
		"managementaddress": {}, "managementaddresses": {}, "socketpath": {},
		"daemonref": {}, "daemoninstanceref": {}, "providerendpoint": {},
		"providertoken": {}, "accountref": {}, "resourceid": {}, "leaseid": {},
	}
	var visit func(any) error
	visit = func(current any) error {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if _, blocked := forbidden[strings.ToLower(key)]; blocked {
					return fail(ErrInvalidPlan, path+"."+key, "route projection contains forbidden provider, credential, or host authority")
				}
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for index, child := range typed {
				if err := visit(child); err != nil {
					return fmt.Errorf("%s[%d]: %w", path, index, err)
				}
			}
		}
		return nil
	}
	return visit(value)
}
