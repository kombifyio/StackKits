package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const DokployBootstrapModuleRef = "stackkits-dokploy-bootstrap-runtime"
const DokployBootstrapUnitRef = "bootstrap"
const DokployBootstrapOutputRef = "platform/dokploy/bootstrap.json"
const dokployBootstrapSchema = "stackkit.dokploy-bootstrap/v1|node-local,basement-standard|components:dokploy0.30,postgres16|images:catalog-bound|api:loopback-only|swarm:none|ingress:kit-router-only,forward-auth|ui-route:module-route,forward-auth-user|owner:first-signup,node-custody|secrets:node-local-files"

// DokployBootstrapDocument is the node recipe for the Dokploy platform that
// the selected Dokploy adapter deploys through. Dokploy runs without Swarm and
// without its own Traefik; the Kit router stays the only ingress owner.
type DokployBootstrapDocument struct {
	APIVersion        string                             `json:"apiVersion"`
	Kind              string                             `json:"kind"`
	KitRef            string                             `json:"kitRef"`
	SiteRef           string                             `json:"siteRef"`
	NodeRef           string                             `json:"nodeRef"`
	DomainBase        string                             `json:"domainBase"`
	Components        []KomodoBootstrapImage             `json:"components"`
	Route             ApplicationDeliveryRouteDescriptor `json:"route"`
	CredentialCustody string                             `json:"credentialCustody"`
	ProviderLifecycle string                             `json:"providerLifecycle"`
}

// Host is the forward-auth protected Dokploy UI on the Kit router: the authored
// module route, or the Kit default before the first workload authors it.
func (d DokployBootstrapDocument) Host() string {
	if d.Route.Host != "" {
		return d.Route.Host
	}
	return "dokploy." + d.DomainBase
}

func DokployBootstrapRendererContract() RendererContract {
	hash := sha256.Sum256([]byte(dokployBootstrapSchema))
	return RendererContract{Kind: "native-config", RendererRef: "stackkit", TemplateRef: "builtin://platform/dokploy/bootstrap/v1.json", Version: "1.0.0", ContractHash: "sha256:" + hex.EncodeToString(hash[:])}
}

func init() {
	registerProductRegistryExtension(func(registry *Registry) error {
		return registry.Register(DokployBootstrapRendererContract(), dokployBootstrapRenderer{})
	})
}

type dokployBootstrapRenderer struct{}

