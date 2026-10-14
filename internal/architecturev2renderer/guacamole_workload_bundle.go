package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"net"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	guacamoleWorkloadModuleID    = "stackkits-guacamole-runtime"
	guacamoleWorkloadUnitID      = "guacamole"
	guacamoleWorkloadTemplateRef = "builtin://workloads/guacamole/bundle/v2.json"
	guacamoleWorkloadVersion     = "2.0.0"
	guacamoleWorkloadAPIVersion  = "stackkit.workload-bundle/v3"
	guacamoleWorkloadOutputRef   = "workloads/guacamole/bundle.json"
)

// Schema identity is independent of the upstream release. Exact image/release
// validation below uses the generated catalog projection, so an image update
// has one source and does not require a second manual render-contract update.
//
//go:embed assets/guacamole/start.sh
var guacamoleStartupScript string

var guacamoleOwnerPasswordFile = selectedPaaSSecretFile{Slot: "owner-password", Target: "/run/secrets/owner-password", PathEnvironment: "STACKKITS_OWNER_PASSWORD_FILE", UID: 1001, GID: 1001}

func init() {
	governedCustodyNodeRights[guacamoleWorkloadModuleID] = governedCommandSecretFile("guacamole", guacamoleOwnerPasswordFile)
}

func guacamoleWorkloadRendererSchema() string {
	asset := sha256.Sum256([]byte(guacamoleStartupScript))
	return `stackkit.workload-bundle/v3|GuacamoleWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:guacd,guacamole|compute-profile:module-compute-profile-v1|allocation:equal-resident-share-v1|connection:remote-connection-v1|xml:tmpfs-private-sha256|governed-startup:sha256:` + hex.EncodeToString(asset[:]) + `|secret-material:not-included`
}

// GuacamoleWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the selected-PaaS executor. OwnerPasswordRef is opaque.
type GuacamoleWorkloadBundleDescriptor struct {
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

func GuacamoleWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(guacamoleWorkloadRendererSchema()))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: guacamoleWorkloadTemplateRef, Version: guacamoleWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type guacamoleWorkloadBundleRenderer struct{ contract RendererContract }

func newGuacamoleWorkloadBundleRenderer() guacamoleWorkloadBundleRenderer {
	return guacamoleWorkloadBundleRenderer{contract: GuacamoleWorkloadBundleRendererContract()}
}

