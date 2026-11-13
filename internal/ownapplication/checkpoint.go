package ownapplication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	contract "github.com/kombifyio/stackkits/contracts/ownapplication"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

// DataCheckpoint is the application-scoped data checkpoint of one tag update:
// a retained, operation-bound copy of every named volume of the application,
// taken while it was stopped and read back as a file-tree digest. It lives in
// the signed own-application record, so only the operation that took it can
// restore it. State records whether the copy is still the only intact data:
//
//   - taken: the update that took it has not finished verifying;
//   - kept: a rollback did not restore it; the live data may be half
//     restored and the copy is the only intact data;
//   - retained: the update verified (or never ran); live data is good;
//   - restoring: a rollback is copying it back into the live volumes;
//   - restored: the rollback copied it back and read the live data back;
//   - discarded: the owner removed the copy.
//
// While a checkpoint is taken, restoring or kept no new tag update runs, and a new
// checkpoint removes an earlier copy only after it read back and only when
// the earlier one is retained, restored or discarded.
type DataCheckpoint struct {
	State         string             `json:"state"`
	OperationID   string             `json:"operationId"`
	Image         string             `json:"image"`
	ResolvedImage string             `json:"resolvedImage"`
	HelperImage   string             `json:"helperImage"`
	PlanHash      string             `json:"planHash"`
	Volumes       []CheckpointVolume `json:"volumes"`
	At            time.Time          `json:"at"`
}

type CheckpointVolume struct {
	LiveName     string `json:"liveName"`
	RollbackName string `json:"rollbackName"`
	Files        int64  `json:"files"`
	Bytes        int64  `json:"bytes"`
	Digest       string `json:"digest"`
}

var helperImagePattern = regexp.MustCompile(`^[a-z0-9][^@\s]*@sha256:[0-9a-f]{64}$`)

// applicationVolumes names the live named volume of every container of the
// application, in declaration order.
func applicationVolumes(b contract.Bundle) []string {
	volumes := []string{}
	if b.Application.IsProject() {
		for _, s := range b.Application.Services {
			if s.Volume != nil {
				volumes = append(volumes, localbackuppolicy.StandaloneComposeVolumeName(b.Application.WorkloadRef(), b.Installation.NodeRef, s.Name, "data"))
			}
		}
	} else if b.Application.Volume != nil {
		volumes = append(volumes, localbackuppolicy.StandaloneComposeVolumeName(b.Application.WorkloadRef(), b.Installation.NodeRef, "app", "data"))
	}
	return volumes
}

func checkpointAuthority(b contract.Bundle, c DataCheckpoint) (restoreactivation.Authority, []restoreactivation.Volume) {
	project := "stackkit-" + b.Application.WorkloadRef() + "-" + b.Installation.NodeRef
	authority := restoreactivation.Authority{OperationID: restoreactivation.OwnCheckpointOperationPrefix + c.OperationID, PlanHash: c.PlanHash, KopiaHelperImage: c.HelperImage}
	volumes := make([]restoreactivation.Volume, 0, len(c.Volumes))
	for _, v := range c.Volumes {
		volume := restoreactivation.Volume{ComponentRef: b.Application.WorkloadRef(), LogicalName: "data", ComposeProject: project, LiveName: v.LiveName, RollbackName: v.RollbackName}
		authority.Volumes = append(authority.Volumes, v.LiveName)
		authority.VolumeDetails = append(authority.VolumeDetails, volume)
		volumes = append(volumes, volume)
	}
	return authority, volumes
}

func sameReadback(r backuplifecycle.VolumeReadback, v CheckpointVolume) bool {
	return r.Files == v.Files && r.Bytes == v.Bytes && r.Digest == v.Digest
}

