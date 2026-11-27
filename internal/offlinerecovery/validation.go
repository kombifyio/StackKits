package offlinerecovery

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

func canonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return raw, nil
}

func sha256Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func decodeCanonical(raw []byte, limit int64, destination any) error {
	if len(raw) == 0 || int64(len(raw)) > limit {
		return errors.New("document size is outside the allowed bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("document contains multiple JSON values")
		}
		return err
	}
	canonical, err := canonicalJSON(destination)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, canonical) {
		return errors.New("document is not canonical JSON")
	}
	return nil
}

// Digest is the canonical identity of the complete recipe, including step order.
func (p Plan) Digest() (string, error) {
	if err := validatePlan(p); err != nil {
		return "", err
	}
	raw, err := canonicalJSON(p)
	if err != nil {
		return "", err
	}
	return sha256Digest(raw), nil
}

// ArtifactSetDigest binds the exact sorted artifact identities, digests, and
// sizes. Paths and versions are already bound by the plan digest.
func (p Plan) ArtifactSetDigest() (string, error) {
	if err := validatePlan(p); err != nil {
		return "", err
	}
	return artifactSetDigest(p.Artifacts)
}

func artifactSetDigest(artifacts []Artifact) (string, error) {
	type identity struct {
		ID        string `json:"id"`
		SHA256    string `json:"sha256"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	identities := make([]identity, 0, len(artifacts))
	for _, artifact := range artifacts {
		identities = append(identities, identity{
			ID: artifact.ID, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes,
		})
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i].ID < identities[j].ID })
	raw, err := canonicalJSON(identities)
	if err != nil {
		return "", err
	}
	return sha256Digest(raw), nil
}

func validatePlan(plan Plan) error {
	if plan.SchemaVersion != PlanSchemaVersion {
		return deny("plan_contract_unsupported", "the recovery plan contract is unsupported", "regenerate the local recovery plan with the current StackKits release")
	}
	if !refPattern.MatchString(plan.Version) || !refPattern.MatchString(plan.OperationID) {
		return deny("plan_identity_invalid", "the recovery plan version or operation identity is invalid", "regenerate the plan with canonical version and operation identifiers")
	}
	if plan.Mode != ModeStandard {
		return deny("standard_mode_required", "offline recovery is admitted only in Standard Mode", "use the standalone local StackKits recovery path; do not add an Advanced executor")
	}
	if !validOwnerRef(plan.OwnerRef) {
		return deny("plan_binding_invalid", "the recovery plan ownerRef is invalid", "regenerate the plan from the current node custody and resolved plan")
	}
	for _, binding := range []struct{ label, value string }{
		{"siteRef", plan.SiteRef}, {"nodeRef", plan.NodeRef},
		{"channelRef", plan.ChannelRef}, {"stackId", plan.StackID},
	} {
		if !refPattern.MatchString(binding.value) {
			return deny("plan_binding_invalid", fmt.Sprintf("the recovery plan %s is invalid", binding.label), "regenerate the plan from the current node custody and resolved plan")
		}
	}
	if len(plan.WorkloadRefs) > MaxWorkloads || !sortedUniqueStrings(plan.WorkloadRefs) {
		return deny("plan_workloads_invalid", "the recovery plan workload set is duplicated, unsorted, or too large", "regenerate a bounded plan with unique sorted workload references")
	}
	for _, workloadRef := range plan.WorkloadRefs {
		if !refPattern.MatchString(workloadRef) {
			return deny("plan_workloads_invalid", "the recovery plan contains an invalid workload reference", "regenerate the plan with canonical local workload references")
		}
	}
	workloads := stringSet(plan.WorkloadRefs)
	artifacts := make(map[string]Artifact, len(plan.Artifacts))
	if len(plan.Artifacts) == 0 || len(plan.Artifacts) > MaxArtifacts {
		return deny("plan_artifacts_invalid", "the recovery plan artifact set is empty or too large", "cache the exact plan artifacts and regenerate the bounded plan")
	}
	var total int64
	previousID := ""
	for _, artifact := range plan.Artifacts {
		if !refPattern.MatchString(artifact.ID) || artifact.ID <= previousID {
			return deny("plan_artifacts_invalid", "the recovery plan artifacts are duplicated, unsorted, or invalid", "regenerate the plan with unique artifact identifiers sorted by ID")
		}
		previousID = artifact.ID
		if _, err := confinedfs.ValidatePortablePath(artifact.RelativePath); err != nil {
			return deny("plan_artifact_path_invalid", fmt.Sprintf("cached artifact %q has an unsafe relative path", artifact.ID), "place the cached artifact at a canonical portable path beneath the cache root")
		}
		if !refPattern.MatchString(artifact.Version) || !digestPattern.MatchString(artifact.SHA256) ||
			artifact.SizeBytes <= 0 || artifact.SizeBytes > MaxArtifactBytes {
			return deny("plan_artifact_identity_invalid", fmt.Sprintf("cached artifact %q has invalid version, digest, or size", artifact.ID), "regenerate the plan from verified cached artifact metadata")
		}
		if _, duplicate := artifacts[artifact.ID]; duplicate {
			return deny("plan_artifact_duplicate", fmt.Sprintf("cached artifact %q is duplicated", artifact.ID), "regenerate the plan with one exact artifact identity")
		}
		artifacts[artifact.ID] = artifact
		total += artifact.SizeBytes
		if total > MaxTotalArtifactSize {
			return deny("plan_artifacts_too_large", "the recovery plan exceeds the total cached artifact bound", "split the recovery into independently authorized bounded plans")
		}
	}
	if len(plan.Steps) == 0 || len(plan.Steps) > MaxSteps {
		return deny("plan_steps_invalid", "the recovery plan has no steps or exceeds the step bound", "regenerate a bounded recovery recipe")
	}
	seenSteps := make(map[string]struct{}, len(plan.Steps))
	usedArtifacts := make(map[string]struct{}, len(plan.Artifacts))
	destinationPrefix := ".stackkit/offline-recovery/" + plan.OperationID + "/"
	for _, step := range plan.Steps {
		if !refPattern.MatchString(step.ID) {
			return deny("plan_step_identity_invalid", "a recovery step has an invalid identity", "regenerate the plan with canonical step identifiers")
		}
		if _, duplicate := seenSteps[step.ID]; duplicate {
			return deny("plan_step_duplicate", fmt.Sprintf("recovery step %q is duplicated", step.ID), "regenerate the plan with unique step identifiers")
		}
		seenSteps[step.ID] = struct{}{}
		switch step.Kind {
		case StepMaterialize:
			if _, exists := artifacts[step.ArtifactID]; !exists || step.WorkloadRef != "" {
				return deny("plan_step_scope_invalid", fmt.Sprintf("materialize step %q does not name one exact plan artifact", step.ID), "regenerate the step from the plan artifact set")
			}
			if _, err := confinedfs.ValidatePortablePath(step.Destination); err != nil ||
				!strings.HasPrefix(step.Destination, destinationPrefix) {
				return deny("plan_step_destination_invalid", fmt.Sprintf("materialize step %q escapes its plan-owned destination", step.ID), "regenerate the step beneath its exact operation-owned recovery directory")
			}
			usedArtifacts[step.ArtifactID] = struct{}{}
		case StepRestart, StepVerify:
			if _, exists := workloads[step.WorkloadRef]; !exists || step.ArtifactID != "" || step.Destination != "" {
				return deny("plan_step_scope_invalid", fmt.Sprintf("workload step %q is outside the plan's selected workloads", step.ID), "regenerate the step with one exact plan-selected local workload")
			}
		default:
			return deny("plan_step_kind_unsupported", fmt.Sprintf("recovery step %q has an unsupported kind", step.ID), "regenerate the recipe using only materialize, restart, and verify steps")
		}
	}
	if len(usedArtifacts) != len(plan.Artifacts) {
		return deny("plan_artifact_unused", "the recovery plan contains an artifact no step uses", "regenerate the plan so every cached artifact is bound to a materialize step")
	}
	return nil
}

func validateAuthorityShape(authority Authority) error {
	if authority.SchemaVersion != AuthoritySchemaVersion || !refPattern.MatchString(authority.Version) {
		return deny("authority_contract_unsupported", "the offline recovery authority contract is unsupported", "obtain a new authority from the current local Owner policy authority")
	}
	if authority.Mode != ModeStandard {
		return deny("standard_mode_required", "offline recovery authority is valid only in Standard Mode", "obtain a Standard Mode local authority; do not route through an Advanced executor")
	}
	if !digestPattern.MatchString(authority.PlanDigest) || !digestPattern.MatchString(authority.ArtifactSetDigest) {
		return deny("authority_binding_invalid", "the offline recovery authority has no exact plan and artifact binding", "obtain a new authority over the verified plan and artifact set")
	}
	if !validOwnerRef(authority.OwnerRef) {
		return deny("authority_binding_invalid", "the offline recovery authority ownerRef is invalid", "obtain a new authority bound to current local Owner custody")
	}
	for _, binding := range []struct{ label, value string }{
		{"siteRef", authority.SiteRef},
		{"nodeRef", authority.NodeRef}, {"channelRef", authority.ChannelRef},
		{"operationId", authority.OperationID},
	} {
		if !refPattern.MatchString(binding.value) {
			return deny("authority_binding_invalid", fmt.Sprintf("the offline recovery authority %s is invalid", binding.label), "obtain a new authority bound to current local Owner custody")
		}
	}
	if !keyIDPattern.MatchString(authority.OwnerKeyID) {
		return deny("authority_key_invalid", "the offline recovery authority names an invalid Owner key", "obtain a new authority from the established local Owner key")
	}
	if !canonicalUTC(authority.IssuedAt) || !canonicalUTC(authority.ExpiresAt) ||
		!authority.ExpiresAt.After(authority.IssuedAt) ||
		authority.ExpiresAt.Sub(authority.IssuedAt) > MaxAuthorityLifetime {
		return deny("authority_lifetime_invalid", "the offline recovery authority has no bounded valid lifetime", "obtain a new authority with UTC second precision and a lifetime no longer than five minutes")
	}
	signature := authority.Signature
	if signature.OwnerRef != authority.OwnerRef || signature.KeyID != authority.OwnerKeyID || signature.Value == "" {
		return deny("authority_signature_missing", "the offline recovery authority is not signed by its named Owner key", "obtain a signed authority through local Owner policy-state custody")
	}
	raw, err := base64.RawStdEncoding.Strict().DecodeString(signature.Value)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return deny("authority_signature_malformed", "the offline recovery authority signature is malformed", "obtain a newly signed authority; do not repair signature bytes locally")
	}
	return nil
}

func canonicalUTC(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Nanosecond() == 0
}

func validateClock(reading ClockReading) error {
	if reading.Wall.IsZero() || reading.Monotonic <= 0 {
		return deny("clock_untrusted", "the local clock has no trusted wall and monotonic anchor", "restart the local recovery process and obtain a new authority on its continuing clock")
	}
	return nil
}

func validateExecutorIdentity(identity ExecutorIdentity, nodeRef string) error {
	if !refPattern.MatchString(identity.ID) || !refPattern.MatchString(identity.Version) ||
		!digestPattern.MatchString(identity.Digest) || identity.NodeRef != nodeRef || identity.Mode != ModeStandard ||
		identity.NetworkAccess || identity.ShellAccess {
		return deny("executor_not_local_standard", "the workload executor is not an exact local Standard Mode adapter", "use the existing local StackKits workload adapter with network and shell access disabled")
	}
	return nil
}

func validOwnerRef(value string) bool {
	return len(value) > 0 && len(value) <= 128 && ownerRefPattern.MatchString(value)
}

func sortedUniqueStrings(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] >= values[index] {
			return false
		}
	}
	return true
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}