func (r guacamoleWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateGuacamoleWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.guacamole-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: guacamoleWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseGuacamoleWorkloadBundle validates the closed generated artifact before
// any selected-PaaS owner may consume it.
func ParseGuacamoleWorkloadBundle(data []byte) (GuacamoleWorkloadBundleDescriptor, error) {
	path := "guacamoleWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return GuacamoleWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Guacamole workload bundle", err)
	}
	legacy := bundle.APIVersion == "stackkit.workload-bundle/v2"
	if (!legacy && bundle.APIVersion != guacamoleWorkloadAPIVersion) || bundle.Kind != "GuacamoleWorkloadBundle" ||
		bundle.Workload.Ref != "remote" || bundle.Workload.AlternativeRef != "guacamole" ||
		bundle.Workload.ModuleRef != guacamoleWorkloadModuleID || bundle.Workload.Release != guacamoleRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != "guacamole" ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return GuacamoleWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Guacamole image contract")
	}
	if !validGuacamoleSecretRefs(bundle.SecretRefs) {
		return GuacamoleWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires opaque owner-password references")
	}
	if len(bundle.ConfigFiles) != 0 {
		return GuacamoleWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "Guacamole runtime accepts no startup configuration overrides")
	}
	if legacy && len(bundle.ComputeProfile) != 0 {
		return GuacamoleWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".computeProfile", "retained v2 Remote artifacts cannot carry a selected v3 profile")
	}
	if !legacy && len(bundle.ComputeProfile) == 0 {
		return GuacamoleWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".computeProfile", "Remote allocation requires its selected compiler-bound profile")
	}
	components, err := validateGuacamoleRuntimeComponents(bundle.Components, bundle.ComputeProfile, path+".components")
	if err != nil {
		return GuacamoleWorkloadBundleDescriptor{}, err
	}
	if err := validateGuacamoleServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return GuacamoleWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, guacamoleWorkloadModuleID, "remote", 8080, path+".deliveryRoute"); err != nil {
			return GuacamoleWorkloadBundleDescriptor{}, err
		}
	}
	if _, err := guacamoleConnectionFromXML(components[1].Environment["STACKKITS_REMOTE_CONNECTION_XML"]); err != nil {
		return GuacamoleWorkloadBundleDescriptor{}, err
	}

	descriptor := GuacamoleWorkloadBundleDescriptor{
		WorkloadRef: "remote", ModuleRef: guacamoleWorkloadModuleID, Release: guacamoleRelease,
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

//nolint:gocyclo // Keep the complete Guacamole authority check at one boundary.
func validateGuacamoleWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + guacamoleWorkloadModuleID + ".renderUnits." + guacamoleWorkloadUnitID
	if unit.ModuleID() != guacamoleWorkloadModuleID || unit.ID() != guacamoleWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", guacamoleWorkloadModuleID, guacamoleWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Guacamole workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != guacamoleImageRef ||
		!hasDigest || imageDigest != guacamoleImageDigest || !hasEntry || entry != "guacamole" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Guacamole image contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Guacamole requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	routeUnit, computeProfile, err := consumeModuleComputeProfile(unit, path+".inputs.compute-profile")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	deliveryRoute, connectionXML, err := validateGuacamoleDeliveryInputs(routeUnit, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"owner-password"}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Guacamole requires owner-password slots")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil ||
		!validGuacamoleSecretRefs(secretRefs) {
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
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != guacamoleWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", guacamoleWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	limits, err := guacamoleSelectedComponentLimits(computeProfile)
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	for i := range components {
		components[i].Resources = limits
	}
	components, err = validateGuacamoleRuntimeComponents(components, computeProfile, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact Guacamole endpoint")
	}
	if err := validateGuacamoleServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if _, provided := components[1].Environment["STACKKITS_REMOTE_CONNECTION_XML"]; provided {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "connection XML comes only from the validated public binding")
	}
	components[1].Environment["STACKKITS_REMOTE_CONNECTION_XML"] = connectionXML

	bundle := selectedPaaSWorkloadBundle{
		APIVersion: guacamoleWorkloadAPIVersion, Kind: "GuacamoleWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute, ComputeProfile: computeProfile,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "remote", "guacamole"
	bundle.Workload.ModuleRef, bundle.Workload.Release = guacamoleWorkloadModuleID, guacamoleRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateGuacamoleRuntimeComponents(components []selectedPaaSRuntimeComponent, profile json.RawMessage, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 2 {
		return nil, fail(ErrInvalidPlan, path, "requires the exact Guacamole and internal guacd pair")
	}
	// The compiler canonically sorts components by ID. Normalize by identity
	// before applying the dependency-ordered runtime contract.
	byID := make(map[string]selectedPaaSRuntimeComponent, len(components))
	for _, component := range components {
		if _, duplicate := byID[component.ID]; duplicate {
			return nil, fail(ErrInvalidPlan, path, "duplicate Guacamole component identity")
		}
		byID[component.ID] = component
	}
	guacd, hasGuacd := byID["guacd"]
	guacamole, hasGuacamole := byID["guacamole"]
	if !hasGuacd || !hasGuacamole {
		return nil, fail(ErrInvalidPlan, path, "requires the exact Guacamole and internal guacd pair")
	}
	components = []selectedPaaSRuntimeComponent{guacd, guacamole}
	environment := map[string]string{"GUACD_HOSTNAME": "guacd", "GUACD_PORT": "4822", "WEBAPP_CONTEXT": "ROOT", "GUACAMOLE_HOME": "/dev/shm/stackkits-guacamole"}
	if connectionXML, exists := components[1].Environment["STACKKITS_REMOTE_CONNECTION_XML"]; exists {
		if _, err := guacamoleConnectionFromXML(connectionXML); err != nil {
			return nil, err
		}
		environment["STACKKITS_REMOTE_CONNECTION_XML"] = connectionXML
	}
	expected := []selectedPaaSRuntimeComponent{
		{ID: "guacd", Role: "application", Lifecycle: "daemon", Image: selectedPaaSRuntimeImage{Ref: guacdImageRef, Digest: guacdImageDigest}, DependsOn: []string{}, NetworkRefs: []string{"guacamole-internal"}, Egress: true, Health: selectedPaaSRuntimeHealth{Kind: "command", Command: []string{"nc", "-z", "127.0.0.1", "4822"}}},
		{ID: "guacamole", Role: "application", Lifecycle: "daemon", Image: selectedPaaSRuntimeImage{Ref: guacamoleImageRef, Digest: guacamoleImageDigest}, DependsOn: []string{"guacd"}, NetworkRefs: []string{"guacamole-internal"}, Environment: environment, Command: []string{"/bin/sh", "-ec", guacamoleStartupScript}, SecretFiles: []selectedPaaSSecretFile{guacamoleOwnerPasswordFile}, Health: selectedPaaSRuntimeHealth{Kind: "http", Port: 8080, Path: "/"}},
	}
	limits, err := guacamoleSelectedComponentLimits(profile)
	if err != nil {
		return nil, wrap(ErrInvalidPlan, path+".resources", "validate the selected Remote allocation", err)
	}
	for i := range expected {
		expected[i].Resources = limits
	}
	actualJSON, _ := json.Marshal(components)
	expectedJSON, _ := json.Marshal(expected)
	if !bytes.Equal(actualJSON, expectedJSON) {
		return nil, fail(ErrInvalidPlan, path, "Guacamole image, graph, custody, command or route boundary differs from its authority")
	}
	return components, nil
}

func validateGuacamoleServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "remote" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 8080 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "guacamole-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "remote" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "Guacamole route authority differs from the governed Guacamole endpoint")
	}
	return nil
}

