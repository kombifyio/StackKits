package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
)

// dockerRestoreDrillStaging verifies a drill's staged restore with the same
// read-only checks restore activation performs before its first live
// mutation, and removes only the drill's staged tree afterwards.
type dockerRestoreDrillStaging struct {
	workspace   string
	authority   nativeV2BackupAuthority
	plan        generationartifact.VerifiedPlan
	manifest    generationartifact.ArtifactManifest
	graph       restoreactivation.RuntimeRecoveryGraph
	runtime     restoreactivation.Runtime
	remover     restoreactivation.StagedRestoreRemover
	drillID     string
	stagingPath string
}

func newRestoreDrillStaging(
	ctx context.Context,
	workspace string,
	authority nativeV2BackupAuthority,
	drillID, stagingPath string,
) (advancedRestoreDrillStaging, error) {
	plan, manifest, err := readNativeV2RestorePlanManifest(ctx, workspace, authority.OutputRoot)
	if err != nil {
		return nil, err
	}
	if plan.Binding() != authority.Lineage.Binding {
		return nil, errors.New("restore drill Plan differs from the current Apply authority")
	}
	graph, err := restoreactivation.DeriveRuntimeRecoveryGraph(workspace, plan, manifest, drillID)
	if err != nil {
		return nil, err
	}
	runtime, err := restoreactivation.NewDockerRuntime(workspace)
	if err != nil {
		return nil, err
	}
	remover, ok := runtime.(restoreactivation.StagedRestoreRemover)
	if !ok {
		return nil, errors.New("restore drill runtime cannot remove its staged restore")
	}
	return &dockerRestoreDrillStaging{
		workspace: workspace, authority: authority, plan: plan, manifest: manifest,
		graph: graph, runtime: runtime, remover: remover,
		drillID: drillID, stagingPath: stagingPath,
	}, nil
}

func (staging *dockerRestoreDrillStaging) Verify(
	ctx context.Context,
	result backuplifecycle.RestoreResult,
) ([]restoreDrillCheck, error) {
	var checks []restoreDrillCheck
	step := func(name string, check func() error) error {
		err := check()
		entry := restoreDrillCheck{Name: name, Status: "passed"}
		if err != nil {
			entry.Status, entry.Detail = "failed", err.Error()
		}
		checks = append(checks, entry)
		return err
	}
	if err := step("restore-result-signature", func() error {
		return backuplifecycle.VerifyRestoreResult(staging.workspace, result)
	}); err != nil {
		return checks, err
	}
	if err := step("restore-result-authority", func() error {
		if result.AuthorizationLineage != staging.authority.Lineage ||
			result.OwnerRef != staging.authority.OwnerRef {
			return errors.New("staged restore result differs from the current Owner and Apply authority")
		}
		return nil
	}); err != nil {
		return checks, err
	}
	if err := step("repository-content", func() error {
		if !result.Receipt.RepositoryContentVerified ||
			result.Receipt.StagingPath != staging.stagingPath {
			return errors.New("repository did not verify the staged snapshot content")
		}
		return nil
	}); err != nil {
		return checks, err
	}
	var authority restoreactivation.Authority
	if err := step("recovery-graph-binding", func() error {
		var err error
		authority, err = restoreactivation.DeriveAuthority(
			staging.workspace, staging.plan, staging.manifest, result, staging.drillID,
		)
		return err
	}); err != nil {
		return checks, err
	}
	if err := step("volume-custody", func() error {
		return staging.runtime.Inspect(ctx, authority)
	}); err != nil {
		return checks, err
	}
	if err := step("staged-volume-set", func() error {
		return staging.runtime.ValidateStaging(ctx, authority)
	}); err != nil {
		return checks, fmt.Errorf("staged restore is incomplete: %w", err)
	}
	return checks, nil
}

func (staging *dockerRestoreDrillStaging) Remove(ctx context.Context) error {
	return staging.remover.RemoveStagedRestore(ctx, staging.graph, staging.stagingPath)
}
