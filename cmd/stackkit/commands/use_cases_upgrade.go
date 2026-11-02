package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applicationrelease"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

const applicationReleaseTimeout = 30 * time.Minute

type useCaseUpgradeOptions struct {
	to, rollback, finalize, status, operationID, expectedHash string
	ownerApprove, json                                        bool
}

func newUseCasesUpgradeCommand() *cobra.Command {
	o := &useCaseUpgradeOptions{}
	cmd := &cobra.Command{
		Use:   "upgrade [slug]",
		Short: "Move one application to another admitted release, keeping its prior data for a rollback",
		Long: `Move one selected application to another release its catalog admits, without
touching unrelated applications. The command stops only that application,
copies each of its governed volumes into a retained volume, reads the copy back
as a file-tree digest, records an owner-signed checkpoint and selects the target
release. The application stays stopped until normal generate and apply start
the target release.

--rollback <operation-id> stops the application again, verifies the retained
copy, copies it back, proves the live data equals the checkpoint and selects the
prior release; the next apply starts the prior release on exactly that data, and
data written since the checkpoint is discarded. Rolling back a checkpoint that
never completed releases it and restarts the unchanged application. While an
operation is open, apply starts only the release it expects; without one, apply
refuses every move to an earlier release.
--finalize <operation-id> removes the retained data once the change is
accepted (or after the rollback was applied). --status reads a record.`,
		Example: `  # Move Vaultwarden to an admitted later release, then apply it
  stackkit use-cases upgrade vault --to 1.37.5 --owner-approve
  stackkit generate && stackkit apply

  # Return to the prior release and its data, then apply it
  stackkit use-cases upgrade --rollback release.vault.20261101T120000Z --owner-approve
  stackkit generate && stackkit apply

  # Accept the result and remove the retained data
  stackkit use-cases upgrade --finalize release.vault.20261101T120000Z --owner-approve`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			modes := 0
			for _, set := range []bool{o.to != "", o.rollback != "", o.finalize != "", o.status != ""} {
				if set {
					modes++
				}
			}
			switch {
			case modes != 1:
				return errors.New("choose exactly one of <slug> --to, --rollback, --finalize or --status")
			case o.to != "" && len(args) != 1:
				return errors.New("--to requires the use case slug")
			case o.to == "" && len(args) != 0:
				return errors.New("--rollback, --finalize and --status take an operation ID, not a slug")
			case o.status != "":
				return runApplicationReleaseStatus(cmd, *o)
			case !o.ownerApprove:
				return errors.New("--owner-approve is required: the application is stopped while its data is copied")
			case o.rollback != "":
				return runApplicationReleaseRollback(cmd, *o)
			case o.finalize != "":
				return runApplicationReleaseFinalize(cmd, *o)
			}
			return runApplicationReleaseForward(cmd, strings.TrimSpace(args[0]), *o)
		},
	}
	cmd.Flags().StringVar(&o.to, "to", "", "Admitted release to move the selected application to")
	cmd.Flags().StringVar(&o.rollback, "rollback", "", "Return the application of this release operation to its prior release and data")
	cmd.Flags().StringVar(&o.finalize, "finalize", "", "Remove the retained data of this release operation")
	cmd.Flags().StringVar(&o.status, "status", "", "Read one release operation record")
	cmd.Flags().StringVar(&o.operationID, "operation-id", "", "Resume this interrupted release checkpoint instead of starting a new one")
	cmd.Flags().StringVar(&o.expectedHash, "expected-spec-hash", "", "Require the current canonical spec hash")
	cmd.Flags().BoolVar(&o.ownerApprove, "owner-approve", false, "Approve stopping the application while its data is copied")
	cmd.Flags().BoolVar(&o.json, "json", false, "Emit the release operation result as JSON")
	return cmd
}

// releaseWorkload is the one selected application of a use case whose catalog
// admits a release choice, read from the applied plan.
type releaseWorkload struct {
	workload, module, entry string
	current                 architecturev2renderer.WorkloadRelease
	releases                []architecturev2renderer.WorkloadRelease
}

