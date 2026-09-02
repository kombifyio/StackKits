package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

const (
	localKopiaRuntimeUnitID        = "source-policy"
	localKopiaRuntimeRendererRef   = "stackkit"
	localKopiaRuntimeTemplateRef   = "builtin://home/backup/kopia-source/v1.json"
	localKopiaRuntimeVersion       = "1.0.0"
	localKopiaRuntimeOutputRef     = "home/backup/kopia-source-policy.json"
	localKopiaRuntimeLiteOutputRef = "home/backup/kopia-source-policy-lite.json"
	localKopiaRuntimeToken         = "@@POLICY@@"
)

const localKopiaRuntimeTemplate = `{"apiVersion":"` + localbackuppolicy.APIVersion + `","kind":"` + localbackuppolicy.Kind + `","policy":@@POLICY@@}
`

var localKopiaRuntimePlanInputRefs = []string{
	"backupPolicy", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId",
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
	outputRef, _ := localKopiaRuntimeOutputRefForModule(unit.ModuleID())
	return []UnitOutput{{Ref: outputRef, Bytes: output}}, nil
}

type localKopiaBackupSource = localbackuppolicy.Source

type localKopiaRuntimeValues struct {
	BackupSource localKopiaBackupSource `json:"backup-source"`
}

func localKopiaCoreProfile(moduleID string) (closedLocalCoreProfile, bool) {
	switch moduleID {
	case basementCoreModuleID:
		return basementClosedLocalCoreProfile(), true
	case basementCoreLiteModuleID:
		return basementClosedLocalCoreLiteProfile(), true
	default:
		return closedLocalCoreProfile{}, false
	}
}

func localKopiaRuntimeOutputRefForModule(moduleID string) (string, bool) {
	switch moduleID {
	case basementCoreModuleID:
		return localKopiaRuntimeOutputRef, true
	case basementCoreLiteModuleID:
		return localKopiaRuntimeLiteOutputRef, true
	default:
		return "", false
	}
}

//nolint:gocyclo // This is the closed CUE-to-local-runtime authority boundary.
func validateLocalKopiaRuntimeUnit(unit RenderUnit, contract RendererContract) (localbackuppolicy.Policy, error) {
	profile, supported := localKopiaCoreProfile(unit.ModuleID())
	outputRef, hasOutputRef := localKopiaRuntimeOutputRefForModule(unit.ModuleID())
	path := "resolvedPlan.modules." + unit.ModuleID() + ".renderUnits." + localKopiaRuntimeUnitID
	if !supported || !hasOutputRef || unit.ID() != localKopiaRuntimeUnitID {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path, "renderer accepts only the governed Full-Core or CoreLite %s unit", localKopiaRuntimeUnitID)
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
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "stackkit" || engine != profile.runtimeEngine || !hasEngine ||
		imageRef != profile.imageRef || !hasImageRef || imageDigest != profile.imageDigest || !hasImageDigest ||
		entryComponent != profile.entryComponent || !hasEntryComponent || unit.InstanceScope() != "node-local" || !hasSite || !hasNode {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".runtime", "requires the exact node-local %s runtime", profile.displayName)
	}
	if unit.InstanceID() != localKopiaRuntimeUnitID+"-node-"+nodeRef ||
		!containsExact(unit.LogicalSiteRefs(), siteRef) ||
		!containsExact(unit.LogicalNodeRefs(), nodeRef) {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".instances", "requires one exact node-local Basement core target")
	}
	if err := validateClosedLocalCoreComponents(unit.RuntimeComponentsJSON(), profile.componentsJSON, path+".runtime.components", profile.displayName); err != nil {
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
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != outputRef {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", outputRef)
	}
	var inputs struct {
		homeBackupTargetPlanInputs
		BackupPolicy struct {
			APIVersion          string                      `json:"apiVersion"`
			Kind                string                      `json:"kind"`
			Schedule            localbackuppolicy.Schedule  `json:"schedule"`
			DataClasses         []string                    `json:"dataClasses"`
			Retention           localbackuppolicy.Retention `json:"retention"`
			RestoreVerification json.RawMessage             `json:"restoreVerification"`
		} `json:"backupPolicy"`
	}
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
	if values.BackupSource.CoreModuleRef != "" && values.BackupSource.CoreModuleRef != profile.moduleID {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".values.backup-source.coreModuleRef", "must bind the selected %s runtime", profile.displayName)
	}
	if profile.moduleID == basementCoreLiteModuleID && values.BackupSource.CoreModuleRef != profile.moduleID {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".values.backup-source.coreModuleRef", "CoreLite source policy must carry its explicit module binding")
	}
	if err := localbackuppolicy.ValidateSourceProjection(values.BackupSource); err != nil {
		return localbackuppolicy.Policy{}, wrap(ErrInvalidPlan, path+".values.backup-source", "validate the compiler-owned read-only Docker volume source", err)
	}
	applications, err := localbackuppolicy.ApplicationVolumesForTarget(values.BackupSource.ApplicationVolumes, siteRef, nodeRef)
	if err != nil {
		return localbackuppolicy.Policy{}, wrap(ErrInvalidPlan, path+".values.backup-source.applicationVolumes", "select the exact node-local application volumes", err)
	}
	runtimes, err := localbackuppolicy.ApplicationRuntimesForTarget(values.BackupSource.ApplicationRuntimes, siteRef, nodeRef)
	if err != nil {
		return localbackuppolicy.Policy{}, wrap(ErrInvalidPlan, path+".values.backup-source.applicationRuntimes", "select the exact node-local application runtime graphs", err)
	}
	policy, err := localbackuppolicy.NewWithApplicationVolumesAndRuntimesForCoreModule(profile.moduleID, inputs.StackID, siteRef, nodeRef, applications, runtimes)
	if err != nil {
		return localbackuppolicy.Policy{}, wrap(ErrInvalidPlan, path+".policy", "build governed local Kopia runtime policy", err)
	}
	if inputs.BackupPolicy.APIVersion != "stackkit.backup-policy/v1" || inputs.BackupPolicy.Kind != "BackupPolicy" {
		return localbackuppolicy.Policy{}, fail(ErrInvalidPlan, path+".planInputs.backupPolicy", "requires the resolved CUE backup policy")
	}
	if err := inputs.BackupPolicy.Retention.Validate(); err != nil {
		return localbackuppolicy.Policy{}, wrap(ErrInvalidPlan, path+".planInputs.backupPolicy.retention", "validate resolved backup retention", err)
	}
	policy.Retention = &inputs.BackupPolicy.Retention
	if err := inputs.BackupPolicy.Schedule.Validate(); err != nil {
		return localbackuppolicy.Policy{}, wrap(ErrInvalidPlan, path+".planInputs.backupPolicy.schedule", "validate resolved backup schedule", err)
	}
	policy.Schedule = &inputs.BackupPolicy.Schedule
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

var _ UnitRenderer = localKopiaRuntimeRenderer{}
