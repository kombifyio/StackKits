package backuplifecycle

import "github.com/kombifyio/stackkits/internal/localbackuppolicy"

// BackupCoverage describes the selected source policy, not proof that its data
// is recoverable. Only the owner-bound configuration supplies these facts.
type BackupCoverage struct {
	Scope                string                                `json:"scope"`
	RecoveryMode         string                                `json:"recoveryMode"`
	PolicyArtifactDigest string                                `json:"policyArtifactDigest"`
	Target               localbackuppolicy.Target              `json:"target"`
	ManagedVolumeNames   []string                              `json:"managedVolumeNames"`
	ApplicationVolumes   []localbackuppolicy.ApplicationVolume `json:"applicationVolumes,omitempty"`
	ExcludePaths         []string                              `json:"excludePaths"`
	OffHostRecovery      string                                `json:"offHostRecovery"`
	ApplicationRecovery  string                                `json:"applicationRecovery"`
}

func sourceCoverage(configuration Configuration) *BackupCoverage {
	policy := clonePolicy(configuration.Policy)
	return &BackupCoverage{
		Scope: "declared-source-policy", RecoveryMode: "physical-whole-set",
		PolicyArtifactDigest: configuration.PolicyArtifactDigest, Target: policy.Target,
		ManagedVolumeNames: policy.Source.ManagedVolumeNames, ApplicationVolumes: policy.Source.ApplicationVolumes,
		ExcludePaths: policy.Source.ExcludePaths,
		// No native producer currently attests independent off-host custody or
		// functional data/client recovery. Repository and HTTP health cannot do so.
		OffHostRecovery: "unverified", ApplicationRecovery: "unverified",
	}
}
