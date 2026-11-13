package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/advancedtrust"
	"github.com/kombifyio/stackkits/internal/applicationrelease"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

const applicationReleaseTimeout = 30 * time.Minute

type useCaseUpgradeOptions struct {
	to, digest, rollback, finalize, status, operationID, expectedHash, capability string
	ownerApprove, acceptUntested, apply, overrides, json                          bool
}

func newUseCasesUpgradeCommand() *cobra.Command {
	o := &useCaseUpgradeOptions{}
	cmd := &cobra.Command{
		Use:   "upgrade [slug]",
		Short: "Move one application to a newer release of its entry image, keeping its prior data for a rollback",
		Long: `Move one selected application to a newer release of its entry image, without
touching unrelated applications or the StackKits release. --to names a tested
release the catalog admits; --to with --digest sha256:... names an untested
stable upstream release of the same image inside the module's release track
(same major; a "patch" track also keeps the minor), which needs
--accept-untested. A major release moves only with a StackKits release. An
untested tag is re-resolved from its registry for this node's platform and
refused when it no longer names that digest. The selection is stored as the
workload's release override {version, ref, digest, tested}. A later StackKits
release whose default is at or above the override supersedes it; a newer
in-track override is kept (--overrides reports which).

The command stops only that application, copies each of its governed volumes
into a retained volume, reads the copy back as a file-tree digest, records an
owner-signed checkpoint and selects the target release. The application stays
stopped until generate and apply start the target release.

--apply runs the whole update on this node: checkpoint, generate, apply and
live verify. When generate, apply or verify fails, it rolls back to the prior
release and data, applies again and finalizes that rollback. With --json the
result names update_outcome (updated, rolled_back, rollback_failed or
refused), reason_code and the prior and target version and digest.

--rollback <operation-id> stops the application again, verifies the retained
copy, copies it back, proves the live data equals the checkpoint and selects the
prior release; the next apply starts the prior release on exactly that data, and
data written since the checkpoint is discarded. Rolling back a checkpoint that
never completed releases it and restarts the unchanged application. While an
operation is open, apply starts only the release it expects (or a later
StackKits default that supersedes it); without one, apply refuses every move
to an earlier release.
--finalize <operation-id> removes the retained data once the change is
accepted (or after the rollback was applied). --status reads a record.

On a node Techstack manages, a forward change also needs --capability: the
exact Techstack-signed application-release.update capability for this use
case, release, digest and tested or untested kind, verified offline against
the Owner-approved Advanced trust before the application is stopped. The
record keeps that authority, and the result reports the prior and target
StackSpec hashes so Techstack's desired state follows the override. A
standalone node takes no capability.`,
		Example: `  # Move Vaultwarden to a tested catalog release, then apply it
  stackkit use-cases upgrade vault --to 1.37.5 --owner-approve
  stackkit generate && stackkit apply

  # Move to an untested in-track release in one step, rolling back on failure
  stackkit use-cases upgrade vault --to 1.37.6 --digest sha256:<manifest digest> --accept-untested --owner-approve --apply --json

  # Return to the prior release and its data, then apply it
  stackkit use-cases upgrade --rollback release.vault.20261101T120000Z --owner-approve
  stackkit generate && stackkit apply

  # Accept the result and remove the retained data
  stackkit use-cases upgrade --finalize release.vault.20261101T120000Z --owner-approve

  # Report every release override and whether this release keeps it
  stackkit use-cases upgrade --overrides --json`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			modes := 0
			for _, set := range []bool{o.to != "", o.rollback != "", o.finalize != "", o.status != "", o.overrides} {
				if set {
					modes++
				}
			}
			switch {
			case modes != 1:
				return errors.New("choose exactly one of <slug> --to, --rollback, --finalize, --status or --overrides")
			case o.to == "" && (o.digest != "" || o.acceptUntested || o.apply || o.capability != ""):
				return errors.New("--digest, --accept-untested, --apply and --capability belong to <slug> --to")
			case o.to != "" && len(args) != 1:
				return errors.New("--to requires the use case slug")
			case o.to == "" && len(args) != 0:
				return errors.New("--rollback, --finalize, --status and --overrides take no slug")
			case o.status != "":
				return runApplicationReleaseStatus(cmd, *o)
			case o.overrides:
				return runApplicationReleaseOverrides(cmd, *o)
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
	cmd.Flags().StringVar(&o.to, "to", "", "Release (image tag) to move the selected application to")
	cmd.Flags().StringVar(&o.digest, "digest", "", "Exact sha256 manifest digest of the --to release; required for a release outside the catalog")
	cmd.Flags().BoolVar(&o.acceptUntested, "accept-untested", false, "Accept a release the catalog has not tested (inside the module's release track only)")
	cmd.Flags().BoolVar(&o.apply, "apply", false, "Run checkpoint, generate, apply and live verify, rolling back automatically on failure")
	cmd.Flags().StringVar(&o.rollback, "rollback", "", "Return the application of this release operation to its prior release and data")
	cmd.Flags().StringVar(&o.finalize, "finalize", "", "Remove the retained data of this release operation")
	cmd.Flags().StringVar(&o.status, "status", "", "Read one release operation record")
	cmd.Flags().BoolVar(&o.overrides, "overrides", false, "Report every application release override and whether this release keeps it")
	cmd.Flags().StringVar(&o.operationID, "operation-id", "", "Resume this interrupted release checkpoint instead of starting a new one")
	cmd.Flags().StringVar(&o.expectedHash, "expected-spec-hash", "", "Require the current canonical spec hash")
	cmd.Flags().StringVar(&o.capability, "capability", "", "Techstack application-release.update capability file; required on a Techstack-managed node")
	cmd.Flags().BoolVar(&o.ownerApprove, "owner-approve", false, "Approve stopping the application while its data is copied")
	cmd.Flags().BoolVar(&o.json, "json", false, "Emit the release operation result as JSON")
	return cmd
}

// releaseWorkload is the one selected application of a use case whose catalog
// admits a release choice, read from the applied plan.
type releaseWorkload struct {
	workload, module, entry string
	current                 architecturev2renderer.WorkloadRelease
	choice                  resolvedplan.ComponentReleaseChoice
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
				Components []struct {
					ID    string `json:"id"`
					Image struct {
						Ref    string `json:"ref"`
						Digest string `json:"digest"`
					} `json:"image"`
				} `json:"components"`
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
		choice, ok := architecturev2renderer.WorkloadReleaseChoice(workload.Alternative.ModuleRef)
		if !ok {
			continue
		}
		for _, module := range document.Modules {
			if module.ID != workload.Alternative.ModuleRef {
				continue
			}
			// A native host installation (the Marketplace Coolify) declares no
			// runtime image; its entry component carries the pin.
			image := module.Runtime.Image
			if image.Ref == "" {
				for _, component := range module.Runtime.Components {
					if component.ID == module.Runtime.EntryComponentRef {
						image = component.Image
					}
				}
			}
			release, ok := architecturev2renderer.AdmittedWorkloadReleaseForImage(module.ID, image.Ref, image.Digest)
			if !ok {
				release = imageReleaseTag(image.Ref)
				if !architecturev2renderer.WorkloadReleaseInTrack(module.ID, release) {
					return releaseWorkload{}, fmt.Errorf("%s runs an image outside its admitted releases and track", workload.ID)
				}
			}
			current := architecturev2renderer.WorkloadRelease{Release: release, Ref: image.Ref, Digest: image.Digest}
			found = append(found, releaseWorkload{workload: workload.ID, module: module.ID, entry: module.Runtime.EntryComponentRef, current: current, choice: choice})
		}
	}
	if len(found) != 1 {
		return releaseWorkload{}, errors.New("the use case selects no single application with a release choice")
	}
	return found[0], nil
}

func imageReleaseTag(ref string) string {
	separator := strings.LastIndex(ref, ":")
	if separator <= strings.LastIndex(ref, "/") || strings.Contains(ref, "@") {
		return ""
	}
	return ref[separator+1:]
}

// Reason codes of a refused application update.
const (
	updateRefusedNoReleaseChoice   = "no_release_choice"
	updateRefusedUntestedNoDigest  = "untested_requires_digest"
	updateRefusedNotAccepted       = "untested_not_accepted"
	updateRefusedInvalidRelease    = "release_invalid"
	updateRefusedOutOfTrack        = "release_outside_track"
	updateRefusedSuperseded        = "release_not_after_default"
	updateRefusedNotNewer          = "release_not_newer"
	updateRefusedDigestMismatch    = "digest_mismatch"
	updateRefusedRegistry          = "registry_unresolved"
	updateRefusedOperationOpen     = "release_operation_open"
	updateRefusedIntent            = "intent_refused"
	updateFailedGenerate           = "generate_failed"
	updateFailedApply              = "apply_failed"
	updateFailedVerify             = "verify_failed"
	updateFailedCheckpoint         = "checkpoint_failed"
	updateFailedRollback           = "rollback_failed"
	updateOutcomeUpdated           = "updated"
	updateOutcomeRolledBack        = "rolled_back"
	updateOutcomeRollbackFailed    = "rollback_failed"
	updateOutcomeRefused           = "refused"
	applicationUpdateResultVersion = "stackkit.application-update-result/v1"
)

// applicationUpdateRefusal refuses an update before any runtime change.
type applicationUpdateRefusal struct {
	reason string
	err    error
}

func (r *applicationUpdateRefusal) Error() string { return r.err.Error() }
func (r *applicationUpdateRefusal) Unwrap() error { return r.err }

func refuseUpdate(reason string, format string, args ...any) error {
	return &applicationUpdateRefusal{reason: reason, err: fmt.Errorf(format, args...)}
}

// releaseUpdateTarget resolves --to and --digest into the exact override the
// catalog authority admits for this workload.
func releaseUpdateTarget(selected releaseWorkload, o useCaseUpgradeOptions) (resolvedplan.ComponentReleaseOverride, error) {
	var target *resolvedplan.ComponentReleaseOverride
	for _, image := range selected.choice.Admitted {
		if image.Release != o.to {
			continue
		}
		if o.digest != "" && o.digest != image.Digest {
			return resolvedplan.ComponentReleaseOverride{}, refuseUpdate(updateRefusedDigestMismatch, "tested release %s is admitted with digest %s, not %s", o.to, image.Digest, o.digest)
		}
		target = &resolvedplan.ComponentReleaseOverride{Version: image.Release, Ref: image.Ref, Digest: image.Digest, Tested: true}
	}
	if target == nil {
		names := make([]string, len(selected.choice.Admitted))
		for index, image := range selected.choice.Admitted {
			names[index] = image.Release
		}
		if o.digest == "" {
			return resolvedplan.ComponentReleaseOverride{}, refuseUpdate(updateRefusedUntestedNoDigest, "release %q is not a tested release of %s (tested: %s); an untested release needs --digest sha256:... and --accept-untested", o.to, selected.workload, strings.Join(names, ", "))
		}
		if !o.acceptUntested {
			return resolvedplan.ComponentReleaseOverride{}, refuseUpdate(updateRefusedNotAccepted, "release %s of %s is untested (tested: %s); pass --accept-untested to accept it", o.to, selected.workload, strings.Join(names, ", "))
		}
		defaultRef := selected.choice.Admitted[0].Ref
		target = &resolvedplan.ComponentReleaseOverride{Version: o.to, Ref: defaultRef[:strings.LastIndex(defaultRef, ":")] + ":" + o.to, Digest: o.digest}
	}
	state, err := resolvedplan.EvaluateComponentRelease(selected.choice, *target)
	if err != nil {
		reason := updateRefusedInvalidRelease
		if resolvedplan.StableComponentRelease(target.Version) && !resolvedplan.ComponentReleaseInTrack(selected.choice.Track, selected.choice.Admitted[0].Release, target.Version) {
			reason = updateRefusedOutOfTrack
		}
		return resolvedplan.ComponentReleaseOverride{}, refuseUpdate(reason, "%s: %v", selected.workload, err)
	}
	if state != resolvedplan.ComponentReleaseApplied {
		return resolvedplan.ComponentReleaseOverride{}, refuseUpdate(updateRefusedSuperseded, "release %s is not after the StackKits default %s of %s", target.Version, selected.choice.Admitted[0].Release, selected.workload)
	}
	if target.Version == selected.current.Release {
		return resolvedplan.ComponentReleaseOverride{}, refuseUpdate(updateRefusedNotNewer, "%s already runs release %s", selected.workload, target.Version)
	}
	if later, comparable := releaseOrder(target.Version, selected.current.Release); !comparable || !later {
		return resolvedplan.ComponentReleaseOverride{}, refuseUpdate(updateRefusedNotNewer, "%s is earlier than the running %s; return with stackkit use-cases upgrade --rollback <operation-id> of the change that left it", target.Version, selected.current.Release)
	}
	return *target, nil
}

func runApplicationReleaseForward(cmd *cobra.Command, slug string, o useCaseUpgradeOptions) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), applicationReleaseTimeout)
	defer cancel()
	if o.operationID != "" {
		record, err := resumeApplicationReleaseCheckpoint(ctx, slug, o)
		return finishApplicationReleaseForward(ctx, cmd, o, record, err)
	}
	record, err := startApplicationReleaseCheckpoint(ctx, slug, o)
	return finishApplicationReleaseForward(ctx, cmd, o, record, err)
}

