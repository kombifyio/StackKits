package commands

import (
	"errors"
	"time"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackupruntime"
)

// The renewal is operation authority, never replacement generation or Apply
// evidence. The original binding, repository and every applied hash stay intact.
func verifyRestoreDrillBackupRenewal(authority nativeV2BackupAuthority, grant advancedcapability.Grant, drillID string, now time.Time) error {
	return verifyRestoreDrillTargetRenewal(authority.Plan.ApplyRequirements().BackupTargetBindings, authority.Lineage.Binding.PlanHash, localbackupruntime.RepositoryIDForCoreModule(authority.Policy.Source.CoreModuleRef), grant, drillID, now)
}

func verifyRestoreDrillTargetRenewal(bindings []generationartifact.ApplyBackupTargetBindingRequirement, planHash, repositoryID string, grant advancedcapability.Grant, drillID string, now time.Time) error {
	if grant.BackupRenewal != nil {
		renewal := grant.BackupRenewal
		if !now.Before(grant.ExpiresAt) || renewal.JobID != drillID || renewal.PlanHash != planHash || renewal.RepositoryID != repositoryID {
			return errors.New("managed backup renewal differs from this drill's exact applied authority")
		}
		matched := false
		for _, binding := range bindings {
			if binding.BindingHash == renewal.BindingHash && binding.BackupTargetRef == renewal.TargetRef && binding.CustodyAttestationRef == renewal.CustodyAttestationRef && binding.ContractOwnerRef == "stackkits-cloud-offsite-backup" && binding.StackID == grant.StackID {
				if matched {
					return errors.New("managed backup renewal target is ambiguous")
				}
				if err := grant.AuthorizeBackupRenewal(drillID, planHash, repositoryID, binding.BindingHash, binding.BackupTargetRef, binding.CustodyAttestationRef, now); err != nil {
					return err
				}
				matched = true
			}
		}
		if !matched {
			return errors.New("managed backup renewal cannot replace the applied repository or credentials")
		}
		return nil
	}
	// Fresh rollout authority remains supported. An expired managed target
	// requires a new positive operation grant; a generic capability cannot renew it.
	for _, binding := range bindings {
		if binding.ContractOwnerRef == "stackkits-cloud-offsite-backup" {
			until, err := time.Parse(time.RFC3339Nano, binding.ValidUntil)
			if err != nil || !now.Before(until) {
				return errors.New("managed backup target requires a fresh signed restore-drill renewal")
			}
		}
	}
	return nil
}
