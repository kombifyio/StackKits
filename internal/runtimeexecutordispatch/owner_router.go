package runtimeexecutordispatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/kombifyio/stackkits/internal/runtimeapplyv2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// OwnerRoute binds one complete, already-verified RuntimeTarget to one typed
// executor selected by the product integration. Target is the authority
// selector: matching only an owner name, module, or requirement ID would allow
// contract, node, workload, artifact, or access authority substitution.
//
// OwnerRoute contains no endpoint, credential, provider lifecycle, lease, or
// transport configuration. Remote execution remains the responsibility of an
// outer execution-channel dispatcher selected by the owning control plane.
type OwnerRoute struct {
	Target       runtimeexecutor.RuntimeTarget
	Executor     runtimeexecutor.Executor
	Compensation runtimeapply.CompensationMode
}

// OwnerRouter dispatches the independently executable owners on one exact
// execution channel. All target mappings and child requests are prepared
// before the first child is invoked. When constructed with a Journal, durable
// idempotency and resume are fixed at the router boundary.
type OwnerRouter struct {
	identity   runtimeexecutor.ExecutorIdentity
	channelRef string
	siteRef    string
	nodeRef    string
	routes     map[string]ownerRoute
	journal    runtimeapply.Journal
}

type ownerRoute struct {
	requirementID string
	identity      runtimeexecutor.ExecutorIdentity
	executor      runtimeexecutor.Executor
	compensation  runtimeapply.CompensationMode
}

// NewOwnerRouter fixes the exact target-to-owner mapping at construction.
// Apply request callers cannot choose or replace a child executor.
func NewOwnerRouter(identity runtimeexecutor.ExecutorIdentity, routes []OwnerRoute) (*OwnerRouter, error) {
	return newOwnerRouter(identity, routes, nil)
}

// NewOwnerRouterWithJournal fixes a durable provider-neutral operation journal
// at construction. Apply callers cannot supply or replace it.
func NewOwnerRouterWithJournal(identity runtimeexecutor.ExecutorIdentity, routes []OwnerRoute, journal runtimeapply.Journal) (*OwnerRouter, error) {
	if nilRuntimeApplyJournal(journal) {
		return nil, errors.New("runtime-owner router requires a journal")
	}
	return newOwnerRouter(identity, routes, journal)
}

func newOwnerRouter(identity runtimeexecutor.ExecutorIdentity, routes []OwnerRoute, journal runtimeapply.Journal) (*OwnerRouter, error) {
	if len(routes) == 0 {
		return nil, errors.New("runtime-owner router requires at least one route")
	}
	registered := make(map[string]ownerRoute, len(routes))
	seenRequirements := make(map[string]struct{}, len(routes))
	channelRef := ""
	siteRef := ""
	nodeRef := ""
	for index, route := range routes {
		target := cloneRuntimeTarget(route.Target)
		if route.Executor == nil || target.RequirementID == "" ||
			len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
			!channelPattern.MatchString(target.ExecutionChannelRef) {
			return nil, fmt.Errorf("runtime-owner route %d is not bound to one exact target and execution channel", index)
		}
		if channelRef == "" {
			channelRef = target.ExecutionChannelRef
			siteRef = target.SiteRefs[0]
			nodeRef = target.NodeRefs[0]
		} else if target.ExecutionChannelRef != channelRef {
			return nil, errors.New("runtime-owner routes must belong to one exact execution channel")
		} else if target.SiteRefs[0] != siteRef || target.NodeRefs[0] != nodeRef {
			return nil, errors.New("runtime-owner routes must belong to one exact Site/node")
		}
		if _, duplicate := seenRequirements[target.RequirementID]; duplicate {
			return nil, fmt.Errorf("runtime requirement %q is registered more than once", target.RequirementID)
		}
		seenRequirements[target.RequirementID] = struct{}{}
		targetHash, err := hashRuntimeTarget(target)
		if err != nil {
			return nil, fmt.Errorf("hash runtime-owner route %d: %w", index, err)
		}
		if _, duplicate := registered[targetHash]; duplicate {
			return nil, fmt.Errorf("runtime-owner target %q is registered more than once", target.RequirementID)
		}
		childIdentity, err := safeExecutorIdentity(route.Executor)
		if err != nil {
			return nil, fmt.Errorf("runtime-owner route %d identity: %w", index, err)
		}
		compensation, err := normalizeCompensationMode(route.Compensation)
		if err != nil {
			return nil, fmt.Errorf("runtime-owner route %d: %w", index, err)
		}
		registered[targetHash] = ownerRoute{
			requirementID: target.RequirementID,
			identity:      childIdentity,
			executor:      route.Executor,
			compensation:  compensation,
		}
	}
	return &OwnerRouter{
		identity: identity, channelRef: channelRef, siteRef: siteRef, nodeRef: nodeRef,
		routes: registered, journal: journal,
	}, nil
}

func (r *OwnerRouter) Identity() runtimeexecutor.ExecutorIdentity { return r.identity }

