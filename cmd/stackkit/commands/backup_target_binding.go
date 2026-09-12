package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func runBackupTargetImport(cmd *cobra.Command, approved, rebind bool, candidateDigest string) error {
	if !approved {
		return errors.New("backup target import requires --owner-approve")
	}
	raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), (32<<10)+1))
	if err != nil || len(raw) > 32<<10 {
		return errors.New("backup target input exceeds its bounded JSON contract")
	}
	defer backupcustody.Clear(raw)
	var input struct {
		backupcustody.S3TargetMaterial
		Passphrase string `json:"passphrase"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return errors.New("backup target input is invalid JSON")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("backup target input contains trailing content")
	}
	material := input.S3TargetMaterial
	material.Passphrase = []byte(input.Passphrase)
	defer backupcustody.Clear(material.Passphrase)
	if rebind {
		if !nativeV2BackupDigestPattern.MatchString(candidateDigest) {
			return errors.New("rebind requires --candidate-digest")
		}
		if err := prepareBackupTargetRebind(cmd, material); err != nil {
			return err
		}
	}
	initial, initialSource, err := inspectBackupTargetSource(cmd.Context())
	if err != nil {
		return err
	}
	return withLifecycleMutation(initial.WorkspaceRoot, "backup target import", func() error {
		return withArchitectureV2OutputLock(initial.WorkspaceRoot, initial.OutputRoot, func(_ *confinedfs.Transaction, _ *confinedfs.OutputLock) error {
			current, source, err := inspectBackupTargetSource(cmd.Context())
			if err != nil {
				return err
			}
			if initial.Plan.Binding() != current.Plan.Binding() || initialSource != source {
				return errors.New("backup target authority changed while acquiring the lifecycle lock")
			}
			requirement, err := backupTargetRequirement(current)
			if err != nil {
				return err
			}
			inventoryRaw, inventoryPath, err := locateArchitectureV2Inventory(current.WorkspaceRoot, "")
			if err != nil {
				return err
			}
			if inventoryPath == "" {
				return errors.New("backup target requires the existing owner Inventory")
			}
			var inventory map[string]any
			if err := yaml.Unmarshal(inventoryRaw, &inventory); err != nil {
				return errors.New("backup target Inventory is malformed")
			}
			relative, err := federationWorkspacePath(current.WorkspaceRoot, inventoryPath, "Inventory")
			if err != nil {
				return err
			}
			stored, loadErr := backupcustody.StoredS3TargetAuthority(current.WorkspaceRoot)
			if loadErr != nil && !errors.Is(loadErr, backupcustody.ErrMissing) {
				return loadErr
			}
			targetRef, custodyRef, err := backupcustody.S3TargetReferences(current.WorkspaceRoot, material)
			if err != nil {
				return err
			}
			var binding resolvedplan.ExternalBackupTargetBinding
			var authority backupcustody.S3TargetAuthority
			previousHash := ""
			if loadErr == nil && !rebind {
				if stored.SourceDigest != source || stored.Binding.RequirementsHash != requirement["requirementsHash"] || stored.Binding.BackupTargetRef != targetRef || stored.Binding.CustodyAttestationRef != custodyRef {
					return errors.New("backup target changed; explicit --rebind preserves only identical target and credentials")
				}
				authority = stored
				binding = externalBindingFromCustody(stored.Binding)
			} else {
				if !nativeV2BackupDigestPattern.MatchString(candidateDigest) {
					return errors.New("backup target binding requires --candidate-digest for the installed release")
				}
				binding, err = resolvedplan.IssueExternalBackupTargetBinding(requirement, targetRef, custodyRef, architectureV2ComponentVersion(version), candidateDigest, time.Now().UTC())
				if err != nil {
					return err
				}
				authority, err = projectBackupTargetAuthority(current, source, binding)
				if err != nil {
					return err
				}
				if loadErr == nil {
					sites, _ := inventory["externalBackupTargetBindings"].(map[string]any)
					bindings, _ := sites[stored.Binding.SiteRef].(map[string]any)
					previous, _ := bindings[stored.Binding.CapabilityRef].(map[string]any)
					previousHash, _ = previous["bindingHash"].(string)
				}
			}
			updated, err := resolvedplan.AttachExternalBackupTargetBinding(inventory, requirement, binding, previousHash)
			if err != nil {
				return err
			}
			if loadErr == nil && rebind {
				err = backupcustody.RebindS3Target(current.WorkspaceRoot, authority, material)
			} else {
				err = backupcustody.EstablishS3Target(current.WorkspaceRoot, authority, material)
			}
			if err != nil {
				return err
			}
			canonical, err := resolvedplan.CanonicalJSON(updated)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(inventory, updated) {
				if err := writeFederationPrivateAtomic(current.WorkspaceRoot, relative, canonical); err != nil {
					return err
				}
			}
			return writeCommandResult(cmd, cmd.CommandPath(), map[string]string{"state": "custodied", "backupTargetRef": authority.Binding.BackupTargetRef, "custodyAttestationRef": authority.Binding.CustodyAttestationRef, "nextAction": "stackkit generate"})
		})
	})
}

func backupTargetRequirement(generated nativeV2AppliedAuthority) (resolvedplan.BackupTargetRequirement, error) {
	var plan map[string]any
	if err := json.Unmarshal(generated.Plan.Canonical(), &plan); err != nil {
		return nil, err
	}
	sites, _ := plan["backupTargetRequirements"].(map[string]any)
	capabilities, _ := sites[generated.Owner.Binding.SiteRef].(map[string]any)
	requirement, _ := capabilities["offsite-object-backup"].(map[string]any)
	if requirement == nil {
		return nil, errors.New("enable offsite-object-backup and generate before importing its target")
	}
	return resolvedplan.BackupTargetRequirement(requirement), nil
}

func projectBackupTargetAuthority(generated nativeV2AppliedAuthority, source string, binding resolvedplan.ExternalBackupTargetBinding) (backupcustody.S3TargetAuthority, error) {
	bindings, err := generated.Plan.ProjectExternalBackupTargetBinding(binding)
	if err != nil {
		return backupcustody.S3TargetAuthority{}, err
	}
	for _, projected := range bindings {
		if projected.SiteRef == generated.Owner.Binding.SiteRef && reflect.DeepEqual(projected.TargetNodeRefs, []string{generated.Owner.Binding.NodeRef}) {
			return backupcustody.S3TargetAuthority{Binding: projected, SourceDigest: source}, nil
		}
	}
	return backupcustody.S3TargetAuthority{}, errors.New("backup target has no exact generated runtime projection")
}

func externalBindingFromCustody(binding generationartifact.ApplyBackupTargetBindingRequirement) resolvedplan.ExternalBackupTargetBinding {
	return resolvedplan.ExternalBackupTargetBinding{"apiVersion": "stackkit.external-backup-target-binding/v1", "kind": "ExternalBackupTargetBinding", "bindingRef": binding.BindingRef, "backupTargetRef": binding.BackupTargetRef, "custodyAttestationRef": binding.CustodyAttestationRef, "stackId": binding.StackID, "siteRef": binding.SiteRef, "capabilityRef": binding.CapabilityRef, "contractOwnerRef": binding.ContractOwnerRef, "capabilityContractHash": binding.CapabilityContractHash, "requirementsHash": binding.RequirementsHash, "stackkitsVersion": binding.StackKitsVersion, "candidateDigest": binding.CandidateDigest, "specHash": binding.SpecHash, "issuedAt": binding.IssuedAt, "validUntil": binding.ValidUntil, "bindingHash": binding.BindingHash}
}
