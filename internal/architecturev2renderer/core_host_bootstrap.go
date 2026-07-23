package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"reflect"
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
	"kit", "moduleCapabilities", "moduleTargets", "sites", "stackId",
}

var coreHostBootstrapPublicInputRefs = []string{"host-runtime", "storage-roots"}

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
	StackID            string                     `json:"stackId"`
	Kit                executorBundleKit          `json:"kit"`
	Sites              []executorBundleSite       `json:"sites"`
	ModuleTargets      []executorBundleTarget     `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability `json:"moduleCapabilities"`
}

type coreHostBootstrapRuntimeInput struct {
	InstallMode string `json:"installMode"`
	Runtime     string `json:"runtime"`
	Engine      string `json:"engine"`
	DataRoot    string `json:"dataRoot"`
}

type coreHostStorageRootsInput struct {
	DataRoot     string `json:"dataRoot"`
	BackupRoot   string `json:"backupRoot"`
	StacksRoot   string `json:"stacksRoot"`
	MediaRoot    string `json:"mediaRoot,omitempty"`
	VolumeDriver string `json:"volumeDriver"`
}

type coreHostBootstrapValues struct {
	Runtime coreHostBootstrapRuntimeInput `json:"host-runtime"`
	Storage coreHostStorageRootsInput     `json:"storage-roots"`
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
	if !exactStringList(unit.PublicInputRefs(), coreHostBootstrapPublicInputRefs) || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".inputs", "Core host bootstrap accepts only its exact typed compiler-owned inputs")
	}
	if !exactStringList(unit.PlanInputRefs(), coreHostBootstrapPlanInputRefs) {
		return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".planInputRefs", "must exactly match the registered Core host-bootstrap projection")
	}
	if err := validateCoreHostBootstrapBindings(unit.InputBindingsJSON(), path+".inputBindings"); err != nil {
		return coreHostBootstrapPolicy{}, err
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
	var values coreHostBootstrapValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return coreHostBootstrapPolicy{}, wrap(ErrInvalidPlan, path+".values", "decode exact Core host-bootstrap bound inputs", err)
	}
	runtime, err := decodeCoreHostBootstrapRuntimeJSON(values.Runtime, path+".values.host-runtime")
	if err != nil {
		return coreHostBootstrapPolicy{}, err
	}
	storage, err := decodeCoreHostStorageRootsJSON(values.Storage, path+".values.storage-roots")
	if err != nil {
		return coreHostBootstrapPolicy{}, err
	}
	for field, storagePath := range map[string]string{
		"dataRoot": storage.DataRoot, "backupRoot": storage.BackupRoot,
		"stacksRoot": storage.StacksRoot, "mediaRoot": storage.MediaRoot,
	} {
		if storagePath != "" && !safeCoreHostBootstrapStoragePath(storagePath) {
			return coreHostBootstrapPolicy{}, fail(ErrInvalidPlan, path+".values.storage-roots."+field, "v1 permits only a child path beneath /opt, /srv, /mnt, /media, or /var/lib/stackkits")
		}
	}
	if err := validateCoreHostBootstrapTarget(inputs.Sites, inputs.ModuleTargets, siteRef, nodeRef, path+".planInputs"); err != nil {
		return coreHostBootstrapPolicy{}, err
	}
	return newCoreHostBootstrapPolicy(inputs, runtime, storage, siteRef, nodeRef), nil
}

func validateCoreHostBootstrapBindings(raw []byte, path string) error {
	var bindings []rawModuleRenderInputBinding
	if err := decodeStrict(raw, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path, "decode Core host-bootstrap input bindings", err)
	}
	expected := []rawModuleRenderInputBinding{
		{TargetRef: "host-runtime", SourceRef: "host.bootstrapRuntime", ValueType: "host-bootstrap-runtime-v1", Cardinality: "single", Required: true},
		{TargetRef: "storage-roots", SourceRef: "storage.hostRoots", ValueType: "host-storage-roots-v1", Cardinality: "single", Required: true},
	}
	if !reflect.DeepEqual(bindings, expected) {
		return fail(ErrInvalidPlan, path, "must exactly match the two governed Core host-bootstrap bindings")
	}
	return nil
}

func decodeCoreHostBootstrapRuntime(raw json.RawMessage, path string) (coreHostBootstrapRuntimeInput, error) {
	var value coreHostBootstrapRuntimeInput
	if err := decodeStrict(raw, &value); err != nil {
		return value, wrap(ErrInvalidPlan, path, "decode host bootstrap runtime", err)
	}
	return decodeCoreHostBootstrapRuntimeJSON(value, path)
}

func decodeCoreHostBootstrapRuntimeJSON(value coreHostBootstrapRuntimeInput, path string) (coreHostBootstrapRuntimeInput, error) {
	if value.InstallMode != "bootstrapped" || value.Runtime != "docker" || value.Engine != "docker" || !cleanAbsolutePath(value.DataRoot) {
		return value, fail(ErrInvalidPlan, path, "requires exact bootstrapped Docker runtime and an absolute data root")
	}
	return value, nil
}

func decodeCoreHostStorageRoots(raw json.RawMessage, path string) (coreHostStorageRootsInput, error) {
	var value coreHostStorageRootsInput
	if err := decodeStrict(raw, &value); err != nil {
		return value, wrap(ErrInvalidPlan, path, "decode host storage roots", err)
	}
	return decodeCoreHostStorageRootsJSON(value, path)
}

func decodeCoreHostStorageRootsJSON(value coreHostStorageRootsInput, path string) (coreHostStorageRootsInput, error) {
	if value.VolumeDriver != "local" || value.DataRoot == "" || value.BackupRoot == "" || value.StacksRoot == "" {
		return value, fail(ErrInvalidPlan, path, "requires local storage and all mandatory host roots")
	}
	for field, storagePath := range map[string]string{
		"dataRoot": value.DataRoot, "backupRoot": value.BackupRoot,
		"stacksRoot": value.StacksRoot, "mediaRoot": value.MediaRoot,
	} {
		if storagePath != "" && !safeCoreHostBootstrapStoragePath(storagePath) {
			return value, fail(ErrInvalidPlan, path+"."+field, "requires a clean child path beneath a governed StackKit storage root")
		}
	}
	return value, nil
}

func cleanAbsolutePath(value string) bool {
	return path.IsAbs(value) && path.Clean(value) == value
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

func newCoreHostBootstrapPolicy(inputs coreHostBootstrapPlanInputs, runtime coreHostBootstrapRuntimeInput, storage coreHostStorageRootsInput, siteRef, nodeRef string) coreHostBootstrapPolicy {
	policy := coreHostBootstrapPolicy{StackID: inputs.StackID}
	policy.Kit.Slug = inputs.Kit.Slug
	policy.Kit.Version = inputs.Kit.Version
	policy.Kit.DefinitionHash = inputs.Kit.DefinitionHash
	policy.Target.SiteRef = siteRef
	policy.Target.NodeRef = nodeRef
	policy.Runtime.InstallMode = runtime.InstallMode
	policy.Runtime.Runtime = runtime.Runtime
	policy.Runtime.Engine = runtime.Engine
	policy.Runtime.DataRoot = runtime.DataRoot
	paths := map[string]string{
		storage.DataRoot:   "data",
		storage.BackupRoot: "backup",
		storage.StacksRoot: "stacks",
	}
	if storage.MediaRoot != "" {
		paths[storage.MediaRoot] = "media"
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
