package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	komodoCoreModuleID       = "stackkits-komodo-core-runtime"
	komodoCoreUnitID         = "komodo-core-adapter"
	komodoCoreTemplateRef    = "builtin://platform/komodo/core-runtime-adapter/v1.json"
	komodoCoreOutputRef      = "platform/komodo/core-runtime-adapter.json"
	komodoPeripheryModuleID  = "stackkits-komodo-periphery-runtime"
	komodoPeripheryUnitID    = "komodo-periphery-agent"
	komodoPeripheryTemplate  = "builtin://platform/komodo/periphery-agent/v1.json"
	komodoPeripheryOutputRef = "platform/komodo/periphery-agent.json"
	komodoRendererRef        = "stackkit"
	komodoContractVersion    = "1.0.0"
)

const komodoCoreRendererSchema = `stackkit.runtime-adapter/v1|WorkloadRuntimeAdapter|komodo|container:selected-paas|operations:apply,observe,rollback,backup,restore|provider-lifecycle:not-owned|credentials:external-owner|endpoints:not-included|agent:komodo-periphery|auth:external-mutual-key|evidence:required`
const komodoPeripheryRendererSchema = `stackkit.runtime-adapter-agent/v1|WorkloadRuntimeAdapterAgent|komodo-periphery|adapter:komodo|role:node-agent|target:control-authority-site-workers|direction:outbound-to-control-plane|transport:tls|minimum:TLS1.3|authentication:mutual-key|host-execution:executor-mediated|provider-lifecycle:not-owned|credentials:external-owner|endpoints:not-included|evidence:required`

type komodoTarget struct {
	SiteRef     string `json:"siteRef"`
	NodeRef     string `json:"nodeRef"`
	InstanceRef string `json:"instanceRef"`
}

type komodoClosedInputs struct {
	ArtifactAPIVersions []string `json:"artifactApiVersions"`
	PublicValues        string   `json:"publicValues"`
	SecretRefs          string   `json:"secretRefs"`
	CredentialMaterial  string   `json:"credentialMaterial"`
	EndpointMaterial    string   `json:"endpointMaterial"`
}

type komodoOwnership struct {
	ProviderLifecycle string `json:"providerLifecycle"`
	Credentials       string `json:"credentials"`
	Endpoints         string `json:"endpoints"`
	HostExecution     string `json:"hostExecution"`
	Execution         string `json:"execution"`
}

type komodoCoreBundle struct {
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
		AgentRefs           []string `json:"agentRefs"`
	} `json:"adapter"`
	Target         komodoTarget       `json:"target"`
	Inputs         komodoClosedInputs `json:"inputs"`
	Authentication struct {
		Mode              string `json:"mode"`
		CredentialCustody string `json:"credentialCustody"`
		CredentialPayload string `json:"credentialPayload"`
	} `json:"authentication"`
	Ownership    komodoOwnership `json:"ownership"`
	Verification struct {
		HealthContractRef         string   `json:"healthContractRef"`
		RequiredPhases            []string `json:"requiredPhases"`
		DigestBinding             bool     `json:"digestBinding"`
		RuntimeReadback           bool     `json:"runtimeReadback"`
		RouteReadback             bool     `json:"routeReadback"`
		AgentRegistrationReadback bool     `json:"agentRegistrationReadback"`
	} `json:"verification"`
}

type komodoPeripheryBundle struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Agent      struct {
		ID          string `json:"id"`
		AdapterRef  string `json:"adapterRef"`
		ProviderRef string `json:"providerRef"`
		ModuleRef   string `json:"moduleRef"`
		Role        string `json:"role"`
		TargetScope string `json:"targetScope"`
	} `json:"agent"`
	Target     komodoTarget       `json:"target"`
	Inputs     komodoClosedInputs `json:"inputs"`
	Connection struct {
		Direction         string `json:"direction"`
		Transport         string `json:"transport"`
		MinimumTLSVersion string `json:"minimumTlsVersion"`
		Authentication    string `json:"authentication"`
		EndpointPayload   string `json:"endpointPayload"`
	} `json:"connection"`
	Ownership    komodoOwnership `json:"ownership"`
	Verification struct {
		HealthContractRef   string   `json:"healthContractRef"`
		RequiredPhases      []string `json:"requiredPhases"`
		CoreTrustBinding    bool     `json:"coreTrustBinding"`
		NodeIdentityBinding bool     `json:"nodeIdentityBinding"`
		RuntimeReadback     bool     `json:"runtimeReadback"`
	} `json:"verification"`
}

