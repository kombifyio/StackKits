package architecturev2

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/rilactionv2"
)

// RILActionAdmissionInput binds an upstream handoff to one authenticated
// tenant context and one current resolution issued by this Service. The
// caller cannot supply a plan, catalog, runtime owner, or provider binding.
type RILActionAdmissionInput struct {
	Current         CurrentResolution
	TrustedTenantID string
	Envelope        []byte
	EvaluatedAt     time.Time
}

// RILActionValidation is non-authorizing evidence that one handoff matched the
// current StackKits plan and CUE primitive. Executable reports only whether
// this binary owns the exact CUE-selected executor; execution must still pass
// through ExecuteRILActionAt and its replay guard.
type RILActionValidation struct {
	ActionCardID          string    `json:"actionCardId"`
	ExecutionID           string    `json:"executionId"`
	TraceID               string    `json:"traceId"`
	TenantID              string    `json:"tenantId"`
	StackID               string    `json:"stackId"`
	PrimitiveID           string    `json:"primitiveId"`
	PrimitiveContractHash string    `json:"primitiveContractHash"`
	ResolvedPlanHash      string    `json:"resolvedPlanHash"`
	TargetScope           string    `json:"targetScope"`
	RequestDigest         string    `json:"requestDigest"`
	EvaluatedAt           time.Time `json:"evaluatedAt"`
	Support               string    `json:"support"`
	Executable            bool      `json:"executable"`
}

// ValidateRILActionHandoffAt validates a short-lived upstream envelope against
// one fresh product resolution and the exact embedded CUE primitive. The
// result is discovery/evidence only: it is not an execution authorization.
func (s *Service) ValidateRILActionHandoffAt(input RILActionAdmissionInput) (RILActionValidation, error) {
	if s == nil || s.authority == nil || s.generation == nil {
		return RILActionValidation{}, resolveError(ErrAuthorityLoad, "Architecture v2 product authority is not initialized", nil)
	}
	current := input.Current
	if !current.valid || current.owner != s.generation || current.key == "" ||
		!s.generation.matchesIssuedResolution(current.key, current.epoch, current.plan.Binding()) {
		return RILActionValidation{}, resolveError(ErrRILActionAdmission, "a fresh current resolution issued by this service is required", nil)
	}
	if strings.TrimSpace(input.TrustedTenantID) == "" || input.TrustedTenantID != strings.TrimSpace(input.TrustedTenantID) {
		return RILActionValidation{}, resolveError(ErrRILActionAdmission, "trusted tenant identity is required", nil)
	}
	request, err := rilaction.DecodeRequestAt(input.Envelope, input.EvaluatedAt)
	if err != nil {
		return RILActionValidation{}, resolveError(ErrRILActionAdmission, "approved action envelope is invalid", err)
	}
	if request.TenantID != input.TrustedTenantID {
		return RILActionValidation{}, resolveError(ErrRILActionAdmission, "approved action tenant does not match authenticated context", nil)
	}
	if request.StackID != current.stackID {
		return RILActionValidation{}, resolveError(ErrRILActionAdmission, "approved action stack does not match the current resolution", nil)
	}
	binding := current.plan.Binding()
	if request.ResolvedPlanHash != binding.PlanHash {
		return RILActionValidation{}, resolveError(ErrRILActionAdmission, "approved action plan hash is stale or substituted", nil)
	}
	primitive, err := s.rilActionPrimitive(request.Primitive.ID)
	if err != nil {
		return RILActionValidation{}, err
	}
	if err := validateRILActionPrimitiveBinding(request, primitive); err != nil {
		return RILActionValidation{}, resolveError(ErrRILActionAdmission, err.Error(), err)
	}
	var plan resolvedplan.ResolvedPlan
	if err := json.Unmarshal(current.plan.Canonical(), &plan); err != nil {
		return RILActionValidation{}, resolveError(ErrRILActionAdmission, "decode current governed plan", err)
	}
	if err := validateRILActionTarget(request, primitive, plan, current.plan.ApplyRequirements()); err != nil {
		return RILActionValidation{}, resolveError(ErrRILActionAdmission, err.Error(), err)
	}
	digest, err := rilaction.ComputeRequestDigest(request)
	if err != nil {
		return RILActionValidation{}, resolveError(ErrRILActionAdmission, "derive approved action request digest", err)
	}
	return RILActionValidation{
		ActionCardID: request.ActionCardID, ExecutionID: request.ExecutionID, TraceID: request.TraceID,
		TenantID: request.TenantID, StackID: request.StackID,
		PrimitiveID: request.Primitive.ID, PrimitiveContractHash: request.Primitive.ContractHash,
		ResolvedPlanHash: request.ResolvedPlanHash, TargetScope: string(request.Target.Scope),
		RequestDigest: digest, EvaluatedAt: input.EvaluatedAt, Support: primitive.Support,
		Executable: primitive.Support == "executor-bound" && s.rilActionExecutors.owns(primitive.executorIdentity()),
	}, nil
}

