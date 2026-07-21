package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

const (
	publicTLSExecutorContractModuleID    = "stackkits-public-tls-contract"
	publicTLSExecutorContractUnitID      = "executor-contract"
	publicTLSExecutorContractRendererRef = "stackkit"
	publicTLSExecutorContractTemplateRef = "builtin://cloud/tls/executor-contract/v1.json"
	publicTLSExecutorContractVersion     = "1.0.0"
	publicTLSExecutorContractOutputRef   = "cloud/tls/executor-contract.json"
	publicTLSExecutorContractToken       = "@@PLAN_INPUTS@@"

	publicTLSCapabilityRef = "public-tls"
	publicTLSProviderRef   = "stackkits-public-tls"
	publicTLSProfileRef    = "stackkits-public-tls-profile"
	publicTLSIssuerRef     = "stackkits-public-acme"
)

const publicTLSExecutorContractTemplate = `{"apiVersion":"stackkit.public-tls-executor-contract/v1","kind":"PublicTLSExecutorContract","module":{"id":"stackkits-public-tls-contract","version":"1.0.0"},"contract":{"certificateIssuance":"not-included","credentials":"not-included","execution":"unverified","generation":"supported","providerLifecycle":"not-owned","runtimeEnforcement":"unverified","scope":"generation-only","serverProviderAuthority":"not-owned"},"planInputs":@@PLAN_INPUTS@@}
`

var publicTLSExecutorContractPlanInputRefs = []string{"kit", "moduleTargets", "publicTLS", "stackId"}

