package architecturev2renderer

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/kombifyio/stackkits/internal/securitybaseline"
)

const (
	securityBaselineModuleID    = "security-baseline"
	securityBaselineUnitID      = "host-policy"
	securityBaselineRendererRef = "stackkit"
	securityBaselineTemplateRef = "builtin://foundation/security-baseline/apply.sh"
	securityBaselineVersion     = "1.0.0"
	securityBaselineOutputRef   = "foundation/security-baseline/apply.sh"
)

// SecurityBaselineRendererContract returns the exact built-in implementation
// identity for the canonical Architecture v2 host policy. The hash binds the
// catalog contract to the rendered script bytes, not merely to a renderer
// function name.
func SecurityBaselineRendererContract() (RendererContract, error) {
	policy, err := securitybaseline.RenderV2HostPolicy()
	if err != nil {
		return RendererContract{}, wrap(ErrRendererFailure, "renderer.security-baseline", "render canonical host policy", err)
	}
	return RendererContract{
		Kind:         "host",
		RendererRef:  securityBaselineRendererRef,
		TemplateRef:  securityBaselineTemplateRef,
		Version:      securityBaselineVersion,
		ContractHash: securitybaseline.ContractHash(policy),
	}, nil
}

// NewProductRegistry returns the exact built-in renderer set for product
// Architecture v2 plans. Registration has no version, prefix, or latest
// fallback: every future template change needs a new governed contract.
func NewProductRegistry() (*Registry, error) {
	policy, err := securitybaseline.RenderV2HostPolicy()
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.security-baseline", "render canonical host policy", err)
	}
	contract := RendererContract{
		Kind:         "host",
		RendererRef:  securityBaselineRendererRef,
		TemplateRef:  securityBaselineTemplateRef,
		Version:      securityBaselineVersion,
		ContractHash: securitybaseline.ContractHash(policy),
	}
	registry := NewRegistry()
	if err := registry.Register(contract, securityBaselineHostPolicyRenderer{policy: append([]byte(nil), policy...), contract: contract}); err != nil {
		return nil, err
	}
	coreHostBootstrap := newCoreHostBootstrapRenderer()
	if err := registry.Register(coreHostBootstrap.contract, coreHostBootstrap); err != nil {
		return nil, err
	}
	homeBackupTarget := newHomeBackupTargetRenderer()
	if err := registry.Register(homeBackupTarget.contract, homeBackupTarget); err != nil {
		return nil, err
	}
	socketProxy := newSocketProxyComposeRenderer()
	if err := registry.Register(socketProxy.contract, socketProxy); err != nil {
		return nil, err
	}
	localAutonomy := newLocalAutonomyPolicyRenderer()
	if err := registry.Register(localAutonomy.contract, localAutonomy); err != nil {
		return nil, err
	}
	homeAccess := newHomeAccessPolicyRenderer()
	if err := registry.Register(homeAccess.contract, homeAccess); err != nil {
		return nil, err
	}
	homeLANDiscovery := newHomeLANDiscoveryPolicyRenderer()
	if err := registry.Register(homeLANDiscovery.contract, homeLANDiscovery); err != nil {
		return nil, err
	}
	for _, renderer := range []identityTrustPolicyRenderer{
		newHomeDeviceAuthorityPolicyRenderer(),
		newBasementIdentityTrustPolicyRenderer(),
		newCloudIdentityTrustPolicyRenderer(),
	} {
		if err := registry.Register(renderer.contract, renderer); err != nil {
			return nil, err
		}
	}
	if err := registerExecutorContractBundleRenderers(registry); err != nil {
		return nil, err
	}
	publicTLS := newPublicTLSExecutorContractRenderer()
	if err := registry.Register(publicTLS.contract, publicTLS); err != nil {
		return nil, err
	}
	immichWorkload := newImmichWorkloadBundleRenderer()
	if err := registry.Register(immichWorkload.contract, immichWorkload); err != nil {
		return nil, err
	}
	for _, extension := range productRegistryExtensions {
		if err := extension(registry); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

type securityBaselineHostPolicyRenderer struct {
	policy   []byte
	contract RendererContract
}

func (r securityBaselineHostPolicyRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateSecurityBaselineUnit(unit, r.contract); err != nil {
		return nil, err
	}
	if len(r.policy) == 0 || securitybaseline.ContractHash(r.policy) != r.contract.ContractHash {
		return nil, fail(ErrOutputChanged, "renderer.security-baseline.policy", "embedded host policy does not match its registered contract hash")
	}
	return []UnitOutput{{Ref: securityBaselineOutputRef, Bytes: append([]byte(nil), r.policy...)}}, nil
}

//nolint:gocyclo // Keep the exact fail-closed authority checks linear and auditable at this renderer boundary.
func validateSecurityBaselineUnit(unit RenderUnit, contract RendererContract) error {
	path := "resolvedPlan.modules." + securityBaselineModuleID + ".renderUnits." + securityBaselineUnitID
	if unit.ModuleID() != securityBaselineModuleID || unit.ID() != securityBaselineUnitID {
		return fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", securityBaselineModuleID, securityBaselineUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered security-baseline contract")
	}
	if unit.InstanceScope() != "node-local" {
		return fail(ErrInvalidPlan, path+".instances."+unit.InstanceID()+".scope", "security baseline must be node-local")
	}
	if _, present := unit.SiteRef(); !present {
		return fail(ErrInvalidPlan, path+".instances."+unit.InstanceID()+".siteRef", "an exact site is required")
	}
	if _, present := unit.NodeRef(); !present {
		return fail(ErrInvalidPlan, path+".instances."+unit.InstanceID()+".nodeRef", "an exact node is required")
	}
	if _, present := unit.DaemonRef(); present {
		return fail(ErrInvalidPlan, path+".instances."+unit.InstanceID()+".daemonRef", "host policy must not bind a runtime daemon")
	}
	if _, present := unit.DaemonInstanceRef(); present {
		return fail(ErrInvalidPlan, path+".instances."+unit.InstanceID()+".daemonInstanceRef", "host policy must not bind a runtime daemon instance")
	}
	if _, present := unit.DaemonEngine(); present {
		return fail(ErrInvalidPlan, path+".instances."+unit.InstanceID()+".daemonEngine", "host policy must not receive a daemon engine")
	}
	if _, present := unit.DaemonSocketPath(); present {
		return fail(ErrInvalidPlan, path+".instances."+unit.InstanceID()+".daemonSocketPath", "host policy must not receive a daemon socket")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) {
		return fail(ErrInvalidPlan, path+".inputs", "security-baseline v1.0.0 is an input-free policy contract")
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return fail(ErrInvalidPlan, path+".interfaces", "host policy must not widen service, daemon, or runtime-network authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
		DaemonRef   string `json:"daemonRef,omitempty"`
	}
	if err := json.Unmarshal(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" || placement.DaemonRef != "" {
		return fail(ErrInvalidPlan, path+".placement", "security baseline requires exact node-local/one-per-node placement")
	}
	outputs := unit.DeclaredOutputs()
	if len(outputs) != 1 || outputs[0] != securityBaselineOutputRef {
		return fail(ErrInvalidPlan, path+".outputs", "security baseline requires exactly output %q", securityBaselineOutputRef)
	}
	return nil
}

func emptyJSONObject(value []byte) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("{}"))
}

func emptyJSONArray(value []byte) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("[]"))
}
