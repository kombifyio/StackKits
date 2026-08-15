package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

const (
	localKopiaRuntimeUnitID      = "source-policy"
	localKopiaRuntimeRendererRef = "stackkit"
	localKopiaRuntimeTemplateRef = "builtin://home/backup/kopia-source/v1.json"
	localKopiaRuntimeVersion     = "1.0.0"
	localKopiaRuntimeOutputRef   = "home/backup/kopia-source-policy.json"
	localKopiaRuntimeToken       = "@@POLICY@@"
)

const localKopiaRuntimeTemplate = `{"apiVersion":"` + localbackuppolicy.APIVersion + `","kind":"` + localbackuppolicy.Kind + `","policy":@@POLICY@@}
`

var localKopiaRuntimePlanInputRefs = []string{
	"kit", "moduleCapabilities", "moduleTargets", "sites", "stackId",
}

var localKopiaRuntimePublicInputRefs = []string{"backup-source"}

// LocalKopiaRuntimeRendererContract identifies the secret-free projection
// consumed by the owner-authorized local Kopia command path.
func LocalKopiaRuntimeRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(localKopiaRuntimeTemplate))
	return RendererContract{
		Kind: "native-config", RendererRef: localKopiaRuntimeRendererRef,
		TemplateRef: localKopiaRuntimeTemplateRef, Version: localKopiaRuntimeVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type localKopiaRuntimeRenderer struct {
	template []byte
	contract RendererContract
}

func newLocalKopiaRuntimeRenderer() localKopiaRuntimeRenderer {
	return localKopiaRuntimeRenderer{
		template: []byte(localKopiaRuntimeTemplate),
		contract: LocalKopiaRuntimeRendererContract(),
	}
}

func (r localKopiaRuntimeRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy, err := validateLocalKopiaRuntimeUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	if coreHostBootstrapTemplateHash(r.template) != r.contract.ContractHash ||
		bytes.Count(r.template, []byte(localKopiaRuntimeToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.local-kopia-runtime.template", "embedded local Kopia policy does not match its registered contract")
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.local-kopia-runtime.policy", "marshal canonical local Kopia policy", err)
	}
	output := bytes.Replace(r.template, []byte(localKopiaRuntimeToken), policyJSON, 1)
	canonical, err := localbackuppolicy.ArtifactBytes(policy)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.local-kopia-runtime.policy", "marshal canonical local Kopia policy artifact", err)
	}
	if !bytes.Equal(output, canonical) {
		return nil, fail(ErrOutputChanged, "renderer.local-kopia-runtime.template", "registered template does not materially produce the canonical local Kopia policy artifact")
	}
	return []UnitOutput{{Ref: localKopiaRuntimeOutputRef, Bytes: output}}, nil
}

type localKopiaBackupSource = localbackuppolicy.Source

type localKopiaRuntimeValues struct {
	BackupSource localKopiaBackupSource `json:"backup-source"`
}

//nolint:gocyclo // This is the closed CUE-to-local-runtime authority boundary.
func validateLocalKopiaRuntimeUnit(unit RenderUnit, contract RendererContract) (localbackuppolicy.Policy, error) {
	path := "resolvedPlan.modules." + basementCoreModuleID + ".renderUnits." + localKopiaRuntimeUnitID
	if unit.ModuleID() != basementCoreModuleID || unit.ID() != localKopiaRuntimeUnitID {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", basementCoreModuleID, localKopiaRuntimeUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return localbackuppolicy.Policy{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered local Kopia contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImageRef := unit.ContainerImageRef()
	imageDigest, hasImageDigest := unit.ContainerImageDigest()
	entryComponent, hasEntryComponent := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "stackkit" || engine != "docker" || !hasEngine ||
		imageRef != "ghcr.io/coollabsio/coolify:4.1.2" || !hasImageRef ||
		imageDigest != "sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f" || !hasImageDigest ||
		entryComponent != "coolify" || !hasEntryComponent || unit.InstanceScope() != "node-local" || !hasSite || !hasNode {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".runtime", "requires the exact node-local Basement core runtime")
	}
	if unit.InstanceID() != localKopiaRuntimeUnitID+"-node-"+nodeRef ||
		!containsExact(unit.LogicalSiteRefs(), siteRef) ||
		!containsExact(unit.LogicalNodeRefs(), nodeRef) {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".instances", "requires one exact node-local Basement core target")
	}
	if err := validateBasementCoreComponents(unit.RuntimeComponentsJSON(), path+".runtime.components"); err != nil {
		return localbackuppolicy.Policy{}, err
	}
	if !exactStringList(unit.PublicInputRefs(), localKopiaRuntimePublicInputRefs) ||
		len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) ||
		!exactStringList(unit.PlanInputRefs(), localKopiaRuntimePlanInputRefs) {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".inputs", "accepts only the typed compiler-owned backup source and no credentials")
	}
	if err := validateLocalKopiaRuntimeBinding(unit.InputBindingsJSON(), path+".inputBindings"); err != nil {
		return localbackuppolicy.Policy{}, err
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) ||
		!emptyJSONArray(unit.RequiredInterfacesJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) ||
		!emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".interfaces", "Kopia receives no provider, published service, socket, or privileged authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != localKopiaRuntimeOutputRef {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", localKopiaRuntimeOutputRef)
	}
	var inputs homeBackupTargetPlanInputs
	if err := decodeStrict(unit.PlanInputsJSON(), &inputs); err != nil {
		return localbackuppolicy.Policy{}, wrap(ErrInvalidPlan, path+".planInputs", "decode exact local Kopia inputs", err)
	}
	if (inputs.Kit.Slug != "basement-kit" && inputs.Kit.Slug != "modern-homelab") ||
		inputs.Kit.Version == "" || !validSHA256(inputs.Kit.DefinitionHash) {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".planInputs.kit", "requires an exact Home-capable governed kit identity")
	}
	if len(inputs.ModuleCapabilities) != 1 || inputs.ModuleCapabilities[0].ID != "local-backup-runtime" ||
		!validSHA256(inputs.ModuleCapabilities[0].ContractHash) {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".planInputs.moduleCapabilities", "module must own only local-backup-runtime")
	}
	if err := validateCoreHostBootstrapTarget(inputs.Sites, inputs.ModuleTargets, siteRef, nodeRef, path+".planInputs"); err != nil {
		return localbackuppolicy.Policy{}, err
	}
	var values localKopiaRuntimeValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return localbackuppolicy.Policy{}, wrap(ErrInvalidPlan, path+".values", "decode typed local Kopia backup source", err)
	}
	if !reflect.DeepEqual(values.BackupSource, governedLocalKopiaBackupSource()) {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".values.backup-source", "must match the read-only governed Docker volume source")
	}
	policy, err := localbackuppolicy.New(inputs.StackID, siteRef, nodeRef)
	if err != nil {
		return localbackuppolicy.Policy{}, wrap(ErrInvalidPlan, path+".policy", "build governed local Kopia runtime policy", err)
	}
	return policy, nil
}

func validateLocalKopiaRuntimeBinding(raw []byte, path string) error {
	var bindings []rawModuleRenderInputBinding
	if err := decodeStrict(raw, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path, "decode local Kopia input binding", err)
	}
	if len(bindings) != 1 {
		return fail(ErrInvalidPlan, path, "requires exactly one governed backup-source binding")
	}
	binding := bindings[0]
	if binding.TargetRef != "backup-source" || binding.SourceRef != "backup.localKopiaSource" ||
		binding.ValueType != "local-kopia-backup-source-v1" || binding.Cardinality != "single" ||
		!binding.Required || len(binding.DefaultValue) != 0 {
		return fail(ErrInvalidPlan, path, "does not match the governed local Kopia source binding")
	}
	return nil
}

func governedLocalKopiaBackupSource() localKopiaBackupSource {
	return localbackuppolicy.GovernedSource()
}

var _ UnitRenderer = localKopiaRuntimeRenderer{}