func startApplicationReleaseCheckpoint(ctx context.Context, slug string, o useCaseUpgradeOptions) (applicationrelease.Record, error) {
	wd := getWorkDir()
	applied, err := inspectNativeV2AppliedAuthority(ctx, wd, specFile)
	if err != nil {
		return applicationrelease.Record{}, fmt.Errorf("a release change starts from the current applied plan: %w", err)
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return applicationrelease.Record{}, err
	}
	workloads, err := service.SelectedUseCaseWorkloads(applied.Plan, slug)
	if err != nil {
		return applicationrelease.Record{}, err
	}
	selected, err := selectReleaseWorkload(applied.Plan, workloads)
	if err != nil {
		return applicationrelease.Record{}, refuseUpdate(updateRefusedNoReleaseChoice, "%v", err)
	}
	target, err := releaseUpdateTarget(selected, o)
	if err != nil {
		return applicationrelease.Record{}, err
	}
	open, found, err := applicationrelease.OpenFor(wd, selected.workload)
	if err != nil {
		return applicationrelease.Record{}, err
	}
	if found {
		return applicationrelease.Record{}, refuseUpdate(updateRefusedOperationOpen, "release operation %s still governs %s (%s); roll it back or finalize it first", open.OperationID, selected.workload, open.State)
	}
	specPath, _, _, err := config.NewLoader(wd).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return applicationrelease.Record{}, err
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return applicationrelease.Record{}, err
	}
	current, err := service.ValidateStackSpec(raw)
	if err != nil {
		return applicationrelease.Record{}, err
	}
	if o.expectedHash != "" && o.expectedHash != current.SpecHash {
		return applicationrelease.Record{}, refuseUpdate(updateRefusedIntent, "expected spec hash does not match current intent; current spec hash: %s", current.SpecHash)
	}
	managed, err := admitApplicationReleaseIntent(wd, current.CanonicalStackSpec, applied.Plan, slug, target, o.capability)
	if err != nil {
		return applicationrelease.Record{}, err
	}
	candidate, err := service.MaterializeUseCaseChange(current.CanonicalStackSpec, selected.workload, architecturev2.UseCaseChange{Settings: map[string]any{resolvedplan.ComponentReleaseSetting: target.Setting()}}, false)
	if err != nil {
		return applicationrelease.Record{}, refuseUpdate(updateRefusedIntent, "%v", err)
	}
	// An untested tag is re-resolved now, for this node's platform: a moved
	// tag or a missing platform refuses before the application is stopped.
	if !target.Tested {
		if err := nativehost.ResolveApplicationReleaseDigest(ctx, target.Ref, target.Digest, goruntime.GOOS, goruntime.GOARCH); err != nil {
			if errors.Is(err, nativehost.ErrApplicationReleaseDigestMismatch) {
				return applicationrelease.Record{}, refuseUpdate(updateRefusedDigestMismatch, "%v", err)
			}
			return applicationrelease.Record{}, refuseUpdate(updateRefusedRegistry, "%v", err)
		}
	}
	record := applicationrelease.Record{
		OperationID: "release." + selected.workload + "." + time.Now().UTC().Format("20060102T150405Z"),
		State:       applicationrelease.StateCheckpointStarted, UseCaseRef: slug, WorkloadRef: selected.workload, ModuleRef: selected.module,
		EntryComponent: selected.entry, NodeRef: applied.Owner.Binding.NodeRef,
		PriorRelease: selected.current.Release, PriorImage: applicationrelease.Image{Ref: selected.current.Ref, Digest: selected.current.Digest},
		TargetRelease: target.Version, TargetImage: applicationrelease.Image{Ref: target.Ref, Digest: target.Digest}, TargetUntested: !target.Tested, Managed: managed,
		PriorSpecHash: current.SpecHash, TargetSpecHash: candidate.SpecHash, PriorSpec: current.CanonicalStackSpec, TargetSpec: candidate.CanonicalStackSpec,
	}
	return checkpointApplicationRelease(ctx, service, specPath, record, func(record applicationrelease.Record) (applicationrelease.Record, error) {
		if open, found, err := applicationrelease.OpenFor(wd, selected.workload); err != nil {
			return record, err
		} else if found {
			return record, fmt.Errorf("release operation %s now governs %s", open.OperationID, selected.workload)
		}
		// The intent switch at the end compares the exact prior spec hash, so a
		// concurrent change of intent refuses the checkpoint. The plan gate takes
		// this same lifecycle lock and is not re-entered here.
		authority, err := restoreactivation.DeriveApplicationReleaseAuthority(wd, applied.Plan, applied.Manifest, applied.Lineage, record.OperationID, selected.workload)
		if err != nil {
			return record, err
		}
		record.ComposeProject, record.PlanHash, record.KopiaHelperImage = authority.ComposeRuntimes[0].Project, authority.PlanHash, authority.KopiaHelperImage
		for _, volume := range authority.VolumeDetails {
			record.Volumes = append(record.Volumes, applicationrelease.Volume{ComponentRef: volume.ComponentRef, LogicalName: volume.LogicalName, ComposeProject: volume.ComposeProject, LiveName: volume.LiveName, RollbackName: volume.RollbackName})
		}
		// The Marketplace Coolify keeps APP_KEY, database credentials, its API
		// token and server identity in private runtime files beside its
		// database volume; both move back together on a rollback.
		if selected.module == architecturev2renderer.MarketplaceCoolifyModuleRef {
			if record.RuntimeFiles, err = applicationrelease.CaptureRuntimeFiles(wd, marketplaceReleaseRuntimeFiles()); err != nil {
				return record, err
			}
		}
		return record, nil
	})
}