// takeCheckpoint copies every volume of the stopped application into its
// operation-bound retained volume and reads both back. A retained copy left by
// an interrupted run of the same operation is removed first, so a retry never
// returns an older copy.
func takeCheckpoint(ctx context.Context, workspace string, b contract.Bundle, operationID, helperImage, planHash string) (DataCheckpoint, error) {
	if !helperImagePattern.MatchString(helperImage) || planHash == "" {
		return DataCheckpoint{}, errors.New("the data checkpoint needs the installed digest-pinned backup helper image and the applied plan")
	}
	c := DataCheckpoint{OperationID: operationID, Image: b.Application.Image, ResolvedImage: b.ResolvedImage, HelperImage: helperImage, PlanHash: planHash, At: time.Now().UTC()}
	for _, live := range applicationVolumes(b) {
		sum := sha256.Sum256([]byte(operationID))
		c.Volumes = append(c.Volumes, CheckpointVolume{LiveName: live, RollbackName: live + "-checkpoint-" + hex.EncodeToString(sum[:8])})
	}
	if len(c.Volumes) == 0 {
		return DataCheckpoint{}, errors.New("the application has no named volume to checkpoint")
	}
	authority, volumes := checkpointAuthority(b, c)
	defer func() {
		// A copy that did not read back is never kept: remove it, best effort.
		if c.Volumes[0].Digest == "" {
			_ = dropCheckpoint(context.WithoutCancel(ctx), workspace, b, c)
		}
	}()
	err := lifecyclemutation.WithIdleMutation(workspace, lifecyclemutation.JoinRequest{}, func() error {
		runtime, err := restoreactivation.NewDockerRuntime(workspace)
		if err != nil {
			return err
		}
		for i, volume := range volumes {
			if err = runtime.CleanupRollback(ctx, authority, volume); err != nil {
				return err
			}
			if err = runtime.PrepareRollback(ctx, authority, volume); err != nil {
				return err
			}
			live, err := runtime.DigestVolume(ctx, authority, volume, false)
			if err != nil {
				return err
			}
			retained, err := restoreactivation.DigestRetainedVolume(ctx, workspace, authority, volume)
			if err != nil {
				return err
			}
			if retained.Digest != live.Digest || retained.Files != live.Files || retained.Bytes != live.Bytes {
				return errors.New("the retained copy of " + volume.LiveName + " differs from its live data")
			}
			c.Volumes[i].Files, c.Volumes[i].Bytes, c.Volumes[i].Digest = retained.Files, retained.Bytes, retained.Digest
		}
		return nil
	})
	if err != nil {
		for i := range c.Volumes {
			c.Volumes[i].Digest = ""
		}
		return c, err
	}
	c.State = CheckpointTaken
	return c, nil
}

// restoreCheckpoint copies the retained data back into the stopped
// application's live volumes and proves each equals its checkpoint readback.
func restoreCheckpoint(ctx context.Context, workspace string, b contract.Bundle, c DataCheckpoint) error {
	authority, volumes := checkpointAuthority(b, c)
	return lifecyclemutation.WithIdleMutation(workspace, lifecyclemutation.JoinRequest{}, func() error {
		runtime, err := restoreactivation.NewDockerRuntime(workspace)
		if err != nil {
			return err
		}
		for i, volume := range volumes {
			retained, err := restoreactivation.DigestRetainedVolume(ctx, workspace, authority, volume)
			if err != nil {
				return err
			}
			if !sameReadback(retained, c.Volumes[i]) {
				return errors.New("the retained copy of " + volume.LiveName + " differs from its checkpoint; live data was not changed")
			}
			if err = runtime.RestoreVolume(ctx, authority, volume); err != nil {
				return err
			}
			live, err := runtime.DigestVolume(ctx, authority, volume, false)
			if err != nil {
				return err
			}
			if !sameReadback(live, c.Volumes[i]) {
				return errors.New("restored " + volume.LiveName + " differs from its checkpoint; the application stays stopped")
			}
		}
		return nil
	})
}

// dropCheckpoint removes the retained copies of a checkpoint.
func dropCheckpoint(ctx context.Context, workspace string, b contract.Bundle, c DataCheckpoint) error {
	authority, volumes := checkpointAuthority(b, c)
	runtime, err := restoreactivation.NewDockerRuntime(workspace)
	if err != nil {
		return err
	}
	for _, volume := range volumes {
		if err = runtime.CleanupRollback(ctx, authority, volume); err != nil {
			return err
		}
	}
	return nil
}

// Checkpoint states (see DataCheckpoint).
const (
	CheckpointTaken     = "taken"
	CheckpointKept      = "kept"
	CheckpointRestoring = "restoring"
	CheckpointRetained  = "retained"
	CheckpointRestored  = "restored"
	CheckpointDiscarded = "discarded"
)

// checkpointBlocks reports whether a checkpoint still holds the only intact
// data (or its update has not verified), so no new tag update may run.
func checkpointBlocks(c *DataCheckpoint) bool {
	return c != nil && (c.State == CheckpointTaken || c.State == CheckpointKept || c.State == CheckpointRestoring)
}

// recordCheckpoint stores a read-back checkpoint in the signed record. An
// earlier copy is removed only after this one read back, and only when the
// earlier one is retained, restored or discarded; a kept or unfinished copy is
// never replaced.
func recordCheckpoint(ctx context.Context, workspace, name string, c DataCheckpoint) error {
	return lifecyclemutation.WithIdleMutation(workspace, lifecyclemutation.JoinRequest{}, func() error {
		s, err := Load(workspace)
		if err != nil {
			return err
		}
		r, ok := s.Applications[name]
		if !ok {
			return errors.New("own application is absent")
		}
		if prior := r.Checkpoint; prior != nil && prior.OperationID != c.OperationID {
			if checkpointBlocks(prior) {
				return errors.New("checkpoint of " + prior.OperationID + " is " + prior.State + " and still holds the only intact data")
			}
			if prior.State != CheckpointDiscarded {
				if err = dropCheckpoint(ctx, workspace, r.Bundle, *prior); err != nil {
					return err
				}
			}
		}
		r.Checkpoint = &c
		s.Applications[name] = r
		return save(workspace, s)
	})
}

