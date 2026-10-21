package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	runtipiRuntimeAdapterModuleID    = "stackkits-runtipi-runtime"
	runtipiRuntimeAdapterUnitID      = "runtipi-adapter"
	runtipiRuntimeAdapterRendererRef = "stackkit"
	runtipiRuntimeAdapterTemplateRef = "builtin://platform/runtipi/runtime-adapter/v1.json"
	runtipiRuntimeAdapterVersion     = "1.0.0"
	runtipiRuntimeAdapterOutputRef   = "platform/runtipi/runtime-adapter.json"
)

const runtipiRuntimeAdapterRendererSchema = `stackkit.runtime-adapter/v1|WorkloadRuntimeAdapter|runtipi|container:application-adapter|operations:apply,observe|provider-lifecycle:not-owned|credentials:local-owner|routes:artifact-bound|evidence:required`

type runtipiRuntimeAdapterBundle struct {
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

// RuntipiRuntimeAdapterRendererContract returns the exact implementation
// identity of the provider-free Runtipi workload-adapter handoff.
func RuntipiRuntimeAdapterRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(runtipiRuntimeAdapterRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: runtipiRuntimeAdapterRendererRef,
		TemplateRef: runtipiRuntimeAdapterTemplateRef, Version: runtipiRuntimeAdapterVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type runtipiRuntimeAdapterRenderer struct{ contract RendererContract }

func newRuntipiRuntimeAdapterRenderer() runtipiRuntimeAdapterRenderer {
	return runtipiRuntimeAdapterRenderer{contract: RuntipiRuntimeAdapterRendererContract()}
}

func (r runtipiRuntimeAdapterRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateRuntipiRuntimeAdapterUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.runtipi-runtime-adapter", "marshal governed adapter handoff", err)
	}
	return []UnitOutput{{Ref: runtipiRuntimeAdapterOutputRef, Bytes: append(data, '\n')}}, nil
}

//nolint:gocyclo // Every authority surface stays explicit at the adapter boundary.
func validateRuntipiRuntimeAdapterUnit(unit RenderUnit, contract RendererContract) (runtipiRuntimeAdapterBundle, error) {
	path := "resolvedPlan.modules." + runtipiRuntimeAdapterModuleID + ".renderUnits." + runtipiRuntimeAdapterUnitID
	if unit.ModuleID() != runtipiRuntimeAdapterModuleID || unit.ID() != runtipiRuntimeAdapterUnitID {
		return runtipiRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", runtipiRuntimeAdapterModuleID, runtipiRuntimeAdapterUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return runtipiRuntimeAdapterBundle{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Runtipi adapter contract")
	}
	if unit.RuntimeKind() != "control-plane" || unit.RuntimeDelivery() != "external-control-plane" {
		return runtipiRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".runtime", "Runtipi handoff requires exact control-plane/external-control-plane ownership")
	}
	if _, present := unit.RuntimeEngine(); present {
		return runtipiRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".runtime.engine", "handoff must not carry an execution engine")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return runtipiRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "Runtipi adapter handoff requires one exact node-local target")
	}
	if _, present := unit.DaemonRef(); present {
		return runtipiRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "adapter handoff receives no daemon authority")
	}
	if _, present := unit.DaemonInstanceRef(); present {
		return runtipiRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "adapter handoff receives no daemon instance")
	}
	if _, present := unit.DaemonSocketPath(); present {
		return runtipiRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "adapter handoff receives no daemon socket")
	}
	if _, present := unit.DaemonEngine(); present {
		return runtipiRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".instances", "adapter handoff receives no daemon engine")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || len(unit.PlanInputRefs()) != 0 || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) || !emptyJSONObject(unit.PlanInputsJSON()) || !emptyJSONArray(unit.InputBindingsJSON()) {
		return runtipiRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".inputs", "Runtipi adapter handoff accepts no caller, secret, or compiler input")
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) {
		return runtipiRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".interfaces", "adapter handoff receives no route, endpoint, network, interface, or socket authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return runtipiRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != runtipiRuntimeAdapterOutputRef {
		return runtipiRuntimeAdapterBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", runtipiRuntimeAdapterOutputRef)
	}

	bundle := runtipiRuntimeAdapterBundle{APIVersion: "stackkit.runtime-adapter/v1", Kind: "WorkloadRuntimeAdapter"}
	bundle.Adapter.ID = "runtipi"
	bundle.Adapter.ProviderRef = "stackkits-runtipi"
	bundle.Adapter.ModuleRef = runtipiRuntimeAdapterModuleID
	bundle.Adapter.Version = "1.0.0"
	bundle.Adapter.SupportedKinds = []string{"container"}
	bundle.Adapter.SupportedDeliveries = []string{"application-adapter"}
	bundle.Adapter.Operations = []string{"apply", "observe"}
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Inputs.ArtifactAPIVersions = []string{"stackkit.workload-bundle/v2"}
	bundle.Inputs.PublicValues = "artifact-bound-only"
	bundle.Inputs.SecretRefs = "opaque-artifact-references-only"
	bundle.Inputs.CredentialMaterial = "forbidden"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "local-owner"
	bundle.Ownership.Endpoints = "resolved-route-only"
	bundle.Ownership.Execution = "authenticated-local-node-adapter"
	bundle.Verification.HealthContractRef = "runtipi-runtime-contract"
	bundle.Verification.RequiredPhases = []string{"apply", "observe"}
	bundle.Verification.DigestBinding = true
	bundle.Verification.RuntimeReadback = true
	bundle.Verification.RouteReadback = true
	return bundle, nil
}