func (dokployBootstrapRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	contract := DokployBootstrapRendererContract()
	if unit.ModuleID() != DokployBootstrapModuleRef || unit.ID() != DokployBootstrapUnitRef || unit.Kind() != contract.Kind ||
		unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash || unit.RuntimeKind() != "host" || unit.RuntimeDelivery() != "stackkit" ||
		unit.InstanceScope() != "node-local" {
		return nil, fmt.Errorf("Dokploy bootstrap requires its exact node-local renderer contract")
	}
	if !exactStringList(unit.PublicInputRefs(), []string{"delivery-route"}) || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) ||
		!exactStringList(unit.PlanInputRefs(), []string{"kit"}) {
		return nil, fmt.Errorf("Dokploy bootstrap accepts only the compiler-owned Kit binding and its module route")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) {
		return nil, fmt.Errorf("Dokploy bootstrap has no caller-supplied interface authority")
	}
	var values struct {
		Route *applicationDeliveryRoute `json:"delivery-route"`
	}
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return nil, err
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != DokployBootstrapOutputRef {
		return nil, fmt.Errorf("Dokploy bootstrap requires its one declared output")
	}
	site, hasSite := unit.SiteRef()
	node, hasNode := unit.NodeRef()
	domain, hasDomain := unit.NetworkDomainBase()
	if !hasSite || !hasNode || !hasDomain {
		return nil, fmt.Errorf("Dokploy bootstrap requires the selected node and resolved Kit domain")
	}
	var inputs struct {
		Kit executorBundleKit `json:"kit"`
	}
	if err := decodeStrict(unit.PlanInputsJSON(), &inputs); err != nil {
		return nil, err
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return nil, err
	}
	document := DokployBootstrapDocument{APIVersion: "stackkit.dokploy-bootstrap/v1", Kind: "DokployBootstrap", KitRef: inputs.Kit.Slug,
		SiteRef: site, NodeRef: node, DomainBase: domain, CredentialCustody: "local-owner", ProviderLifecycle: "not-owned"}
	if values.Route != nil {
		if err := validateParsedApplicationDeliveryRoute(*values.Route, DokployBootstrapModuleRef, "dokploy", 3000, "dokploy.delivery-route"); err != nil {
			return nil, err
		}
		document.Route = values.Route.descriptor()
	}
	roles := map[string]string{"dokploy": "application", "postgres": "database"}
	for _, component := range components {
		if roles[component.ID] != component.Role || component.Lifecycle != "daemon" || component.Resources == nil ||
			len(component.Environment) != 0 || len(component.SecretEnvironment) != 0 || len(component.Command) != 0 || len(component.Entrypoint) != 0 ||
			len(component.Volumes) != 0 || len(component.OwnerEnvironment) != 0 || component.SandboxRuntime != "" {
			return nil, fmt.Errorf("Dokploy bootstrap component is outside the closed Dokploy/PostgreSQL recipe")
		}
		document.Components = append(document.Components, KomodoBootstrapImage{ID: component.ID, ImageRef: component.Image.Ref,
			ImageDigest: component.Image.Digest, MemoryLimit: component.Resources.MemoryLimit})
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	if _, err := ParseDokployBootstrapDocument(raw); err != nil {
		return nil, err
	}
	return []UnitOutput{{Ref: DokployBootstrapOutputRef, Bytes: append(raw, '\n')}}, nil
}

func ParseDokployBootstrapDocument(raw []byte) (DokployBootstrapDocument, error) {
	var document DokployBootstrapDocument
	if err := decodeStrict(raw, &document); err != nil {
		return document, err
	}
	if document.APIVersion != "stackkit.dokploy-bootstrap/v1" || document.Kind != "DokployBootstrap" || document.KitRef != "basement-kit" ||
		document.SiteRef == "" || document.NodeRef == "" || !komodoBootstrapDomainPattern.MatchString(document.DomainBase) ||
		strings.Contains(document.DomainBase, "..") || !strings.Contains(document.DomainBase, ".") ||
		document.CredentialCustody != "local-owner" || document.ProviderLifecycle != "not-owned" {
		return document, fmt.Errorf("Dokploy bootstrap requires an exact Basement node, Kit domain and custody binding")
	}
	refs := map[string]string{"dokploy": "docker.io/dokploy/dokploy:v0.30.", "postgres": "docker.io/library/postgres:16-"}
	seen := map[string]bool{}
	for _, component := range document.Components {
		prefix := refs[component.ID]
		if prefix == "" || seen[component.ID] || !strings.HasPrefix(component.ImageRef, prefix) ||
			!komodoBootstrapDigestPattern.MatchString(component.ImageDigest) || !komodoBootstrapMemoryPattern.MatchString(component.MemoryLimit) {
			return document, fmt.Errorf("Dokploy bootstrap contains an unbound image or component")
		}
		seen[component.ID] = true
	}
	if len(seen) != len(refs) {
		return document, fmt.Errorf("Dokploy bootstrap requires Dokploy and its node PostgreSQL dependency")
	}
	// The route's TLS floor is the Kit network's own; Host() follows the route,
	// so the host is pinned to the Kit default here.
	if document.Route.ID != "" && !(kitRouterUIRoute{moduleRef: DokployBootstrapModuleRef, serviceRef: "dokploy", kitRef: document.KitRef,
		host: "dokploy." + document.DomainBase, targetPort: 3000, tlsMinVersions: []string{"TLS1.2", "TLS1.3"}}).matches(document.Route) {
		return document, fmt.Errorf("Dokploy UI requires its own Kit router, HTTPS and forward authentication")
	}
	return document, nil
}
