package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

const MarketplaceRuntipiModuleRef = "stackkits-marketplace-runtipi-runtime"
const MarketplaceRuntipiUnitRef = "marketplace"
const MarketplaceRuntipiOutputRef = "workloads/marketplace/runtipi-installation.json"
const RuntipiRelease = "v4.10.2"
const RuntipiCLIArchiveSHA256 = "77c618daaa4e65eb5dd4f9bb6ee192a345afc4b52a3e5d2e50d30d7e00c3ad64"
const marketplaceRuntipiSchema = "stackkit.marketplace-runtipi/v1|runtipi4.10.2|node-local|cli:release-sha256|images:catalog-bound|proxy:kit-router|api:loopback|custody:encrypted-local-owner|readiness:operator,closed-registration|provider-lifecycle:not-owned"

// MarketplaceRuntipiDocument describes a node-owned upstream installation. Its
// renderer and owner are registered for the explicit, non-default Marketplace
// alternative; qualification still requires a current-pin test-window receipt.
type MarketplaceRuntipiDocument struct {
	APIVersion      string                             `json:"apiVersion"`
	Kind            string                             `json:"kind"`
	KitRef          string                             `json:"kitRef"`
	SiteRef         string                             `json:"siteRef"`
	NodeRef         string                             `json:"nodeRef"`
	DomainBase      string                             `json:"domainBase"`
	SubdomainPrefix string                             `json:"subdomainPrefix,omitempty"`
	Release         string                             `json:"release"`
	CLIArchiveHash  string                             `json:"cliArchiveHash"`
	Components      []KomodoBootstrapImage             `json:"components"`
	Route           ApplicationDeliveryRouteDescriptor `json:"route"`
}

func (d MarketplaceRuntipiDocument) MarketplaceContext() MarketplaceCoolifyDocument {
	return MarketplaceCoolifyDocument{KitRef: d.KitRef, SiteRef: d.SiteRef, NodeRef: d.NodeRef, DomainBase: d.DomainBase, SubdomainPrefix: d.SubdomainPrefix, Route: d.Route}
}

func (d MarketplaceRuntipiDocument) Host() string { return d.MarketplaceContext().Host() }

func MarketplaceRuntipiRendererContract() RendererContract {
	hash := sha256.Sum256([]byte(marketplaceRuntipiSchema))
	return RendererContract{Kind: "native-config", RendererRef: "stackkit", TemplateRef: "builtin://workloads/marketplace/runtipi-installation/v1.json", Version: "1.0.0", ContractHash: "sha256:" + hex.EncodeToString(hash[:])}
}

// RegisterPreparedRuntipiRenderers registers the Marketplace renderer and the
// still-prepared adapter renderer for explicit opt-in callers; the product registry registers only the Marketplace renderer.
func RegisterPreparedRuntipiRenderers(registry *Registry) error {
	if registry == nil {
		return errors.New("Runtipi preparation requires a renderer registry")
	}
	if err := registry.Register(MarketplaceRuntipiRendererContract(), marketplaceRuntipiRenderer{}); err != nil {
		return err
	}
	adapter := newRuntipiRuntimeAdapterRenderer()
	return registry.Register(adapter.contract, adapter)
}

type marketplaceRuntipiRenderer struct{}

