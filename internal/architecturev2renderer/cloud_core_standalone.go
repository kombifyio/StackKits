package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"gopkg.in/yaml.v3"
)

const (
	cloudStandaloneCoreModuleID         = "stackkits-cloud-core-standalone-runtime"
	cloudStandaloneCoreComposeTemplate  = "builtin://cloud/core-standalone/compose/v1.yaml"
	cloudStandaloneCoreComposeOutputRef = "platform/cloud-core-standalone/compose.yaml"
	cloudStandaloneCoreVersion          = "1.0.0"
	cloudStandaloneCoreComposeSchema    = `stackkit.cloud-core-standalone-compose/v1|artifact-revision:3|resolved-network-domain:required|resolved-subdomain-prefix:optional|runtime-listeners:catalog-bound,direct-loopback-only|services:router,socket-proxy,pocketid,tinyauth,hub,kopia-agent|networks:cloud-core-host-reachable,cloud-control-internal,cloud-backup-outbound-no-peer|kopia:owner-local-source-policy|public-routes:declared-default-closed|credentials:service-scoped-owner-signed-cloud-runtime-custody|external-backup:required-before-apply|public-tls:separate-owner-traefik-acme-http-01|service-lifecycle:stackkits-local|server-provider-lifecycle:not-owned|mem-limit:catalog-resources`
)

type cloudCoreEndpointProfile struct {
	port        int
	healthRef   string
	privilege   string
	ingressAuth string
}

// cloudCoreRenderProfile contains the invariant portions shared by the
// existing PaaS-bearing Cloud renderer and the standalone Cloud renderer.
// The renderer contract, module identity, graph, route set, and Compose
// function remain profile-owned so an explicit existing plan cannot silently
// switch graphs.
type cloudCoreRenderProfile struct {
	displayName       string
	moduleID          string
	unitID            string
	outputRef         string
	runtimeEngine     string
	imageRef          string
	imageDigest       string
	entryComponent    string
	componentsJSON    string
	serviceEndpoints  map[string]cloudCoreEndpointProfile
	allowedExposures  []string
	componentValidate func([]byte, string) error
	renderCompose     func(string, string) []byte
}

func cloudCoreRenderProfileForCloudCore() cloudCoreRenderProfile {
	return cloudCoreRenderProfile{
		displayName:      "Cloud core",
		moduleID:         cloudCoreModuleID,
		unitID:           cloudCoreComposeUnitID,
		outputRef:        cloudCoreComposeOutputRef,
		runtimeEngine:    "docker",
		imageRef:         "ghcr.io/coollabsio/coolify:4.1.2",
		imageDigest:      "sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f",
		entryComponent:   "coolify",
		componentsJSON:   cloudCoreComponentsJSON,
		allowedExposures: []string{"public", "remote-private"},
		serviceEndpoints: map[string]cloudCoreEndpointProfile{
			"base":    {port: 80, healthRef: "cloud-hub-http", privilege: "user", ingressAuth: "native"},
			"id":      {port: 1411, healthRef: "cloud-pocketid-http", privilege: "user", ingressAuth: "native"},
			"auth":    {port: 3000, healthRef: "cloud-tinyauth-http", privilege: "user", ingressAuth: "native"},
			"coolify": {port: 8080, healthRef: "cloud-coolify-http", privilege: "user", ingressAuth: "native"},
		},
		componentValidate: validateCloudCoreComponents,
		renderCompose: func(domain, prefix string) []byte {
			return RenderCloudCoreComposeForAddress(domain, prefix)
		},
	}
}

func cloudStandaloneCoreRenderProfile() cloudCoreRenderProfile {
	return cloudCoreRenderProfile{
		displayName:      "Cloud standalone core",
		moduleID:         cloudStandaloneCoreModuleID,
		unitID:           cloudCoreComposeUnitID,
		outputRef:        cloudStandaloneCoreComposeOutputRef,
		runtimeEngine:    "docker",
		imageRef:         "docker.io/library/nginx:alpine",
		imageDigest:      "sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752",
		entryComponent:   "hub",
		componentsJSON:   cloudStandaloneCoreComponentsJSON,
		allowedExposures: []string{"public", "remote-private"},
		serviceEndpoints: map[string]cloudCoreEndpointProfile{
			"base": {port: 80, healthRef: "cloud-hub-http", privilege: "user", ingressAuth: "native"},
			"id":   {port: 1411, healthRef: "cloud-pocketid-http", privilege: "user", ingressAuth: "native"},
			"auth": {port: 3000, healthRef: "cloud-tinyauth-http", privilege: "user", ingressAuth: "native"},
		},
		componentValidate: func(data []byte, path string) error {
			return validateClosedLocalCoreComponents(data, cloudStandaloneCoreComponentsJSON, path, "Cloud standalone core")
		},
		renderCompose: func(domain, prefix string) []byte {
			return RenderCloudStandaloneCoreComposeForAddress(domain, prefix)
		},
	}
}

