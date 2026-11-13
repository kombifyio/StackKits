package ownapplication

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	contract "github.com/kombifyio/stackkits/contracts/ownapplication"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

// ImageUpdateVersion is the result of a tag update of one own application.
const ImageUpdateVersion = "stackkit.own-application-update/v1"

// TargetNotChecked is the only check state of a tag update target: kombify
// tests catalog releases, never the image of an own application.
const TargetNotChecked = "not_checked_by_kombify"

// ImageUpdateRequest updates a one-container own application to a newer tag
// of its own image repository. The dry run resolves the tag to its registry
// digest and returns the review digest; the execution names both again.
// HelperImage (the installed digest-pinned backup helper) and PlanHash (the
// applied plan) bind the data checkpoint of an application with a volume.
type ImageUpdateRequest struct {
	Name          string
	Tag           string
	AllowMajor    bool
	DryRun        bool
	ResolvedImage string
	OperationID   string
	ReviewDigest  string
	OwnerApproved bool
	HelperImage   string
	PlanHash      string
}

type ImageUpdateImage struct {
	Image    string `json:"image"`
	Version  string `json:"version,omitempty"`
	Resolved string `json:"resolvedImage,omitempty"`
}

// ImageUpdateCheckpoint reports the application-scoped data checkpoint.
type ImageUpdateCheckpoint struct {
	OperationID string   `json:"operationId"`
	Volumes     []string `json:"volumes"`
	// State is planned (dry run) or the recorded checkpoint state: taken,
	// restoring, retained (update verified; the copy stays until discarded or replaced by
	// the next checkpoint), restored, kept (a rollback did not restore it; the
	// copy is the only intact data and blocks new updates) or discarded.
	State string `json:"state"`
	// RetainedBytes is the size of the retained copy on the node.
	RetainedBytes  int64    `json:"retainedBytes,omitempty"`
	RestoreCommand []string `json:"restoreCommand,omitempty"`
	DiscardCommand []string `json:"discardCommand,omitempty"`
}

// ImageUpdateResult carries the outcome code (reviewed, updated, refused,
// rolled_back, rollback_failed) and the reason code of every other outcome.
// rolled_back means both the prior image and the checkpoint data are back.
type ImageUpdateResult struct {
	APIVersion   string                 `json:"apiVersion"`
	Name         string                 `json:"name"`
	Outcome      string                 `json:"outcome"`
	Reason       string                 `json:"reasonCode,omitempty"`
	Message      string                 `json:"message,omitempty"`
	TargetCheck  string                 `json:"targetCheck"`
	AllowMajor   bool                   `json:"allowMajor"`
	Prior        ImageUpdateImage       `json:"prior"`
	Target       ImageUpdateImage       `json:"target"`
	ReviewDigest string                 `json:"reviewDigest,omitempty"`
	OperationID  string                 `json:"operationId,omitempty"`
	Checkpoint   *ImageUpdateCheckpoint `json:"checkpoint,omitempty"`
	// ReplacesCheckpoint is the retained copy a tag update's dry run will
	// replace once its own checkpoint reads back: its state and size.
	ReplacesCheckpoint *ImageUpdateCheckpoint `json:"replacesCheckpoint,omitempty"`
	Update             *Result                `json:"update,omitempty"`
	Rollback           *Result                `json:"rollback,omitempty"`
}

