package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

const MarketplaceCoolifyModuleRef = "stackkits-marketplace-coolify-runtime"
const MarketplaceCoolifyUnitRef = "marketplace"
const MarketplaceCoolifyOutputRef = "workloads/marketplace/installation.json"
const marketplaceCoolifySchema = "stackkit.marketplace-coolify/v1|artifact-revision:2|node-local,basement-and-cloud-standard|core:basement-lite-or-cloud-standalone|network:kit-core-bound|identity-subdomain-prefix:plan-bound|application:coolify4.4|components:coolify,postgres15,redis7|images:catalog-bound|api:loopback-only|identity:pocketid-pkce,root-email,household-members|ui:kit-router,forward-auth,cloud-public-tls-or-home-private|autoupdate:false|secrets:node-custody|provider-lifecycle:not-owned|backup:node-installation-volumes,quiesced"

var marketplaceIdentityPrefixPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// MarketplaceCoolifyDocument is a node installation recipe. Coolify is an
// application and never replaces the selected workload runtime adapter.
type MarketplaceCoolifyDocument struct {
	APIVersion      string                             `json:"apiVersion"`
	Kind            string                             `json:"kind"`
	KitRef          string                             `json:"kitRef"`
	SiteRef         string                             `json:"siteRef"`
	NodeRef         string                             `json:"nodeRef"`
	DomainBase      string                             `json:"domainBase"`
	SubdomainPrefix string                             `json:"subdomainPrefix,omitempty"`
	Components      []KomodoBootstrapImage             `json:"components"`
	Route           ApplicationDeliveryRouteDescriptor `json:"route"`
}

// MarketplaceCoreModuleRef names the existing standalone Core contract for
// this Kit. A legacy Core that already embeds Coolify is not admitted.
func MarketplaceCoreModuleRef(kitRef string) (string, bool) {
	switch kitRef {
	case "basement-kit":
		return "stackkits-basement-core-lite-runtime", true
	case "cloud-kit":
		return "stackkits-cloud-core-standalone-runtime", true
	default:
		return "", false
	}
}

func (d MarketplaceCoolifyDocument) Host() string {
	if d.SubdomainPrefix != "" {
		return "marketplace." + d.SubdomainPrefix + "." + d.DomainBase
	}
	return "marketplace." + d.DomainBase
}

func MarketplaceCoolifyRendererContract() RendererContract {
	hash := sha256.Sum256([]byte(marketplaceCoolifySchema))
	return RendererContract{Kind: "native-config", RendererRef: "stackkit", TemplateRef: "builtin://workloads/marketplace/installation/v1.json", Version: "1.0.0", ContractHash: "sha256:" + hex.EncodeToString(hash[:])}
}

type marketplaceCoolifyRenderer struct{}

func (marketplaceCoolifyRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	contract := MarketplaceCoolifyRendererContract()
	if unit.ModuleID() != MarketplaceCoolifyModuleRef || unit.ID() != MarketplaceCoolifyUnitRef || unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash || unit.RuntimeKind() != "host" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "node-local" {
		return nil, errors.New("Marketplace requires its exact node-local installation renderer")
	}
	if !exactStringList(unit.PublicInputRefs(), []string{"delivery-route"}) || !exactStringList(unit.PlanInputRefs(), []string{"kit"}) || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) {
		return nil, errors.New("Marketplace installation accepts only its compiler-bound Kit and route; secret material remains in node custody")
	}
	var inputs struct {
		Kit executorBundleKit `json:"kit"`
	}
	if err := decodeStrict(unit.PlanInputsJSON(), &inputs); err != nil {
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
		return nil, errors.New("Marketplace requires its selected node and Kit domain")
	}
	d := MarketplaceCoolifyDocument{APIVersion: "stackkit.marketplace-coolify/v1", Kind: "MarketplaceCoolify", KitRef: inputs.Kit.Slug, SiteRef: site, NodeRef: node, DomainBase: domain}
	if d.KitRef == "cloud-kit" {
		d.SubdomainPrefix, _ = unit.NetworkSubdomainPrefix()
	}
	if values.Route != nil {
		if err := validateParsedApplicationDeliveryRoute(*values.Route, MarketplaceCoolifyModuleRef, "marketplace", 8080, "marketplace.delivery-route"); err != nil {
			return nil, err
		}
		d.Route = values.Route.descriptor()
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return nil, err
	}
	roles := map[string]string{"coolify": "application", "postgres": "database", "redis": "cache"}
	for _, c := range components {
		if roles[c.ID] != c.Role || c.Lifecycle != "daemon" || c.Resources == nil || len(c.Command) != 0 || len(c.Entrypoint) != 0 || len(c.Environment) != 0 || len(c.SecretEnvironment) != 0 || len(c.OwnerEnvironment) != 0 {
			return nil, errors.New("Marketplace component differs from the closed installation recipe")
		}
		seenVolumes := map[string]bool{}
		for _, v := range c.Volumes {
			target, admitted := localbackuppolicy.MarketplaceVolumeTarget(c.ID, v.ID)
			if !admitted || seenVolumes[v.ID] || v.Target != target || v.Class != "persistent" || !v.Backup || v.SelfPath || v.SharedFrom != nil {
				return nil, errors.New("Marketplace volume differs from its closed installation backup layout")
			}
			seenVolumes[v.ID] = true
		}
		wantedVolumes := 1
		if c.ID == "coolify" {
			wantedVolumes = 6
		}
		if len(seenVolumes) != wantedVolumes {
			return nil, errors.New("Marketplace installation has an incomplete persistent volume contract")
		}
		d.Components = append(d.Components, KomodoBootstrapImage{ID: c.ID, ImageRef: c.Image.Ref, ImageDigest: c.Image.Digest, MemoryLimit: c.Resources.MemoryLimit})
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if _, err := ParseMarketplaceCoolifyDocument(raw); err != nil {
		return nil, err
	}
	if !exactStringList(unit.DeclaredOutputs(), []string{MarketplaceCoolifyOutputRef}) {
		return nil, errors.New("Marketplace installation must own its exact output")
	}
	return []UnitOutput{{Ref: MarketplaceCoolifyOutputRef, Bytes: append(raw, '\n')}}, nil
}

