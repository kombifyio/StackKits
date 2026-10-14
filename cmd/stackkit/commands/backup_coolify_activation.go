package commands

import (
	"context"
	"errors"

	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
)

func nativeCoolifyRestoreActivationRuntime(ctx context.Context, workspace string, base restoreactivation.Runtime, selected restoreactivation.Authority, workload string, held nativeV2BackupAuthority) (restoreactivation.Runtime, error) {
	current := func(ctx context.Context) (backupexec.ApplicationRuntimeBinding, error) {
		authority, err := inspectNativeV2BackupAuthorityForRequest(ctx, workspace, specFile)
		if err != nil {
			return backupexec.ApplicationRuntimeBinding{}, err
		}
		if !sameNativeV2BackupAuthority(held, authority) {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Coolify restore current Apply authority changed")
		}
		bindings, err := nativeBackupCoolifyBindings(ctx, authority)
		if err != nil {
			return backupexec.ApplicationRuntimeBinding{}, err
		}
		for _, binding := range bindings {
			if binding.Runtime.WorkloadRef == workload {
				return binding, nil
			}
		}
		return backupexec.ApplicationRuntimeBinding{}, errors.New("Coolify restore workload has no current signed source binding")
	}
	binding, err := current(ctx)
	if err != nil {
		return nil, err
	}
	return restoreactivation.WithApplicationBinding(base, selected, binding, current)
}