func selectReleaseWorkload(plan generationartifact.VerifiedPlan, workloads []string) (releaseWorkload, error) {
	var document struct {
		Workloads []struct {
			ID          string `json:"id"`
			Alternative struct {
				ModuleRef string `json:"moduleRef"`
			} `json:"alternative"`
		} `json:"workloads"`
		Modules []struct {
			ID      string `json:"id"`
			Runtime struct {
				EntryComponentRef string `json:"entryComponentRef"`
				Image             struct {
					Ref    string `json:"ref"`
					Digest string `json:"digest"`
				} `json:"image"`
			} `json:"runtime"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(plan.Canonical(), &document); err != nil {
		return releaseWorkload{}, err
	}
	var found []releaseWorkload
	for _, workload := range document.Workloads {
		if !containsString(workloads, workload.ID) {
			continue
		}
		releases := architecturev2renderer.AdmittedWorkloadReleases(workload.Alternative.ModuleRef)
		if len(releases) < 2 {
			continue
		}
		for _, module := range document.Modules {
			if module.ID != workload.Alternative.ModuleRef {
				continue
			}
			release, ok := architecturev2renderer.AdmittedWorkloadReleaseForImage(module.ID, module.Runtime.Image.Ref, module.Runtime.Image.Digest)
			if !ok {
				return releaseWorkload{}, fmt.Errorf("%s runs an image outside its admitted releases", workload.ID)
			}
			current := architecturev2renderer.WorkloadRelease{Release: release, Ref: module.Runtime.Image.Ref, Digest: module.Runtime.Image.Digest}
			found = append(found, releaseWorkload{workload: workload.ID, module: module.ID, entry: module.Runtime.EntryComponentRef, current: current, releases: releases})
		}
	}
	if len(found) != 1 {
		return releaseWorkload{}, errors.New("the use case selects no single application with an admitted release choice")
	}
	return found[0], nil
}

func runApplicationReleaseForward(cmd *cobra.Command, slug string, o useCaseUpgradeOptions) error {
	wd := getWorkDir()
	ctx, cancel := context.WithTimeout(cmd.Context(), applicationReleaseTimeout)
	defer cancel()
	if o.operationID != "" {
		return resumeApplicationReleaseCheckpoint(ctx, cmd, slug, o)
	}
	applied, err := inspectNativeV2AppliedAuthority(ctx, wd, specFile)
	if err != nil {
		return fmt.Errorf("a release change starts from the current applied plan: %w", err)
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return err
	}
	workloads, err := service.SelectedUseCaseWorkloads(applied.Plan, slug)
	if err != nil {
		return err
	}
	selected, err := selectReleaseWorkload(applied.Plan, workloads)
	if err != nil {
		return err
	}
	var target *architecturev2renderer.WorkloadRelease
	var names []string
	for index, release := range selected.releases {
		names = append(names, release.Release)
		if release.Release == o.to {
			target = &selected.releases[index]
		}
	}
	if target == nil {
		return fmt.Errorf("release %q is not admitted for %s; admitted releases: %s", o.to, selected.workload, strings.Join(names, ", "))
	}
	if target.Release == selected.current.Release {
		return fmt.Errorf("%s already runs release %s", selected.workload, target.Release)
	}
	if later, comparable := releaseOrder(target.Release, selected.current.Release); !comparable || !later {
		return fmt.Errorf("%s is earlier than the running %s; return with stackkit use-cases upgrade --rollback <operation-id> of the change that left it", target.Release, selected.current.Release)
	}
	open, found, err := applicationrelease.OpenFor(wd, selected.workload)
	if err != nil {
		return err
	}
	if found {
		return fmt.Errorf("release operation %s still governs %s (%s); roll it back or finalize it first", open.OperationID, selected.workload, open.State)
	}
	specPath, _, _, err := config.NewLoader(wd).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	current, err := service.ValidateStackSpec(raw)
	if err != nil {
		return err
	}
	if o.expectedHash != "" && o.expectedHash != current.SpecHash {
		return fmt.Errorf("expected spec hash does not match current intent; current spec hash: %s", current.SpecHash)
	}
	if err := requireStandardSingleNodeIntent(current.CanonicalStackSpec); err != nil {
		return err
	}
	candidate, err := service.MaterializeUseCaseChange(current.CanonicalStackSpec, selected.workload, architecturev2.UseCaseChange{Settings: map[string]any{"release": target.Release}}, false)
	if err != nil {
		return err
	}
	record := applicationrelease.Record{
		OperationID: "release." + selected.workload + "." + time.Now().UTC().Format("20060102T150405Z"),
		State:       applicationrelease.StateCheckpointStarted, UseCaseRef: slug, WorkloadRef: selected.workload, ModuleRef: selected.module,
		EntryComponent: selected.entry, NodeRef: applied.Owner.Binding.NodeRef,
		PriorRelease: selected.current.Release, PriorImage: applicationrelease.Image{Ref: selected.current.Ref, Digest: selected.current.Digest},
		TargetRelease: target.Release, TargetImage: applicationrelease.Image{Ref: target.Ref, Digest: target.Digest},
		PriorSpecHash: current.SpecHash, TargetSpecHash: candidate.SpecHash, PriorSpec: current.CanonicalStackSpec, TargetSpec: candidate.CanonicalStackSpec,
	}
	return checkpointApplicationRelease(ctx, cmd, o, service, specPath, record, func(record applicationrelease.Record) (applicationrelease.Record, error) {
		if open, found, err := applicationrelease.OpenFor(wd, selected.workload); err != nil {
			return record, err
		} else if found {
			return record, fmt.Errorf("release operation %s now governs %s", open.OperationID, selected.workload)
		}
		// The lifecycle lock fences the applied plan the checkpoint binds.
		locked, err := inspectNativeV2AppliedAuthority(ctx, wd, specFile)
		if err != nil {
			return record, err
		}
		if locked.Plan.Binding() != applied.Plan.Binding() || locked.Lineage != applied.Lineage {
			return record, errors.New("the applied plan changed before the release checkpoint")
		}
		authority, err := restoreactivation.DeriveApplicationReleaseAuthority(wd, applied.Plan, applied.Manifest, applied.Lineage, record.OperationID, selected.workload)
		if err != nil {
			return record, err
		}
		record.ComposeProject, record.PlanHash, record.KopiaHelperImage = authority.ComposeRuntimes[0].Project, authority.PlanHash, authority.KopiaHelperImage
		for _, volume := range authority.VolumeDetails {
			record.Volumes = append(record.Volumes, applicationrelease.Volume{ComponentRef: volume.ComponentRef, LogicalName: volume.LogicalName, ComposeProject: volume.ComposeProject, LiveName: volume.LiveName, RollbackName: volume.RollbackName})
		}
		return record, nil
	})
}

// resumeApplicationReleaseCheckpoint continues an interrupted checkpoint from
// its signed record alone: live data was not changed, and the intent may
// already name the target release.
func resumeApplicationReleaseCheckpoint(ctx context.Context, cmd *cobra.Command, slug string, o useCaseUpgradeOptions) error {
	wd := getWorkDir()
	record, err := applicationrelease.Read(wd, strings.TrimSpace(o.operationID))
	if err != nil {
		return err
	}
	if record.State != applicationrelease.StateCheckpointStarted || record.UseCaseRef != slug || record.TargetRelease != o.to {
		return fmt.Errorf("%s is no interrupted checkpoint of %s to %s", record.OperationID, slug, o.to)
	}
	service, specPath, err := applicationReleaseIntentAuthority(wd)
	if err != nil {
		return err
	}
	return checkpointApplicationRelease(ctx, cmd, o, service, specPath, record, nil)
}

// checkpointApplicationRelease holds the application, retains and reads back
// each governed volume and switches the intent, all under the lifecycle lock.
func checkpointApplicationRelease(ctx context.Context, cmd *cobra.Command, o useCaseUpgradeOptions, service *architecturev2.Service, specPath string, record applicationrelease.Record, bind func(applicationrelease.Record) (applicationrelease.Record, error)) error {
	wd := getWorkDir()
	err := withLifecycleMutation(wd, "use-cases upgrade", func() error {
		var err error
		if bind != nil {
			if record, err = bind(record); err != nil {
				return err
			}
		} else if record, err = applicationrelease.Read(wd, record.OperationID); err != nil {
			return err
		} else if record.State != applicationrelease.StateCheckpointStarted {
			return fmt.Errorf("release operation %s is %s, not an interrupted checkpoint", record.OperationID, record.State)
		}
		if record, err = applicationrelease.Persist(wd, record); err != nil {
			return err
		}
		authority := applicationReleaseAuthority(record)
		if err := restoreactivation.HoldApplicationProject(ctx, wd, authority, record.ComposeProject); err != nil {
			return err
		}
		runtime, err := restoreactivation.NewDockerRuntime(wd)
		if err != nil {
			return err
		}
		for index, volume := range authority.VolumeDetails {
			// A volume read back before an interruption stays complete: Apply
			// starts nothing of this application while the checkpoint is open.
			if record.Volumes[index].Checkpoint != nil {
				continue
			}
			if err := runtime.PrepareRollback(ctx, authority, volume); err != nil {
				return err
			}
			live, err := runtime.DigestVolume(ctx, authority, volume, false)
			if err != nil {
				return err
			}
			retained, err := restoreactivation.DigestRetainedVolume(ctx, wd, authority, volume)
			if err != nil {
				return err
			}
			if retained != live {
				return fmt.Errorf("the retained copy of %s differs from its live data", volume.LiveName)
			}
			record.Volumes[index].Checkpoint = &applicationrelease.Readback{Files: retained.Files, Bytes: retained.Bytes, Digest: retained.Digest}
			if record, err = applicationrelease.Persist(wd, record); err != nil {
				return err
			}
		}
		if err := persistApplicationReleaseIntent(wd, service, specPath, record.PriorSpecHash, record.TargetSpec); err != nil {
			return err
		}
		record.State = applicationrelease.StateCheckpointed
		record, err = applicationrelease.Persist(wd, record)
		return err
	})
	if err != nil {
		return fmt.Errorf("release checkpoint %s did not complete; resume with --operation-id %s or release it with --rollback %s: %w", record.OperationID, record.OperationID, record.OperationID, err)
	}
	return writeApplicationReleaseResult(cmd, o, record, "Checkpointed "+record.WorkloadRef+" "+record.PriorRelease+"; selected "+record.TargetRelease,
		[]string{"stackkit generate", "stackkit apply", "stackkit use-cases upgrade --rollback " + record.OperationID + " --owner-approve (return to " + record.PriorRelease + " with its data)", "stackkit use-cases upgrade --finalize " + record.OperationID + " --owner-approve (accept and remove the retained data)"})
}

// persistApplicationReleaseIntent switches the StackSpec from one exact
// release intent to the other; an already switched spec is left as it is.
func persistApplicationReleaseIntent(wd string, service *architecturev2.Service, specPath, fromHash string, to []byte) error {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	current, err := service.ValidateStackSpec(raw)
	if err != nil {
		return err
	}
	candidate, err := service.ValidateStackSpec(to)
	if err != nil {
		return err
	}
	if current.SpecHash == candidate.SpecHash {
		return nil
	}
	if current.SpecHash != fromHash {
		return fmt.Errorf("intent changed after the release checkpoint (spec hash %s); restore it before continuing", current.SpecHash)
	}
	_, err = stackspecintent.Persist(stackspecintent.Request{WorkspaceRoot: wd, SpecPath: specPath, Candidate: candidate.CanonicalStackSpec, ExpectedSpecHash: fromHash, BuildVersion: version, Authority: service})
	return err
}

func requireStandardSingleNodeIntent(canonical []byte) error {
	var document struct {
		Install struct {
			Mode string `json:"mode"`
		} `json:"install"`
		Nodes []json.RawMessage `json:"nodes"`
		Sites []json.RawMessage `json:"sites"`
	}
	if err := json.Unmarshal(canonical, &document); err != nil {
		return err
	}
	if document.Install.Mode == "advanced" || len(document.Nodes) != 1 || len(document.Sites) != 1 {
		return errors.New("a scoped release change runs on a Standard single-node installation; managed intent changes through Techstack Advanced dispatch")
	}
	return nil
}