// resumeApplicationReleaseCheckpoint continues an interrupted checkpoint from
// its signed record alone: live data was not changed, and the intent may
// already name the target release.
func resumeApplicationReleaseCheckpoint(ctx context.Context, slug string, o useCaseUpgradeOptions) (applicationrelease.Record, error) {
	wd := getWorkDir()
	record, err := applicationrelease.Read(wd, strings.TrimSpace(o.operationID))
	if err != nil {
		return record, err
	}
	if record.State != applicationrelease.StateCheckpointStarted || record.UseCaseRef != slug || record.TargetRelease != o.to {
		return record, fmt.Errorf("%s is no interrupted checkpoint of %s to %s", record.OperationID, slug, o.to)
	}
	// A managed checkpoint resumes only under the authority it started with.
	if managed, err := applicationReleaseIntentManaged(wd, record.PriorSpec); err != nil {
		return record, err
	} else if managed && record.Managed == nil {
		return record, refuseUpdate(updateRefusedIntent, "%s started without the Techstack authority this managed node requires", record.OperationID)
	}
	service, specPath, err := applicationReleaseIntentAuthority(wd)
	if err != nil {
		return record, err
	}
	return checkpointApplicationRelease(ctx, service, specPath, record, nil)
}

// finishApplicationReleaseForward reports a checkpoint, or with --apply runs
// the update to its outcome.
func finishApplicationReleaseForward(ctx context.Context, cmd *cobra.Command, o useCaseUpgradeOptions, record applicationrelease.Record, err error) error {
	if o.apply {
		// A checkpoint that started and failed has stopped the application:
		// it is released at once, which restarts the unchanged application.
		if err != nil && applicationrelease.ValidOperationID(record.OperationID) {
			if stored, readErr := applicationrelease.Read(getWorkDir(), record.OperationID); readErr == nil && stored.State == applicationrelease.StateCheckpointStarted {
				return applyApplicationRelease(ctx, cmd, o, stored, err)
			}
		}
		if err != nil {
			var refusal *applicationUpdateRefusal
			result := applicationUpdateResult{Outcome: updateOutcomeRefused, Reason: updateFailedCheckpoint, Message: err.Error()}
			if errors.As(err, &refusal) {
				result.Reason = refusal.reason
			}
			if record.OperationID != "" {
				result = applicationUpdateResultFor(record, result.Outcome, result.Reason, result.Message)
			}
			return writeApplicationUpdateResult(cmd, o, result, err)
		}
		return applyApplicationRelease(ctx, cmd, o, record, nil)
	}
	if err != nil {
		return err
	}
	return writeApplicationReleaseResult(cmd, o, record, "Checkpointed "+record.WorkloadRef+" "+record.PriorRelease+"; selected "+record.TargetRelease,
		[]string{"stackkit generate", "stackkit apply", "stackkit use-cases upgrade --rollback " + record.OperationID + " --owner-approve (return to " + record.PriorRelease + " with its data)", "stackkit use-cases upgrade --finalize " + record.OperationID + " --owner-approve (accept and remove the retained data)"})
}

