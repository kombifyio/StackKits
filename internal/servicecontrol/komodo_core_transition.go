package servicecontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

// TransitionNativeKomodoCore transfers only an already custodied host recipe.
// Fresh generated-plan selection is verified inside the existing lifecycle
// lock; the subsequent normal Apply still owns all recipe and runtime effects.
func TransitionNativeKomodoCore(ctx context.Context, workspace, workload, resume string, approved bool, selectPlan func(platformdeploy.HostDeployment) (string, error)) (ManagerTransitionResult, error) {
	return transitionNativeKomodoCore(ctx, workspace, workload, resume, approved, selectPlan, observeManagerBindings, platformdeploy.LoadOwnerNativeKomodoConfig)
}

func transitionNativeKomodoCore(ctx context.Context, workspace, workload, resume string, approved bool, selectPlan func(platformdeploy.HostDeployment) (string, error), observe func(context.Context, string, string) (string, string, error), nativeConfig func(context.Context, string) (platformdeploy.HTTPConfig, error)) (ManagerTransitionResult, error) {
	return nativeKomodoCoreHandover(ctx, workspace, workload, resume, approved, false, selectPlan, observe, nativeConfig)
}

// CancelNativeKomodoCoreTransfer restores the signed source claim only while
// this protocol has never sent an attachment request to the target Core.
func CancelNativeKomodoCoreTransfer(ctx context.Context, workspace, workload, resume string, approved bool) (ManagerTransitionResult, error) {
	return nativeKomodoCoreHandover(ctx, workspace, workload, resume, approved, true, nil, observeManagerBindings, platformdeploy.LoadOwnerNativeKomodoConfig)
}