func (marketplaceRuntipiRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	contract := MarketplaceRuntipiRendererContract()
	if unit.ModuleID() != MarketplaceRuntipiModuleRef || unit.ID() != "marketplace" || unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash || unit.RuntimeKind() != "host" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "node-local" {
		return nil, errors.New("Runtipi requires its exact node-local installation renderer")
	}
	if !exactStringList(unit.PublicInputRefs(), []string{"delivery-route"}) || !exactStringList(unit.PlanInputRefs(), []string{"kit"}) || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) {
		return nil, errors.New("Runtipi installation accepts only its compiler-bound Kit and route")
	}
	var plan struct {
		Kit executorBundleKit `json:"kit"`
	}
	if err := decodeStrict(unit.PlanInputsJSON(), &plan); err != nil {
		return nil, err
	}
	var values struct {
		Route *applicationDeliveryRoute `json:"delivery-route"`
	}
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return nil, err
	}
	site, hasSite := unit.SiteRef()
	node, hasNode := unit.NodeRef()
	domain, hasDomain := unit.NetworkDomainBase()
	if !hasSite || !hasNode || !hasDomain {
		return nil, errors.New("Runtipi requires its selected node and Kit domain")
	}
	d := MarketplaceRuntipiDocument{APIVersion: "stackkit.marketplace-runtipi/v1", Kind: "MarketplaceRuntipi", KitRef: plan.Kit.Slug, SiteRef: site, NodeRef: node, DomainBase: domain, Release: RuntipiRelease, CLIArchiveHash: RuntipiCLIArchiveSHA256}
	if d.KitRef == "cloud-kit" {
		d.SubdomainPrefix, _ = unit.NetworkSubdomainPrefix()
	}
	if values.Route != nil {
		if err := validateParsedApplicationDeliveryRoute(*values.Route, MarketplaceRuntipiModuleRef, "marketplace", 3000, "runtipi.delivery-route"); err != nil {
			return nil, err
		}
		d.Route = values.Route.descriptor()
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return nil, err
	}
	for _, component := range components {
		if component.Lifecycle != "daemon" || component.Resources == nil || len(component.Command) != 0 || len(component.Entrypoint) != 0 || len(component.Environment) != 0 || len(component.SecretEnvironment) != 0 || len(component.OwnerEnvironment) != 0 {
			return nil, errors.New("Runtipi component differs from its closed upstream installation recipe")
		}
		d.Components = append(d.Components, KomodoBootstrapImage{ID: component.ID, ImageRef: component.Image.Ref, ImageDigest: component.Image.Digest, MemoryLimit: component.Resources.MemoryLimit})
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if _, err := ParseMarketplaceRuntipiDocument(raw); err != nil {
		return nil, err
	}
	if !exactStringList(unit.DeclaredOutputs(), []string{MarketplaceRuntipiOutputRef}) {
		return nil, errors.New("Runtipi installation must own its exact private artifact")
	}
	return []UnitOutput{{Ref: MarketplaceRuntipiOutputRef, Bytes: append(raw, '\n')}}, nil
}

func ParseMarketplaceRuntipiDocument(raw []byte) (MarketplaceRuntipiDocument, error) {
	var d MarketplaceRuntipiDocument
	if err := decodeStrict(raw, &d); err != nil {
		return d, err
	}
	core, supported := MarketplaceCoreModuleRef(d.KitRef)
	if d.APIVersion != "stackkit.marketplace-runtipi/v1" || d.Kind != "MarketplaceRuntipi" || !supported || d.SiteRef == "" || d.NodeRef == "" || !komodoBootstrapDomainPattern.MatchString(d.DomainBase) || !strings.Contains(d.DomainBase, ".") || strings.Contains(d.DomainBase, "..") || d.Release != RuntipiRelease || d.CLIArchiveHash != RuntipiCLIArchiveSHA256 {
		return d, errors.New("Runtipi requires its exact release, verified CLI and own Basement/Cloud node")
	}
	if d.SubdomainPrefix != "" && (d.KitRef != "cloud-kit" || len(d.SubdomainPrefix) > 63 || !marketplaceIdentityPrefixPattern.MatchString(d.SubdomainPrefix)) {
		return d, errors.New("Runtipi prefix differs from the closed Cloud address contract")
	}
	wanted := map[string]string{"runtipi": "ghcr.io/runtipi/runtipi:v4.10.2", "postgres": "postgres:14", "queue": "rabbitmq:4-alpine"}
	seen := map[string]bool{}
	for _, component := range d.Components {
		if wanted[component.ID] != component.ImageRef || seen[component.ID] || !komodoBootstrapDigestPattern.MatchString(component.ImageDigest) || !komodoBootstrapMemoryPattern.MatchString(component.MemoryLimit) {
			return d, errors.New("Runtipi requires its complete digest-pinned backend, PostgreSQL and queue")
		}
		seen[component.ID] = true
	}
	if len(seen) != len(wanted) {
		return d, errors.New("Runtipi installation dependency is missing")
	}
	if d.Route.ID != "" && (d.Route.ModuleRef != MarketplaceRuntipiModuleRef || d.Route.ServiceRef != "marketplace" || d.Route.CoreModuleRef != core || d.Route.Host != d.Host() || d.Route.Path != "/" || d.Route.Protocol != "https" || d.Route.Port != 443 || d.Route.UpstreamProtocol != "http" || d.Route.TargetPort != 3000 || !d.Route.TLSRequired || d.Route.TLSMinVersion != "TLS1.3" || d.Route.IngressAuth != "forward-auth" || (d.Route.Exposure != "local" && d.Route.Exposure != "remote-private" && (d.KitRef != "cloud-kit" || d.Route.Exposure != "public"))) {
		return d, errors.New("Runtipi UI requires its own Kit router, HTTPS and forward authentication")
	}
	return d, nil
}