// checkpointApplicationRelease holds the application, retains and reads back
// each governed volume and switches the intent, all under the lifecycle lock.
func checkpointApplicationRelease(ctx context.Context, service *architecturev2.Service, specPath string, record applicationrelease.Record, bind func(applicationrelease.Record) (applicationrelease.Record, error)) (applicationrelease.Record, error) {
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
		if err := applicationrelease.RetainRuntimeFiles(wd, record); err != nil {
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
		return nil
	})
	// The intent writer takes the lifecycle lock itself. Until the record
	// completes, Apply starts nothing of this application.
	if err == nil {
		err = persistApplicationReleaseIntent(wd, service, specPath, record.PriorSpecHash, record.TargetSpec)
	}
	if err == nil {
		record.State = applicationrelease.StateCheckpointed
		record, err = applicationrelease.Persist(wd, record)
	}
	if err != nil {
		return record, fmt.Errorf("release checkpoint %s did not complete; resume with --operation-id %s or release it with --rollback %s: %w", record.OperationID, record.OperationID, record.OperationID, err)
	}
	return record, nil
}

// marketplaceReleaseRuntimeFiles is the private Marketplace runtime closure a
// release checkpoint retains (localbackuppolicy.MarketplaceRuntimeFiles).
func marketplaceReleaseRuntimeFiles() []string {
	names := localbackuppolicy.MarketplaceRuntimeFiles()
	paths := make([]string, len(names))
	for index, name := range names {
		paths[index] = path.Join(localbackuppolicy.MarketplaceRuntimeDirectory, name)
	}
	return paths
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

// admitApplicationReleaseIntent admits the intent change of one scoped release
// change. A Standard single-node installation needs only the Owner's approval.
// A node Techstack manages changes its intent only under the exact
// application-release.update capability Techstack issued for this use case,
// release, digest and release kind, verified offline against the
// Owner-approved trust, as the managed transfer steps are; the returned
// authority is kept in the owner-signed record. Both modes run on one node of
// one site: a multi-node intent changes through Advanced change sets.
func admitApplicationReleaseIntent(workspace string, canonical []byte, plan generationartifact.VerifiedPlan, slug string, target resolvedplan.ComponentReleaseOverride, capabilityPath string) (*applicationrelease.ManagedAuthority, error) {
	var document struct {
		Nodes []json.RawMessage `json:"nodes"`
		Sites []json.RawMessage `json:"sites"`
	}
	if err := json.Unmarshal(canonical, &document); err != nil {
		return nil, err
	}
	managed, err := applicationReleaseIntentManaged(workspace, canonical)
	if err != nil {
		return nil, refuseUpdate(updateRefusedIntent, "%v", err)
	}
	if len(document.Nodes) != 1 || len(document.Sites) != 1 {
		return nil, refuseUpdate(updateRefusedIntent, "a scoped release change runs on a single-node installation; a multi-node intent changes through Techstack Advanced change sets")
	}
	if !managed {
		if strings.TrimSpace(capabilityPath) != "" {
			return nil, refuseUpdate(updateRefusedIntent, "a standalone installation updates without a Techstack capability; this node holds no Advanced trust")
		}
		return nil, nil
	}
	refuse := func(err error) error {
		if reason, ok := advancedcapability.Reason(err); ok {
			return refuseUpdate(string(reason), "%v", err)
		}
		return refuseUpdate(updateRefusedIntent, "%v", err)
	}
	if strings.TrimSpace(capabilityPath) == "" {
		return nil, refuse(&advancedcapability.Denial{Code: advancedcapability.ReasonCapabilityRequired, Field: "capability", Detail: "a release change on a Techstack-managed node requires its exact application-release.update capability; nothing was stopped"})
	}
	raw, err := readAdvancedRegular(resolvePathFromWorkDir(workspace, capabilityPath), maxAdvancedTrustBundleBytes, "Advanced capability")
	if err != nil {
		return nil, refuse(err)
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return nil, refuse(&advancedcapability.Denial{Code: advancedcapability.ReasonCapabilityScopeMismatch, Field: "ownerRef", Detail: "verified local Owner custody is required"})
	}
	trusted, err := advancedtrust.Load(workspace)
	if err != nil {
		return nil, refuse(&advancedcapability.Denial{Code: advancedcapability.ReasonTrustBundleUnavailable, Field: "trustBundle", Detail: "verified Owner-approved local trust is required"})
	}
	var local struct {
		StackID string `json:"stackId"`
	}
	if err := json.Unmarshal(plan.Canonical(), &local); err != nil {
		return nil, refuse(err)
	}
	kind := "tested"
	if !target.Tested {
		kind = "untested"
	}
	trust := trusted.TrustBundle()
	grant, err := advancedcapability.Verify(raw, advancedcapability.Request{
		Now: time.Now().UTC(), TrustBundle: &trust, StackID: local.StackID, OwnerRef: owner.OwnerRef,
		Operation:          advancedcapability.OperationApplicationReleaseUpdate,
		ApplicationRelease: &advancedcapability.ApplicationRelease{ReleaseKind: kind, TargetDigest: target.Digest, TargetRelease: target.Version, UseCaseRef: slug},
	})
	if err != nil {
		return nil, refuse(err)
	}
	digest := sha256.Sum256(raw)
	return &applicationrelease.ManagedAuthority{CapabilityID: grant.CapabilityID, CapabilitySHA256: "sha256:" + hex.EncodeToString(digest[:]), IssuerID: grant.IssuerID, KeyID: grant.KeyID}, nil
}

// applicationReleaseIntentManaged reports whether the intent is Techstack
// managed (Advanced): install.mode advanced or an imported Advanced trust.
func applicationReleaseIntentManaged(workspace string, canonical []byte) (bool, error) {
	var document struct {
		Install struct {
			Mode string `json:"mode"`
		} `json:"install"`
	}
	if err := json.Unmarshal(canonical, &document); err != nil {
		return false, err
	}
	return advancedManagedIntent(workspace, document.Install.Mode)
}