func nativeKomodoCoreHandover(ctx context.Context, workspace, workload, resume string, approved, cancelTransfer bool, selectPlan func(platformdeploy.HostDeployment) (string, error), observe func(context.Context, string, string) (string, string, error), nativeConfig func(context.Context, string) (platformdeploy.HTTPConfig, error)) (ManagerTransitionResult, error) {
	result := ManagerTransitionResult{APIVersion: "stackkit.manager-transition-result/v1", From: "komodo", To: "komodo"}
	if ctx == nil || !approved || (!cancelTransfer && selectPlan == nil) || (cancelTransfer && resume == "") || !contractIDPattern.MatchString(workload) {
		return result, errors.New("native Core transition requires exact workload, fresh generated selection and explicit owner approval")
	}
	err := lifecyclemutation.WithIdleMutation(workspace, lifecyclemutation.JoinRequest{}, func() error {
		owner, err := localevidence.LoadOwnerCustody(workspace)
		if err != nil {
			return err
		}
		project := "stackkit-" + workload + "-" + owner.Binding.NodeRef
		result.Project = project
		state, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
		if err != nil {
			return err
		}
		if state.WorkloadRef != workload || state.SiteRef != owner.Binding.SiteRef || state.NodeRef != owner.Binding.NodeRef || platformdeploy.HostDeploymentManager(state) != "komodo" || state.Deployment.Platform != "komodo" {
			return errors.New("native Core transfer requires the existing exact owner/node Komodo workload")
		}
		h := state.Handover
		if !cancelTransfer && h != nil && h.Status == "pending" && h.TargetCreateState == "restoring-source" {
			return &Error{ReasonCode: "workload_manager_handover_pending", Retryable: true,
				Message: "Core handover has durably selected cancellation; resume only the exact operation with --cancel"}
		}
		var planHash string
		if cancelTransfer {
			if h == nil || h.Status != "pending" {
				return errors.New("cancellation requires the exact pending signed Core transfer")
			}
			planHash = h.TargetPlanHash
		} else {
			planHash, err = selectPlan(state)
			if err != nil {
				return err
			}
		}
		target, err := nativeConfig(ctx, workspace)
		if err != nil {
			return err
		}
		if target.ServerID == state.ServerID || target.ServerID == "" {
			return errors.New("workload already names the native Core or target has no identity")
		}
		source, err := platformdeploy.LoadOwnerKomodoConfigForServer(ctx, workspace, state.ServerID)
		if err != nil {
			return err
		}
		oldCore, newCore := platformdeploy.NewKomodoAdapter(source), platformdeploy.NewKomodoAdapter(target)
		directory := filepath.Join(workspace, ".stackkit", "runtime", "applications", project)
		recipe, runtime, err := observe(ctx, workspace, project)
		if err != nil {
			return err
		}
		if h != nil && h.Status == "pending" {
			if resume != h.ID || h.CoreProtocol != "komodo-core-detached-create/v2" || h.From != "komodo" || h.To != "komodo" || h.SourceServerID != source.ServerID || h.SourceExternalID != state.Deployment.ExternalID || h.TargetServerID != target.ServerID || h.TargetPlanHash != planHash || h.RecipeDigest != recipe || h.RuntimeDigest != runtime {
				return errors.New("resume requires the original signed Core transfer, Plan and unchanged recipe/runtime")
			}
		} else {
			if resume != "" {
				return errors.New("requested Core handover is not pending")
			}
			attachment, err := oldCore.HostStackAttachment(ctx, state.Deployment, directory)
			if err != nil {
				return err
			}
			if attachment != source.ServerID {
				return errors.New("source Core attachment differs from node custody")
			}
			if err := newCore.HostStackNameAvailable(ctx, project); err != nil {
				return err
			}
			var nonce [16]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				return err
			}
			h = &platformdeploy.HostManagerHandover{ID: "handover-" + hex.EncodeToString(nonce[:]), From: "komodo", To: "komodo", Status: "pending", RecipeDigest: recipe, RuntimeDigest: runtime, SourceServerID: source.ServerID, SourceExternalID: state.Deployment.ExternalID, TargetServerID: target.ServerID, TargetPlanHash: planHash, TargetCreateState: "not-started", CoreProtocol: "komodo-core-detached-create/v2", SourceDetachState: "not-started"}
			state.Manager, state.Handover = "komodo", h
			if err := platformdeploy.SaveOwnerHostDeployment(workspace, project, state); err != nil {
				return err
			}
		}
		result.OperationID, result.Status = h.ID, "pending"
		result.RecipeDigest, result.RuntimeDigest = recipe, runtime
		if h.SourceDetachState == "not-started" {
			// An inverse write can outlive its caller too. Journal and send it
			// once, before permitting either target activation or cancellation.
			h.SourceDetachState = "detaching"
			if err := platformdeploy.SaveOwnerHostDeployment(workspace, project, state); err != nil {
				return err
			}
			if err := oldCore.SetHostStackAttachment(ctx, state.Deployment, directory, false); err != nil {
				return err
			}
		} else if h.SourceDetachState == "detaching" {
			attachment, err := oldCore.HostStackAttachment(ctx, state.Deployment, directory)
			if err != nil {
				return err
			}
			if attachment != "" {
				return errors.New("initial source detachment has no verified completion; retain pending custody and reconcile only by readback")
			}
		}
		if h.SourceDetachState != "detached" {
			h.SourceDetachState = "detached"
			if err := platformdeploy.SaveOwnerHostDeployment(workspace, project, state); err != nil {
				return err
			}
		}
		if cancelTransfer {
			if h.TargetCreateState == "attaching" {
				return errors.New("a target attachment may be in flight; recover this exact handover forward before any cancellation")
			}
			// Every possible earlier Create request in this signed protocol has
			// server_id="". Even a delayed commit cannot reacquire control.
			if err := newCore.RequireUnattachedHandoverTarget(ctx, project, directory, h.ID, h.TargetExternalID); err != nil {
				return err
			}
			if h.TargetCreateState != "restoring-source" {
				// Send restoration once. Persist direction before the write,
				// excluding forward recovery if its response is lost.
				h.TargetCreateState = "restoring-source"
				if err := platformdeploy.SaveOwnerHostDeployment(workspace, project, state); err != nil {
					return err
				}
				if err := oldCore.SetHostStackAttachment(ctx, state.Deployment, directory, true); err != nil {
					return err
				}
			} else {
				// The original request may still be in flight. Repeating it
				// could commit after this handover and a later transition.
				attachment, err := oldCore.HostStackAttachment(ctx, state.Deployment, directory)
				if err != nil {
					return err
				}
				if attachment != source.ServerID {
					return errors.New("source restoration has no verified completion; retain pending custody and reconcile only by readback")
				}
			}
			afterRecipe, afterRuntime, err := observe(ctx, workspace, project)
			if err != nil {
				return err
			}
			if recipe != afterRecipe || runtime != afterRuntime {
				return errors.New("cancellation changed recipe/runtime; custody remains pending")
			}
			if err := newCore.RequireUnattachedHandoverTarget(ctx, project, directory, h.ID, h.TargetExternalID); err != nil {
				return err
			}
			h.Status = "cancelled"
			if err := platformdeploy.SaveOwnerHostDeployment(workspace, project, state); err != nil {
				return err
			}
			confirmed, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
			if err != nil {
				return err
			}
			if err := platformdeploy.RequireHostDeploymentManager(confirmed, "komodo"); err != nil {
				return err
			}
			result.Status, result.ExternalID = "cancelled", state.Deployment.ExternalID
			result.EvidenceRef, err = (&Controller{workspace: workspace}).persistEvidence(confirmed)
			return err
		}
		// Detach and independently read back before giving the native Core any
		// attachment. Neither operation executes Compose or deletes data.
		sourceAttachment, err := oldCore.HostStackAttachment(ctx, state.Deployment, directory)
		if err != nil {
			return err
		}
		if sourceAttachment != "" {
			return errors.New("source is no longer detached; retain pending custody without repeating the original detach")
		}
		var ref platformdeploy.DeploymentRef
		if h.TargetCreateState == "not-started" {
			h.TargetCreateState = "creating"
			if err := platformdeploy.SaveOwnerHostDeployment(workspace, project, state); err != nil {
				return err
			}
			ref, err = newCore.CreateHostStackForHandover(ctx, project, directory, h.ID)
		} else if h.TargetCreateState == "creating" {
			ref, err = newCore.FindHostStackForHandover(ctx, project, directory, h.ID)
		} else {
			ref = platformdeploy.DeploymentRef{Platform: "komodo", AppName: project, ExternalID: h.TargetExternalID}
		}
		if err != nil {
			return err
		}
		attachment, err := newCore.HostStackHandoverAttachment(ctx, ref, directory, h.ID)
		if err != nil {
			return err
		}
		if h.TargetCreateState != "attaching" {
			if attachment != "" {
				return errors.New("target acquired an attachment before its signed attachment phase")
			}
			h.TargetExternalID, h.TargetCreateState = ref.ExternalID, "created"
			if err := platformdeploy.SaveOwnerHostDeployment(workspace, project, state); err != nil {
				return err
			}
			h.TargetCreateState = "attaching"
			if err := platformdeploy.SaveOwnerHostDeployment(workspace, project, state); err != nil {
				return err
			}
			if err := newCore.SetHostStackAttachment(ctx, ref, directory, true); err != nil {
				return err
			}
		} else if attachment != target.ServerID {
			// A prior request may still commit after a later handover. Do not
			// send another attachment while that original outcome is unknown.
			return errors.New("target attachment has no verified completion; retain pending custody and reconcile only by readback")
		}
		attachment, err = newCore.HostStackHandoverAttachment(ctx, ref, directory, h.ID)
		if err != nil {
			return err
		}
		if attachment != target.ServerID {
			return errors.New("native target attachment does not match the signed transfer")
		}
		afterRecipe, afterRuntime, err := observe(ctx, workspace, project)
		if err != nil {
			return err
		}
		if recipe != afterRecipe || runtime != afterRuntime {
			return errors.New("Core transfer changed recipe, container, mount or address; custody remains pending")
		}
		// Recheck the detached source after target observation, then commit the
		// single signed controller identity. Other workload records are untouched.
		attachment, err = oldCore.HostStackAttachment(ctx, state.Deployment, directory)
		if err != nil {
			return err
		}
		if attachment != "" {
			return errors.New("source controller regained an attachment; custody remains pending")
		}
		state.ServerID, state.Deployment, h.Status, state.PlanBinding = target.ServerID, ref, "succeeded", nil
		if err := platformdeploy.SaveOwnerHostDeployment(workspace, project, state); err != nil {
			return err
		}
		confirmed, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
		if err != nil {
			return err
		}
		if err := platformdeploy.RequireHostDeploymentManager(confirmed, "komodo"); err != nil {
			return err
		}
		result.ExternalID, result.Status = ref.ExternalID, "succeeded"
		result.EvidenceRef, err = (&Controller{workspace: workspace}).persistEvidence(confirmed)
		return err
	})
	if err != nil && result.Status == "pending" {
		return result, &Error{ReasonCode: "workload_manager_handover_pending", Retryable: true, Message: fmt.Sprintf("Core handover %s remains pending; inspect and resume only this operation: %v", result.OperationID, err)}
	}
	return result, err
}