func hashRendererSchema(schema string) string {
	sum := sha256.Sum256([]byte(schema))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// KomodoCoreRendererContract returns the exact Core API handoff identity.
func KomodoCoreRendererContract() RendererContract {
	return RendererContract{Kind: "native-config", RendererRef: komodoRendererRef, TemplateRef: komodoCoreTemplateRef, Version: komodoContractVersion, ContractHash: hashRendererSchema(komodoCoreRendererSchema)}
}

// KomodoPeripheryRendererContract returns the exact node-agent handoff identity.
func KomodoPeripheryRendererContract() RendererContract {
	return RendererContract{Kind: "native-config", RendererRef: komodoRendererRef, TemplateRef: komodoPeripheryTemplate, Version: komodoContractVersion, ContractHash: hashRendererSchema(komodoPeripheryRendererSchema)}
}

type komodoCoreRenderer struct{ contract RendererContract }
type komodoPeripheryRenderer struct{ contract RendererContract }

func newKomodoCoreRenderer() komodoCoreRenderer {
	return komodoCoreRenderer{contract: KomodoCoreRendererContract()}
}
func newKomodoPeripheryRenderer() komodoPeripheryRenderer {
	return komodoPeripheryRenderer{contract: KomodoPeripheryRendererContract()}
}

func (r komodoCoreRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := validateClosedKomodoUnit(unit, r.contract, komodoCoreModuleID, komodoCoreUnitID, komodoCoreOutputRef, "control-plane")
	if err != nil {
		return nil, err
	}
	bundle := komodoCoreBundle{APIVersion: "stackkit.runtime-adapter/v1", Kind: "WorkloadRuntimeAdapter", Target: target}
	bundle.Adapter.ID, bundle.Adapter.ProviderRef, bundle.Adapter.ModuleRef, bundle.Adapter.Version = "komodo", "stackkits-komodo", komodoCoreModuleID, komodoContractVersion
	bundle.Adapter.SupportedKinds = []string{"container"}
	bundle.Adapter.SupportedDeliveries = []string{"selected-paas"}
	bundle.Adapter.Operations = []string{"apply", "observe", "rollback", "backup", "restore"}
	bundle.Adapter.AgentRefs = []string{"komodo-periphery"}
	bundle.Inputs = closedKomodoInputs([]string{"stackkit.workload-bundle/v1"})
	bundle.Authentication.Mode, bundle.Authentication.CredentialCustody, bundle.Authentication.CredentialPayload = "external-mutual-key", "external-owner", "not-included"
	bundle.Ownership = closedKomodoOwnership("authenticated-external-adapter")
	bundle.Verification.HealthContractRef = "komodo-core-runtime-contract"
	bundle.Verification.RequiredPhases = []string{"authenticate", "apply", "observe", "rollback", "backup", "restore"}
	bundle.Verification.DigestBinding, bundle.Verification.RuntimeReadback, bundle.Verification.RouteReadback, bundle.Verification.AgentRegistrationReadback = true, true, true, true
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.komodo-core", "marshal governed Core handoff", err)
	}
	return []UnitOutput{{Ref: komodoCoreOutputRef, Bytes: append(data, '\n')}}, nil
}