// Outcome and reason codes of a tag update and of a checkpoint rollback.
const (
	UpdateReviewed       = "reviewed"
	UpdateUpdated        = "updated"
	UpdateRefused        = "refused"
	UpdateRolledBack     = "rolled_back"
	UpdateRollbackFailed = "rollback_failed"

	ReasonNotFound           = "application_not_found"
	ReasonProject            = "project_unsupported"
	ReasonPending            = "operation_pending"
	ReasonTagInvalid         = "tag_not_versioned"
	ReasonVariantChange      = "variant_change"
	ReasonVersionUnknown     = "current_version_unknown"
	ReasonNotNewer           = "not_newer"
	ReasonMajorChange        = "major_change"
	ReasonResolveFailed      = "resolve_failed"
	ReasonTagMoved           = "tag_moved"
	ReasonCheckpoint         = "checkpoint_failed"
	ReasonUpdateRefused      = "update_refused"
	ReasonUpdateFailed       = "update_failed"
	ReasonVerifyFailed       = "verify_failed"
	ReasonApprovalMissing    = "approval_missing"
	ReasonStopFailed         = "stop_failed"
	ReasonDataRestoreFailed  = "data_restore_failed"
	ReasonImageRollback      = "image_rollback_failed"
	ReasonRollbackVerify     = "rollback_verify_failed"
	ReasonCheckpointMissing  = "checkpoint_unavailable"
	ReasonCheckpointBlocks   = "checkpoint_unrestored"
	ReasonCheckpointSpace    = "checkpoint_space_insufficient"
	ReasonCheckpointInFlight = "checkpoint_in_flight"
	UpdateDiscarded          = "discarded"
)

// UpdateStabilityWindow is how long an updated application must keep running
// before the update counts as verified; UpdateHealthWindow bounds the wait for
// an image healthcheck to report healthy; RollbackBudget bounds a rollback,
// which runs on its own context so a cancelled update still rolls back.
var (
	UpdateStabilityWindow = 30 * time.Second
	UpdateHealthWindow    = 3 * time.Minute
	UpdateHealthPoll      = 5 * time.Second
	RollbackBudget        = 30 * time.Minute
)

var (
	resolveUpdateImage = nativehost.ResolveOwnApplicationImage
	imageVersionLabel  = func(ctx context.Context, workspace string, b contract.Bundle) (string, error) {
		runtime, err := nativehost.NewOwnApplicationRuntime(workspace)
		if err != nil {
			return "", err
		}
		return runtime.ImageVersionLabel(ctx, b)
	}
	executeOwnApplication = Execute
	loadOwnApplications   = Load
	versionTag            = regexp.MustCompile(`^v?([0-9]{1,9})(?:\.([0-9]{1,9}))?(?:\.([0-9]{1,9}))?(?:\.([0-9]{1,9}))?([-_+][A-Za-z0-9_.+-]*)?$`)
)

type imageVersion struct {
	parts   [4]int
	variant string
}

func parseImageVersion(tag string) (imageVersion, bool) {
	m := versionTag.FindStringSubmatch(tag)
	if m == nil {
		return imageVersion{}, false
	}
	var v imageVersion
	for i := 0; i < 4; i++ {
		if m[i+1] != "" {
			v.parts[i], _ = strconv.Atoi(m[i+1])
		}
	}
	v.variant = m[5]
	return v, true
}

