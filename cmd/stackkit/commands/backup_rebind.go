package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// rebindBackupAfterConvergence moves an existing local backup configuration
// to the authority a converged change set or Advanced reconcile left behind.
// A change set that adds a workload selects its data volume in the source
// policy and re-signs Plan and Apply, so the configuration made before it no
// longer binds and every later checkpoint, backup run and restore refuses it.
// Rebinding keeps the Owner, authority and repository (Basement and Cloud
// alike); a host without a configuration keeps none until `stackkit backup
// configure` or the first checkpoint creates it. It reports whether the
// configuration moved.
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

// runAutomaticBackupRebind runs after a converged change-set apply or
// Advanced reconcile, beside the automatic owner setup. A failure never
// undoes the converged runtime: it is reported as a `backup-configure`
// rollout event, and the next checkpoint rebinds in place again.
func runAutomaticBackupRebind(ctx context.Context, workspace string) {
	if strings.TrimSpace(lifecycleJoinOperation) != "" {
		return
	}
	rebound, err := rebindBackupAfterConvergence(ctx, workspace)
	if err != nil {
		printWarning("local backup configuration was not rebound to the converged state: %v; run `stackkit backup configure`", err)
		rolloutFailure("backup-configure", fmt.Errorf("rebind local backup configuration: %w", err))
		return
	}
	if rebound {
		rolloutEvent("backup-configure", "succeeded", "local backup configuration rebound to the converged state", nil)
	}
}
