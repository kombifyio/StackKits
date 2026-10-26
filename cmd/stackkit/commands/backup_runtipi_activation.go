package commands

import (
	"context"
	"errors"
	"reflect"

	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

func nativeRuntipiRestoreActivationRuntime(ctx context.Context, workspace string, base restoreactivation.Runtime, selected restoreactivation.Authority, binding backupexec.ApplicationRuntimeBinding, current func(context.Context) (backupexec.ApplicationRuntimeBinding, error), held nativeV2BackupAuthority) (restoreactivation.Runtime, error) {
	bound, err := restoreactivation.WithAppGroupVolumeBinding(base, selected, binding, current)
	if err != nil {
		return nil, err
	}
	deployments, err := nativeBackupApplicationDeployments(held)
	if err != nil {
		return nil, err
	}
	deployment, exists := deployments[binding.Runtime.ComposeProject]
	if !exists || deployment.RuntimeAdapter.ID != "runtipi" {
		return nil, errors.New("restore native group has no exact signed deployment")
	}
	policyDigest, err := localbackuppolicy.SourceDigest(held.Policy.SourceProjection())
	if err != nil {
		return nil, err
	}
	appliedRequestDigest, err := nativeBackupAppliedRequestDigest(held)
	if err != nil {
		return nil, err
	}
	capture := func(ctx context.Context) ([]backuplifecycle.AppGroupControl, error) {
		if _, err := current(ctx); err != nil {
			return nil, err
		}
		b, err := nativehost.DeriveRuntipiAppGroupBinding(ctx, workspace, deployment, held.Lineage, nativeRuntipiTerminalSourceDigest, policyDigest, selected.OperationID, appliedRequestDigest)
		if err != nil {
			return nil, err
		}
		g, err := nativehost.CaptureRuntipiAppGroup(ctx, workspace, deployment, b)
		if err != nil {
			return nil, err
		}
		return []backuplifecycle.AppGroupControl{g}, nil
	}
	driver := func(ctx context.Context, session *lifecyclemutation.Session, g backuplifecycle.AppGroupControl) (backuplifecycle.AppGroupControlDriver, error) {
		if _, err := current(ctx); err != nil {
			return nil, err
		}
		custody, err := nativehost.NewRuntipiRestoreGroupCustody(ctx, workspace, deployment, held.Lineage, session)
		if err != nil {
			return nil, err
		}
		return nativehost.NewRuntipiBackupControlDriver(workspace, g, custody)
	}
	return restoreactivation.WithAppGroupSessionControls(bound, selected, capture, driver)
}

// Read the original signed activation group rather than treating initially
// stopped applications as a failed readiness probe or starting them to verify.
func nativeRuntipiRestoreReadback(ctx context.Context, held nativeV2BackupAuthority, deployment nativehost.SelectedPaaSWorkloadDeployment, graph localbackuppolicy.ApplicationRuntime, operation string) error {
	record, err := lifecyclemutation.ReadRestoreAppGroupRecord(held.WorkspaceRoot, operation)
	if err != nil {
		return err
	}
	if record.RestoreActivation == nil || record.OperationID != operation {
		return errors.New("Runtipi restore has no signed activation group")
	}
	retained, err := nativehost.LoadRuntipiTerminalQualification(held.WorkspaceRoot, deployment, nativeRuntipiTerminalSourceDigest)
	if err != nil {
		return err
	}
	policyDigest, err := localbackuppolicy.SourceDigest(held.Policy.SourceProjection())
	if err != nil {
		return err
	}
	appliedRequestDigest, err := nativeBackupAppliedRequestDigest(held)
	if err != nil {
		return err
	}
	matches := 0
	for _, g := range record.RestoreActivation.AppGroups {
		if g.Binding.LogicalProject != graph.ComposeProject {
			continue
		}
		matches++
		b := g.Binding
		q := retained.Qualification
		if b.JournalOperationID != operation || b.OwnerRef != held.OwnerRef || b.SiteRef != graph.SiteRef || b.NodeRef != graph.NodeRef || b.PhysicalProject != graph.ComposeProject+"__user" || b.PolicySourceDigest != policyDigest || b.AppliedRequestDigest != appliedRequestDigest || !reflect.DeepEqual(b.AuthorityLineage, held.Lineage) || b.SourceDigest != q.SourceDigest || b.ManagerID != q.ManagerID || b.ManagerContainerID != q.ManagerContainerID || b.ManagerImage != q.ManagerImage || b.InstallationDigest != retained.InstallationDigest || b.QualificationDigest != backuplifecycle.AppGroupDigest(q) || !backuplifecycle.AppGroupRestored(g) {
			return errors.New("Runtipi restored group differs from signed current source/qualification/power")
		}
		actual, err := nativehost.ObserveRuntipiAppGroupGeneration(ctx, g, "verify")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(actual, g.Current()) {
			return errors.New("Runtipi restored generation differs from signed current adoption")
		}
	}
	if matches != 1 {
		return errors.New("Runtipi restore requires one exact current group")
	}
	return nil
}

// Unrelated workloads must retain authenticated current generation and power,
// without acquiring membership in another workload's activation journal.
func nativeRuntipiCurrentReadback(ctx context.Context, held nativeV2BackupAuthority, deployment nativehost.SelectedPaaSWorkloadDeployment) error {
	policyDigest, err := localbackuppolicy.SourceDigest(held.Policy.SourceProjection())
	if err != nil {
		return err
	}
	appliedRequestDigest, err := nativeBackupAppliedRequestDigest(held)
	if err != nil {
		return err
	}
	return nativehost.VerifyRuntipiCurrentBackupGraph(ctx, held.WorkspaceRoot, deployment, held.Lineage, nativeRuntipiTerminalSourceDigest, policyDigest, appliedRequestDigest)
}
