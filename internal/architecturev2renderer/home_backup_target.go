package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	homeBackupTargetModuleID    = "stackkits-home-backup-target"
	homeBackupTargetUnitID      = "backup-policy"
	homeBackupTargetRendererRef = "stackkit"
	homeBackupTargetTemplateRef = "builtin://home/backup-target/v1.json"
	homeBackupTargetVersion     = "1.0.0"
	homeBackupTargetOutputRef   = "home/backup/target-policy.json"
	homeBackupTargetToken       = "@@POLICY@@"
)

const homeBackupTargetTemplate = `{"apiVersion":"stackkit.home-backup-target-policy/v1","kind":"HomeBackupTargetPolicy","contract":{"networkAccess":"none","operations":["observe-backup-directory"],"providerLifecycle":"not-owned","scope":"home-control-plane-node"},"policy":@@POLICY@@}
`

var homeBackupTargetPlanInputRefs = []string{
	"kit", "moduleCapabilities", "moduleTargets", "sites", "stackId",
}

var homeBackupTargetPublicInputRefs = []string{"backup-root"}

// HomeBackupTargetRendererContract returns the exact implementation identity
// for the node-local Home backup-target observation policy.
func HomeBackupTargetRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(homeBackupTargetTemplate))
	return RendererContract{
		Kind: "native-config", RendererRef: homeBackupTargetRendererRef,
		TemplateRef: homeBackupTargetTemplateRef, Version: homeBackupTargetVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type homeBackupTargetRenderer struct {
	template []byte
	contract RendererContract
}

func newHomeBackupTargetRenderer() homeBackupTargetRenderer {
	return homeBackupTargetRenderer{
		template: []byte(homeBackupTargetTemplate),
		contract: HomeBackupTargetRendererContract(),
	}
}

func (r homeBackupTargetRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy, err := validateHomeBackupTargetUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.home-backup-target.policy", "marshal typed node-local policy", err)
	}
	if coreHostBootstrapTemplateHash(r.template) != r.contract.ContractHash || bytes.Count(r.template, []byte(homeBackupTargetToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.home-backup-target.template", "embedded backup policy does not match its registered contract")
	}
	output := bytes.Replace(r.template, []byte(homeBackupTargetToken), canonical, 1)
	return []UnitOutput{{Ref: homeBackupTargetOutputRef, Bytes: output}}, nil
}

type homeBackupTargetPlanInputs struct {
	StackID            string                     `json:"stackId"`
	Kit                executorBundleKit          `json:"kit"`
	Sites              []executorBundleSite       `json:"sites"`
	ModuleTargets      []executorBundleTarget     `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability `json:"moduleCapabilities"`
}

type homeBackupRootInput struct {
	Path         string `json:"path"`
	VolumeDriver string `json:"volumeDriver"`
}

type homeBackupTargetValues struct {
	BackupRoot homeBackupRootInput `json:"backup-root"`
}

type homeBackupTargetPolicy struct {
	StackID string `json:"stackId"`
	Kit     struct {
		Slug           string `json:"slug"`
		Version        string `json:"version"`
		DefinitionHash string `json:"definitionHash"`
	} `json:"kit"`
	Target struct {
		SiteRef string `json:"siteRef"`
		NodeRef string `json:"nodeRef"`
	} `json:"target"`
	Directory struct {
		Path    string `json:"path"`
		Mode    string `json:"mode"`
		Purpose string `json:"purpose"`
	} `json:"directory"`
}

//nolint:gocyclo // This is a closed CUE-to-host authority boundary.
func validateHomeBackupTargetUnit(unit RenderUnit, contract RendererContract) (homeBackupTargetPolicy, error) {
	path := "resolvedPlan.modules." + homeBackupTargetModuleID + ".renderUnits." + homeBackupTargetUnitID
	if unit.ModuleID() != homeBackupTargetModuleID || unit.ID() != homeBackupTargetUnitID {
		return homeBackupTargetPolicy{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", homeBackupTargetModuleID, homeBackupTargetUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return homeBackupTargetPolicy{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Home backup-target contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.RuntimeKind() != "host" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "node-local" || !hasSite || !hasNode {
		return homeBackupTargetPolicy{}, fail(ErrInvalidPlan, path+".instances", "Home backup target requires one exact node-local host/stackkit target")
	}
	if _, present := unit.RuntimeEngine(); present {
		return homeBackupTargetPolicy{}, fail(ErrInvalidPlan, path+".runtime.engine", "Home backup observation has no runtime-engine authority")
	}
	if _, present := unit.DaemonRef(); present {
		return homeBackupTargetPolicy{}, fail(ErrInvalidPlan, path+".instances", "Home backup observation must not receive daemon authority")
	}
	if !exactStringList(unit.PublicInputRefs(), homeBackupTargetPublicInputRefs) || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) {
		return homeBackupTargetPolicy{}, fail(ErrInvalidPlan, path+".inputs", "Home backup target accepts only its exact typed compiler-owned input")
	}
	if !exactStringList(unit.PlanInputRefs(), homeBackupTargetPlanInputRefs) {
		return homeBackupTargetPolicy{}, fail(ErrInvalidPlan, path+".planInputRefs", "must exactly match the registered Home backup-target projection")
	}
	if err := validateHomeBackupTargetBinding(unit.InputBindingsJSON(), path+".inputBindings"); err != nil {
		return homeBackupTargetPolicy{}, err
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return homeBackupTargetPolicy{}, fail(ErrInvalidPlan, path+".interfaces", "Home backup target has no service, network, socket, or privileged-interface authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return homeBackupTargetPolicy{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != homeBackupTargetOutputRef {
		return homeBackupTargetPolicy{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", homeBackupTargetOutputRef)
	}

	var inputs homeBackupTargetPlanInputs
	if err := decodeStrict(unit.PlanInputsJSON(), &inputs); err != nil {
		return homeBackupTargetPolicy{}, wrap(ErrInvalidPlan, path+".planInputs", "decode exact Home backup-target inputs", err)
	}
	if err := requireContractID(inputs.StackID, path+".planInputs.stackId"); err != nil {
		return homeBackupTargetPolicy{}, err
	}
	if (inputs.Kit.Slug != "basement-kit" && inputs.Kit.Slug != "modern-homelab") || inputs.Kit.Version == "" || !validSHA256(inputs.Kit.DefinitionHash) {
		return homeBackupTargetPolicy{}, fail(ErrInvalidPlan, path+".planInputs.kit", "requires an exact Home-capable governed kit identity")
	}
	if len(inputs.ModuleCapabilities) != 1 || inputs.ModuleCapabilities[0].ID != "local-backup-target" || !validSHA256(inputs.ModuleCapabilities[0].ContractHash) {
		return homeBackupTargetPolicy{}, fail(ErrInvalidPlan, path+".planInputs.moduleCapabilities", "module must own only the exact local-backup-target capability")
	}
	var values homeBackupTargetValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return homeBackupTargetPolicy{}, wrap(ErrInvalidPlan, path+".values", "decode exact Home backup-target bound input", err)
	}
	backupRoot, err := validateHomeBackupRoot(values.BackupRoot, path+".values.backup-root")
	if err != nil {
		return homeBackupTargetPolicy{}, err
	}
	if err := validateCoreHostBootstrapTarget(inputs.Sites, inputs.ModuleTargets, siteRef, nodeRef, path+".planInputs"); err != nil {
		return homeBackupTargetPolicy{}, err
	}
	return newHomeBackupTargetPolicy(inputs, backupRoot, siteRef, nodeRef), nil
}

func validateHomeBackupTargetBinding(raw []byte, path string) error {
	var bindings []rawModuleRenderInputBinding
	if err := decodeStrict(raw, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path, "decode Home backup-target input binding", err)
	}
	if len(bindings) != 1 {
		return fail(ErrInvalidPlan, path, "requires exactly one governed backup-root binding")
	}
	binding := bindings[0]
	if binding.TargetRef != "backup-root" || binding.SourceRef != "storage.backupRoot" ||
		binding.ValueType != "local-backup-root-v1" || binding.Cardinality != "single" ||
		!binding.Required || len(binding.DefaultValue) != 0 {
		return fail(ErrInvalidPlan, path, "does not match the governed backup-root binding")
	}
	return nil
}

func decodeHomeBackupRoot(raw json.RawMessage, path string) (homeBackupRootInput, error) {
	var value homeBackupRootInput
	if err := decodeStrict(raw, &value); err != nil {
		return value, wrap(ErrInvalidPlan, path, "decode local backup root", err)
	}
	return validateHomeBackupRoot(value, path)
}

func validateHomeBackupRoot(value homeBackupRootInput, path string) (homeBackupRootInput, error) {
	if value.VolumeDriver != "local" || !safeCoreHostBootstrapStoragePath(value.Path) {
		return value, fail(ErrInvalidPlan, path, "requires one safe host-local backup root prepared by Core")
	}
	return value, nil
}

func newHomeBackupTargetPolicy(inputs homeBackupTargetPlanInputs, backupRoot homeBackupRootInput, siteRef, nodeRef string) homeBackupTargetPolicy {
	policy := homeBackupTargetPolicy{StackID: inputs.StackID}
	policy.Kit.Slug = inputs.Kit.Slug
	policy.Kit.Version = inputs.Kit.Version
	policy.Kit.DefinitionHash = inputs.Kit.DefinitionHash
	policy.Target.SiteRef = siteRef
	policy.Target.NodeRef = nodeRef
	policy.Directory.Path = backupRoot.Path
	policy.Directory.Mode = "0750"
	policy.Directory.Purpose = "backup"
	return policy
}