func validGuacamoleSecretRefs(refs map[string]string) bool {
	return len(refs) == 1 && validSecretReference(refs["owner-password"])
}

// Destination credentials are deliberately absent: Guacamole prompts the owner.
type guacamoleConnection struct {
	Protocol string `json:"protocol"`
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
	HostKey  string `json:"host-key,omitempty"`
}

func validateGuacamoleConnection(c guacamoleConnection) error {
	path := "remote-connection"
	if c.Protocol != "ssh" && c.Protocol != "rdp" && c.Protocol != "vnc" {
		return fail(ErrInvalidPlan, path, "requires explicit SSH, RDP or VNC protocol")
	}
	if len(c.Hostname) > 253 || c.Hostname == "" || (net.ParseIP(c.Hostname) == nil && !regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?$`).MatchString(c.Hostname)) || c.Port < 1 || c.Port > 65535 {
		return fail(ErrInvalidPlan, path, "requires an explicit destination hostname and port")
	}
	if c.Protocol != "ssh" {
		if c.HostKey != "" {
			return fail(ErrInvalidPlan, path, "SSH host-key applies only to SSH destinations")
		}
		return nil
	}
	parts := strings.Fields(c.HostKey)
	host := c.Hostname
	if c.Port != 22 || strings.Contains(host, ":") {
		host = "[" + host + "]:" + strconv.Itoa(c.Port)
	}
	if len(parts) != 3 || len(c.HostKey) > 16384 || strings.ContainsAny(c.HostKey, "\r\n") || parts[0] != host {
		return fail(ErrInvalidPlan, path, "SSH requires one exact same-host OpenSSH known_hosts entry")
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(parts[1] + " " + parts[2]))
	if err != nil || len(options) != 0 || len(rest) != 0 || key.Type() != parts[1] {
		return fail(ErrInvalidPlan, path, "SSH host-key is not an exact OpenSSH public key")
	}
	return nil
}

type guacamoleXMLParameter struct {
	Name  string `xml:"name,attr"`
	Value string `xml:",chardata"`
}
type guacamoleXMLConnection struct {
	XMLName    xml.Name                `xml:"connection"`
	Name       string                  `xml:"name,attr"`
	Protocol   string                  `xml:"protocol"`
	Parameters []guacamoleXMLParameter `xml:"param"`
}

func guacamoleConnectionXML(c guacamoleConnection) (string, error) {
	if err := validateGuacamoleConnection(c); err != nil {
		return "", err
	}
	connection := guacamoleXMLConnection{Name: "Remote", Protocol: c.Protocol, Parameters: []guacamoleXMLParameter{{"hostname", c.Hostname}, {"port", strconv.Itoa(c.Port)}}}
	if c.Protocol == "ssh" {
		connection.Parameters = append(connection.Parameters, guacamoleXMLParameter{"host-key", c.HostKey})
	}
	data, err := xml.Marshal(connection)
	if err != nil {
		return "", fail(ErrInvalidPlan, "remote-connection", "cannot encode public destination")
	}
	return string(data), nil
}

func guacamoleConnectionFromXML(raw string) (guacamoleConnection, error) {
	c := guacamoleConnection{}
	var decoded guacamoleXMLConnection
	if raw == "" || len(raw) > 32768 || xml.Unmarshal([]byte(raw), &decoded) != nil {
		return c, fail(ErrInvalidPlan, "remote-connection", "public connection XML is invalid")
	}
	c.Protocol = decoded.Protocol
	for _, p := range decoded.Parameters {
		switch p.Name {
		case "hostname":
			c.Hostname = p.Value
		case "port":
			c.Port, _ = strconv.Atoi(p.Value)
		case "host-key":
			c.HostKey = p.Value
		default:
			return c, fail(ErrInvalidPlan, "remote-connection", "destination credentials and overrides are not admitted")
		}
	}
	canonical, err := guacamoleConnectionXML(c)
	if err != nil {
		return c, err
	}
	if canonical != raw {
		return c, fail(ErrInvalidPlan, "remote-connection", "XML differs from the canonical public connection")
	}
	return c, nil
}

func validateGuacamoleDeliveryInputs(unit RenderUnit, path string) (*applicationDeliveryRoute, string, error) {
	if !exactStringList(unit.PublicInputRefs(), []string{"delivery-route", "remote-connection"}) || len(unit.PlanInputRefs()) != 0 || !emptyJSONObject(unit.PlanInputsJSON()) {
		return nil, "", fail(ErrInvalidPlan, path, "requires only the exact delivery route and public remote destination")
	}
	var bindings []rawModuleRenderInputBinding
	expected := []rawModuleRenderInputBinding{
		{TargetRef: "delivery-route", SourceRef: "network.moduleRoute", ValueType: "authority-bound-module-route-v1", Cardinality: "single", DefaultValue: json.RawMessage("null")},
	}
	if decodeStrict(unit.InputBindingsJSON(), &bindings) != nil || !reflect.DeepEqual(bindings, expected) {
		return nil, "", fail(ErrInvalidPlan, path, "remote binding identity differs from the compiler-owned contract")
	}
	var values struct {
		Route      *applicationDeliveryRoute `json:"delivery-route"`
		Connection guacamoleConnection       `json:"remote-connection"`
	}
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return nil, "", wrap(ErrInvalidPlan, path, "decode closed remote inputs", err)
	}
	connectionXML, err := guacamoleConnectionXML(values.Connection)
	if err != nil {
		return nil, "", err
	}
	if values.Route != nil {
		if err := validateParsedApplicationDeliveryRoute(*values.Route, guacamoleWorkloadModuleID, "remote", 8080, path+".delivery-route"); err != nil {
			return nil, "", err
		}
	}
	return values.Route, connectionXML, nil
}