// PublicTLSExecutorContractRendererContract returns the exact immutable
// generation-only handoff identity for the catalog-owned public TLS contract.
func PublicTLSExecutorContractRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(publicTLSExecutorContractTemplate))
	return RendererContract{
		Kind: "native-config", RendererRef: publicTLSExecutorContractRendererRef,
		TemplateRef: publicTLSExecutorContractTemplateRef, Version: publicTLSExecutorContractVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type publicTLSExecutorContractRenderer struct {
	template []byte
	contract RendererContract
}

func newPublicTLSExecutorContractRenderer() publicTLSExecutorContractRenderer {
	return publicTLSExecutorContractRenderer{
		template: []byte(publicTLSExecutorContractTemplate),
		contract: PublicTLSExecutorContractRendererContract(),
	}
}

func (r publicTLSExecutorContractRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan, err := validatePublicTLSExecutorContractUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.public-tls-executor-contract.planInputs", "marshal typed public TLS executor contract", err)
	}
	if err := rejectPublicTLSExecutorContractLeaks(canonical, "renderer.public-tls-executor-contract.planInputs"); err != nil {
		return nil, err
	}
	if publicTLSExecutorContractTemplateHash(r.template) != r.contract.ContractHash || bytes.Count(r.template, []byte(publicTLSExecutorContractToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.public-tls-executor-contract.template", "embedded public TLS executor contract does not match its registered contract")
	}
	output := bytes.Replace(r.template, []byte(publicTLSExecutorContractToken), canonical, 1)
	return []UnitOutput{{Ref: publicTLSExecutorContractOutputRef, Bytes: output}}, nil
}

func publicTLSExecutorContractTemplateHash(template []byte) string {
	sum := sha256.Sum256(template)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type publicTLSExecutorPlan struct {
	StackID       string                      `json:"stackId"`
	Kit           publicTLSExecutorKit        `json:"kit"`
	ModuleTargets []executorBundleTarget      `json:"moduleTargets"`
	PublicTLS     publicTLSExecutorProjection `json:"publicTLS"`
}

type publicTLSExecutorKit struct {
	Slug           string `json:"slug"`
	Version        string `json:"version"`
	DefinitionHash string `json:"definitionHash"`
}

type publicTLSExecutorProjection struct {
	CapabilityRef string                   `json:"capabilityRef"`
	ProviderRef   string                   `json:"providerRef"`
	Profile       publicTLSExecutorProfile `json:"profile"`
	Issuer        publicTLSExecutorIssuer  `json:"issuer"`
	Routes        []publicTLSExecutorRoute `json:"routes"`
}

type publicTLSExecutorProfile struct {
	ID                 string   `json:"id"`
	CapabilityRef      string   `json:"capabilityRef"`
	Mode               string   `json:"mode"`
	TrustDomain        string   `json:"trustDomain"`
	MinimumVersion     string   `json:"minimumVersion"`
	AllowedIssuerKinds []string `json:"allowedIssuerKinds"`
}

type publicTLSExecutorIssuer struct {
	ID                   string                          `json:"id"`
	CapabilityRef        string                          `json:"capabilityRef"`
	Kind                 string                          `json:"kind"`
	Challenge            string                          `json:"challenge"`
	SupportedSiteKinds   []string                        `json:"supportedSiteKinds"`
	ValiditySeconds      int                             `json:"validitySeconds"`
	RequiredInputSlotIDs []string                        `json:"requiredInputSlotIDs"`
	MaterialSlots        []publicTLSExecutorMaterialSlot `json:"materialSlots"`
	Renewal              publicTLSExecutorRenewal        `json:"renewal"`
}

type publicTLSExecutorMaterialSlot struct {
	ID          string `json:"id"`
	Purpose     string `json:"purpose"`
	Sensitivity string `json:"sensitivity"`
}

type publicTLSExecutorRenewal struct {
	Required           bool   `json:"required"`
	HealthGateRef      string `json:"healthGateRef"`
	RenewBeforeSeconds int    `json:"renewBeforeSeconds"`
}

type publicTLSExecutorRoute struct {
	ID       string                    `json:"id"`
	Host     string                    `json:"host"`
	Port     int                       `json:"port"`
	Path     string                    `json:"path"`
	Exposure string                    `json:"exposure"`
	Protocol string                    `json:"protocol"`
	TLS      publicTLSExecutorRouteTLS `json:"tls"`
}

type publicTLSExecutorRouteTLS struct {
	Mode       string `json:"mode"`
	MinVersion string `json:"minVersion"`
	ProfileRef string `json:"profileRef"`
	IssuerRef  string `json:"issuerRef"`
}

func validatePublicTLSExecutorContractUnit(unit RenderUnit, contract RendererContract) (publicTLSExecutorPlan, error) {
	raw, err := validateGenerationOnlyPolicyUnit(unit, generationOnlyPolicyUnitSpec{
		moduleID: publicTLSExecutorContractModuleID, unitID: publicTLSExecutorContractUnitID,
		outputRef: publicTLSExecutorContractOutputRef, policyName: "public TLS executor contract",
		contract: contract, planInputRefs: publicTLSExecutorContractPlanInputRefs,
		validatePlanInput: validatePublicTLSExecutorPlanInputs,
	})
	if err != nil {
		return publicTLSExecutorPlan{}, err
	}
	var plan publicTLSExecutorPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return publicTLSExecutorPlan{}, wrap(ErrInvalidPlan, "resolvedPlan.modules."+publicTLSExecutorContractModuleID+".renderUnits."+publicTLSExecutorContractUnitID+".planInputs", "decode exact public TLS plan inputs", err)
	}
	nodeRefs := make([]string, len(plan.ModuleTargets))
	for index := range plan.ModuleTargets {
		nodeRefs[index] = plan.ModuleTargets[index].ID
	}
	if !exactStringList(unit.LogicalNodeRefs(), nodeRefs) {
		return publicTLSExecutorPlan{}, fail(ErrInvalidPlan, "resolvedPlan.modules."+publicTLSExecutorContractModuleID+".renderUnits."+publicTLSExecutorContractUnitID+".placement", "logical nodes must exactly equal the public TLS module targets")
	}
	return plan, nil
}

//nolint:gocyclo // Profile, issuer, material-slot, placement, and route binding form one closed authority boundary.
func validatePublicTLSExecutorPlanInputs(raw []byte, path string) ([]string, error) {
	var plan publicTLSExecutorPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact public TLS executor inputs", err)
	}
	if err := requireContractID(plan.StackID, path+".stackId"); err != nil {
		return nil, err
	}
	if !containsExecutorBundleString([]string{"cloud-kit", "modern-homelab"}, plan.Kit.Slug) || strings.TrimSpace(plan.Kit.Version) == "" || !validSHA256(plan.Kit.DefinitionHash) {
		return nil, fail(ErrInvalidPlan, path+".kit", "public TLS requires an exact Cloud-capable kit identity")
	}
	if len(plan.ModuleTargets) == 0 {
		return nil, fail(ErrInvalidPlan, path+".moduleTargets", "public TLS requires at least one governed module target")
	}
	siteSet := map[string]struct{}{}
	previousTarget := ""
	for index, target := range plan.ModuleTargets {
		targetPath := fmt.Sprintf("%s.moduleTargets[%d]", path, index)
		if err := requireContractID(target.ID, targetPath+".id"); err != nil {
			return nil, err
		}
		if previousTarget != "" && target.ID <= previousTarget {
			return nil, fail(ErrDuplicate, targetPath+".id", "module targets must be unique and sorted")
		}
		previousTarget = target.ID
		if err := requireContractID(target.SiteRef, targetPath+".siteRef"); err != nil {
			return nil, err
		}
		if strings.TrimSpace(target.FailureDomain) == "" || validSecretReference(strings.ToLower(target.FailureDomain)) {
			return nil, fail(ErrInvalidPlan, targetPath+".failureDomain", "failure domain must be non-secret declared intent")
		}
		if !sortedUniqueNonEmpty(target.Roles) || target.DeclaredHardware.Arch != "amd64" && target.DeclaredHardware.Arch != "arm64" || target.DeclaredHardware.Profile == "" {
			return nil, fail(ErrInvalidPlan, targetPath, "target roles and declared hardware are incomplete")
		}
		if target.DeclaredHardware.CPUCores < 0 || target.DeclaredHardware.RAMGB < 0 || target.DeclaredHardware.StorageGB < 0 {
			return nil, fail(ErrInvalidPlan, targetPath+".declaredHardware", "declared capacities cannot be negative")
		}
		siteSet[target.SiteRef] = struct{}{}
	}
	siteRefs := make([]string, 0, len(siteSet))
	for siteRef := range siteSet {
		siteRefs = append(siteRefs, siteRef)
	}
	sort.Strings(siteRefs)

	tls := plan.PublicTLS
	if tls.CapabilityRef != publicTLSCapabilityRef || tls.ProviderRef != publicTLSProviderRef {
		return nil, fail(ErrInvalidPlan, path+".publicTLS", "TLS projection must bind the exact catalog public-tls adapter")
	}
	profile := tls.Profile
	if profile.ID != publicTLSProfileRef || profile.CapabilityRef != publicTLSCapabilityRef || profile.Mode != "terminate-at-edge" || profile.TrustDomain != "web-pki" || !containsExecutorBundleString([]string{"TLS1.2", "TLS1.3"}, profile.MinimumVersion) || !exactStringList(profile.AllowedIssuerKinds, []string{"acme"}) {
		return nil, fail(ErrInvalidPlan, path+".publicTLS.profile", "TLS profile is outside the exact WebPKI edge-termination contract")
	}
	issuer := tls.Issuer
	if issuer.ID != publicTLSIssuerRef || issuer.CapabilityRef != publicTLSCapabilityRef || issuer.Kind != "acme" || issuer.Challenge != "tls-alpn-01" || !exactStringList(issuer.SupportedSiteKinds, []string{"cloud"}) || issuer.ValiditySeconds != 7776000 || len(issuer.RequiredInputSlotIDs) != 0 {
		return nil, fail(ErrInvalidPlan, path+".publicTLS.issuer", "issuer is outside the exact catalog-owned ACME contract")
	}
	wantSlots := []publicTLSExecutorMaterialSlot{
		{ID: "certificate", Purpose: "certificate-chain", Sensitivity: "public"},
		{ID: "private-key", Purpose: "private-key", Sensitivity: "secret"},
		{ID: "acme-account-key", Purpose: "issuer-account-key", Sensitivity: "secret"},
	}
	if len(issuer.MaterialSlots) != len(wantSlots) {
		return nil, fail(ErrInvalidPlan, path+".publicTLS.issuer.materialSlots", "issuer must expose only the exact typed logical material slots")
	}
	for index := range wantSlots {
		if issuer.MaterialSlots[index] != wantSlots[index] {
			return nil, fail(ErrInvalidPlan, fmt.Sprintf("%s.publicTLS.issuer.materialSlots[%d]", path, index), "material slot identity, purpose, or sensitivity drifted")
		}
	}
	if !issuer.Renewal.Required || issuer.Renewal.HealthGateRef != "public-tls-renewal-contract" || issuer.Renewal.RenewBeforeSeconds != 2592000 || issuer.Renewal.RenewBeforeSeconds >= issuer.ValiditySeconds {
		return nil, fail(ErrInvalidPlan, path+".publicTLS.issuer.renewal", "renewal policy is outside the exact catalog contract")
	}
	previousRoute := ""
	for index, route := range tls.Routes {
		routePath := fmt.Sprintf("%s.publicTLS.routes[%d]", path, index)
		if err := requireContractID(route.ID, routePath+".id"); err != nil {
			return nil, err
		}
		if previousRoute != "" && route.ID <= previousRoute {
			return nil, fail(ErrDuplicate, routePath+".id", "public TLS routes must be unique and sorted")
		}
		previousRoute = route.ID
		_, hostAddressErr := netip.ParseAddr(route.Host)
		if !validExecutorBundleHost(route.Host) || hostAddressErr == nil || route.Port < 1 || route.Port > 65535 || !strings.HasPrefix(route.Path, "/") || strings.ContainsAny(route.Path, "\r\n\x00") || route.Exposure != "public" || route.Protocol != "https" {
			return nil, fail(ErrInvalidPlan, routePath, "route must be an exact public HTTPS hostname binding")
		}
		if route.TLS.Mode != "terminate-at-edge" || route.TLS.ProfileRef != profile.ID || route.TLS.IssuerRef != issuer.ID || !containsExecutorBundleString([]string{"TLS1.2", "TLS1.3"}, route.TLS.MinVersion) || profile.MinimumVersion == "TLS1.3" && route.TLS.MinVersion != "TLS1.3" {
			return nil, fail(ErrInvalidPlan, routePath+".tls", "route must bind the selected TLS profile and issuer without weakening its minimum version")
		}
	}
	if err := rejectPublicTLSExecutorContractLeaks(raw, path); err != nil {
		return nil, err
	}
	return siteRefs, nil
}

