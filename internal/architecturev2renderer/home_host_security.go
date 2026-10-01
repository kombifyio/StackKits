package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	homeHostSecurityModuleID    = "stackkits-home-host-security-runtime"
	homeHostSecurityUnitID      = "executor-contract"
	homeHostSecurityRendererRef = "stackkit"
	homeHostSecurityTemplateRef = "builtin://home/host-security/executor-contract/v1.json"
	homeHostSecurityVersion     = "1.0.0"
	homeHostSecurityOutputRef   = "home/host-security/executor-contract.json"
)

// homeHostSecurityTemplate is the complete, input-free policy the Home host
// security owner enforces. It names no address, interface, port or credential:
// the owner derives LAN, overlay and management facts from the host itself and
// never closes the management path. Changing any byte is a new governed
// contract (a new hash in the catalog).
const homeHostSecurityTemplate = `{"apiVersion":"stackkit.home-host-security-policy/v1","kind":"HomeHostSecurityPolicy","contract":{"apply":"typed-local-operations","baselineVersion":"stackkit.host-security-baseline/1.0.0","credentials":"not-included","enforcedControls":["firewall.default_inbound","ssh.password_authentication","ssh.root_login","ssh.port","bruteforce.fail2ban","updates.unattended_upgrades","kernel.sysctl"],"managementPath":"never-closed","providerLifecycle":"not-owned","runtimeEnforcement":"adapter-verified","scope":"home-host-node","serverProviderAuthority":"not-owned"},"policy":{"bruteForceProtection":"fail2ban-sshd-jail","exceptions":"owner-approved-visible-expiring","firewall":{"defaultInbound":"drop","trustedSources":["lan-private-ranges","overlay-interfaces","container-bridges"]},"ssh":{"passwordAuthentication":"no","rootLogin":"no-or-key-only"}}}
`

// HomeHostSecurityPolicyBytes returns the exact policy artifact the Home host
// security owner accepts.
func HomeHostSecurityPolicyBytes() []byte { return []byte(homeHostSecurityTemplate) }

// HomeHostSecurityRendererContract returns the exact built-in identity of the
// Home host security policy. The hash binds the catalog contract to the
// rendered bytes.
func HomeHostSecurityRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(homeHostSecurityTemplate))
	return RendererContract{
		Kind: "native-config", RendererRef: homeHostSecurityRendererRef,
		TemplateRef: homeHostSecurityTemplateRef, Version: homeHostSecurityVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type homeHostSecurityRenderer struct {
	policy   []byte
	contract RendererContract
}

func newHomeHostSecurityRenderer() homeHostSecurityRenderer {
	return homeHostSecurityRenderer{policy: HomeHostSecurityPolicyBytes(), contract: HomeHostSecurityRendererContract()}
}

func (r homeHostSecurityRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateHomeHostSecurityUnit(unit, r.contract); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(r.policy)
	if "sha256:"+hex.EncodeToString(sum[:]) != r.contract.ContractHash || !json.Valid(r.policy) {
		return nil, fail(ErrOutputChanged, "renderer.home-host-security.policy", "embedded policy does not match its registered contract hash")
	}
	return []UnitOutput{{Ref: homeHostSecurityOutputRef, Bytes: append([]byte(nil), r.policy...)}}, nil
}

//nolint:gocyclo // Keep the exact fail-closed authority checks linear and auditable at this renderer boundary.
func validateHomeHostSecurityUnit(unit RenderUnit, contract RendererContract) error {
	path := "resolvedPlan.modules." + homeHostSecurityModuleID + ".renderUnits." + homeHostSecurityUnitID
	if unit.ModuleID() != homeHostSecurityModuleID || unit.ID() != homeHostSecurityUnitID {
		return fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", homeHostSecurityModuleID, homeHostSecurityUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Home host-security contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.RuntimeKind() != "host" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		unit.InstanceID() != homeHostSecurityUnitID+"-node-"+nodeRef || siteRef == "" {
		return fail(ErrInvalidPlan, path+".instances", "requires one exact node-local host/stackkit target")
	}
	if _, present := unit.RuntimeEngine(); present {
		return fail(ErrInvalidPlan, path+".runtime.engine", "runtime-engine authority is forbidden")
	}
	if _, present := unit.DaemonRef(); present {
		return fail(ErrInvalidPlan, path+".instances", "daemon authority is forbidden")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) {
		return fail(ErrInvalidPlan, path+".inputs", "the Home host-security policy is an input-free contract")
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return fail(ErrInvalidPlan, path+".interfaces", "service, network, socket and privileged-interface authority is forbidden")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
		DaemonRef   string `json:"daemonRef,omitempty"`
	}
	if err := json.Unmarshal(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" || placement.DaemonRef != "" {
		return fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	outputs := unit.DeclaredOutputs()
	if len(outputs) != 1 || outputs[0] != homeHostSecurityOutputRef {
		return fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", homeHostSecurityOutputRef)
	}
	return nil
}