func (v imageVersion) compare(o imageVersion) int {
	for i := range v.parts {
		if v.parts[i] != o.parts[i] {
			if v.parts[i] < o.parts[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// imageRepositoryTag splits an own-application image into its repository and
// tag; a digest-pinned image has no tag.
func imageRepositoryTag(image string) (string, string) {
	if repo, _, ok := strings.Cut(image, "@"); ok {
		return repo, ""
	}
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		return image[:i], image[i+1:]
	}
	return image, ""
}

// RestoreCommand is the exact reviewed command sequence that restores the
// checkpoint of operationID and returns the application to its prior image.
// DiscardCommand is the exact reviewed command sequence that removes the
// retained copy of the checkpoint of operationID.
func DiscardCommand(name, operationID string) []string {
	return []string{
		"stackkit app own --name " + name + " --action discard-checkpoint --checkpoint " + operationID + " --dry-run --json",
		"stackkit app own --name " + name + " --action discard-checkpoint --checkpoint " + operationID + " --review-digest <reviewDigest> --operation-id " + operationID + "-discard --owner-approve --json",
	}
}

// checkpointReport projects a recorded checkpoint for the owner: its state,
// volumes, the size of the retained copy and the commands that act on it.
func checkpointReport(name string, c DataCheckpoint) *ImageUpdateCheckpoint {
	report := &ImageUpdateCheckpoint{OperationID: c.OperationID, State: c.State, Volumes: []string{}}
	for _, v := range c.Volumes {
		report.Volumes = append(report.Volumes, v.LiveName)
		report.RetainedBytes += v.Bytes
	}
	if c.State == CheckpointDiscarded {
		report.RetainedBytes = 0
		return report
	}
	report.RestoreCommand = RestoreCommand(name, c.OperationID)
	report.DiscardCommand = DiscardCommand(name, c.OperationID)
	return report
}

func RestoreCommand(name, operationID string) []string {
	return []string{
		"stackkit app own --name " + name + " --action rollback --checkpoint " + operationID + " --dry-run --json",
		"stackkit app own --name " + name + " --action rollback --checkpoint " + operationID + " --review-digest <reviewDigest> --operation-id " + operationID + "-restore --owner-approve --json",
	}
}

// UpdateImage reviews or runs a tag update: the same repository, a newer
// version with the same major (unless AllowMajor) and the same variant suffix.
// The execution re-resolves the tag and refuses a moved tag before anything
// stops, stops the application and takes the application-scoped data
// checkpoint, runs the reviewed own-application update and verifies it. Any
// failure stops the application, restores the checkpoint data, rolls back to
// the prior image and verifies again.
func UpdateImage(ctx context.Context, workspace string, installation contract.Installation, req ImageUpdateRequest) (ImageUpdateResult, error) {
	out := ImageUpdateResult{APIVersion: ImageUpdateVersion, Name: req.Name, TargetCheck: TargetNotChecked, OperationID: req.OperationID, AllowMajor: req.AllowMajor}
	refuse := func(reason, message string) (ImageUpdateResult, error) {
		out.Outcome, out.Reason, out.Message = UpdateRefused, reason, message
		return out, errors.New(message)
	}
	s, err := loadOwnApplications(workspace)
	if err != nil {
		return refuse(ReasonPending, err.Error())
	}
	current, exists := s.Applications[req.Name]
	if !exists || current.State == "removed" {
		return refuse(ReasonNotFound, "own application is absent")
	}
	if current.Bundle.Application.IsProject() {
		return refuse(ReasonProject, "a tag update covers one-container applications; update a project through its reviewed document")
	}
	if current.Operation.ID != "" && current.Operation.ID == req.OperationID && !req.DryRun {
		if current.Operation.Status == "succeeded" && current.Operation.Action == "update" {
			out.Outcome = UpdateUpdated
			out.Message = "this operation already completed"
			return out, nil
		}
		return refuse(ReasonPending, "this operation ID already started; inspect status before a new reviewed operation")
	}
	if current.Operation.Status != "succeeded" || current.TransitionFrom != nil {
		return refuse(ReasonPending, "an own application operation is pending; inspect status and reconcile it first")
	}
	if checkpointBlocks(current.Checkpoint) {
		// A checkpoint whose rollback did not restore it (or whose update never
		// verified) is the only intact data: no new update may replace it.
		out.Checkpoint = checkpointReport(req.Name, *current.Checkpoint)
		return refuse(ReasonCheckpointBlocks, "the data checkpoint of "+current.Checkpoint.OperationID+" is "+current.Checkpoint.State+" and still holds the only intact data; restore it (--action rollback --checkpoint) or discard it (--action discard-checkpoint) first")
	}
	repo, currentTag := imageRepositoryTag(current.Bundle.Application.Image)
	out.Prior = ImageUpdateImage{Image: current.Bundle.Application.Image, Resolved: current.Bundle.ResolvedImage}
	target, ok := parseImageVersion(req.Tag)
	if !ok {
		return refuse(ReasonTagInvalid, "the target must be a version tag such as 1.27.3 or v2.4.0-alpine")
	}
	out.Target = ImageUpdateImage{Image: repo + ":" + req.Tag, Version: req.Tag}
	if _, _, err = contract.SplitImage(out.Target.Image); err != nil {
		return refuse(ReasonTagInvalid, err.Error())
	}
	priorVersion := currentTag
	if priorVersion == "" {
		if priorVersion, err = imageVersionLabel(ctx, workspace, current.Bundle); err != nil {
			priorVersion = ""
		}
	}
	out.Prior.Version = priorVersion
	prior, known := parseImageVersion(priorVersion)
	// --allow-major admits another major or an unversioned running image; it
	// never admits a downgrade, the same version or another variant.
	switch {
	case !known && !req.AllowMajor:
		return refuse(ReasonVersionUnknown, "the running image names no version (no tag and no org.opencontainers.image.version label); confirm the target with --allow-major")
	case known && prior.variant != target.variant:
		return refuse(ReasonVariantChange, "the target tag changes the image variant ("+prior.variant+" to "+target.variant+")")
	case known && target.compare(prior) <= 0:
		return refuse(ReasonNotNewer, "the target version is not newer than the running "+priorVersion)
	case known && target.parts[0] != prior.parts[0] && !req.AllowMajor:
		return refuse(ReasonMajorChange, "the target changes the major version; confirm it with --allow-major")
	}
	next := current.Bundle.Application
	next.Image = out.Target.Image
	hasVolume := current.Bundle.Application.HasVolume()
	update := Request{Action: "update", Name: req.Name, Application: &next, ImageTag: req.Tag, AllowMajor: req.AllowMajor}

	if req.DryRun {
		if req.ResolvedImage != "" || req.ReviewDigest != "" || req.OperationID != "" {
			return refuse(ReasonTagInvalid, "a dry run resolves its own digest; it takes no resolved image, review digest or operation ID")
		}
		review := update
		review.DryRun = true
		reviewed, e := executeOwnApplication(ctx, workspace, installation, review)
		if e != nil {
			return refuse(ReasonResolveFailed, e.Error())
		}
		out.Outcome = UpdateReviewed
		out.ReviewDigest = reviewed.ReviewDigest
		out.Target.Resolved = reviewed.Record.Bundle.ResolvedImage
		if hasVolume {
			out.Checkpoint = &ImageUpdateCheckpoint{State: "planned", Volumes: applicationVolumes(current.Bundle)}
			if current.Checkpoint != nil && current.Checkpoint.State != CheckpointDiscarded {
				// The earlier retained copy is replaced once the new one reads back.
				out.ReplacesCheckpoint = checkpointReport(req.Name, *current.Checkpoint)
				out.Message = "the " + current.Checkpoint.State + " checkpoint of " + current.Checkpoint.OperationID + " (" + strconv.FormatInt(out.ReplacesCheckpoint.RetainedBytes, 10) + " bytes) is replaced by this update's checkpoint"
			}
		}
		return out, nil
	}
	if !req.OwnerApproved || req.OperationID == "" || req.ReviewDigest == "" || req.ResolvedImage == "" {
		return refuse(ReasonApprovalMissing, "a tag update runs with --owner-approve, --operation-id and the --resolved-image and --review-digest of its dry run")
	}
	out.ReviewDigest = req.ReviewDigest
	out.Target.Resolved = req.ResolvedImage
	// A tag may move between review and execution: resolve it again and refuse
	// before anything stops.
	pinned, err := resolveUpdateImage(ctx, out.Target.Image)
	if err != nil {
		return refuse(ReasonResolveFailed, err.Error())
	}
	if pinned != req.ResolvedImage {
		return refuse(ReasonTagMoved, "the tag now resolves to a different digest than the review; review the update again")
	}

	var checkpoint *DataCheckpoint
	if hasVolume {
		// The checkpoint copies the data of the stopped application. It fails
		// closed: without the installed helper nothing stops.
		if !helperImagePattern.MatchString(req.HelperImage) || req.PlanHash == "" {
			return refuse(ReasonCheckpoint, "the data checkpoint needs the installed digest-pinned backup helper image and the applied plan; nothing changed")
		}
		if _, e := preflightDataCheckpoint(ctx, current.Bundle, req.HelperImage); e != nil {
			reason := ReasonCheckpoint
			if errors.Is(e, errCheckpointSpace) {
				reason = ReasonCheckpointSpace
			}
			return refuse(reason, e.Error()+"; nothing changed")
		}
		wasRunning := current.State == "running"
		if wasRunning {
			if _, e := runReviewed(ctx, workspace, installation, Request{Action: "stop", Name: req.Name}, req.OperationID+".checkpoint-stop"); e != nil {
				return refuse(ReasonCheckpoint, "stopping the application for its data checkpoint failed: "+e.Error())
			}
		}
		c, e := takeDataCheckpoint(ctx, workspace, current.Bundle, req.OperationID, req.HelperImage, req.PlanHash)
		if e == nil {
			if e = recordDataCheckpoint(ctx, workspace, req.Name, c); e != nil {
				// An unrecorded copy is never kept; the recorded one is untouched.
				_ = dropDataCheckpoint(context.WithoutCancel(ctx), workspace, current.Bundle, c)
			}
		}
		if e != nil {
			message := "the data checkpoint failed; the image did not change: " + e.Error()
			if wasRunning {
				if _, startErr := runReviewed(context.WithoutCancel(ctx), workspace, installation, Request{Action: "start", Name: req.Name}, req.OperationID+".checkpoint-start"); startErr != nil {
					message += "; starting the application again failed: " + startErr.Error()
				}
			}
			return refuse(ReasonCheckpoint, message)
		}
		checkpoint = &c
		out.Checkpoint = checkpointReport(req.Name, c)
	}
	// settle records the checkpoint state the update reached.
	settle := func(state string) error {
		if checkpoint == nil {
			return nil
		}
		checkpoint.State = state
		out.Checkpoint = checkpointReport(req.Name, *checkpoint)
		return setDataCheckpointState(workspace, req.Name, checkpoint.OperationID, state)
	}

	run := update
	run.ResolvedImage, run.OperationID, run.ReviewDigest, run.OwnerApproved = req.ResolvedImage, req.OperationID, req.ReviewDigest, true
	updated, err := executeOwnApplication(ctx, workspace, installation, run)
	out.Update = &updated
	reason := ReasonUpdateFailed
	if err != nil && !errors.Is(err, ErrPending) {
		// Nothing of the update ran; a stopped application starts again with
		// its unchanged image and data.
		if checkpoint != nil && current.State == "running" {
			if _, startErr := runReviewed(context.WithoutCancel(ctx), workspace, installation, Request{Action: "start", Name: req.Name}, req.OperationID+".refused-start"); startErr != nil {
				err = errors.Join(err, startErr)
			}
		}
		// The live data is unchanged: the copy is retained, not the only data.
		if settleErr := settle(CheckpointRetained); settleErr != nil {
			err = errors.Join(err, settleErr)
		}
		return refuse(ReasonUpdateRefused, err.Error())
	}
	if err == nil {
		if err = verifyRunning(ctx, workspace, installation, req.Name); err == nil {
			if err = settle(CheckpointRetained); err != nil {
				out.Outcome, out.Reason, out.Message = UpdateUpdated, ReasonPending, "updated and verified; recording the retained checkpoint failed: "+err.Error()
				return out, err
			}
			out.Outcome = UpdateUpdated
			return out, nil
		}
		reason = ReasonVerifyFailed
	}
	failure := err
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), RollbackBudget)
	defer cancel()
	// Until the rollback has restored and read back the data, the copy is the
	// only intact data.
	if settleErr := settle(CheckpointKept); settleErr != nil {
		failure = errors.Join(failure, settleErr)
	}
	rolled, rollbackReason, rollbackErr := rollbackWithData(rctx, workspace, installation, req.Name, req.OperationID, checkpoint)
	if checkpoint != nil {
		out.Checkpoint = checkpointReport(req.Name, *checkpoint)
	}
	out.Rollback = &rolled
	out.Reason = reason
	if rollbackErr != nil {
		out.Outcome = UpdateRollbackFailed
		out.Reason = rollbackReason
		out.Message = failure.Error() + "; rollback: " + rollbackErr.Error()
		return out, errors.Join(failure, rollbackErr)
	}
	out.Outcome = UpdateRolledBack
	out.Message = failure.Error()
	return out, failure
}

// rollbackWithData stops the application, restores the checkpoint data (when
// there is one), returns to the prior image and verifies it. It reports the
// reason code of the first step that failed.
func rollbackWithData(ctx context.Context, workspace string, installation contract.Installation, name, operationID string, checkpoint *DataCheckpoint) (Result, string, error) {
	if checkpoint != nil {
		// Always stop: after a failed update the signed record may still say
		// stopped (the checkpoint stopped it) while a new container runs.
		if _, err := runReviewed(ctx, workspace, installation, Request{Action: "stop", Name: name}, operationID+".rollback-stop"); err != nil {
			return Result{}, ReasonStopFailed, err
		}
		s, err := loadOwnApplications(workspace)
		if err != nil {
			return Result{}, ReasonDataRestoreFailed, err
		}
		if checkpoint.State, err = restoreTracked(ctx, workspace, name, s.Applications[name].Bundle, *checkpoint); err != nil {
			return Result{}, ReasonDataRestoreFailed, err
		}
	}
	rolled, err := runReviewed(ctx, workspace, installation, Request{Action: "rollback", Name: name}, operationID+".rollback")
	if err != nil {
		return rolled, ReasonImageRollback, err
	}
	if err = verifyRunning(ctx, workspace, installation, name); err != nil {
		return rolled, ReasonRollbackVerify, err
	}
	return rolled, "", nil
}

// restoreTracked records restoring (under the lifecycle lock, before any live
// data is written), restores the checkpoint and records restored, or kept when
// the restore failed. It returns the state the checkpoint reached.
func restoreTracked(ctx context.Context, workspace, name string, b contract.Bundle, c DataCheckpoint) (string, error) {
	if err := setDataCheckpointState(workspace, name, c.OperationID, CheckpointRestoring); err != nil {
		return c.State, err
	}
	if err := restoreDataCheckpoint(ctx, workspace, b, c); err != nil {
		if keptErr := setDataCheckpointState(workspace, name, c.OperationID, CheckpointKept); keptErr != nil {
			return CheckpointRestoring, errors.Join(err, keptErr)
		}
		return CheckpointKept, err
	}
	if err := setDataCheckpointState(workspace, name, c.OperationID, CheckpointRestored); err != nil {
		return CheckpointRestoring, err
	}
	return CheckpointRestored, nil
}

// RollbackWithCheckpoint is the reviewed manual form of the automatic
// rollback: it restores the data checkpoint that operation took and returns
// the application to the prior image recorded with it. The dry run returns
// the review digest; the execution names it.
func RollbackWithCheckpoint(ctx context.Context, workspace string, installation contract.Installation, req Request) (ImageUpdateResult, error) {
	out := ImageUpdateResult{APIVersion: ImageUpdateVersion, Name: req.Name, TargetCheck: TargetNotChecked, OperationID: req.OperationID}
	refuse := func(reason, message string) (ImageUpdateResult, error) {
		out.Outcome, out.Reason, out.Message = UpdateRefused, reason, message
		return out, errors.New(message)
	}
	s, err := loadOwnApplications(workspace)
	if err != nil {
		return refuse(ReasonPending, err.Error())
	}
	current, exists := s.Applications[req.Name]
	if !exists || current.State == "removed" {
		return refuse(ReasonNotFound, "own application is absent")
	}
	c := current.Checkpoint
	if c == nil || c.OperationID != req.Checkpoint || c.State == CheckpointDiscarded {
		return refuse(ReasonCheckpointMissing, "this application holds no data checkpoint of operation "+req.Checkpoint)
	}
	if current.Bundle.ResolvedImage != c.ResolvedImage && current.PreviousResolvedImage != c.ResolvedImage {
		return refuse(ReasonCheckpointMissing, "the checkpoint belongs to an image this application no longer records")
	}
	out.Target = ImageUpdateImage{Image: c.Image, Resolved: c.ResolvedImage}
	out.Prior = ImageUpdateImage{Image: current.Bundle.Application.Image, Resolved: current.Bundle.ResolvedImage}
	out.Checkpoint = checkpointReport(req.Name, *c)
	imageBack := current.Bundle.ResolvedImage == c.ResolvedImage
	review := Request{Action: "rollback", Name: req.Name, Checkpoint: req.Checkpoint, DryRun: true}
	if imageBack {
		// The image is back already (an earlier rollback restored it): the
		// review covers restoring the data and starting that image again.
		review.Action = "start"
		if current.State == "running" {
			review.Action = "restart"
		}
		review.Checkpoint = ""
	}
	if req.DryRun {
		reviewed, e := executeOwnApplication(ctx, workspace, installation, review)
		if e != nil {
			return refuse(ReasonUpdateRefused, e.Error())
		}
		out.Outcome, out.ReviewDigest = UpdateReviewed, reviewDigestWithCheckpoint(reviewed.ReviewDigest, *c)
		return out, nil
	}
	if !req.OwnerApproved || req.OperationID == "" || req.ReviewDigest == "" {
		return refuse(ReasonApprovalMissing, "a checkpoint rollback runs with --owner-approve, --operation-id and the --review-digest of its dry run")
	}
	reviewed, err := executeOwnApplication(ctx, workspace, installation, review)
	if err != nil || reviewDigestWithCheckpoint(reviewed.ReviewDigest, *c) != req.ReviewDigest {
		return refuse(ReasonUpdateRefused, "review digest is stale or differs from the checkpoint rollback")
	}
	out.ReviewDigest = req.ReviewDigest
	// From the first step on, the copy is the only intact data until it is
	// restored and read back.
	if err = setDataCheckpointState(workspace, req.Name, c.OperationID, CheckpointKept); err != nil {
		return refuse(ReasonPending, err.Error())
	}
	out.Checkpoint.State = CheckpointKept
	if _, err = runReviewed(ctx, workspace, installation, Request{Action: "stop", Name: req.Name}, req.OperationID+".stop"); err != nil {
		out.Outcome, out.Reason, out.Message = UpdateRollbackFailed, ReasonStopFailed, err.Error()
		return out, err
	}
	if out.Checkpoint.State, err = restoreTracked(ctx, workspace, req.Name, current.Bundle, *c); err != nil {
		out.Outcome, out.Reason, out.Message = UpdateRollbackFailed, ReasonDataRestoreFailed, err.Error()
		return out, err
	}
	out.Checkpoint.State = CheckpointRestored
	action := "rollback"
	if imageBack {
		action = "start"
	}
	rolled, err := runReviewed(ctx, workspace, installation, Request{Action: action, Name: req.Name}, req.OperationID+"."+action)
	out.Rollback = &rolled
	if err == nil {
		err = verifyRunning(ctx, workspace, installation, req.Name)
	}
	if err != nil {
		out.Outcome, out.Reason, out.Message = UpdateRollbackFailed, ReasonImageRollback, err.Error()
		return out, err
	}
	out.Outcome = UpdateRolledBack
	return out, nil
}

// DiscardCheckpoint is the owner's explicit decision to remove the retained
// copy of a checkpoint, also one a failed rollback kept: the live data then
// stays as it is. The dry run returns the review digest; the execution names
// it.
func DiscardCheckpoint(ctx context.Context, workspace string, req Request) (ImageUpdateResult, error) {
	out := ImageUpdateResult{APIVersion: ImageUpdateVersion, Name: req.Name, TargetCheck: TargetNotChecked, OperationID: req.OperationID}
	refuse := func(reason, message string) (ImageUpdateResult, error) {
		out.Outcome, out.Reason, out.Message = UpdateRefused, reason, message
		return out, errors.New(message)
	}
	s, err := loadOwnApplications(workspace)
	if err != nil {
		return refuse(ReasonPending, err.Error())
	}
	current, exists := s.Applications[req.Name]
	if !exists {
		return refuse(ReasonNotFound, "own application is absent")
	}
	c := current.Checkpoint
	if c == nil || c.OperationID != req.Checkpoint || c.State == CheckpointDiscarded {
		return refuse(ReasonCheckpointMissing, "this application holds no data checkpoint of operation "+req.Checkpoint)
	}
	out.Checkpoint = checkpointReport(req.Name, *c)
	if c.State == CheckpointTaken || c.State == CheckpointRestoring {
		return refuse(ReasonCheckpointInFlight, "the update or rollback of this checkpoint has not finished; its copy is not discarded")
	}
	digest := reviewDigestWithCheckpoint("discard-checkpoint", *c)
	if req.DryRun {
		out.Outcome, out.ReviewDigest = UpdateReviewed, digest
		return out, nil
	}
	if !req.OwnerApproved || req.OperationID == "" || req.ReviewDigest == "" {
		return refuse(ReasonApprovalMissing, "discarding a checkpoint runs with --owner-approve, --operation-id and the exact --review-digest of its dry run")
	}
	// The state, the review digest, the removal and the signed record are all
	// checked and written under one lifecycle lock.
	discarded, err := discardDataCheckpoint(ctx, workspace, req.Name, c.OperationID, req.ReviewDigest)
	if err != nil {
		return refuse(ReasonCheckpoint, "discarding the checkpoint did not complete: "+err.Error())
	}
	out.Outcome, out.ReviewDigest, out.Checkpoint = UpdateDiscarded, req.ReviewDigest, checkpointReport(req.Name, discarded)
	return out, nil
}

func reviewDigestWithCheckpoint(review string, c DataCheckpoint) string {
	return hash(struct {
		Review     string
		Checkpoint DataCheckpoint
	}{review, c})
}

// runReviewed reviews one own-application action and runs it under the
// returned review digest, as one step of an already admitted operation.
func runReviewed(ctx context.Context, workspace string, installation contract.Installation, req Request, operationID string) (Result, error) {
	review := req
	review.DryRun = true
	reviewed, err := executeOwnApplication(ctx, workspace, installation, review)
	if err != nil {
		return reviewed, err
	}
	req.OperationID, req.ReviewDigest, req.OwnerApproved = operationID, reviewed.ReviewDigest, true
	return executeOwnApplication(ctx, workspace, installation, req)
}

// verifyRunning requires every container running and, where the image has a
// healthcheck, healthy: a starting container is waited for within
// UpdateHealthWindow and never counts as verified. The check holds again after
// the stability window.
func verifyRunning(ctx context.Context, workspace string, installation contract.Installation, name string) error {
	check := func() (bool, error) {
		status, err := executeOwnApplication(ctx, workspace, installation, Request{Action: "status", Name: name})
		if err != nil {
			return false, err
		}
		if status.ObservationError != "" {
			return false, errors.New(status.ObservationError)
		}
		var containers map[string]struct {
			State  string `json:"State"`
			Health string `json:"Health"`
		}
		if json.Unmarshal(status.Observation, &containers) != nil || len(containers) == 0 {
			return false, errors.New("own application status has no container observation")
		}
		starting := false
		for service, c := range containers {
			if c.State != "running" || c.Health == "unhealthy" {
				return false, errors.New("own application container " + service + " is " + c.State + " " + c.Health)
			}
			if c.Health != "" && c.Health != "healthy" {
				starting = true
			}
		}
		return starting, nil
	}
	healthy := func() error {
		deadline := time.Now().Add(UpdateHealthWindow)
		for {
			starting, err := check()
			if err != nil {
				return err
			}
			if !starting {
				return nil
			}
			if !time.Now().Before(deadline) {
				return errors.New("own application healthcheck did not report healthy within " + UpdateHealthWindow.String())
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(UpdateHealthPoll):
			}
		}
	}
	if err := healthy(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(UpdateStabilityWindow):
	}
	return healthy()
}
