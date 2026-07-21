// Package runtimeexecutordispatch routes an already-authorized shared runtime
// request across exact opaque execution channels. It owns no transport,
// endpoint discovery, credentials, provider lifecycle, or product policy.
package runtimeexecutordispatch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

var channelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$`)

// Route binds one opaque plan-owned channel identity to one executor selected
// by the owning control plane. ChannelRef is not an endpoint or credential.
type Route struct {
	ChannelRef string
	Executor   runtimeexecutor.Executor
}

// Dispatcher is a provider-neutral composite executor. Every child invocation
// is re-sealed and independently verified through runtimeexecutor.Invoke.
type Dispatcher struct {
	identity runtimeexecutor.ExecutorIdentity
	routes   map[string]routedExecutor
}

type routedExecutor struct {
	identity runtimeexecutor.ExecutorIdentity
	executor runtimeexecutor.Executor
}

func New(identity runtimeexecutor.ExecutorIdentity, routes []Route) (*Dispatcher, error) {
	if len(routes) == 0 {
		return nil, errors.New("execution-channel dispatcher requires at least one route")
	}
	registered := make(map[string]routedExecutor, len(routes))
	for index, route := range routes {
		if !channelPattern.MatchString(route.ChannelRef) || route.Executor == nil {
			return nil, fmt.Errorf("execution-channel route %d is invalid", index)
		}
		if _, duplicate := registered[route.ChannelRef]; duplicate {
			return nil, fmt.Errorf("execution channel %q is registered more than once", route.ChannelRef)
		}
		childIdentity, err := safeExecutorIdentity(route.Executor)
		if err != nil {
			return nil, fmt.Errorf("execution-channel route %d identity: %w", index, err)
		}
		registered[route.ChannelRef] = routedExecutor{identity: childIdentity, executor: route.Executor}
	}
	return &Dispatcher{identity: identity, routes: registered}, nil
}

func (d *Dispatcher) Identity() runtimeexecutor.ExecutorIdentity { return d.identity }

func (d *Dispatcher) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("execution-channel dispatcher requires a context")
	}
	if d == nil || len(d.routes) == 0 {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("execution-channel dispatcher is not initialized")
	}
	if err := request.Validate(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate sealed dispatcher request: %w", err)
	}
	if request.Executor != d.identity {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("dispatcher request does not bind the selected dispatcher identity")
	}
	groups, err := partitionRequest(request, d.routes)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	channels := make([]string, 0, len(groups))
	for channelRef := range groups {
		channels = append(channels, channelRef)
	}
	sort.Strings(channels)
	outcome := runtimeexecutor.ExecutionOutcome{}
	for _, channelRef := range channels {
		if err := ctx.Err(); err != nil {
			return runtimeexecutor.ExecutionOutcome{}, err
		}
		group := groups[channelRef]
		child := d.routes[channelRef]
		childRequest, err := sealChildRequest(request, child.identity, group)
		if err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("seal execution-channel child request for %q: %w", channelRef, err)
		}
		var result runtimeexecutor.ExecutionResult
		if len(childRequest.AccessBindings) == 0 {
			result, err = runtimeexecutor.Invoke(ctx, child.executor, childRequest)
		} else {
			authorizationTime, parseErr := time.Parse(time.RFC3339Nano, childRequest.AuthorizationTime)
			if parseErr != nil {
				return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("parse execution-channel authorization time for %q: %w", channelRef, parseErr)
			}
			result, err = runtimeexecutor.InvokeAt(ctx, child.executor, childRequest, authorizationTime)
		}
		if err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("execute channel %q: %w", channelRef, err)
		}
		outcome.Runtime = append(outcome.Runtime, result.Runtime...)
		outcome.Health = append(outcome.Health, result.Health...)
	}
	return outcome, nil
}

type requestGroup struct {
	runtime        []runtimeexecutor.RuntimeTarget
	health         []runtimeexecutor.HealthTarget
	accessBindings []runtimeexecutor.AccessBinding
}

func partitionRequest(request runtimeexecutor.ExecutionRequest, routes map[string]routedExecutor) (map[string]*requestGroup, error) {
	groups := make(map[string]*requestGroup)
	for _, target := range request.RuntimeTargets {
		if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || !channelPattern.MatchString(target.ExecutionChannelRef) {
			return nil, fmt.Errorf("runtime target %q is not independently channel-bound to one Site/node", target.RequirementID)
		}
		if _, exists := routes[target.ExecutionChannelRef]; !exists {
			return nil, fmt.Errorf("runtime target %q references an unregistered execution channel", target.RequirementID)
		}
		group := groups[target.ExecutionChannelRef]
		if group == nil {
			group = &requestGroup{}
			groups[target.ExecutionChannelRef] = group
		}
		group.runtime = append(group.runtime, target)
	}
	for _, health := range request.HealthTargets {
		if len(health.SiteRefs) != 1 || len(health.NodeRefs) != 1 {
			return nil, fmt.Errorf("health target %q is not independently bound to one Site/node", health.RequirementID)
		}
		matchedChannel := ""
		matches := 0
		for _, target := range request.RuntimeTargets {
			if !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
				continue
			}
			if health.TargetKind == "module" && health.TargetRef == target.ModuleRef ||
				health.TargetKind == "runtime" && health.TargetRef == target.InstanceRef {
				matchedChannel = target.ExecutionChannelRef
				matches++
			}
		}
		if matches != 1 {
			return nil, fmt.Errorf("health target %q has %d exact runtime/channel owners", health.RequirementID, matches)
		}
		groups[matchedChannel].health = append(groups[matchedChannel].health, health)
	}
	bindings := make(map[string]runtimeexecutor.AccessBinding, len(request.AccessBindings))
	for _, binding := range request.AccessBindings {
		bindings[binding.ID] = binding
	}
	for channelRef, group := range groups {
		if len(group.runtime) == 0 || len(group.health) == 0 {
			return nil, fmt.Errorf("execution channel %q has an incomplete runtime/health set", channelRef)
		}
		referenced := make(map[string]struct{})
		for _, target := range group.runtime {
			for _, bindingRef := range target.AccessBindingRefs {
				binding, exists := bindings[bindingRef]
				if !exists || binding.RuntimeRequirementID != target.RequirementID {
					return nil, fmt.Errorf("runtime target %q has an absent or foreign access binding", target.RequirementID)
				}
				if _, duplicate := referenced[bindingRef]; duplicate {
					return nil, fmt.Errorf("execution channel %q references access binding %q more than once", channelRef, bindingRef)
				}
				referenced[bindingRef] = struct{}{}
				group.accessBindings = append(group.accessBindings, binding)
			}
		}
		sort.Slice(group.accessBindings, func(i, j int) bool { return group.accessBindings[i].ID < group.accessBindings[j].ID })
	}
	return groups, nil
}

func safeExecutorIdentity(executor runtimeexecutor.Executor) (identity runtimeexecutor.ExecutorIdentity, err error) {
	defer func() {
		if recover() != nil {
			identity = runtimeexecutor.ExecutorIdentity{}
			err = errors.New("child executor identity panicked")
		}
	}()
	return executor.Identity(), nil
}

func sealChildRequest(parent runtimeexecutor.ExecutionRequest, identity runtimeexecutor.ExecutorIdentity, group *requestGroup) (runtimeexecutor.ExecutionRequest, error) {
	referenced := make(map[string]struct{})
	for _, target := range group.runtime {
		for _, artifactRef := range target.ArtifactRefs {
			referenced[artifactRef] = struct{}{}
		}
	}
	artifacts := make([]runtimeexecutor.Artifact, 0, len(referenced)+1)
	for _, artifact := range parent.Artifacts {
		_, needed := referenced[artifact.ID]
		if artifact.OwnerKind == "plan" || needed {
			artifacts = append(artifacts, artifact)
			delete(referenced, artifact.ID)
		}
	}
	if len(referenced) != 0 {
		return runtimeexecutor.ExecutionRequest{}, errors.New("execution-channel child request is missing a target artifact")
	}
	return runtimeexecutor.SealRequest(runtimeexecutor.ExecutionRequest{
		APIVersion: runtimeexecutor.APIVersion, Executor: identity,
		PlanHash: parent.PlanHash, ManifestHash: parent.ManifestHash,
		GenerationReceiptHash: parent.GenerationReceiptHash, RequirementsHash: parent.RequirementsHash,
		EvidenceBundleHash: parent.EvidenceBundleHash,
		AuthorizationTime:  parent.AuthorizationTime,
		RuntimeTargets:     append([]runtimeexecutor.RuntimeTarget(nil), group.runtime...),
		HealthTargets:      append([]runtimeexecutor.HealthTarget(nil), group.health...),
		AccessBindings:     append([]runtimeexecutor.AccessBinding(nil), group.accessBindings...),
		Artifacts:          artifacts,
	})
}