// AdmitRILActionAt is the execution-facing admission seam. It reuses the exact
// validation instant and admits only an executor bound by both CUE and this
// binary. Every contract-only or unknown executor fails before side effects.
func (s *Service) AdmitRILActionAt(input RILActionAdmissionInput) (RILActionValidation, error) {
	validated, err := s.ValidateRILActionHandoffAt(input)
	if err != nil {
		return RILActionValidation{}, err
	}
	if validated.Executable {
		return validated, nil
	}
	return RILActionValidation{}, resolveError(
		ErrRILActionUnavailable,
		fmt.Sprintf("RIL action primitive %q is %s and has no authenticated StackKits runtime owner", validated.PrimitiveID, validated.Support),
		nil,
	)
}

func (s *Service) rilActionPrimitive(id string) (RILActionPrimitiveCatalogEntry, error) {
	entries, err := s.ListRILActionPrimitives()
	if err != nil {
		return RILActionPrimitiveCatalogEntry{}, err
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return RILActionPrimitiveCatalogEntry{}, resolveError(ErrRILActionAdmission, "approved action primitive is not owned by the product catalog", nil)
}

func validateRILActionPrimitiveBinding(request rilaction.Request, primitive RILActionPrimitiveCatalogEntry) error {
	if request.Primitive.ContractHash != primitive.PrimitiveContractHash ||
		request.Primitive.OperationClass != primitive.Owner.OperationClass {
		return fmt.Errorf("approved action primitive identity does not match the CUE product authority")
	}
	if string(request.Approval.Class) != primitive.Approval.Class {
		return fmt.Errorf("approved action ceremony does not match the primitive contract")
	}
	if request.Grant.Audience != primitive.Grant.Audience || !slices.Equal(request.Grant.Scopes, primitive.Grant.Scopes) {
		return fmt.Errorf("approved action grant does not exactly match the primitive contract")
	}
	if string(request.Target.Scope) != primitive.Target.Scope {
		return fmt.Errorf("approved action target scope does not match the primitive contract")
	}
	if primitive.Target.RequiresNodeRef != (request.Target.NodeRef != "") {
		return fmt.Errorf("approved action node target does not match the primitive contract")
	}
	if primitive.Target.RequiresRuntimeInstanceRef != (request.Target.RuntimeInstanceRef != "") {
		return fmt.Errorf("approved action runtime target does not match the primitive contract")
	}
	declared := make(map[string]RILActionPrimitiveInput, len(primitive.Inputs))
	for _, input := range primitive.Inputs {
		declared[input.ID] = input
	}
	seen := make(map[string]struct{}, len(request.Inputs))
	for _, input := range request.Inputs {
		contract, exists := declared[input.ID]
		if !exists || string(input.Type) != contract.Type {
			return fmt.Errorf("approved action input set does not match the primitive contract")
		}
		seen[input.ID] = struct{}{}
	}
	for _, input := range primitive.Inputs {
		if input.Required {
			if _, exists := seen[input.ID]; !exists {
				return fmt.Errorf("approved action omits a required primitive input")
			}
		}
	}
	return nil
}

func validateRILActionTarget(request rilaction.Request, primitive RILActionPrimitiveCatalogEntry, plan resolvedplan.ResolvedPlan, requirements generationartifact.ApplyRequirements) error {
	if request.Target.Scope == rilaction.TargetScopeStack {
		return nil
	}
	if !planContainsSite(plan, request.Target.SiteRef) {
		return fmt.Errorf("approved action target Site is absent from the current plan")
	}
	if request.Target.NodeRef != "" && !planContainsNode(plan, request.Target.SiteRef, request.Target.NodeRef) {
		return fmt.Errorf("approved action target node is absent from the current Site")
	}
	switch request.Target.Scope {
	case rilaction.TargetScopeModuleInstance:
		if !planContainsModule(plan, request.Target.ModuleInstanceRef, request.Target.SiteRef, request.Target.NodeRef) {
			return fmt.Errorf("approved action module target is absent from the current plan placement")
		}
		if authority := primitive.ExtensionAuthority; authority != nil &&
			!planContainsModuleAuthority(plan, request.Target.ModuleInstanceRef, authority.ModuleRef, authority.ProviderRef) {
			return fmt.Errorf("approved action module target does not match its CUE extension authority")
		}
	case rilaction.TargetScopeRuntimeInstance:
		if !requirementsContainRuntime(requirements, request.Target) {
			return fmt.Errorf("approved action runtime target is absent from the current plan execution graph")
		}
		if authority := primitive.ExtensionAuthority; authority != nil &&
			!requirementsContainRuntimeAuthority(requirements, request.Target.RuntimeInstanceRef, authority.ModuleRef, authority.ProviderRef) {
			return fmt.Errorf("approved action runtime target does not match its CUE extension authority")
		}
	default:
		return fmt.Errorf("approved action target scope is unsupported")
	}
	return nil
}

func planContainsModuleAuthority(plan resolvedplan.ResolvedPlan, instanceRef, moduleRef, providerRef string) bool {
	for _, value := range objectList(plan["modules"]) {
		if value["id"] == instanceRef && value["id"] == moduleRef && value["providerRef"] == providerRef {
			return true
		}
	}
	return false
}

func planContainsSite(plan resolvedplan.ResolvedPlan, siteRef string) bool {
	for _, value := range objectList(plan["sites"]) {
		if value["id"] == siteRef {
			return true
		}
	}
	return false
}

func planContainsNode(plan resolvedplan.ResolvedPlan, siteRef, nodeRef string) bool {
	for _, value := range objectList(plan["nodes"]) {
		if value["id"] == nodeRef && value["siteRef"] == siteRef {
			return true
		}
	}
	return false
}

func planContainsModule(plan resolvedplan.ResolvedPlan, moduleRef, siteRef, nodeRef string) bool {
	for _, value := range objectList(plan["modules"]) {
		if value["id"] != moduleRef || !anyStringListContains(value["siteRefs"], siteRef) {
			continue
		}
		if nodeRef == "" || anyStringListContains(value["nodeRefs"], nodeRef) {
			return true
		}
	}
	return false
}

func requirementsContainRuntime(requirements generationartifact.ApplyRequirements, target rilaction.TargetBinding) bool {
	matches := 0
	for _, runtime := range requirements.RuntimeInstances {
		if runtime.InstanceRef != target.RuntimeInstanceRef || !slices.Contains(runtime.SiteRefs, target.SiteRef) || !slices.Contains(runtime.NodeRefs, target.NodeRef) {
			continue
		}
		channel, err := runtimeTargetExecutionChannel(runtime, requirements.Hosts)
		if err != nil || channel != target.ExecutionChannelRef {
			continue
		}
		matches++
	}
	return matches == 1
}

func requirementsContainRuntimeAuthority(requirements generationartifact.ApplyRequirements, instanceRef, moduleRef, providerRef string) bool {
	matches := 0
	for _, runtime := range requirements.RuntimeInstances {
		if runtime.InstanceRef == instanceRef && runtime.ModuleRef == moduleRef && runtime.ProviderRef == providerRef {
			matches++
		}
	}
	return matches == 1
}

func objectList(value any) []map[string]any {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if object, ok := item.(map[string]any); ok {
			result = append(result, object)
		}
	}
	return result
}

func anyStringListContains(value any, want string) bool {
	raw, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range raw {
		if item == want {
			return true
		}
	}
	return false
}

func (c *generationCoordinator) matchesIssuedResolution(key string, epoch uint64, binding generationartifact.PlanBinding) bool {
	slot := c.existingSlot(key)
	if slot == nil {
		return false
	}
	slot.mu.RLock()
	defer slot.mu.RUnlock()
	return slot.occupied && slot.state.epoch == epoch && slot.state.stage == generationStageIssued && slot.state.binding == binding
}
