package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	standaloneComposeRuntimeAdapterModuleID    = "stackkits-standalone-compose-runtime"
	standaloneComposeRuntimeAdapterUnitID      = "standalone-compose-adapter"
	standaloneComposeRuntimeAdapterRendererRef = "stackkit"
	standaloneComposeRuntimeAdapterTemplateRef = "builtin://platform/standalone-compose/runtime-adapter/v1.json"
	standaloneComposeRuntimeAdapterVersion     = "1.0.0"
	standaloneComposeRuntimeAdapterOutputRef   = "platform/standalone-compose/runtime-adapter.json"
)

const standaloneComposeRuntimeAdapterRendererSchema = `stackkit.runtime-adapter/v1|WorkloadRuntimeAdapter|standalone-compose|container:application-adapter|operations:apply,observe|provider-lifecycle:not-owned|credentials:local-owner|routes:artifact-bound|evidence:required`

// StandaloneComposeRuntimeAdapterRendererContract returns the immutable
// implementation identity of the StackKits-owned no-PaaS adapter.
func StandaloneComposeRuntimeAdapterRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(standaloneComposeRuntimeAdapterRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: standaloneComposeRuntimeAdapterRendererRef,
		TemplateRef: standaloneComposeRuntimeAdapterTemplateRef, Version: standaloneComposeRuntimeAdapterVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type standaloneComposeRuntimeAdapterRenderer struct{ contract RendererContract }

func newStandaloneComposeRuntimeAdapterRenderer() standaloneComposeRuntimeAdapterRenderer {
	return standaloneComposeRuntimeAdapterRenderer{contract: StandaloneComposeRuntimeAdapterRendererContract()}
}

func (r standaloneComposeRuntimeAdapterRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateStandaloneComposeRuntimeAdapterUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.standalone-compose-runtime-adapter", "marshal governed adapter contract", err)
	}
	return []UnitOutput{{Ref: standaloneComposeRuntimeAdapterOutputRef, Bytes: append(data, '\n')}}, nil
}

//nolint:gocyclo // The adapter authority boundary remains explicit and fail closed.
func validateStandaloneComposeRuntimeAdapterUnit(unit RenderUnit, contract RendererContract) (coolifyRuntimeAdapterBundle, error) {
	path := "resolvedPlan.modules." + standaloneComposeRuntimeAdapterModuleID + ".renderUnits." + standaloneComposeRuntimeAdapterUnitID
	if unit.ModuleID() != standaloneComposeRuntimeAdapterModuleID || unit.ID() != standaloneComposeRuntimeAdapterUnitID {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", standaloneComposeRuntimeAdapterModuleID, standaloneComposeRuntimeAdapterUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return coolifyRuntimeAdapterBundle{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered standalone Compose adapter contract")
	}
	if unit.RuntimeKind() != "host" || unit.RuntimeDelivery() != "stackkit" {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".runtime", "standalone Compose adapter requires exact host/stackkit ownership")
	}
	if _, present := unit.RuntimeEngine(); present {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".runtime.engine", "adapter contract must not invent an execution engine")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "standalone Compose adapter requires one exact node-local target")
	}
	if _, present := unit.DaemonRef(); present {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "adapter contract receives no raw daemon authority")
	}
	if _, present := unit.DaemonInstanceRef(); present {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "adapter contract receives no daemon instance")
	}
	if _, present := unit.DaemonSocketPath(); present {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "adapter contract receives no daemon socket")
	}
	if _, present := unit.DaemonEngine(); present {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "adapter contract receives no daemon engine")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || len(unit.PlanInputRefs()) != 0 ||
		!emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) ||
		!emptyJSONObject(unit.PlanInputsJSON()) || !emptyJSONArray(unit.InputBindingsJSON()) {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".inputs", "adapter contract accepts no caller, secret, or compiler input")
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) ||
		!emptyJSONArray(unit.RequiredInterfacesJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".interfaces", "adapter contract receives no workload route, interface, network, or socket authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != standaloneComposeRuntimeAdapterOutputRef {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", standaloneComposeRuntimeAdapterOutputRef)
	}

	bundle := coolifyRuntimeAdapterBundle{APIVersion: "stackkit.runtime-adapter/v1", Kind: "WorkloadRuntimeAdapter"}
	bundle.Adapter.ID = "standalone-compose"
	bundle.Adapter.ProviderRef = "stackkits-standalone-compose"
	bundle.Adapter.ModuleRef = standaloneComposeRuntimeAdapterModuleID
	bundle.Adapter.Version = "1.0.0"
	bundle.Adapter.SupportedKinds = []string{"container"}
	bundle.Adapter.SupportedDeliveries = []string{"application-adapter"}
	bundle.Adapter.Operations = []string{"apply", "observe"}
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Inputs.ArtifactAPIVersions = []string{"stackkit.workload-bundle/v2"}
	bundle.Inputs.PublicValues = "artifact-bound-only"
	bundle.Inputs.SecretRefs = "local-owner-custody-only"
	bundle.Inputs.CredentialMaterial = "forbidden-in-artifact"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "local-owner"
	bundle.Ownership.Endpoints = "resolved-route-only"
	bundle.Ownership.Execution = "stackkits-standalone-compose"
	bundle.Verification.HealthContractRef = "standalone-compose-runtime-contract"
	bundle.Verification.RequiredPhases = []string{"apply", "observe"}
	bundle.Verification.DigestBinding = true
	bundle.Verification.RuntimeReadback = true
	bundle.Verification.RouteReadback = true
	return bundle, nil
}
