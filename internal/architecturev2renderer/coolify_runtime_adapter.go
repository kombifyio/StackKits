package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	coolifyRuntimeAdapterModuleID    = "stackkits-coolify-runtime"
	coolifyRuntimeAdapterUnitID      = "coolify-adapter"
	coolifyRuntimeAdapterRendererRef = "stackkit"
	coolifyRuntimeAdapterTemplateRef = "builtin://platform/coolify/runtime-adapter/v1.json"
	coolifyRuntimeAdapterVersion     = "1.0.0"
	coolifyRuntimeAdapterOutputRef   = "platform/coolify/runtime-adapter.json"
)

const coolifyRuntimeAdapterRendererSchema = `stackkit.runtime-adapter/v1|WorkloadRuntimeAdapter|coolify|container:selected-paas|operations:apply,observe,rollback|provider-lifecycle:not-owned|credentials:external-owner|endpoints:not-included|evidence:required`

type coolifyRuntimeAdapterBundle struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Adapter    struct {
		ID                  string   `json:"id"`
		ProviderRef         string   `json:"providerRef"`
		ModuleRef           string   `json:"moduleRef"`
		Version             string   `json:"version"`
		SupportedKinds      []string `json:"supportedKinds"`
		SupportedDeliveries []string `json:"supportedDeliveries"`
		Operations          []string `json:"operations"`
	} `json:"adapter"`
	Target struct {
		SiteRef     string `json:"siteRef"`
		NodeRef     string `json:"nodeRef"`
		InstanceRef string `json:"instanceRef"`
	} `json:"target"`
	Inputs struct {
		ArtifactAPIVersions []string `json:"artifactApiVersions"`
		PublicValues        string   `json:"publicValues"`
		SecretRefs          string   `json:"secretRefs"`
		CredentialMaterial  string   `json:"credentialMaterial"`
	} `json:"inputs"`
	Ownership struct {
		ProviderLifecycle string `json:"providerLifecycle"`
		Credentials       string `json:"credentials"`
		Endpoints         string `json:"endpoints"`
		Execution         string `json:"execution"`
	} `json:"ownership"`
	Verification struct {
		HealthContractRef string   `json:"healthContractRef"`
		RequiredPhases    []string `json:"requiredPhases"`
		DigestBinding     bool     `json:"digestBinding"`
		RuntimeReadback   bool     `json:"runtimeReadback"`
		RouteReadback     bool     `json:"routeReadback"`
	} `json:"verification"`
}

// CoolifyRuntimeAdapterRendererContract returns the exact implementation
// identity of the provider-free Coolify workload-adapter handoff.
func CoolifyRuntimeAdapterRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(coolifyRuntimeAdapterRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: coolifyRuntimeAdapterRendererRef,
		TemplateRef: coolifyRuntimeAdapterTemplateRef, Version: coolifyRuntimeAdapterVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type coolifyRuntimeAdapterRenderer struct{ contract RendererContract }

func newCoolifyRuntimeAdapterRenderer() coolifyRuntimeAdapterRenderer {
	return coolifyRuntimeAdapterRenderer{contract: CoolifyRuntimeAdapterRendererContract()}
}

func (r coolifyRuntimeAdapterRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateCoolifyRuntimeAdapterUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.coolify-runtime-adapter", "marshal governed adapter handoff", err)
	}
	return []UnitOutput{{Ref: coolifyRuntimeAdapterOutputRef, Bytes: append(data, '\n')}}, nil
}

//nolint:gocyclo // Every authority surface stays explicit at the adapter boundary.
func validateCoolifyRuntimeAdapterUnit(unit RenderUnit, contract RendererContract) (coolifyRuntimeAdapterBundle, error) {
	path := "resolvedPlan.modules." + coolifyRuntimeAdapterModuleID + ".renderUnits." + coolifyRuntimeAdapterUnitID
	if unit.ModuleID() != coolifyRuntimeAdapterModuleID || unit.ID() != coolifyRuntimeAdapterUnitID {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", coolifyRuntimeAdapterModuleID, coolifyRuntimeAdapterUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return coolifyRuntimeAdapterBundle{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Coolify adapter contract")
	}
	if unit.RuntimeKind() != "control-plane" || unit.RuntimeDelivery() != "external-control-plane" {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".runtime", "Coolify handoff requires exact control-plane/external-control-plane ownership")
	}
	if _, present := unit.RuntimeEngine(); present {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".runtime.engine", "handoff must not carry an execution engine")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "Coolify adapter handoff requires one exact node-local target")
	}
	if _, present := unit.DaemonRef(); present {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "adapter handoff receives no daemon authority")
	}
	if _, present := unit.DaemonInstanceRef(); present {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "adapter handoff receives no daemon instance")
	}
	if _, present := unit.DaemonSocketPath(); present {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "adapter handoff receives no daemon socket")
	}
	if _, present := unit.DaemonEngine(); present {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "adapter handoff receives no daemon engine")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || len(unit.PlanInputRefs()) != 0 || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) || !emptyJSONObject(unit.PlanInputsJSON()) || !emptyJSONArray(unit.InputBindingsJSON()) {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".inputs", "Coolify adapter handoff accepts no caller, secret, or compiler input")
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".interfaces", "adapter handoff receives no route, endpoint, network, interface, or socket authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != coolifyRuntimeAdapterOutputRef {
		return coolifyRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", coolifyRuntimeAdapterOutputRef)
	}

	bundle := coolifyRuntimeAdapterBundle{APIVersion: "stackkit.runtime-adapter/v1", Kind: "WorkloadRuntimeAdapter"}
	bundle.Adapter.ID = "coolify"
	bundle.Adapter.ProviderRef = "stackkits-coolify"
	bundle.Adapter.ModuleRef = coolifyRuntimeAdapterModuleID
	bundle.Adapter.Version = "1.0.0"
	bundle.Adapter.SupportedKinds = []string{"container"}
	bundle.Adapter.SupportedDeliveries = []string{"selected-paas"}
	bundle.Adapter.Operations = []string{"apply", "observe", "rollback"}
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Inputs.ArtifactAPIVersions = []string{"stackkit.workload-bundle/v1"}
	bundle.Inputs.PublicValues = "artifact-bound-only"
	bundle.Inputs.SecretRefs = "opaque-artifact-references-only"
	bundle.Inputs.CredentialMaterial = "forbidden"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "external-owner"
	bundle.Ownership.Endpoints = "not-included"
	bundle.Ownership.Execution = "authenticated-external-adapter"
	bundle.Verification.HealthContractRef = "coolify-runtime-contract"
	bundle.Verification.RequiredPhases = []string{"apply", "observe", "rollback"}
	bundle.Verification.DigestBinding = true
	bundle.Verification.RuntimeReadback = true
	bundle.Verification.RouteReadback = true
	return bundle, nil
}
