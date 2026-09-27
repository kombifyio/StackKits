package advancedcapability

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// BackupRenewal authorizes one local staged drill against unchanged applied
// authority after fresh managed entitlement, usage and remote target attestation.
// It grants no remote snapshot or data activation authority. All fields are
// canonical ASCII strings so both issuers serialize identical JCS bytes.
type BackupRenewal struct {
	AgentID               string `json:"agentId"`
	BindingHash           string `json:"bindingHash"`
	CustodyAttestationRef string `json:"custodyAttestationRef"`
	DeploymentID          string `json:"deploymentId"`
	FreshAttestationRef   string `json:"freshAttestationRef"`
	JobID                 string `json:"jobId"`
	MeasuredAt            string `json:"measuredAt"`
	PlanHash              string `json:"planHash"`
	QuotaBytes            string `json:"quotaBytes"`
	RepositoryID          string `json:"repositoryId"`
	TargetRef             string `json:"targetRef"`
	TenantID              string `json:"tenantId"`
	UsedBytes             string `json:"usedBytes"`
}

func (renewal BackupRenewal) Validate(at time.Time) error {
	id := regexp.MustCompile(`^[A-Za-z0-9_:@|.-]{1,180}$`)
	digest := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	ref := func(prefix, value string) bool {
		return regexp.MustCompile(`^` + prefix + `://sha256/[0-9a-f]{64}$`).MatchString(value)
	}
	for _, value := range []string{renewal.AgentID, renewal.DeploymentID, renewal.JobID, renewal.TenantID} {
		if !id.MatchString(value) {
			return fmt.Errorf("backup renewal identity is invalid")
		}
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{7,99}$`).MatchString(renewal.JobID) || !digest.MatchString(renewal.PlanHash) || !digest.MatchString(renewal.BindingHash) || !ref("backup-target", renewal.TargetRef) || !ref("backup-custody-attestation", renewal.CustodyAttestationRef) || !ref("backup-custody-attestation", renewal.FreshAttestationRef) || renewal.RepositoryID != "kopia:local:cloud" {
		return fmt.Errorf("backup renewal scope is invalid")
	}
	measured, err := time.Parse(time.RFC3339Nano, renewal.MeasuredAt)
	if err != nil || measured.UTC().Format(time.RFC3339Nano) != renewal.MeasuredAt || measured.After(at.Add(time.Second)) || at.Sub(measured) > 5*time.Minute {
		return fmt.Errorf("backup renewal measurement is stale")
	}
	quota, err := strconv.ParseInt(renewal.QuotaBytes, 10, 64)
	if err != nil || quota <= 0 || strconv.FormatInt(quota, 10) != renewal.QuotaBytes {
		return fmt.Errorf("backup renewal quota is invalid")
	}
	used, err := strconv.ParseInt(renewal.UsedBytes, 10, 64)
	if err != nil || used < 0 || used >= quota || strconv.FormatInt(used, 10) != renewal.UsedBytes {
		return fmt.Errorf("backup renewal usage is invalid")
	}
	return nil
}

func (renewal BackupRenewal) canonical() []byte { raw, _ := json.Marshal(renewal); return raw }

// AuthorizeBackupRenewal narrows a verified grant to one immutable local
// operation and repository. Callers derive this scope from signed Apply custody.
func (grant Grant) AuthorizeBackupRenewal(jobID, planHash, repositoryID, bindingHash, targetRef, custodyRef string, now time.Time) error {
	renewal := grant.BackupRenewal
	if renewal == nil || !now.Before(grant.ExpiresAt) || renewal.JobID != jobID || renewal.PlanHash != planHash || renewal.RepositoryID != repositoryID || renewal.BindingHash != bindingHash || renewal.TargetRef != targetRef || renewal.CustodyAttestationRef != custodyRef {
		return fmt.Errorf("backup renewal differs from the approved operation or applied repository")
	}
	return nil
}
