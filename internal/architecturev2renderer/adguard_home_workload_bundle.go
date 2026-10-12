package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

const (
	adguardHomeWorkloadModuleID    = "stackkits-adguard-home-runtime"
	adguardHomeWorkloadUnitID      = "adguard-home"
	adguardHomeWorkloadTemplateRef = "builtin://workloads/adguard-home/bundle/v1.json"
	adguardHomeWorkloadVersion     = "1.0.0"
	adguardHomeWorkloadOutputRef   = "workloads/adguard-home/bundle.json"
)

func init() {
	governedCustodyNodeRights[adguardHomeWorkloadModuleID] = governedEntrypointSecretFiles(map[string][]selectedPaaSSecretFile{"adguard-home": nil})
}

const adguardHomeWorkloadRendererSchema = "stackkit.workload-bundle/v2|AdguardHomeWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|dns-policy:private-selected-clients-v1|dns-port:53535-opt-in|dhcp:off|querylog:off|owner:bcrypt-file|startup:sha256:b4994455865c0ccd2e9a7ecdf720713c4e2ba61132e156dc72e7c61003899427"

type AdguardHomeWorkloadBundleDescriptor struct {
	OwnerPasswordRef string
	WorkloadRef      string
	ModuleRef        string
	Release          string
	SiteRef          string
	NodeRef          string
	InstanceRef      string
	Components       []SelectedPaaSWorkloadComponentDescriptor
	Route            ApplicationDeliveryRouteDescriptor
}

func AdguardHomeWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(adguardHomeWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: adguardHomeWorkloadTemplateRef, Version: adguardHomeWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type adguardHomeWorkloadBundleRenderer struct{ contract RendererContract }

func newAdguardHomeWorkloadBundleRenderer() adguardHomeWorkloadBundleRenderer {
	return adguardHomeWorkloadBundleRenderer{contract: AdguardHomeWorkloadBundleRendererContract()}
}

func (r adguardHomeWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateAdguardHomeWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.adguardHome-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: adguardHomeWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseAdguardHomeWorkloadBundle(data []byte) (AdguardHomeWorkloadBundleDescriptor, error) {
	path := "adguardHomeWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return AdguardHomeWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed AdguardHome workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "AdguardHomeWorkloadBundle" ||
		bundle.Workload.Ref != "network" || bundle.Workload.AlternativeRef != "adguard-home" ||
		bundle.Workload.ModuleRef != adguardHomeWorkloadModuleID || bundle.Workload.Release != adguardHomeRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != adguardHomeWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return AdguardHomeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed AdguardHome "+adguardHomeRelease+" contract")
	}
	if len(bundle.ConfigFiles) != 0 {
		return AdguardHomeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "Network accepts no configuration overrides")
	}
	if len(bundle.SecretRefs) != 1 || !validSecretReference(bundle.SecretRefs["owner-password"]) {
		return AdguardHomeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "AdguardHome requires only the opaque owner password reference")
	}
	for index, component := range bundle.Components {
		if _, _, err := parseLANRights(component, adguardHomeWorkloadModuleID, fmt.Sprintf("%s.components[%d]", path, index)); err != nil {
			return AdguardHomeWorkloadBundleDescriptor{}, err
		}
	}
	components, err := validateAdguardHomeRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return AdguardHomeWorkloadBundleDescriptor{}, err
	}
	if err := validateAdguardHomeServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return AdguardHomeWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, adguardHomeWorkloadModuleID, "network", 8080, path+".deliveryRoute"); err != nil {
			return AdguardHomeWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := AdguardHomeWorkloadBundleDescriptor{
		OwnerPasswordRef: bundle.SecretRefs["owner-password"], WorkloadRef: "network", ModuleRef: adguardHomeWorkloadModuleID, Release: adguardHomeRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Components: make([]SelectedPaaSWorkloadComponentDescriptor, len(components)),
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

func validateAdguardHomeWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + adguardHomeWorkloadModuleID + ".renderUnits." + adguardHomeWorkloadUnitID
	if unit.ModuleID() != adguardHomeWorkloadModuleID || unit.ID() != adguardHomeWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", adguardHomeWorkloadModuleID, adguardHomeWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered AdguardHome workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != adguardHomeImageRef ||
		!hasDigest || imageDigest != adguardHomeImageDigest || !hasEntry || entry != adguardHomeWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact AdguardHome "+adguardHomeRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "AdguardHome requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, settings, err := validateApplicationDeliveryInputsWithSettings(unit, adguardHomeWorkloadModuleID, "network", 8080, []string{"lan-listener", "dns-policy"}, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || len(secretRefs) != 1 || !validSecretReference(secretRefs["owner-password"]) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "AdguardHome requires the opaque owner password reference")
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"owner-password"}) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "AdguardHome bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != adguardHomeWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", adguardHomeWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	for index := range components {
		if err := validateDeclaredLANRights(adguardHomeWorkloadModuleID, components[index], path+".runtime.components"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
		if err := materializeLANRights(&components[index], settings, path+".inputs"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
	}
	domain, ok := unit.NetworkDomainBase()
	if !ok {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "Network requires the existing resolved local zone")
	}
	policy, config, err := adguardHomePolicyConfiguration(settings["dns-policy"], domain, path)
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	for index := range components {
		components[index].Environment = map[string]string{"STACKKITS_DNS_POLICY": policy, "STACKKITS_DOMAIN": domain, "STACKKITS_DNS_CONFIG_JSON": config}
	}
	components, err = validateAdguardHomeRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact DNS filter endpoint")
	}
	if err := validateAdguardHomeServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "AdguardHomeWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "network", "adguard-home"
	bundle.Workload.ModuleRef, bundle.Workload.Release = adguardHomeWorkloadModuleID, adguardHomeRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateAdguardHomeRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 || components[0].ID != "adguard-home" || components[0].Lifecycle != "daemon" ||
		components[0].Image.Ref != adguardHomeImageRef || components[0].Image.Digest != adguardHomeImageDigest ||
		components[0].Health.Kind != "http" || components[0].Health.Path != "/login.html" || components[0].Health.Port != 8080 || !validAdguardHomeEnvironment(components[0].Environment) ||
		!sameEnvironment(components[0].SecretEnvironment, map[string]string{"STACKKITS_OWNER_HASH_FILE": "owner-password"}) || len(components[0].SecretFiles) != 0 || !exactStringList(components[0].Entrypoint, []string{"/bin/sh"}) || !adguardHomeCommandMatches(components[0].Command) {
		return nil, fail(ErrInvalidPlan, path, "AdguardHome runtime graph differs from the closed "+adguardHomeRelease+" contract")
	}
	want := map[string]selectedPaaSRuntimeVolume{
		"config": {ID: "config", Target: "/opt/adguardhome/conf", Class: "persistent", Backup: true},
		"work":   {ID: "work", Target: "/opt/adguardhome/work", Class: "persistent", Backup: false},
	}
	if len(components[0].Volumes) != len(want) {
		return nil, fail(ErrInvalidPlan, path+".volumes", "AdguardHome volume set differs from the closed contract")
	}
	for _, volume := range components[0].Volumes {
		expected, ok := want[volume.ID]
		if volume.ID == "library" && volume.HostPath != "" {
			if !safeCoreHostBootstrapStoragePath(volume.HostPath) {
				return nil, fail(ErrInvalidPlan, path+".volumes", "media source must be a clean path beneath a governed storage root")
			}
			expected.HostPath = volume.HostPath
		}
		if !ok || volume != expected {
			return nil, fail(ErrInvalidPlan, path+".volumes", "AdguardHome volume %q differs from the closed contract", volume.ID)
		}
	}
	return components, nil
}

func validateAdguardHomeServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "network" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 8080 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "adguard-home-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "network" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "DNS filter route authority differs from the governed AdguardHome endpoint")
	}
	return nil
}

func validAdguardHomeEnvironment(environment map[string]string) bool {
	if len(environment) != 3 {
		return false
	}
	policy, config, err := adguardHomePolicyConfiguration([]byte(environment["STACKKITS_DNS_POLICY"]), environment["STACKKITS_DOMAIN"], "adguardHome.environment")
	return err == nil && policy == environment["STACKKITS_DNS_POLICY"] && config == environment["STACKKITS_DNS_CONFIG_JSON"]
}

func adguardHomeCommandMatches(command []string) bool {
	var want []string
	return json.Unmarshal([]byte(adguardHomeCommandJSON), &want) == nil && exactStringList(command, want)
}

// The upstream configuration remains application-owned behavior. StackKits
// supplies the exact explicit baseline at each activation; DHCP and log custody
// are never inferred from the home network or an observed client.
type adguardHomeDNSPolicy struct {
	SelectedClients []string `json:"selectedClients"`
	LocalResolver   string   `json:"localResolver"`
	BlockedDomains  []string `json:"blockedDomains"`
}

var adguardHomeDomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

func adguardHomePolicyConfiguration(raw []byte, domain, path string) (string, string, error) {
	var p adguardHomeDNSPolicy
	if err := decodeStrict(raw, &p); err != nil {
		return "", "", wrap(ErrInvalidPlan, path, "decode explicit DNS policy", err)
	}
	if !adguardHomeDomainPattern.MatchString(domain) || len(p.SelectedClients) == 0 || len(p.SelectedClients) > 32 || len(p.BlockedDomains) == 0 || len(p.BlockedDomains) > 128 {
		return "", "", fail(ErrInvalidPlan, path, "requires a canonical local zone, explicit private clients and blocked domains")
	}
	host, port, err := net.SplitHostPort(p.LocalResolver)
	address, addrErr := netip.ParseAddr(host)
	if err != nil || addrErr != nil || !address.IsPrivate() || port != "53" {
		return "", "", fail(ErrInvalidPlan, path, "local resolver must be an existing private IP on port 53")
	}
	clients := []any{}
	seen := map[string]bool{}
	for index, client := range p.SelectedClients {
		prefix, err := netip.ParsePrefix(client)
		if err != nil {
			address, e := netip.ParseAddr(client)
			if e != nil {
				return "", "", fail(ErrInvalidPlan, path, "selected client must be an explicit private IP or prefix")
			}
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		if !prefix.Addr().IsPrivate() || prefix != prefix.Masked() || prefix.Bits() < 8 || prefix.Addr().Is6() && prefix.Bits() < 7 {
			return "", "", fail(ErrInvalidPlan, path, "selected client must stay within a canonical private address range")
		}
		// Checking the last address as well prevents a private starting address
		// from admitting a prefix that extends into publicly routable clients.
		allowed := false
		for _, network := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"} {
			outer := netip.MustParsePrefix(network)
			if outer.Contains(prefix.Addr()) && prefix.Bits() >= outer.Bits() {
				allowed = true
			}
		}
		if !allowed || seen[prefix.String()] {
			return "", "", fail(ErrInvalidPlan, path, "client ranges must be distinct and entirely private")
		}
		seen[prefix.String()] = true
		p.SelectedClients[index] = prefix.String()
		clients = append(clients, map[string]any{"name": "selected-" + strconv.Itoa(index+1), "ids": []string{prefix.String()}, "use_global_settings": false, "filtering_enabled": true, "safebrowsing_enabled": false, "parental_enabled": false, "use_global_blocked_services": true})
	}
	rules := []string{}
	for _, name := range p.BlockedDomains {
		if len(name) > 253 || !adguardHomeDomainPattern.MatchString(name) || name == domain || strings.HasSuffix(name, "."+domain) {
			return "", "", fail(ErrInvalidPlan, path, "blocked names must be canonical domains outside the owned local zone")
		}
		rules = append(rules, "||"+name+"^")
	}
	configuration := map[string]any{
		"schema_version": 34,
		"http":           map[string]any{"address": "0.0.0.0:8080"},
		"users":          []any{map[string]any{"name": "owner", "password": "@@OWNER_BCRYPT@@"}},
		"dns":            map[string]any{"bind_hosts": []string{"0.0.0.0"}, "port": 53535, "upstream_dns": []string{p.LocalResolver}, "bootstrap_dns": []string{"9.9.9.9"}, "allowed_clients": []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7", "127.0.0.0/8", "::1/128"}, "enable_dnssec": true, "ratelimit": 20, "cache_enabled": false, "cache_size": 0, "use_private_ptr_resolvers": false, "resolve_clients": false},
		"filtering":      map[string]any{"protection_enabled": true, "filtering_enabled": false, "blocking_mode": "nxdomain"},
		"user_rules":     rules, "filters": []any{},
		"clients":    map[string]any{"persistent": clients, "runtime_sources": map[string]bool{"whois": false, "arp": false, "rdns": false, "dhcp": false, "hosts": false}},
		"querylog":   map[string]any{"enabled": false, "file_enabled": false},
		"statistics": map[string]any{"enabled": false},
		"dhcp":       map[string]any{"enabled": false},
	}
	canonical, _ := json.Marshal(p)
	body, _ := json.Marshal(configuration)
	return string(canonical), string(body), nil
}
