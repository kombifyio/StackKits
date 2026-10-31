package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/workloadremoval"
)

// A host installation's Product Apply placement records Site and node. Its
// exact execution channel is held in the sealed runtime request, so removal
// must admit the intersection of both authorities and the caller's placement.
func appliedMarketplaceRemovalPlacement(applied architecturev2.VerifiedApplyResult, request runtimeexecutor.ExecutionRequest, options architectureV2ExecutionCLIOptions) (workloadremoval.AppliedPlacement, error) {
	summary := applied.Summary()
	if summary.AppliedRequestDigest != request.RequestDigest {
		return workloadremoval.AppliedPlacement{}, errors.New("Marketplace Apply result differs from its sealed runtime request")
	}
	candidate := workloadremoval.AppliedPlacement{
		AppliedRequestDigest: request.RequestDigest, WorkloadRef: "marketplace",
		SiteRef: strings.TrimSpace(options.localSiteRef), NodeRef: strings.TrimSpace(options.localNodeRef),
		ExecutionChannelRef: strings.TrimSpace(options.localChannelRef),
	}
	if candidate.SiteRef == "" || candidate.NodeRef == "" || candidate.ExecutionChannelRef == "" {
		return candidate, errors.New("Marketplace removal requires an explicit local Site, node and execution channel")
	}
	matches := 0
	for _, workload := range summary.AppliedWorkloads {
		if workload.WorkloadRef != candidate.WorkloadRef || workload.RuntimeOwnerRef != architecturev2renderer.MarketplaceCoolifyModuleRef {
			continue
		}
		for _, placement := range workload.Placements {
			if placement.SiteRef == candidate.SiteRef && placement.NodeRef == candidate.NodeRef &&
				(placement.ExecutionChannelRef == "" || placement.ExecutionChannelRef == candidate.ExecutionChannelRef) {
				candidate.RequirementID, candidate.InstanceRef = workload.RequirementID, workload.InstanceRef
				matches++
			}
		}
	}
	if matches != 1 {
		return candidate, fmt.Errorf("Marketplace removal requires exactly one verified local Apply placement; found %d", matches)
	}
	selected, err := workloadremoval.SelectAppliedWorkloadPlacement(request, candidate)
	if err != nil {
		return candidate, err
	}
	if !architectureV2MarketplaceRemovalTarget(selected.RuntimeTargets[0]) {
		return candidate, errors.New("Marketplace removal requires its exact local native installation target")
	}
	return candidate, nil
}
