package commands

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kombifyio/stackkits/internal/advanceddrift"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/terramatehost"
	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
)

// Per-stack drift (docs/ARCHITECTURE.md "Advanced drift per stack (Stage 1)"):
// under the `terramate` generation target the drift report adds one
// `stacks[]` entry per stack of the local host project next to the unchanged
// native drift fields.

var loadAdvancedDriftLayout = loadCurrentAdvancedDriftLayout

// loadCurrentAdvancedDriftLayout renders the verified plan and derives the
// local host layout (Site and node of the Owner custody binding; a one-host
// graph needs neither).
func loadCurrentAdvancedDriftLayout(
	ctx context.Context, workspace string, observation architectureV2DriftObservation,
) (terramatehost.Layout, error) {
	if len(observation.Plan.Canonical()) == 0 {
		return terramatehost.Layout{}, errors.New("per-stack drift requires the verified canonical ResolvedPlan")
	}
	registry, err := architecturev2renderer.NewProductRegistry()
	if err != nil {
		return terramatehost.Layout{}, err
	}
	rendered, err := architecturev2renderer.RenderVerifiedPlan(ctx, observation.Plan, registry)
	if err != nil {
		return terramatehost.Layout{}, fmt.Errorf("render the verified plan for per-stack drift: %w", err)
	}
	var siteRef, nodeRef string
	if owner, err := localevidence.LoadOwnerCustody(workspace); err == nil {
		siteRef, nodeRef = owner.Binding.SiteRef, owner.Binding.NodeRef
	}
	return terramatehost.PlanFromArtifacts(rendered.Artifacts(), siteRef, nodeRef)
}

// attachAdvancedStackDrift adds the per-stack section to a drift report of a
// `terramate` plan and emits one advanced.drift.stack rollout event per
// stack. Reports of every other target stay byte-identical.
func attachAdvancedStackDrift(
	ctx context.Context, workspace string,
	observation architectureV2DriftObservation, report *driftReport,
) error {
	if observation.GenerationTarget != terramatestackgraph.GenerationTarget {
		return nil
	}
	layout, err := loadAdvancedDriftLayout(ctx, workspace, observation)
	if err != nil {
		return err
	}
	tools, toolsErr := terramatehost.PackagedTools()
	stacks, err := advanceddrift.Detect(ctx, advanceddrift.Request{
		WorkspaceRoot: workspace, Layout: layout, Tools: tools, ToolsErr: toolsErr,
		Event: func(stack advanceddrift.Stack) {
			rolloutEvent(advanceddrift.EventPhase, stack.Status,
				"advanced drift stack "+stack.StackID+" "+stack.Status,
				advanceddrift.EventAttributes(stack))
		},
	})
	if err != nil {
		return fmt.Errorf("detect per-stack drift: %w", err)
	}
	report.Mode = "advanced"
	report.StackID = layout.Manifest.StackID
	report.DetectedAt = time.Now().UTC().Format(time.RFC3339)
	report.Stacks = stacks
	report.Status = advanceddrift.OverallStatus(report.HasDrift, stacks)
	return nil
}

// detectCurrentDriftReport observes the current local state and builds the
// complete drift report, including the per-stack section.
func detectCurrentDriftReport(ctx context.Context, workspace string) (driftReport, error) {
	observation, err := observeArchitectureV2Drift(ctx, workspace, specFile)
	if err != nil {
		return driftReport{}, fmt.Errorf("detect Architecture v2 drift: %w", err)
	}
	report, err := newDriftReport(observation)
	if err != nil {
		return driftReport{}, err
	}
	if err := attachAdvancedStackDrift(ctx, workspace, observation, &report); err != nil {
		return driftReport{}, err
	}
	return report, nil
}

// observeAdvancedReconcileDrift is the pre-reconcile drift report of an
// Advanced reconcile. Its per-stack plans read OpenTofu state, so they run
// under the exclusive lifecycle lock, as in `stackkit drift detect`.
func observeAdvancedReconcileDrift(ctx context.Context) (driftReport, error) {
	workspace := getWorkDir()
	observation, err := observeArchitectureV2Drift(ctx, workspace, specFile)
	if err != nil {
		return driftReport{}, fmt.Errorf("detect Architecture v2 drift: %w", err)
	}
	report, err := newDriftReport(observation)
	if err != nil {
		return driftReport{}, err
	}
	err = withLifecycleMutation(workspace, "drift-reconcile", func() error {
		return attachAdvancedStackDrift(ctx, workspace, observation, &report)
	})
	return report, err
}

// advancedDriftReconcileResult is the Advanced reconcile result: the
// unchanged stackkit.advanced-mutation/v1 fields plus the post-reconcile
// drift report.
type advancedDriftReconcileResult struct {
	advancedMutationResult
	DriftReport *driftReport `json:"driftReport,omitempty"`
}