// setCheckpointState moves the recorded checkpoint of operationID to state.
func setCheckpointState(workspace, name, operationID, state string) error {
	return lifecyclemutation.WithIdleMutation(workspace, lifecyclemutation.JoinRequest{}, func() error {
		s, err := Load(workspace)
		if err != nil {
			return err
		}
		r, ok := s.Applications[name]
		if !ok || r.Checkpoint == nil || r.Checkpoint.OperationID != operationID {
			return errors.New("own application holds no checkpoint of " + operationID)
		}
		if err = checkpointTransition(r.Checkpoint.State, state); err != nil {
			return err
		}
		r.Checkpoint.State = state
		s.Applications[name] = r
		return save(workspace, s)
	})
}

// checkpointTransitions is the only graph a recorded checkpoint moves
// through. Nothing leaves discarded: its copy is gone. Discard is never
// reachable from taken or restoring, whose update or rollback has not
// finished; restoring is recorded before any live data is written.
var checkpointTransitions = map[string][]string{
	CheckpointTaken:     {CheckpointRetained, CheckpointKept},
	CheckpointKept:      {CheckpointKept, CheckpointRestoring, CheckpointDiscarded},
	CheckpointRestoring: {CheckpointRestored, CheckpointKept},
	CheckpointRetained:  {CheckpointRetained, CheckpointKept, CheckpointDiscarded},
	CheckpointRestored:  {CheckpointRestored, CheckpointKept, CheckpointDiscarded},
}

func checkpointTransition(from, to string) error {
	for _, allowed := range checkpointTransitions[from] {
		if allowed == to {
			return nil
		}
	}
	return errors.New("a checkpoint does not move from " + from + " to " + to)
}

// discardCheckpoint removes the retained copy of the checkpoint of
// operationID and records it discarded, holding the lifecycle lock from the
// state check through the removal and the signed record, so no update or
// rollback can interleave. expected is the review digest of the dry run,
// recomputed here from the record under the lock.
func discardCheckpoint(ctx context.Context, workspace, name, operationID, expected string) (DataCheckpoint, error) {
	var discarded DataCheckpoint
	err := lifecyclemutation.WithIdleMutation(workspace, lifecyclemutation.JoinRequest{}, func() error {
		s, err := Load(workspace)
		if err != nil {
			return err
		}
		r, ok := s.Applications[name]
		if !ok || r.Checkpoint == nil || r.Checkpoint.OperationID != operationID {
			return errors.New("own application holds no checkpoint of " + operationID)
		}
		c := *r.Checkpoint
		if err = checkpointTransition(c.State, CheckpointDiscarded); err != nil {
			return err
		}
		if reviewDigestWithCheckpoint("discard-checkpoint", c) != expected {
			return errors.New("review digest is stale or differs from the checkpoint discard")
		}
		if err = dropCheckpoint(ctx, workspace, r.Bundle, c); err != nil {
			return err
		}
		c.State = CheckpointDiscarded
		r.Checkpoint = &c
		s.Applications[name] = r
		if err = save(workspace, s); err != nil {
			return err
		}
		discarded = c
		return nil
	})
	return discarded, err
}

var errCheckpointSpace = errors.New("not enough free space")

// checkpointFree is the free space a checkpoint copy of bytes needs.
func checkpointFree(bytes int64) int64 { return bytes + bytes/10 + 64<<20 }

// preflightCheckpoint measures every volume of the running application and
// the free space of the Docker volume filesystem before anything stops.
func preflightCheckpoint(ctx context.Context, b contract.Bundle, helperImage string) (int64, error) {
	var total, free int64
	for _, live := range applicationVolumes(b) {
		_, bytes, available, err := measureVolume(ctx, helperImage, "checkpoint-"+b.Application.Name, nativehost.OwnAdoptionMount{Type: "volume", Source: live})
		if err != nil {
			return 0, errors.New("the application data could not be measured: " + err.Error())
		}
		total, free = total+bytes, available
	}
	if free < checkpointFree(total) {
		return total, fmt.Errorf("%w: the data checkpoint needs %d bytes free on the Docker volume filesystem; %d are free", errCheckpointSpace, checkpointFree(total), free)
	}
	return total, nil
}

var (
	measureVolume = func(ctx context.Context, helperImage, label string, source nativehost.OwnAdoptionMount) (int64, int64, int64, error) {
		return nativehost.NewOwnAdoptionDocker().Measure(ctx, helperImage, label, source)
	}
	preflightDataCheckpoint = preflightCheckpoint
	setDataCheckpointState  = setCheckpointState
	discardDataCheckpoint   = discardCheckpoint
	dropDataCheckpoint      = dropCheckpoint
	takeDataCheckpoint      = takeCheckpoint
	restoreDataCheckpoint   = restoreCheckpoint
	recordDataCheckpoint    = recordCheckpoint
)