func renderCloudCoreUnit(ctx context.Context, unit RenderUnit, contract RendererContract, profile cloudCoreRenderProfile) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := "resolvedPlan.modules." + profile.moduleID + ".renderUnits." + profile.unitID
	domain, hasDomain := unit.NetworkDomainBase()
	if !hasDomain || !basementDomainPattern.MatchString(domain) || unit.ModuleID() != profile.moduleID || unit.ID() != profile.unitID ||
		unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return nil, fail(ErrInvalidPlan, path, "renderer accepts only the exact %s Compose contract", profile.displayName)
	}
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "stackkit" {
		return nil, fail(ErrInvalidPlan, path+".runtime", "%s requires exact container/stackkit delivery", profile.displayName)
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if !hasEngine || engine != profile.runtimeEngine || !hasImage || imageRef != profile.imageRef || !hasDigest ||
		imageDigest != profile.imageDigest || !hasEntry || entry != profile.entryComponent {
		return nil, fail(ErrInvalidPlan, path+".runtime", "runtime identity differs from the governed %s graph", profile.displayName)
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || unit.InstanceID() != profile.unitID+"-node-"+nodeRef ||
		!containsExact(unit.LogicalSiteRefs(), siteRef) || !containsExact(unit.LogicalNodeRefs(), nodeRef) {
		return nil, fail(ErrInvalidPlan, path+".instances", "%s requires one exact node-local target", profile.displayName)
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || len(unit.PlanInputRefs()) != 0 ||
		!emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) || !emptyJSONObject(unit.PlanInputsJSON()) || !emptyJSONArray(unit.InputBindingsJSON()) {
		return nil, fail(ErrInvalidPlan, path+".inputs", "%s consumes no caller, provider, or secret material", profile.displayName)
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) ||
		!exactStringList(unit.DeclaredOutputs(), []string{profile.outputRef}) {
		return nil, fail(ErrInvalidPlan, path, "%s receives no provider or privileged host authority and emits one governed artifact", profile.displayName)
	}
	if err := profile.componentValidate(unit.RuntimeComponentsJSON(), path+".runtime.components"); err != nil {
		return nil, err
	}
	var endpoints []rawModuleServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != len(profile.serviceEndpoints) {
		return nil, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires the exact %s service endpoints", profile.displayName)
	}
	expected := make(map[string]cloudCoreEndpointProfile, len(profile.serviceEndpoints))
	for ref, endpoint := range profile.serviceEndpoints {
		expected[ref] = endpoint
	}
	for _, endpoint := range endpoints {
		ingressAuth := endpoint.IngressAuth
		if ingressAuth == "" {
			ingressAuth = "native"
		}
		want, ok := expected[endpoint.ServiceRef]
		if !ok || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != want.port || endpoint.RequiredPrivilege != want.privilege ||
			ingressAuth != want.ingressAuth || endpoint.OriginSelector != "control-authority-site" || endpoint.HealthRef != want.healthRef ||
			!exactStringList(endpoint.AllowedIngressProtocols, []string{"http", "https"}) ||
			!exactStringList(endpoint.AllowedExposures, profile.allowedExposures) {
			return nil, fail(ErrInvalidPlan, path+".serviceEndpoints", "%s service route differs from the closed default-deny contract", profile.displayName)
		}
		delete(expected, endpoint.ServiceRef)
	}
	if len(expected) != 0 {
		return nil, fail(ErrInvalidPlan, path+".serviceEndpoints", "%s service endpoint set is incomplete", profile.displayName)
	}
	prefix, _ := unit.NetworkSubdomainPrefix()
	output := renderSiteListenerBindings(unit, profile.renderCompose(domain, prefix))
	if err := validateRuntimeListenerComposeParity(unit.RuntimeListenersJSON(), output, path+".runtimeListeners"); err != nil {
		return nil, err
	}
	return []UnitOutput{{Ref: profile.outputRef, Bytes: output}}, nil
}

// CloudStandaloneCoreComposeRendererContract returns the distinct renderer
// identity for the no-PaaS Cloud core. The existing Cloud contract remains
// unchanged for explicit PaaS-bearing selections.
func CloudStandaloneCoreComposeRendererContract() RendererContract {
	sum := sha256Bytes([]byte(cloudStandaloneCoreComposeSchema))
	return RendererContract{
		Kind: "compose", RendererRef: cloudCoreRendererRef, TemplateRef: cloudStandaloneCoreComposeTemplate,
		Version: cloudStandaloneCoreVersion, ContractHash: "sha256:" + sum,
	}
}

// CloudStandaloneCoreServiceContracts returns the pinned services in
// stable ID order. The hub keeps the existing nginx image and digest as the
// standalone runtime entry component.
func CloudStandaloneCoreServiceContracts() []BasementCoreServiceContract {
	var components []struct {
		ID     string                       `json:"id"`
		Image  struct{ Ref, Digest string } `json:"image"`
		Health struct {
			Kind string `json:"kind"`
		} `json:"health"`
	}
	if err := json.Unmarshal([]byte(cloudStandaloneCoreComponentsJSON), &components); err != nil {
		panic("invalid built-in Cloud standalone core component contract: " + err.Error())
	}
	result := make([]BasementCoreServiceContract, len(components))
	for index, component := range components {
		result[index] = BasementCoreServiceContract{Ref: component.ID, ImageRef: component.Image.Ref, ImageDigest: component.Image.Digest, HealthRequired: component.Health.Kind != "image"}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ref < result[j].Ref })
	return result
}

