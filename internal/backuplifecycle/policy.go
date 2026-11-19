package backuplifecycle

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

type Consistency string

const ConsistencyCrashConsistent Consistency = "crash-consistent"

// AuthorityLineage binds backup side effects to the complete verified
// generation and apply chain, rather than to a plan hash in isolation.
type AuthorityLineage struct {
	Binding               generationartifact.PlanBinding `json:"binding"`
	ManifestHash          string                         `json:"manifestHash"`
	GenerationReceiptHash string                         `json:"generationReceiptHash"`
	ApplyResultHash       string                         `json:"applyResultHash"`
	ApplyReceiptHash      string                         `json:"applyReceiptHash"`
	OwnerBindingDigest    string                         `json:"ownerBindingDigest"`
	PocketIDSubject       string                         `json:"pocketIdSubject"`
}

// Policy is the shared strict renderer-to-lifecycle contract.
type Policy = localbackuppolicy.Policy

func decodePolicyArtifact(artifact []byte) (Policy, error) {
	if len(artifact) == 0 {
		return Policy{}, errors.New("backuplifecycle: local backup policy artifact is required")
	}
	policy, err := localbackuppolicy.Decode(artifact)
	if err != nil {
		return Policy{}, fmt.Errorf("backuplifecycle: decode local backup policy artifact: %w", err)
	}
	return policy, nil
}

func validatePolicy(policy Policy) error {
	if err := localbackuppolicy.ValidateSnapshotPolicy(policy); err != nil {
		return fmt.Errorf("backuplifecycle: local backup policy differs from the governed contract: %w", err)
	}
	return nil
}

func validateLineage(lineage AuthorityLineage) error {
	binding := lineage.Binding
	if !validDigest(binding.PlanHash) || !validDigest(binding.SpecHash) ||
		!validDigest(binding.InventoryHash) || !validDigest(binding.DefinitionHash) ||
		binding.CompilerVersion == "" || binding.Renderer.ID == "" || binding.Renderer.Version == "" ||
		binding.Authority.Class == "" || binding.Authority.Document == "" ||
		binding.Authority.Issuer == "" || !validDigest(binding.Authority.AuthorityFingerprint) ||
		!validDigest(binding.Authority.CatalogHash) ||
		!validDigest(lineage.ManifestHash) || !validDigest(lineage.GenerationReceiptHash) ||
		!validDigest(lineage.ApplyResultHash) || !validDigest(lineage.ApplyReceiptHash) ||
		!validDigest(lineage.OwnerBindingDigest) || strings.TrimSpace(lineage.PocketIDSubject) == "" {
		return errors.New("backuplifecycle: complete plan/generation/apply authority lineage is required")
	}
	return nil
}

func clonePolicy(policy Policy) Policy {
	policy.Source = policy.SourceProjection()
	policy.DataClasses = append([]string(nil), policy.DataClasses...)
	policy.Runtime = policy.RuntimeProjection()
	if policy.Schedule != nil {
		schedule := *policy.Schedule
		if schedule.HourUTC != nil {
			hour := *schedule.HourUTC
			schedule.HourUTC = &hour
		}
		policy.Schedule = &schedule
	}
	if policy.Retention != nil {
		retention := *policy.Retention
		policy.Retention = &retention
	}
	if policy.RetentionByDataClass != nil {
		retentions := make(map[string]localbackuppolicy.Retention, len(policy.RetentionByDataClass))
		for class, retention := range policy.RetentionByDataClass {
			retentions[class] = retention
		}
		policy.RetentionByDataClass = retentions
	}
	policy.RecoveryObjectives = append([]localbackuppolicy.RecoveryObjective(nil), policy.RecoveryObjectives...)
	for index := range policy.RecoveryObjectives {
		policy.RecoveryObjectives[index].WorkloadRefs = append([]string(nil), policy.RecoveryObjectives[index].WorkloadRefs...)
	}
	return policy
}

func policiesEqual(left, right Policy) bool {
	return reflect.DeepEqual(left, right)
}

func lineagesEqual(left, right AuthorityLineage) bool {
	return digestJSON(left) == digestJSON(right)
}
