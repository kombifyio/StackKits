package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const KomodoBootstrapModuleRef = "stackkits-komodo-bootstrap-runtime"
const KomodoBootstrapUnitRef = "bootstrap"
const KomodoBootstrapOutputRef = "platform/komodo/bootstrap.json"
const komodoBootstrapSchema = "stackkit.komodo-bootstrap/v1|node-local,basement-standard|components:core,periphery,mongo|images:catalog-bound|api:loopback-only|periphery:outbound-wss,tls1.3,mutual-key,kit-ca|secrets:node-custody|host-lifecycle:not-owned|backup:explicit-denial"

type KomodoBootstrapImage struct {
	ID          string `json:"id"`
	ImageRef    string `json:"imageRef"`
	ImageDigest string `json:"imageDigest"`
	MemoryLimit string `json:"memoryLimit"`
}

type KomodoBootstrapDocument struct {
	APIVersion        string                 `json:"apiVersion"`
	Kind              string                 `json:"kind"`
	KitRef            string                 `json:"kitRef"`
	SiteRef           string                 `json:"siteRef"`
	NodeRef           string                 `json:"nodeRef"`
	DomainBase        string                 `json:"domainBase"`
	Components        []KomodoBootstrapImage `json:"components"`
	MinimumTLSVersion string                 `json:"minimumTLSVersion"`
	CredentialCustody string                 `json:"credentialCustody"`
	ProviderLifecycle string                 `json:"providerLifecycle"`
}

func KomodoBootstrapRendererContract() RendererContract {
	hash := sha256.Sum256([]byte(komodoBootstrapSchema))
	return RendererContract{Kind: "native-config", RendererRef: "stackkit", TemplateRef: "builtin://platform/komodo/bootstrap/v1.json", Version: "1.0.0", ContractHash: "sha256:" + hex.EncodeToString(hash[:])}
}

type komodoBootstrapRenderer struct{}

func newKomodoBootstrapRenderer() komodoBootstrapRenderer { return komodoBootstrapRenderer{} }
func (komodoBootstrapRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	contract := KomodoBootstrapRendererContract()
	if unit.ModuleID() != KomodoBootstrapModuleRef || unit.ID() != KomodoBootstrapUnitRef || unit.Kind() != contract.Kind ||
		unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash || unit.RuntimeKind() != "host" || unit.RuntimeDelivery() != "stackkit" ||
		unit.InstanceScope() != "node-local" {
		return nil, fmt.Errorf("Komodo bootstrap requires its exact node-local renderer contract")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) ||
		!exactStringList(unit.PlanInputRefs(), []string{"kit"}) {
		return nil, fmt.Errorf("Komodo bootstrap accepts only the compiler-owned Kit binding")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.ServiceEndpointsJSON()) {
		return nil, fmt.Errorf("Komodo bootstrap has no caller-supplied interface or endpoint authority")
	}
	outputs := unit.DeclaredOutputs()
	if len(outputs) != 1 || outputs[0] != KomodoBootstrapOutputRef {
		return nil, fmt.Errorf("Komodo bootstrap requires its one declared output")
	}
	site, hasSite := unit.SiteRef()
	node, hasNode := unit.NodeRef()
	domain, hasDomain := unit.NetworkDomainBase()
	if !hasSite || !hasNode || !hasDomain {
		return nil, fmt.Errorf("Komodo bootstrap requires the selected node and resolved Kit domain")
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
	document := KomodoBootstrapDocument{APIVersion: "stackkit.komodo-bootstrap/v1", Kind: "KomodoBootstrap", KitRef: inputs.Kit.Slug,
		SiteRef: site, NodeRef: node, DomainBase: domain, MinimumTLSVersion: "TLS1.3", CredentialCustody: "local-owner", ProviderLifecycle: "not-owned"}
	roles := map[string]string{"core": "application", "periphery": "application", "mongo": "database"}
	for _, component := range components {
		if roles[component.ID] != component.Role || component.Lifecycle != "daemon" || component.Resources == nil ||
			len(component.Environment) != 0 || len(component.SecretEnvironment) != 0 || len(component.Command) != 0 || len(component.Entrypoint) != 0 ||
			len(component.Volumes) != 0 || len(component.OwnerEnvironment) != 0 || component.SandboxRuntime != "" {
			return nil, fmt.Errorf("Komodo bootstrap component is outside the closed Core/Periphery dependency recipe")
		}
		document.Components = append(document.Components, KomodoBootstrapImage{ID: component.ID, ImageRef: component.Image.Ref,
			ImageDigest: component.Image.Digest, MemoryLimit: component.Resources.MemoryLimit})
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	if _, err := ParseKomodoBootstrapDocument(raw); err != nil {
		return nil, err
	}
	return []UnitOutput{{Ref: KomodoBootstrapOutputRef, Bytes: append(raw, '\n')}}, nil
}

var komodoBootstrapDomainPattern = regexp.MustCompile("^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$")
var komodoBootstrapMemoryPattern = regexp.MustCompile("^[1-9][0-9]*[mMgG]$")
var komodoBootstrapDigestPattern = regexp.MustCompile("^sha256:[0-9a-f]{64}$")

func ParseKomodoBootstrapDocument(raw []byte) (KomodoBootstrapDocument, error) {
	var document KomodoBootstrapDocument
	if err := decodeStrict(raw, &document); err != nil {
		return document, err
	}
	if document.APIVersion != "stackkit.komodo-bootstrap/v1" || document.Kind != "KomodoBootstrap" || document.KitRef != "basement-kit" ||
		document.SiteRef == "" || document.NodeRef == "" || !komodoBootstrapDomainPattern.MatchString(document.DomainBase) ||
		strings.Contains(document.DomainBase, "..") || !strings.Contains(document.DomainBase, ".") || document.MinimumTLSVersion != "TLS1.3" ||
		document.CredentialCustody != "local-owner" || document.ProviderLifecycle != "not-owned" {
		return document, fmt.Errorf("Komodo bootstrap requires an exact Basement node, Kit domain, TLS and custody binding")
	}
	refs := map[string]string{"core": "ghcr.io/moghtech/komodo-core:", "periphery": "ghcr.io/moghtech/komodo-periphery:", "mongo": "mongo:"}
	seen := map[string]bool{}
	for _, component := range document.Components {
		prefix := refs[component.ID]
		if prefix == "" || seen[component.ID] || !strings.HasPrefix(component.ImageRef, prefix) ||
			!komodoBootstrapDigestPattern.MatchString(component.ImageDigest) || !komodoBootstrapMemoryPattern.MatchString(component.MemoryLimit) {
			return document, fmt.Errorf("Komodo bootstrap contains an unbound image or component")
		}
		seen[component.ID] = true
	}
	if len(seen) != len(refs) {
		return document, fmt.Errorf("Komodo bootstrap requires Core, Periphery and its node database dependency")
	}
	return document, nil
}