// RenderCloudStandaloneCoreComposeForDomain renders the standalone Cloud
// graph for a resolved domain without selecting or embedding a PaaS adapter.
func RenderCloudStandaloneCoreComposeForDomain(domain string) []byte {
	return RenderCloudStandaloneCoreComposeForAddress(domain, "")
}

// RenderCloudStandaloneCoreComposeForAddress renders the standalone graph with
// the same prefix substitution rules as the existing Cloud renderer.
func RenderCloudStandaloneCoreComposeForAddress(domain, prefix string) []byte {
	if !basementDomainPattern.MatchString(domain) {
		return nil
	}
	return renderCloudComposeForAddress(cloudStandaloneCoreCompose, domain, prefix, []string{"id", "auth", "base"})
}

// ExpectedCloudStandaloneCoreComposeArtifact returns the immutable default
// domain artifact used by renderer and executor contract tests.
func ExpectedCloudStandaloneCoreComposeArtifact() []byte {
	return RenderCloudStandaloneCoreComposeForDomain("home.test")
}

// ValidateCloudStandaloneCoreComposeArtifact validates both the standalone
// graph and its resolved domain/prefix substitution.
func ValidateCloudStandaloneCoreComposeArtifact(content []byte) bool {
	return validateCloudComposeArtifact(content, RenderCloudStandaloneCoreComposeForAddress)
}

func newCloudStandaloneCoreComposeRenderer() cloudCoreRenderer {
	profile := cloudStandaloneCoreRenderProfile()
	return cloudCoreRenderer{contract: CloudStandaloneCoreComposeRendererContract(), profile: profile}
}

func renderCloudComposeForAddress(compose, domain, prefix string, serviceRefs []string) []byte {
	serviceDomain := domain
	if prefix != "" {
		serviceDomain = prefix + "-{{STACKKIT_SERVICE}}." + domain
	}
	output := strings.ReplaceAll(compose, "{{STACKKIT_DOMAIN}}", serviceDomain)
	for _, service := range serviceRefs {
		output = strings.ReplaceAll(output, service+"."+prefix+"-{{STACKKIT_SERVICE}}", prefix+"-"+service)
	}
	return []byte(strings.ReplaceAll(output, "{{STACKKIT_SERVICE}}", ""))
}

var cloudStandaloneCoreCompose = buildCloudStandaloneCoreCompose()

var cloudStandaloneCoreComponentsJSON = withCloudKopiaComponent(filterCloudCoreComponentsJSON(cloudCoreComponentsJSON, map[string]struct{}{
	"coolify":          {},
	"coolify-postgres": {},
	"coolify-redis":    {},
	"coolify-realtime": {},
}))

