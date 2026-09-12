package commands

import (
	"context"
	"errors"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/spf13/cobra"
)

func init() { backupCmd.AddCommand(newBackupTargetCommand()) }

func newBackupTargetCommand() *cobra.Command {
	target := &cobra.Command{Use: "target", Short: "Bind an owner-supplied S3 target to the generated backup policy", Args: cobra.NoArgs}
	importCommand := &cobra.Command{Use: "import", Short: "Import encrypted S3 custody from JSON on stdin", Long: "Import JSON fields endpoint, bucket, prefix, region, accessKeyId, secretAccessKey and passphrase from stdin. The passphrase is a string for an existing Kopia repository. Only opaque target references are printed. This imports custody; it does not create a bucket or verify offsite readiness.", Args: cobra.NoArgs, Annotations: map[string]string{noDeployObservabilityAnnotation: "true"}}
	approved := false
	importCommand.Flags().BoolVar(&approved, "owner-approve", false, "Authorize this exact local backup target")
	var rebind bool
	var candidateDigest string
	importCommand.Flags().BoolVar(&rebind, "rebind", false, "Renew Plan/source authority while preserving the existing target and credentials")
	importCommand.Flags().StringVar(&candidateDigest, "candidate-digest", "", "Exact sha256 digest of the installed StackKits release candidate")
	importCommand.RunE = func(cmd *cobra.Command, _ []string) error {
		return runBackupTargetImport(cmd, approved, rebind, candidateDigest)
	}
	status := &cobra.Command{Use: "status", Short: "Verify local target custody without contacting S3", Args: cobra.NoArgs, Annotations: map[string]string{noDeployObservabilityAnnotation: "true"}, RunE: func(cmd *cobra.Command, _ []string) error {
		authority, root, _, err := inspectBackupTargetAuthority(cmd.Context())
		if err != nil {
			return err
		}
		material, err := backupcustody.LoadS3Target(root, authority)
		backupcustody.Clear(material.Passphrase)
		if err != nil {
			return err
		}
		return writeBackupTargetSummary(cmd, authority)
	}}
	target.AddCommand(importCommand, status)
	return target
}

func writeBackupTargetSummary(cmd *cobra.Command, authority backupcustody.S3TargetAuthority) error {
	return writeCommandResult(cmd, cmd.CommandPath(), map[string]string{"state": "custodied", "backupTargetRef": authority.Binding.BackupTargetRef, "custodyAttestationRef": authority.Binding.CustodyAttestationRef})
}

func inspectBackupTargetAuthority(ctx context.Context) (backupcustody.S3TargetAuthority, string, string, error) {
	generated, digest, err := inspectBackupTargetSource(ctx)
	if err != nil {
		return backupcustody.S3TargetAuthority{}, "", "", err
	}
	var result backupcustody.S3TargetAuthority
	for _, binding := range generated.Plan.ApplyRequirements().BackupTargetBindings {
		if binding.SiteRef == generated.Owner.Binding.SiteRef && len(binding.TargetNodeRefs) == 1 && binding.TargetNodeRefs[0] == generated.Owner.Binding.NodeRef {
			if result.SourceDigest != "" {
				return backupcustody.S3TargetAuthority{}, "", "", errors.New("backup target authority is ambiguous")
			}
			result = backupcustody.S3TargetAuthority{Binding: binding, SourceDigest: digest}
		}
	}
	if result.SourceDigest == "" {
		return backupcustody.S3TargetAuthority{}, "", "", errors.New("backup target requires an exact external backup target binding in the generated Plan")
	}
	return result, generated.WorkspaceRoot, generated.OutputRoot, nil
}

func inspectBackupTargetSource(ctx context.Context) (nativeV2AppliedAuthority, string, error) {
	generated, err := inspectNativeV2GeneratedAuthority(ctx, getWorkDir(), specFile)
	if err != nil {
		return nativeV2AppliedAuthority{}, "", err
	}
	_, artifact, err := nativeV2BackupPolicyRequirement(generated.Plan, generated.Owner.Binding.SiteRef, generated.Owner.Binding.NodeRef)
	if err != nil {
		return nativeV2AppliedAuthority{}, "", err
	}
	root, err := confinedfs.Open(generated.WorkspaceRoot)
	if err != nil {
		return nativeV2AppliedAuthority{}, "", err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return nativeV2AppliedAuthority{}, "", err
	}
	defer tx.Close()
	_, _, policy, err := readNativeV2BackupPolicyForArtifact(tx, generated.Manifest, artifact.ID)
	if err != nil {
		return nativeV2AppliedAuthority{}, "", err
	}
	if policy.Source.CoreModuleRef != "stackkits-cloud-core-standalone-runtime" {
		return nativeV2AppliedAuthority{}, "", errors.New("backup target requires the Cloud standalone source policy")
	}
	digest, err := localbackuppolicy.SourceDigest(policy.SourceProjection())
	if err != nil {
		return nativeV2AppliedAuthority{}, "", err
	}
	return generated, digest, nil
}
