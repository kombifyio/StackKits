package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// selectedPaaSAdapterSpec names one provider-free Layer-2 workload-adapter
// handoff. Coolify and Dokploy run the same authority checks over a node-local
// unit; only their identities, operations and custody wording differ.
type selectedPaaSAdapterSpec struct {
	moduleID, unitID, templateRef, outputRef string
	adapterID, providerRef, displayName      string
	schema                                   string
	operations                               []string
	credentials, execution, healthRef        string
}

type selectedPaaSAdapterBundle struct {
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

func (s selectedPaaSAdapterSpec) contract() RendererContract {
	sum := sha256.Sum256([]byte(s.schema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: s.templateRef, Version: "1.0.0",
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type selectedPaaSAdapterRenderer struct {
	spec     selectedPaaSAdapterSpec
	contract RendererContract
}

func newSelectedPaaSAdapterRenderer(spec selectedPaaSAdapterSpec) selectedPaaSAdapterRenderer {
	return selectedPaaSAdapterRenderer{spec: spec, contract: spec.contract()}
}

func (r selectedPaaSAdapterRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateSelectedPaaSAdapterUnit(unit, r.spec, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer."+r.spec.adapterID+"-runtime-adapter", "marshal governed adapter handoff", err)
	}
	return []UnitOutput{{Ref: r.spec.outputRef, Bytes: append(data, '\n')}}, nil
}

//nolint:gocyclo // Every authority surface stays explicit at the adapter boundary.
func validateSelectedPaaSAdapterUnit(unit RenderUnit, spec selectedPaaSAdapterSpec, contract RendererContract) (selectedPaaSAdapterBundle, error) {
	var empty selectedPaaSAdapterBundle
	path := "resolvedPlan.modules." + spec.moduleID + ".renderUnits." + spec.unitID
	if unit.ModuleID() != spec.moduleID || unit.ID() != spec.unitID {
		return empty, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", spec.moduleID, spec.unitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return empty, fail(ErrOutputChanged, path, "render-unit identity differs from the registered %s adapter contract", spec.displayName)
	}
	if unit.RuntimeKind() != "control-plane" || unit.RuntimeDelivery() != "external-control-plane" {
		return empty, fail(ErrInvalidPlan, path+".runtime", "%s handoff requires exact control-plane/external-control-plane ownership", spec.displayName)
	}
	if _, present := unit.RuntimeEngine(); present {
		return empty, fail(ErrInvalidPlan, path+".runtime.engine", "handoff must not carry an execution engine")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return empty, fail(ErrInvalidPlan, path+".instances", "%s adapter handoff requires one exact node-local target", spec.displayName)
	}
	if _, present := unit.DaemonRef(); present {
		return empty, fail(ErrInvalidPlan, path+".instances", "adapter handoff receives no daemon authority")
	}
	if _, present := unit.DaemonInstanceRef(); present {
		return empty, fail(ErrInvalidPlan, path+".instances", "adapter handoff receives no daemon instance")
	}
	if _, present := unit.DaemonSocketPath(); present {
		return empty, fail(ErrInvalidPlan, path+".instances", "adapter handoff receives no daemon socket")
	}
	if _, present := unit.DaemonEngine(); present {
		return empty, fail(ErrInvalidPlan, path+".instances", "adapter handoff receives no daemon engine")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || len(unit.PlanInputRefs()) != 0 || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) || !emptyJSONObject(unit.PlanInputsJSON()) || !emptyJSONArray(unit.InputBindingsJSON()) {
		return empty, fail(ErrInvalidPlan, path+".inputs", "%s adapter handoff accepts no caller, secret, or compiler input", spec.displayName)
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) {
		return empty, fail(ErrInvalidPlan, path+".interfaces", "adapter handoff receives no route, endpoint, network, interface, or socket authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return empty, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != spec.outputRef {
		return empty, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", spec.outputRef)
	}

	bundle := selectedPaaSAdapterBundle{APIVersion: "stackkit.runtime-adapter/v1", Kind: "WorkloadRuntimeAdapter"}
	bundle.Adapter.ID = spec.adapterID
	bundle.Adapter.ProviderRef = spec.providerRef
	bundle.Adapter.ModuleRef = spec.moduleID
	bundle.Adapter.Version = "1.0.0"
	bundle.Adapter.SupportedKinds = []string{"container"}
	bundle.Adapter.SupportedDeliveries = []string{"application-adapter"}
	bundle.Adapter.Operations = append([]string(nil), spec.operations...)
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Inputs.ArtifactAPIVersions = []string{"stackkit.workload-bundle/v2"}
	bundle.Inputs.PublicValues = "artifact-bound-only"
	bundle.Inputs.SecretRefs = "opaque-artifact-references-only"
	bundle.Inputs.CredentialMaterial = "forbidden"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = spec.credentials
	bundle.Ownership.Endpoints = "resolved-route-only"
	bundle.Ownership.Execution = spec.execution
	bundle.Verification.HealthContractRef = spec.healthRef
	bundle.Verification.RequiredPhases = []string{"apply", "observe"}
	bundle.Verification.DigestBinding = true
	bundle.Verification.RuntimeReadback = true
	bundle.Verification.RouteReadback = true
	return bundle, nil
}