func ParseMarketplaceCoolifyDocument(raw []byte) (MarketplaceCoolifyDocument, error) {
	var d MarketplaceCoolifyDocument
	if err := decodeStrict(raw, &d); err != nil {
		return d, err
	}
	coreModuleRef, kitSupported := MarketplaceCoreModuleRef(d.KitRef)
	if d.APIVersion != "stackkit.marketplace-coolify/v1" || d.Kind != "MarketplaceCoolify" || !kitSupported ||
		d.SiteRef == "" || d.NodeRef == "" || !komodoBootstrapDomainPattern.MatchString(d.DomainBase) || !strings.Contains(d.DomainBase, ".") || strings.Contains(d.DomainBase, "..") {
		return d, errors.New("Marketplace requires the exact Basement or Cloud node and Kit domain")
	}
	if d.SubdomainPrefix != "" && (d.KitRef != "cloud-kit" || len(d.SubdomainPrefix) > 63 || !marketplaceIdentityPrefixPattern.MatchString(d.SubdomainPrefix)) {
		return d, errors.New("Marketplace identity prefix differs from the closed Cloud address contract")
	}
	prefixes := map[string]string{"coolify": "coollabsio/coolify:4.4.", "postgres": "postgres:15-", "redis": "redis:7-"}
	seen := map[string]bool{}
	for _, c := range d.Components {
		if prefixes[c.ID] == "" || seen[c.ID] || !strings.HasPrefix(c.ImageRef, prefixes[c.ID]) ||
			!komodoBootstrapDigestPattern.MatchString(c.ImageDigest) || !komodoBootstrapMemoryPattern.MatchString(c.MemoryLimit) {
			return d, errors.New("Marketplace requires its digest-pinned Coolify 4.4, PostgreSQL 15 and Redis 7 installation components")
		}
		seen[c.ID] = true
	}
	if len(seen) != len(prefixes) {
		return d, errors.New("Marketplace installation dependency is missing")
	}
	if d.Route.ID != "" && (d.Route.ModuleRef != MarketplaceCoolifyModuleRef || d.Route.ServiceRef != "marketplace" ||
		d.Route.CoreModuleRef != coreModuleRef || d.Route.Host != d.Host() || d.Route.Path != "/" ||
		d.Route.Protocol != "https" || d.Route.Port != 443 || d.Route.UpstreamProtocol != "http" || d.Route.TargetPort != 8080 ||
		!d.Route.TLSRequired || d.Route.TLSMinVersion != "TLS1.3" || d.Route.IngressAuth != "forward-auth" ||
		(d.Route.Exposure != "local" && d.Route.Exposure != "remote-private" && (d.KitRef != "cloud-kit" || d.Route.Exposure != "public"))) {
		return d, errors.New("Marketplace UI requires the own-node Kit router, HTTPS and forward authentication")
	}
	return d, nil
}

func ValidateMarketplaceCoolifyDocument(d MarketplaceCoolifyDocument) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = ParseMarketplaceCoolifyDocument(raw)
	return err
}
