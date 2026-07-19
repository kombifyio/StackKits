package architecturev2renderer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type generationOnlyPolicyUnitSpec struct {
	moduleID          string
	unitID            string
	outputRef         string
	outputRefs        []string
	policyName        string
	contract          RendererContract
	planInputRefs     []string
	validatePlanInput func([]byte, string) ([]string, error)
}

// validateGenerationOnlyPolicyUnit centralizes the authority-free boundary
// shared by native policy manifests. A product-specific validator still owns
// the exact compiler projection and kit semantics.
//
//nolint:gocyclo // Every forbidden runtime, socket, interface, input, placement, and output authority is checked at one boundary.
func validateGenerationOnlyPolicyUnit(unit RenderUnit, spec generationOnlyPolicyUnitSpec) ([]byte, error) {
	path := "resolvedPlan.modules." + spec.moduleID + ".renderUnits." + spec.unitID
	if unit.ModuleID() != spec.moduleID || unit.ID() != spec.unitID {
		return nil, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", spec.moduleID, spec.unitID)
	}
	contract := spec.contract
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return nil, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered %s contract", spec.policyName)
	}
	if unit.RuntimeKind() != "native" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "module" {
		return nil, fail(ErrInvalidPlan, path, "%s requires exact native/stackkit module-single ownership", spec.policyName)
	}
	if unit.InstanceID() != spec.unitID+"-logical" {
		return nil, fail(ErrInvalidPlan, path+".instances", "module policy requires exact logical instance %q", spec.unitID+"-logical")
	}
	if _, present := unit.SiteRef(); present {
		return nil, fail(ErrInvalidPlan, path+".instances", "module policy instance must not receive a site binding")
	}
	if _, present := unit.NodeRef(); present {
		return nil, fail(ErrInvalidPlan, path+".instances", "module policy instance must not receive a node binding")
	}
	if _, present := unit.DaemonRef(); present {
		return nil, fail(ErrInvalidPlan, path+".instances", "module policy instance must not receive a daemon binding")
	}
	if _, present := unit.DaemonInstanceRef(); present {
		return nil, fail(ErrInvalidPlan, path+".instances", "module policy instance must not receive a daemon instance")
	}
	if _, present := unit.DaemonEngine(); present {
		return nil, fail(ErrInvalidPlan, path+".instances", "module policy instance must not receive a daemon engine")
	}
	if _, present := unit.DaemonSocketPath(); present {
		return nil, fail(ErrInvalidPlan, path+".instances", "module policy instance must not receive a daemon socket")
	}
	if _, present := unit.RuntimeEngine(); present {
		return nil, fail(ErrInvalidPlan, path+".runtime", "native policy must not receive a runtime engine")
	}
	if _, present := unit.ContainerImageRef(); present {
		return nil, fail(ErrInvalidPlan, path+".runtime", "native policy must not receive a container image")
	}
	if _, present := unit.ContainerImageDigest(); present {
		return nil, fail(ErrInvalidPlan, path+".runtime", "native policy must not receive a container digest")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) {
		return nil, fail(ErrInvalidPlan, path+".inputs", "%s accepts compiler-owned plan inputs only", spec.policyName)
	}
	if !exactStringList(unit.PlanInputRefs(), spec.planInputRefs) {
		return nil, fail(ErrInvalidPlan, path+".planInputRefs", "must be the exact closed %s projection", spec.policyName)
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) {
		return nil, fail(ErrInvalidPlan, path+".interfaces", "generation-only policy must not receive service, network, interface, approval, or socket authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "module" || placement.Cardinality != "single" {
		return nil, fail(ErrInvalidPlan, path+".placement", "%s requires exact module/single placement", spec.policyName)
	}
	expectedOutputs := spec.outputRefs
	if len(expectedOutputs) == 0 {
		expectedOutputs = []string{spec.outputRef}
	}
	if outputs := unit.DeclaredOutputs(); !exactStringList(outputs, expectedOutputs) {
		return nil, fail(ErrInvalidPlan, path+".outputs", "%s requires exactly outputs %q", spec.policyName, expectedOutputs)
	}
	planInputs := unit.PlanInputsJSON()
	siteRefs, err := spec.validatePlanInput(planInputs, path+".planInputs")
	if err != nil {
		return nil, err
	}
	if !exactStringList(unit.LogicalSiteRefs(), siteRefs) || len(unit.LogicalNodeRefs()) == 0 {
		return nil, fail(ErrInvalidPlan, path+".placement", "logical placement must exactly cover governed policy sites and retain eligible nodes")
	}
	return planInputs, nil
}

func rejectGenerationOnlyPolicyProjectionLeaks(raw []byte, path, policyName string) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return wrap(ErrInvalidPlan, path, "scan projected policy inputs", err)
	}
	forbiddenKeys := map[string]struct{}{
		"nodes": {}, "management": {}, "managementcidrs": {}, "servicecidrs": {}, "storagecidrs": {},
		"accountref": {}, "addresses": {}, "secretrefs": {}, "credentialref": {}, "credentialrefs": {},
		"socketpath": {}, "daemonsocketpath": {},
	}
	var walk func(any, string) error
	walk = func(current any, currentPath string) error {
		switch typed := current.(type) {
		case map[string]any:
			for key, nested := range typed {
				if _, forbidden := forbiddenKeys[strings.ToLower(key)]; forbidden {
					return fail(ErrInvalidPlan, currentPath+"."+key, "field is outside the closed %s projection", policyName)
				}
				if err := walk(nested, currentPath+"."+key); err != nil {
					return err
				}
			}
		case []any:
			for index, nested := range typed {
				if err := walk(nested, fmt.Sprintf("%s[%d]", currentPath, index)); err != nil {
					return err
				}
			}
		case string:
			lower := strings.ToLower(typed)
			if validSecretReference(lower) || strings.Contains(lower, ".sock") && strings.Contains(lower, "/") {
				return fail(ErrInvalidPlan, currentPath, "secret references and daemon socket paths are forbidden from policy artifacts")
			}
		}
		return nil
	}
	return walk(value, path)
}
