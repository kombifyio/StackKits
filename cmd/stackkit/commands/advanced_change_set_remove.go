package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/workloadremoval"
)

// Candidate omission is not an executor instruction. Retire absent workloads
// from their original applied custody, on this node only, while the enclosing
// Advanced transaction still holds the exact baseline and rollback checkpoint.
func (orchestration *advancedTerramateOrchestration) removeRetiredWorkloads(ctx context.Context, workspace string, verified verifiedAdvancedMutation) error {
	if orchestration == nil {
		return refuseUnorchestratedWorkloadRetirement(verified)
	}
	type workload struct {
		ID       string   `json:"id"`
		SiteRefs []string `json:"siteRefs"`
		NodeRefs []string `json:"nodeRefs"`
	}
	var baseline, candidate struct {
		Workloads []workload `json:"workloads"`
	}
	if err := json.Unmarshal(verified.baselinePlan.Canonical(), &baseline); err != nil {
		return err
	}
	if err := json.Unmarshal(verified.candidatePlan.Canonical(), &candidate); err != nil {
		return err
	}
	binding := verified.admission.owner.Binding
	var removed []string
	for i := len(baseline.Workloads) - 1; i >= 0; i-- {
		old := baseline.Workloads[i]
		if !slices.Contains(old.SiteRefs, binding.SiteRef) || !slices.Contains(old.NodeRefs, binding.NodeRef) {
			continue
		}
		if !slices.ContainsFunc(candidate.Workloads, func(next workload) bool { return next.ID == old.ID }) {
			removed = append(removed, old.ID)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	removed, err := useCaseRemovalOrder(verified.baselinePlan, removed)
	if err != nil {
		return err
	}
	inventory, err := readArchitectureV2Inventory(workspace, "")
	if err != nil {
		return err
	}
	options := architectureV2ExecutionCLIOptions{context: ctx, inventoryData: inventory,
		localSiteRef: binding.SiteRef, localNodeRef: binding.NodeRef, localChannelRef: binding.ChannelRef,
		dataDisposition: workloadremoval.DataDispositionRetain,
		removalEvidenceSink: func(evidence workloadremoval.Evidence) error {
			orchestration.removed = append(orchestration.removed, evidence)
			return nil
		},
	}
	gate := newArchitectureV2ExecutionGate()
	authority, err := gate.openV2Authority(workspace, architectureV2Remove, options)
	if err != nil {
		return err
	}
	if closer, ok := authority.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	_, manifestPath, receiptPath := verified.baselinePlan.MetadataPaths(workspace)
	manifest, err := generationartifact.ReadManifest(manifestPath)
	if err != nil {
		return err
	}
	receipt, err := generationartifact.ReadReceipt(receiptPath)
	if err != nil {
		return err
	}
	return withArchitectureV2OutputLock(workspace, verified.baselinePlan.OutputRoot(), func(transaction *confinedfs.Transaction, _ *confinedfs.OutputLock) error {
		if err := architecturev2.RequireNoPendingOutputTransaction(transaction, verified.baselinePlan.OutputRoot()); err != nil {
			return err
		}
		options.workloadRef, options.workloadRefs = removed[0], removed
		if err := gate.removeV2Workload(workspace, options, authority, verified.baselinePlan, manifest, receipt, transaction); err != nil {
			return fmt.Errorf("remove retired local workloads before candidate adoption: %w", err)
		}
		if len(orchestration.removed) != len(removed) {
			return errors.New("retired workloads have no complete terminal removal evidence")
		}
		for index, evidence := range orchestration.removed {
			if evidence.Authority.WorkloadRef != removed[index] {
				return errors.New("retired workload evidence does not match the exact selected workload")
			}
		}
		return nil
	})
}

func refuseUnorchestratedWorkloadRetirement(verified verifiedAdvancedMutation) error {
	var baseline, candidate struct {
		Workloads []struct {
			ID string `json:"id"`
		} `json:"workloads"`
	}
	if err := json.Unmarshal(verified.baselinePlan.Canonical(), &baseline); err != nil {
		return err
	}
	if err := json.Unmarshal(verified.candidatePlan.Canonical(), &candidate); err != nil {
		return err
	}
	for _, old := range baseline.Workloads {
		if !slices.ContainsFunc(candidate.Workloads, func(next struct {
			ID string `json:"id"`
		}) bool {
			return next.ID == old.ID
		}) {
			return fmt.Errorf("candidate retires workload %q; this Advanced operation has no native removal orchestration; use the Terramate change-set apply lifecycle", old.ID)
		}
	}
	return nil
}
