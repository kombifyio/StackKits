package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localbackupschedule"
)

// rebindBackupAfterConvergence moves an existing local backup configuration
// to the authority a converged Apply, change set or Advanced reconcile left
// behind. Every Apply re-signs Plan and Apply (an upgraded CLI does so even
// for an unchanged spec), and a change set that adds a workload also selects
// its data volume in the source policy, so the configuration made before it
// no longer binds and every later checkpoint, backup run and restore refuses
// it. Rebinding keeps the Owner, authority and repository (Basement and Cloud
// alike) and needs no owner secret; a host without a configuration keeps none
// until `stackkit backup configure` or the first checkpoint creates it. It
// reports whether the configuration moved.
func rebindBackupAfterConvergence(ctx context.Context, workspace string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	initial, err := inspectNativeV2BackupAuthority(ctx, workspace, specFile)
	if err != nil {
		return false, err
	}
	rebound := false
	err = withLifecycleMutation(workspace, "backup rebind", func() error {
		return withArchitectureV2OutputLock(workspace, initial.OutputRoot, func(_ *confinedfs.Transaction, _ *confinedfs.OutputLock) error {
			current, inspectErr := inspectNativeV2BackupAuthority(ctx, workspace, specFile)
			if inspectErr != nil {
				return inspectErr
			}
			if !sameNativeV2BackupAuthority(initial, current) {
				return errors.New("native v2 backup authority changed while acquiring the output lock")
			}
			service, serviceErr := newNativeV2BackupService(current)
			if serviceErr != nil {
				return serviceErr
			}
			operationContext, cancel := nativeV2BackupOperationContext(ctx, backupLongOperationTimeout)
			defer cancel()
			_, moved, rebindErr := service.Rebind(operationContext, backuplifecycle.ConfigureInput{
				OwnerRef: current.OwnerRef, AuthorityRef: current.AuthorityRef,
				Lineage: current.Lineage, PolicyArtifact: append([]byte(nil), current.PolicyArtifact...),
			})
			if errors.Is(rebindErr, os.ErrNotExist) {
				return nil
			}
			rebound = moved
			return rebindErr
		})
	})
	return rebound, err
}

// runAutomaticBackupRebind runs after a converged Apply, change-set apply or
// Advanced reconcile, beside the automatic owner setup. A failure never
// undoes the converged runtime: it is reported as a `backup-configure`
// rollout event with the exact remedy, and the next checkpoint rebinds in
// place again. Owner approval of a schedule is never carried forward: when
// an enabled schedule no longer binds the converged Apply, the owner is told
// that scheduled snapshots pause until `stackkit backup schedule enable`.
func runAutomaticBackupRebind(ctx context.Context, workspace string) {
	if strings.TrimSpace(lifecycleJoinOperation) != "" {
		return
	}
	rebound, err := rebindBackupAfterConvergence(ctx, workspace)
	if err != nil {
		printWarning("local backup configuration was not carried forward to the converged Apply: %v; run `stackkit backup configure`", err)
		rolloutFailure("backup-configure", fmt.Errorf("rebind local backup configuration: %w", err))
		return
	}
	if rebound {
		rolloutEvent("backup-configure", "succeeded", "local backup configuration carried forward to the converged Apply", nil)
	}
	warnStaleBackupSchedule(ctx, workspace)
}

// warnStaleBackupSchedule reports an enabled schedule whose approval no longer
// binds the converged Apply. It reads local records only and never fails the
// Apply; `stackkit status` repeats the same condition until it is resolved.
func warnStaleBackupSchedule(ctx context.Context, workspace string) {
	schedule, err := localbackupschedule.LoadAuthorization(workspace)
	if err != nil || schedule.State != "enabled" {
		return
	}
	current, err := inspectNativeV2BackupAuthority(ctx, workspace, specFile)
	if err != nil || scheduleBindingFollowsAuthority(ctx, current, schedule.Binding) {
		return
	}
	stale := &localbackupschedule.StaleAuthorizationError{
		LineageDiffers: !reflect.DeepEqual(schedule.Binding.Lineage, current.Lineage),
		PolicyDiffers:  schedule.Binding.PolicyDigest != current.PolicyDigest,
	}
	printWarning("%s", stale.Error())
	rolloutEvent("backup-schedule", "stale", stale.Error(), nil)
}