func withCloudKopiaComponent(source string) string {
	var components, basement []map[string]any
	if err := json.Unmarshal([]byte(source), &components); err != nil {
		panic(err)
	}
	if err := json.Unmarshal([]byte(basementCoreComponentsJSON), &basement); err != nil {
		panic(err)
	}
	for _, component := range basement {
		if component["id"] == "kopia-agent" {
			component["networkRefs"] = []string{"cloud-backup"}
			components = append(components, component)
		}
	}
	encoded, err := json.Marshal(components)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func withCloudKopiaCompose(compose string) string {
	var cloud, basement map[string]any
	if err := yaml.Unmarshal([]byte(compose), &cloud); err != nil {
		panic(err)
	}
	if err := yaml.Unmarshal([]byte(basementCoreCompose), &basement); err != nil {
		panic(err)
	}
	source, err := localbackuppolicy.GovernedSourceForCoreModule(localbackuppolicy.CloudCoreModuleRef)
	if err != nil {
		panic(err)
	}
	kopia := basement["services"].(map[string]any)["kopia-agent"].(map[string]any)
	kopia["hostname"] = source.RuntimeProfile().Hostname
	kopia["networks"] = []string{source.RuntimeProfile().NetworkRef}
	volumes := []string{}
	for _, name := range source.ManagedVolumeNames {
		volumes = append(volumes, source.HostPath+"/"+name+"/_data:"+source.ContainerPath+"/"+name+"/_data:ro")
	}
	for name, target := range map[string]string{"kopia-repository": source.RepositoryPath, "kopia-config": source.ConfigPath, "kopia-cache": source.CachePath, "kopia-restore-staging": localbackuppolicy.RestoreStagingPath} {
		volumes = append(volumes, name+":"+target)
		cloud["volumes"].(map[string]any)[name] = map[string]any{}
	}
	sort.Strings(volumes)
	kopia["volumes"] = volumes
	cloud["services"].(map[string]any)["kopia-agent"] = kopia
	cloud["networks"].(map[string]any)["cloud-backup"] = map[string]any{"name": "stackkit-cloud-backup", "internal": false}
	encoded, err := yaml.Marshal(cloud)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func filterCloudCoreComponentsJSON(source string, removed map[string]struct{}) string {
	var components []map[string]any
	if err := json.Unmarshal([]byte(source), &components); err != nil {
		panic("invalid built-in Cloud core component contract: " + err.Error())
	}
	filtered := components[:0]
	for _, component := range components {
		if _, omit := removed[component["id"].(string)]; !omit {
			filtered = append(filtered, component)
		}
	}
	encoded, err := json.Marshal(filtered)
	if err != nil {
		panic("invalid built-in Cloud standalone core component contract: " + err.Error())
	}
	return string(encoded)
}

func buildCloudStandaloneCoreCompose() string {
	compose := filterCloudComposeMapping(cloudCoreCompose, "services", map[string]struct{}{
		"coolify":          {},
		"coolify-postgres": {},
		"coolify-redis":    {},
		"coolify-realtime": {},
	})
	compose = filterCloudComposeMapping(compose, "volumes", map[string]struct{}{
		"coolify-data":          {},
		"coolify-ssh":           {},
		"coolify-applications":  {},
		"coolify-databases":     {},
		"coolify-services":      {},
		"coolify-backups":       {},
		"coolify-postgres-data": {},
		"coolify-redis-data":    {},
	})
	compose = strings.ReplaceAll(compose, `<li><a href="https://coolify.{{STACKKIT_DOMAIN}}">Coolify</a></li>`, "")
	compose = strings.ReplaceAll(compose, "name: stackkit-cloud-core", "name: stackkit-cloud-core-standalone")
	compose = strings.ReplaceAll(compose, "name: stackkit-cloud-control", "name: stackkit-cloud-control-standalone")
	return withCloudKopiaCompose(compose)
}

func filterCloudComposeMapping(compose, section string, removed map[string]struct{}) string {
	var output strings.Builder
	inSection, skip := false, false
	for _, line := range strings.SplitAfter(compose, "\n") {
		raw := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if !inSection {
			output.WriteString(line)
			if raw == section+":" {
				inSection = true
			}
			continue
		}
		if raw != "" && raw[0] != ' ' {
			inSection, skip = false, false
			output.WriteString(line)
			continue
		}
		if len(raw) >= 3 && raw[0] == ' ' && raw[1] == ' ' && raw[2] != ' ' {
			trimmed := strings.TrimSpace(raw)
			if colon := strings.IndexByte(trimmed, ':'); colon > 0 {
				_, skip = removed[trimmed[:colon]]
			}
		}
		if !skip {
			output.WriteString(line)
		}
	}
	return output.String()
}

func validateCloudComposeArtifact(content []byte, render func(string, string) []byte) bool {
	match := regexp.MustCompile("routers[.]pocketid[.]rule=Host\\(`([a-z0-9.-]+)`\\)").FindSubmatch(content)
	if len(match) != 2 {
		return false
	}
	host := string(match[1])
	domain, prefix := strings.TrimPrefix(host, "id."), ""
	if domain == host {
		separator := strings.Index(host, "-id.")
		if separator < 1 {
			return false
		}
		prefix, domain = host[:separator], host[separator+4:]
	}
	return bytes.Equal(content, render(domain, prefix))
}

func sha256Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
