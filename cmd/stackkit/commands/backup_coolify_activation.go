package commands

import (
	"context"
	"errors"

	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

func nativeApplicationRestoreActivationRuntime(ctx context.Context, workspace string, base restoreactivation.Runtime, selected restoreactivation.Authority, workload string, held nativeV2BackupAuthority) (restoreactivation.Runtime, error) {
	current := func(ctx context.Context) (backupexec.ApplicationRuntimeBinding, error) {
		authority, err := inspectNativeV2BackupAuthorityForRequest(ctx, workspace, specFile)
		if err != nil {
			return backupexec.ApplicationRuntimeBinding{}, err
		}
		if !sameNativeV2BackupAuthority(held, authority) {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Application restore current Apply authority changed")
		}
		bindings, err := nativeBackupApplicationBindings(ctx, authority)
		if err != nil {
			return backupexec.ApplicationRuntimeBinding{}, err
		}
		for _, binding := range bindings {
			if binding.Runtime.WorkloadRef == workload {
				return binding, nil
			}
		}
		return backupexec.ApplicationRuntimeBinding{}, errors.New("Application restore workload has no current signed source binding")
	}
	binding, err := current(ctx)
	if err != nil {
		return nil, err
	}
	bound, err := restoreactivation.WithApplicationBinding(base, selected, binding, current)
	if err != nil {
		return nil, err
	}
	deployments, err := nativeBackupApplicationDeployments(held)
	if err != nil {
		return nil, err
	}
	deployment := deployments[binding.Runtime.ComposeProject]
	observe := func(ctx context.Context, ref string) (backupexec.QuiesceContainer, error) {
		if _, err := current(ctx); err != nil {
			return backupexec.QuiesceContainer{}, err
		}
		var ids map[string]string
		var err error
		if binding.Runtime.RuntimeAdapterRef == "coolify" {
			ids, err = nativehost.ObserveCoolifyContainerCustody(ctx, workspace, deployment)
		} else {
			ids, err = nativehost.ObserveKomodoContainerCustody(ctx, workspace, deployment)
		}
		if err != nil {
			return backupexec.QuiesceContainer{}, err
		}
		if ids[ref] == "" {
			return backupexec.QuiesceContainer{}, errors.New("selected component has no retained container")
		}
		c, err := docker.NewLocalClient().InspectContainer(ctx, ids[ref])
		if err != nil {
			return backupexec.QuiesceContainer{}, err
		}
		return backupexec.ObserveQuiesceContainer(*c)
	}
	drivers := map[string]backuplifecycle.RuntimeControlDriver{}
	factory := func(op, ref, id string) (backuplifecycle.RuntimeControlDriver, error) {
		key := op + ":" + ref + ":" + id
		if d := drivers[key]; d != nil {
			return d, nil
		}
		var d backuplifecycle.RuntimeControlDriver
		if binding.Runtime.RuntimeAdapterRef == "coolify" {
			document, err := nativeBackupMarketplaceDocument(ctx, held)
			if err != nil {
				return nil, err
			}
			d, err = nativehost.NewCoolifyBackupControlDriver(workspace, document, deployment, ref, id, op)
			if err != nil {
				return nil, err
			}
		} else {
			d = backuplifecycle.NewSynchronousRuntimeControl(id, func(ctx context.Context, action string) error { return binding.Control(ctx, ref, action) })
		}
		drivers[key] = d
		return d, nil
	}
	if err := restoreactivation.BindApplicationControls(bound, observe, factory); err != nil {
		return nil, err
	}
	return bound, nil
}