func rejectPublicTLSExecutorContractLeaks(raw []byte, path string) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return wrap(ErrInvalidPlan, path, "scan public TLS executor inputs", err)
	}
	forbiddenKeys := map[string]struct{}{
		"accountref": {}, "credential": {}, "credentialref": {}, "credentialrefs": {}, "credentials": {},
		"daemon": {}, "daemonref": {}, "daemonsocketpath": {}, "interfaces": {}, "managementaddress": {},
		"password": {}, "providerlifecycle": {}, "secretref": {}, "secretrefs": {}, "socketpath": {}, "token": {}, "values": {},
	}
	var walk func(any, string) error
	walk = func(current any, currentPath string) error {
		switch typed := current.(type) {
		case map[string]any:
			for key, nested := range typed {
				if _, forbidden := forbiddenKeys[strings.ToLower(key)]; forbidden {
					return fail(ErrInvalidPlan, currentPath+"."+key, "field is outside the closed public TLS executor projection")
				}
				if err := walk(nested, currentPath+"."+key); err != nil {
					return err
				}
			}
		case []any:
			for index, nested := range typed {
				if err := walk(nested, fmt.Sprintf("%s[%d]", currentPath, index)); err != nil {
					return err
				}
			}
		case string:
			lower := strings.ToLower(typed)
			if validSecretReference(lower) || strings.Contains(lower, ".sock") && strings.Contains(lower, "/") {
				return fail(ErrInvalidPlan, currentPath, "secret references and daemon socket paths are forbidden from public TLS executor artifacts")
			}
		}
		return nil
	}
	return walk(value, path)
}
