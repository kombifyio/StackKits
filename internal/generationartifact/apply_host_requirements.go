package generationartifact

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

func appendApplyHostRequirements(plan resolvedplan.ResolvedPlan, targetNodes map[string]struct{}, result *ApplyRequirements) error {
	rawNodes, ok := plan["nodes"].([]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.nodes", "must be an array")
	}
	externalBindings, ok := plan["externalHostBindings"].(map[string]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.externalHostBindings", "must be an object")
	}
	for index, raw := range rawNodes {
		path := fmt.Sprintf("resolvedPlan.nodes[%d]", index)
		node, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		nodeRef, err := requiredString(node, "id", path+".id")
		if err != nil {
			return err
		}
		if _, selected := targetNodes[nodeRef]; !selected {
			continue
		}
		requirement := ApplyHostRequirement{NodeRef: nodeRef}
		if requirement.SiteRef, err = requiredString(node, "siteRef", path+".siteRef"); err != nil {
			return err
		}
		if requirement.FailureDomain, err = requiredString(node, "failureDomain", path+".failureDomain"); err != nil {
			return err
		}
		if rawBinding, exists := externalBindings[nodeRef]; exists {
			binding, ok := rawBinding.(map[string]any)
			if !ok {
				return fail(ErrInvalidPlan, "resolvedPlan.externalHostBindings."+nodeRef, "must be an object")
			}
			requirement.External = true
			if requirement.BindingRef, err = requiredString(binding, "bindingRef", "resolvedPlan.externalHostBindings."+nodeRef+".bindingRef"); err != nil {
				return err
			}
			if requirement.BindingHash, err = requiredString(binding, "bindingHash", "resolvedPlan.externalHostBindings."+nodeRef+".bindingHash"); err != nil {
				return err
			}
			if requirement.ExecutionChannelRef, err = requiredString(binding, "executionChannelRef", "resolvedPlan.externalHostBindings."+nodeRef+".executionChannelRef"); err != nil {
				return err
			}
			bindingSecrets, err := applyStringMap(binding, "secretRefs", "resolvedPlan.externalHostBindings."+nodeRef+".secretRefs")
			if err != nil {
				return err
			}
			for inputRef, secretRef := range bindingSecrets {
				result.Secrets = append(result.Secrets, ApplySecretRequirement{
					ID: "external-host/" + nodeRef + "/" + inputRef, OwnerKind: "external-host-binding", OwnerRef: requirement.BindingRef,
					SourceKind: "external-host-binding", SourceRef: requirement.BindingRef, SourceInputRef: inputRef,
					InputRef: inputRef, SecretRef: secretRef, SiteRefs: []string{requirement.SiteRef}, NodeRefs: []string{nodeRef},
				})
			}
		}
		result.Hosts = append(result.Hosts, requirement)
	}
	return nil
}
