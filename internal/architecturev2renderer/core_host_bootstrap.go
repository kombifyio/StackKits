package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"sort"
	"strings"
)

const (
	coreHostBootstrapModuleID    = "stackkits-core-host-bootstrap"
	coreHostBootstrapUnitID      = "host-policy"
	coreHostBootstrapRendererRef = "stackkit"
	coreHostBootstrapTemplateRef = "builtin://foundation/host-bootstrap/v1.json"
	coreHostBootstrapVersion     = "1.0.0"
	coreHostBootstrapOutputRef   = "foundation/host-bootstrap/policy.json"
	coreHostBootstrapToken       = "@@POLICY@@"
)

const coreHostBootstrapTemplate = `{"apiVersion":"stackkit.core-host-bootstrap-policy/v1","kind":"CoreHostBootstrapPolicy","contract":{"networkAccess":"none","operations":["ensure-storage-directories","observe-existing-runtime"],"providerLifecycle":"not-owned","scope":"node-local"},"policy":@@POLICY@@}
`

var coreHostBootstrapPlanInputRefs = []string{
	"hostRuntimePolicy", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId", "storagePolicy",
}

// CoreHostBootstrapRendererContract returns the exact implementation identity
// for the provider-free, node-local Core host preparation policy.
func CoreHostBootstrapRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(coreHostBootstrapTemplate))
	return RendererContract{
		Kind: "native-config", RendererRef: coreHostBootstrapRendererRef,
		TemplateRef: coreHostBootstrapTemplateRef, Version: coreHostBootstrapVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type coreHostBootstrapRenderer struct {
	template []byte
	contract RendererContract
}

func newCoreHostBootstrapRenderer() coreHostBootstrapRenderer {
	return coreHostBootstrapRenderer{
		template: []byte(coreHostBootstrapTemplate),
		contract: CoreHostBootstrapRendererContract(),
	}
}

func (r coreHostBootstrapRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy, err := validateCoreHostBootstrapUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.core-host-bootstrap.policy", "marshal typed node-local policy", err)
	}
	if coreHostBootstrapTemplateHash(r.template) != r.contract.ContractHash || bytes.Count(r.template, []byte(coreHostBootstrapToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.core-host-bootstrap.template", "embedded host policy does not match its registered contract")
	}
	output := bytes.Replace(r.template, []byte(coreHostBootstrapToken), canonical, 1)
	return []UnitOutput{{Ref: coreHostBootstrapOutputRef, Bytes: output}}, nil
}

func coreHostBootstrapTemplateHash(template []byte) string {
	sum := sha256.Sum256(template)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type coreHostBootstrapPlanInputs struct {
	StackID            string                          `json:"stackId"`
	Kit                executorBundleKit               `json:"kit"`
	Sites              []executorBundleSite            `json:"sites"`
	ModuleTargets      []executorBundleTarget          `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability      `json:"moduleCapabilities"`
	HostRuntimePolicy  executorBundleHostRuntimePolicy `json:"hostRuntimePolicy"`
	StoragePolicy      executorBundleStoragePolicy     `json:"storagePolicy"`
}

type coreHostBootstrapPolicy struct {
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
	Runtime struct {
		InstallMode string `json:"installMode"`
		Runtime     string `json:"runtime"`
		Engine      string `json:"engine"`
		DataRoot    string `json:"dataRoot"`
	} `json:"runtime"`
	Directories []coreHostBootstrapDirectory `json:"directories"`
}

type coreHostBootstrapDirectory struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Purpose string `json:"purpose"`
}

//nolint:gocyclo // This is the closed CUE-to-host authority boundary and every authority class stays explicit.
func validateCoreHostBootstrapUnit(unit RenderUnit, contract RendererContract) (coreHostBootstrapPolicy, error) {
	path := "resolvedPlan.modules." + coreHostBootstrapModuleID + ".renderUnits." + coreHostBootstrapUnitID
	if unit.ModuleID() != coreHostBootstrapModuleID || unit.ID() != coreHostBootstrapUnitID {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", coreHostBootstrapModuleID, coreHostBootstrapUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return coreHostBootstrapPolicy{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Core host-bootstrap contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.RuntimeKind() != "host" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "node-local" || !hasSite || !hasNode {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".instances", "Core host bootstrap requires one exact node-local host/stackkit target")
	}
	if _, present := unit.RuntimeEngine(); present {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".runtime.engine", "runtime engine authority belongs to the typed policy, not the module execution envelope")
	}
	if _, present := unit.DaemonRef(); present {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".instances", "Core host bootstrap must not receive daemon authority")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".inputs", "Core host bootstrap accepts only its closed compiler-owned projection")
	}
	if !exactStringList(unit.PlanInputRefs(), coreHostBootstrapPlanInputRefs) {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".planInputRefs", "must exactly match the registered Core host-bootstrap projection")
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".interfaces", "Core host bootstrap has no service, network, socket, or privileged-interface authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != coreHostBootstrapOutputRef {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", coreHostBootstrapOutputRef)
	}

	var inputs coreHostBootstrapPlanInputs
	if err := decodeStrict(unit.PlanInputsJSON(), &inputs); err != nil {
		return coreHostBootstrapPolicy{}, wrap(ErrInvalidPlan, path+".planInputs", "decode exact Core host-bootstrap inputs", err)
	}
	if err := requireContractID(inputs.StackID, path+".planInputs.stackId"); err != nil {
		return coreHostBootstrapPolicy{}, err
	}
	if inputs.Kit.Version == "" || !validSHA256(inputs.Kit.DefinitionHash) {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".planInputs.kit", "requires an exact governed kit identity")
	}
	if inputs.Kit.Slug != "basement-kit" && inputs.Kit.Slug != "cloud-kit" && inputs.Kit.Slug != "modern-homelab" {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".planInputs.kit.slug", "kit is outside the Core host-bootstrap contract")
	}
	if len(inputs.ModuleCapabilities) != 1 || inputs.ModuleCapabilities[0].ID != "host-bootstrap" || !validSHA256(inputs.ModuleCapabilities[0].ContractHash) {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".planInputs.moduleCapabilities", "module must own only the exact host-bootstrap capability")
	}
	if err := validateHostRuntimePolicy(inputs.HostRuntimePolicy, path+".planInputs.hostRuntimePolicy"); err != nil {
		return coreHostBootstrapPolicy{}, err
	}
	if inputs.HostRuntimePolicy.Install.Mode != "bootstrapped" || inputs.HostRuntimePolicy.Install.Runtime != "docker" || inputs.HostRuntimePolicy.Container == nil || inputs.HostRuntimePolicy.Container.Engine != "docker" {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".planInputs.hostRuntimePolicy", "v1 applies only to an already bootstrapped Docker host")
	}
	if err := validateStoragePolicy(inputs.StoragePolicy, path+".planInputs.storagePolicy"); err != nil {
		return coreHostBootstrapPolicy{}, err
	}
	if inputs.StoragePolicy.VolumeDriver != "local" || inputs.StoragePolicy.External != nil || inputs.StoragePolicy.NFS != nil {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".planInputs.storagePolicy", "v1 prepares only host-local StackKit storage roots")
	}
	for field, storagePath := range map[string]string{
		"dataRoot": inputs.StoragePolicy.DataRoot, "backupRoot": inputs.StoragePolicy.BackupRoot,
		"stacksRoot": inputs.StoragePolicy.StacksRoot, "mediaRoot": inputs.StoragePolicy.MediaRoot,
	} {
		if storagePath != "" && !safeCoreHostBootstrapStoragePath(storagePath) {
			return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".planInputs.storagePolicy."+field, "v1 permits only a child path beneath /opt, /srv, /mnt, /media, or /var/lib/stackkits")
		}
	}
	if err := validateCoreHostBootstrapTarget(inputs.Sites, inputs.ModuleTargets, siteRef, nodeRef, path+".planInputs"); err != nil {
		return coreHostBootstrapPolicy{}, err
	}
	return newCoreHostBootstrapPolicy(inputs, siteRef, nodeRef), nil
}

func safeCoreHostBootstrapStoragePath(value string) bool {
	if value == "" || path.Clean(value) != value {
		return false
	}
	for _, root := range []string{"/opt", "/srv", "/mnt", "/media", "/var/lib/stackkits"} {
		if strings.HasPrefix(value, root+"/") {
			return true
		}
	}
	return false
}

func validateCoreHostBootstrapTarget(sites []executorBundleSite, targets []executorBundleTarget, siteRef, nodeRef, path string) error {
	siteMatches := 0
	for _, site := range sites {
		if site.ID == siteRef {
			siteMatches++
		}
	}
	targetMatches := 0
	for _, target := range targets {
		if target.ID == nodeRef && target.SiteRef == siteRef {
			targetMatches++
		}
	}
	if siteMatches != 1 || targetMatches != 1 {
		return fail(ErrInvalidPlan, path+".moduleTargets", "render instance must bind exactly one declared module target and site")
	}
	return nil
}

func newCoreHostBootstrapPolicy(inputs coreHostBootstrapPlanInputs, siteRef, nodeRef string) coreHostBootstrapPolicy {
	policy := coreHostBootstrapPolicy{StackID: inputs.StackID}
	policy.Kit.Slug = inputs.Kit.Slug
	policy.Kit.Version = inputs.Kit.Version
	policy.Kit.DefinitionHash = inputs.Kit.DefinitionHash
	policy.Target.SiteRef = siteRef
	policy.Target.NodeRef = nodeRef
	policy.Runtime.InstallMode = inputs.HostRuntimePolicy.Install.Mode
	policy.Runtime.Runtime = inputs.HostRuntimePolicy.Install.Runtime
	policy.Runtime.Engine = inputs.HostRuntimePolicy.Container.Engine
	policy.Runtime.DataRoot = inputs.HostRuntimePolicy.Container.DataRoot
	paths := map[string]string{
		inputs.StoragePolicy.DataRoot:   "data",
		inputs.StoragePolicy.BackupRoot: "backup",
		inputs.StoragePolicy.StacksRoot: "stacks",
	}
	if inputs.StoragePolicy.MediaRoot != "" {
		paths[inputs.StoragePolicy.MediaRoot] = "media"
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		policy.Directories = append(policy.Directories, coreHostBootstrapDirectory{Path: path, Mode: "0750", Purpose: paths[path]})
	}
	return policy
}