func (r komodoPeripheryRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := validateClosedKomodoUnit(unit, r.contract, komodoPeripheryModuleID, komodoPeripheryUnitID, komodoPeripheryOutputRef, "host")
	if err != nil {
		return nil, err
	}
	bundle := komodoPeripheryBundle{APIVersion: "stackkit.runtime-adapter-agent/v1", Kind: "WorkloadRuntimeAdapterAgent", Target: target}
	bundle.Agent.ID, bundle.Agent.AdapterRef, bundle.Agent.ProviderRef, bundle.Agent.ModuleRef = "komodo-periphery", "komodo", "stackkits-komodo", komodoPeripheryModuleID
	bundle.Agent.Role, bundle.Agent.TargetScope = "node-agent", "control-authority-site-workers"
	bundle.Inputs = closedKomodoInputs(nil)
	bundle.Connection.Direction, bundle.Connection.Transport, bundle.Connection.MinimumTLSVersion = "outbound-to-control-plane", "tls", "TLS1.3"
	bundle.Connection.Authentication, bundle.Connection.EndpointPayload = "mutual-key", "not-included"
	bundle.Ownership = closedKomodoOwnership("authenticated-external-agent")
	bundle.Verification.HealthContractRef = "komodo-periphery-runtime-contract"
	bundle.Verification.RequiredPhases = []string{"authenticate", "register", "observe"}
	bundle.Verification.CoreTrustBinding, bundle.Verification.NodeIdentityBinding, bundle.Verification.RuntimeReadback = true, true, true
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.komodo-periphery", "marshal governed Periphery handoff", err)
	}
	return []UnitOutput{{Ref: komodoPeripheryOutputRef, Bytes: append(data, '\n')}}, nil
}

func closedKomodoInputs(apiVersions []string) komodoClosedInputs {
	if apiVersions == nil {
		apiVersions = []string{}
	}
	return komodoClosedInputs{ArtifactAPIVersions: apiVersions, PublicValues: "artifact-bound-only", SecretRefs: "opaque-external-owner-only", CredentialMaterial: "not-included", EndpointMaterial: "not-included"}
}

func closedKomodoOwnership(execution string) komodoOwnership {
	return komodoOwnership{ProviderLifecycle: "not-owned", Credentials: "external-owner", Endpoints: "not-included", HostExecution: "executor-mediated", Execution: execution}
}

//nolint:gocyclo // Every forbidden authority surface remains visible at the boundary.
func validateClosedKomodoUnit(unit RenderUnit, contract RendererContract, moduleID, unitID, outputRef, runtimeKind string) (komodoTarget, error) {
	path := "resolvedPlan.modules." + moduleID + ".renderUnits." + unitID
	if unit.ModuleID() != moduleID || unit.ID() != unitID {
		return komodoTarget{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", moduleID, unitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return komodoTarget{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Komodo contract")
	}
	if unit.RuntimeKind() != runtimeKind || unit.RuntimeDelivery() != "external-control-plane" {
		return komodoTarget{}, fail(ErrInvalidPlan, path+".runtime", "Komodo handoff requires exact %s/external-control-plane ownership", runtimeKind)
	}
	if _, present := unit.RuntimeEngine(); present {
		return komodoTarget{}, fail(ErrInvalidPlan, path+".runtime.engine", "handoff must not carry an execution engine")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return komodoTarget{}, fail(ErrInvalidPlan, path+".instances", "Komodo handoff requires one exact node-local target")
	}
	if _, present := unit.DaemonRef(); present {
		return komodoTarget{}, fail(ErrInvalidPlan, path+".instances", "handoff receives no daemon authority")
	}
	if _, present := unit.DaemonInstanceRef(); present {
		return komodoTarget{}, fail(ErrInvalidPlan, path+".instances", "handoff receives no daemon instance")
	}
	if _, present := unit.DaemonSocketPath(); present {
		return komodoTarget{}, fail(ErrInvalidPlan, path+".instances", "handoff receives no daemon socket")
	}
	if _, present := unit.DaemonEngine(); present {
		return komodoTarget{}, fail(ErrInvalidPlan, path+".instances", "handoff receives no daemon engine")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || len(unit.PlanInputRefs()) != 0 || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) || !emptyJSONObject(unit.PlanInputsJSON()) || !emptyJSONArray(unit.InputBindingsJSON()) {
		return komodoTarget{}, fail(ErrInvalidPlan, path+".inputs", "Komodo handoff accepts no caller, secret, or compiler input")
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) {
		return komodoTarget{}, fail(ErrInvalidPlan, path+".interfaces", "Komodo handoff receives no route, endpoint, network, interface, or socket authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return komodoTarget{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != outputRef {
		return komodoTarget{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", outputRef)
	}
	return komodoTarget{SiteRef: siteRef, NodeRef: nodeRef, InstanceRef: unit.InstanceID()}, nil
}
