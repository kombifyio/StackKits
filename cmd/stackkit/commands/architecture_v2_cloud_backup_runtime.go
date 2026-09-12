package commands

import (
	"context"
	"errors"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
)

func newArchitectureV2CloudBackupRegistration(workspace, runtimeVersion string) (architecturev2.ProductRuntimeOwnerRegistration, error) {
	operations, err := runtimeexecutorlocal.NewOSCloudOffsiteBackupOperations(workspace, func(ctx context.Context) (localbackuppolicy.Policy, backupcustody.S3TargetAuthority, error) {
		var policy localbackuppolicy.Policy
		var target backupcustody.S3TargetAuthority
		generated, err := inspectNativeV2GeneratedAuthority(ctx, workspace, specFile)
		if err != nil {
			return policy, target, err
		}
		_, artifact, err := nativeV2BackupPolicyRequirement(generated.Plan, generated.Owner.Binding.SiteRef, generated.Owner.Binding.NodeRef)
		if err != nil {
			return policy, target, err
		}
		root, err := confinedfs.Open(workspace)
		if err != nil {
			return policy, target, err
		}
		defer root.Close()
		tx, err := root.BeginTransaction()
		if err != nil {
			return policy, target, err
		}
		defer tx.Close()
		_, _, policy, err = readNativeV2BackupPolicyForArtifact(tx, generated.Manifest, artifact.ID)
		if err != nil {
			return policy, target, err
		}
		digest, err := localbackuppolicy.SourceDigest(policy.SourceProjection())
		if err != nil {
			return policy, target, err
		}
		for _, binding := range generated.Plan.ApplyRequirements().BackupTargetBindings {
			if binding.SiteRef == generated.Owner.Binding.SiteRef && len(binding.TargetNodeRefs) == 1 && binding.TargetNodeRefs[0] == generated.Owner.Binding.NodeRef {
				if target.SourceDigest != "" {
					return policy, target, errors.New("Cloud offsite target is ambiguous")
				}
				target = backupcustody.S3TargetAuthority{Binding: binding, SourceDigest: digest}
			}
		}
		if target.SourceDigest == "" {
			return policy, target, errors.New("Cloud offsite target is absent")
		}
		return policy, target, nil
	})
	if err != nil {
		return architecturev2.ProductRuntimeOwnerRegistration{}, err
	}
	return architecturev2.NewProductCloudOffsiteBackupRegistration(runtimeVersion, operations)
}