func (r *OwnerRouter) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("runtime-owner router requires a context")
	}
	if r == nil || len(r.routes) == 0 || r.channelRef == "" || r.siteRef == "" || r.nodeRef == "" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("runtime-owner router is not initialized")
	}
	if err := request.Validate(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate sealed runtime-owner request: %w", err)
	}
	if request.Executor != r.identity {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("runtime-owner request does not bind the selected router identity")
	}
	groups, selected, err := partitionOwnerRequest(request, r.channelRef, r.siteRef, r.nodeRef, r.routes)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	prepared := make([]preparedExecution, 0, len(selected))
	for _, targetHash := range selected {
		route := r.routes[targetHash]
		childRequest, err := sealChildRequest(request, route.identity, groups[targetHash])
		if err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("seal runtime-owner child request for %q: %w", route.requirementID, err)
		}
		prepared = append(prepared, preparedExecution{
			label: route.requirementID, executor: route.executor, request: childRequest, compensation: route.compensation,
		})
	}
	return executePrepared(ctx, request, prepared, r.journal)
}

func partitionOwnerRequest(request runtimeexecutor.ExecutionRequest, channelRef, siteRef, nodeRef string, routes map[string]ownerRoute) (map[string]*requestGroup, []string, error) {
	groups := make(map[string]*requestGroup, len(request.RuntimeTargets))
	selectedByRequirement := make(map[string]string, len(request.RuntimeTargets))
	selected := make([]string, 0, len(request.RuntimeTargets))
	for _, target := range request.RuntimeTargets {
		if target.ExecutionChannelRef != channelRef || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
			target.SiteRefs[0] != siteRef || target.NodeRefs[0] != nodeRef {
			return nil, nil, fmt.Errorf("runtime target %q is outside the router execution channel", target.RequirementID)
		}
		targetHash, err := hashRuntimeTarget(target)
		if err != nil {
			return nil, nil, fmt.Errorf("hash runtime target %q: %w", target.RequirementID, err)
		}
		route, exists := routes[targetHash]
		if !exists || route.requirementID != target.RequirementID {
			return nil, nil, fmt.Errorf("runtime target %q has no exact registered owner", target.RequirementID)
		}
		groups[targetHash] = &requestGroup{runtime: []runtimeexecutor.RuntimeTarget{target}}
		selectedByRequirement[target.RequirementID] = targetHash
		selected = append(selected, targetHash)
	}

	for _, health := range request.HealthTargets {
		matchedHash := ""
		matches := 0
		for _, target := range request.RuntimeTargets {
			if !sameTargetScope(health, target) || !healthTargetsRuntime(health, target) {
				continue
			}
			matchedHash = selectedByRequirement[target.RequirementID]
			matches++
		}
		if matches != 1 {
			return nil, nil, fmt.Errorf("health target %q has %d exact runtime owners", health.RequirementID, matches)
		}
		groups[matchedHash].health = append(groups[matchedHash].health, health)
	}

	bindings := make(map[string]runtimeexecutor.AccessBinding, len(request.AccessBindings))
	for _, binding := range request.AccessBindings {
		bindings[binding.ID] = binding
	}
	for targetHash, group := range groups {
		if len(group.runtime) != 1 || len(group.health) == 0 {
			return nil, nil, fmt.Errorf("runtime owner %q has an incomplete runtime/health set", group.runtime[0].RequirementID)
		}
		target := group.runtime[0]
		for _, bindingRef := range target.AccessBindingRefs {
			binding, exists := bindings[bindingRef]
			if !exists || binding.RuntimeRequirementID != target.RequirementID {
				return nil, nil, fmt.Errorf("runtime target %q has an absent or foreign access binding", target.RequirementID)
			}
			group.accessBindings = append(group.accessBindings, binding)
		}
		sort.Slice(group.accessBindings, func(i, j int) bool { return group.accessBindings[i].ID < group.accessBindings[j].ID })
		groups[targetHash] = group
	}
	sort.Slice(selected, func(i, j int) bool {
		return routes[selected[i]].requirementID < routes[selected[j]].requirementID
	})
	return groups, selected, nil
}

func sameTargetScope(health runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget) bool {
	return len(health.SiteRefs) == 1 && len(health.NodeRefs) == 1 &&
		health.SiteRefs[0] == target.SiteRefs[0] && health.NodeRefs[0] == target.NodeRefs[0]
}

func healthTargetsRuntime(health runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget) bool {
	return health.TargetKind == "module" && health.TargetRef == target.ModuleRef ||
		health.TargetKind == "provider" && health.TargetRef == target.ProviderRef ||
		health.TargetKind == "runtime" && health.TargetRef == target.InstanceRef
}

func hashRuntimeTarget(target runtimeexecutor.RuntimeTarget) (string, error) {
	canonical, err := json.Marshal(target)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func cloneRuntimeTarget(target runtimeexecutor.RuntimeTarget) runtimeexecutor.RuntimeTarget {
	request := runtimeexecutor.CloneExecutionRequest(runtimeexecutor.ExecutionRequest{RuntimeTargets: []runtimeexecutor.RuntimeTarget{target}})
	return request.RuntimeTargets[0]
}
