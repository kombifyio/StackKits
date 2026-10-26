package commands

import (
	"context"
	"errors"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localbackupruntime"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

// This identifies the reviewed private terminal protocol. It admits no manager
// image or installation; native custody must independently qualify both.
const nativeRuntipiTerminalSourceDigest = "47fd4975b2fb589e2f401e9b890060bee99b28806373e8b78a481df6d3ea5f57"

// Recording an operation for an explicit group source does not change the
// catalog's support row. Native qualification remains the effect boundary.
func nativeRuntipiLifecycleSource(plan generationartifact.VerifiedPlan, contract applicationlifecycle.Contract, stage string) (bool, error) {
	if contract.Delivery.AdapterRef != "runtipi" || (stage != "backup" && stage != "restore") {
		return false, nil
	}
	return restoreactivation.RuntipiAppGroupSourceSelected(plan.Canonical(), contract.WorkloadRef)
}

func nativeBackupAppliedRequestDigest(authority nativeV2BackupAuthority) (string, error) {
	if authority.AppliedAuthority != nil && authority.AppliedAuthority.Lineage == authority.Lineage && nativeV2BackupDigestPattern.MatchString(authority.AppliedAuthority.AppliedRequestDigest) {
		return authority.AppliedAuthority.AppliedRequestDigest, nil
	}
	if h := authority.HistoricalCustody; h != nil && h.Binding() == authority.Lineage.Binding && h.ManifestHash() == authority.Lineage.ManifestHash && nativeV2BackupDigestPattern.MatchString(h.Request().RequestDigest) {
		return h.Request().RequestDigest, nil
	}
	return "", errors.New("Runtipi backup requires the verified Apply's exact shared request digest")
}

func nativeRuntipiSnapshotBinding(authority nativeV2BackupAuthority) (localbackupruntime.AppGroupSnapshotBinding, bool, error) {
	var binding localbackupruntime.AppGroupSnapshotBinding
	var graph *localbackuppolicy.ApplicationRuntime
	for _, candidate := range authority.Policy.SourceProjection().ApplicationRuntimes {
		if candidate.RuntimeAdapterRef != "runtipi" {
			continue
		}
		if graph != nil {
			return binding, false, errors.New("first Runtipi backup slice requires one complete daemon group")
		}
		copy := candidate
		graph = &copy
	}
	if graph == nil {
		return binding, false, nil
	}
	deployments, err := nativeBackupApplicationDeployments(authority)
	if err != nil {
		return binding, false, err
	}
	deployment := deployments[graph.ComposeProject]
	policyDigest, err := localbackuppolicy.SourceDigest(authority.Policy.SourceProjection())
	if err != nil {
		return binding, false, err
	}
	appliedRequestDigest, err := nativeBackupAppliedRequestDigest(authority)
	if err != nil {
		return binding, false, err
	}
	binding.Capture = func(ctx context.Context, operation string) (backuplifecycle.AppGroupControl, error) {
		b, err := nativehost.DeriveRuntipiAppGroupBinding(ctx, authority.WorkspaceRoot, deployment, authority.Lineage, nativeRuntipiTerminalSourceDigest, policyDigest, operation, appliedRequestDigest)
		if err != nil {
			return backuplifecycle.AppGroupControl{}, err
		}
		return nativehost.CaptureRuntipiAppGroup(ctx, authority.WorkspaceRoot, deployment, b)
	}
	binding.Driver = func(ctx context.Context, group backuplifecycle.AppGroupControl) (backuplifecycle.AppGroupControlDriver, error) {
		custody, err := nativehost.NewRuntipiSnapshotGroupCustody(ctx, authority.WorkspaceRoot, deployment, authority.Lineage)
		if err != nil {
			return nil, err
		}
		return nativehost.NewRuntipiBackupControlDriver(authority.WorkspaceRoot, group, custody)
	}
	return binding, true, nil
}
