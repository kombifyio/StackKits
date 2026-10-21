package advancedcapability

import (
	"fmt"
	"time"
)

// AuthorizeBackupRenewal narrows a verified grant to one immutable local
// operation and repository. Callers derive this scope from signed Apply custody.
func (grant Grant) AuthorizeBackupRenewal(jobID, planHash, repositoryID, bindingHash, targetRef, custodyRef string, now time.Time) error {
	renewal := grant.BackupRenewal
	if renewal == nil || !now.Before(grant.ExpiresAt) || renewal.JobID != jobID || renewal.PlanHash != planHash || renewal.RepositoryID != repositoryID || renewal.BindingHash != bindingHash || renewal.TargetRef != targetRef || renewal.CustodyAttestationRef != custodyRef {
		return fmt.Errorf("backup renewal differs from the approved operation or applied repository")
	}
	return nil
}
