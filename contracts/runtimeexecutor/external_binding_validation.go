package runtimeexecutor

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func validateAccessBindings(bindings []AccessBinding) error {
	if len(bindings) > MaxAccessBindings {
		return invalidRequest("access_bindings", "must contain at most %d bindings", MaxAccessBindings)
	}
	seenIDs := make(map[string]struct{}, len(bindings))
	seenScopes := make(map[string]struct{}, len(bindings))
	for index, binding := range bindings {
		prefix := fmt.Sprintf("access_bindings[%d]", index)
		if err := validateAccessBindingAuthority(prefix, binding); err != nil {
			return err
		}
		if err := validateAccessBindingValidity(prefix, binding); err != nil {
			return err
		}
		if _, duplicate := seenIDs[binding.ID]; duplicate {
			return invalidRequest(prefix+".id", "duplicate access binding id")
		}
		seenIDs[binding.ID] = struct{}{}
		scope := binding.SiteRef + "\x00" + binding.CapabilityRef
		if _, duplicate := seenScopes[scope]; duplicate {
			return invalidRequest(prefix, "duplicate Home Site/capability access authority")
		}
		seenScopes[scope] = struct{}{}
	}
	return nil
}

func validateAccessBindingAuthority(prefix string, binding AccessBinding) error {
	for _, field := range []struct{ name, value string }{
		{"id", binding.ID}, {"runtime_requirement_id", binding.RuntimeRequirementID},
		{"stack_id", binding.StackID}, {fieldSiteRef, binding.SiteRef},
		{"contract_owner_ref", binding.ContractOwnerRef},
	} {
		if err := validateToken(prefix+"."+field.name, field.value); err != nil {
			return err
		}
	}
	if binding.Kind != "home-access" {
		return invalidRequest(prefix+".kind", "must be home-access")
	}
	if binding.CapabilityRef != "private-remote-access" && binding.CapabilityRef != "public-publish-egress" {
		return invalidRequest(prefix+".capability_ref", "must be a closed Home access capability")
	}
	for _, field := range []struct{ name, value string }{
		{"capability_contract_hash", binding.CapabilityContractHash}, {fieldRequirementsHash, binding.RequirementsHash},
		{"binding_hash", binding.BindingHash}, {"candidate_digest", binding.CandidateDigest}, {"spec_hash", binding.SpecHash},
		{"projection_hash", binding.ProjectionHash},
	} {
		if !validDigest(field.value) {
			return invalidRequest(prefix+"."+field.name, "must be a canonical SHA-256 digest")
		}
	}
	if !homeAccessBindingRefPattern.MatchString(binding.BindingRef) {
		return invalidRequest(prefix+".binding_ref", "must be an opaque Home access binding ref")
	}
	if !homeAccessFabricRefPattern.MatchString(binding.AccessFabricRef) {
		return invalidRequest(prefix+".access_fabric_ref", "must be an opaque Home access fabric ref")
	}
	if !semanticVersionPattern.MatchString(binding.StackKitsVersion) {
		return invalidRequest(prefix+".stackkits_version", "must be a semantic version")
	}
	if err := validateRefList(prefix+".target_node_refs", binding.TargetNodeRefs, 1, MaxNodeRefsPerTarget); err != nil {
		return err
	}
	return nil
}

func validateAccessBindingValidity(prefix string, binding AccessBinding) error {
	if validityErr := validateExternalBindingWindow(prefix, binding.IssuedAt, binding.ValidUntil, MaxAccessBindingValidity); validityErr != nil {
		return validityErr
	}
	wantProjectionHash, err := ComputeAccessBindingProjectionHash(binding)
	if err != nil {
		return invalidRequest(prefix+".projection_hash", "cannot derive canonical access projection")
	}
	if binding.ProjectionHash != wantProjectionHash {
		return invalidRequest(prefix+".projection_hash", "does not match the complete canonical access projection")
	}
	return nil
}

func validateAccessBindingClosure(targets []RuntimeTarget, bindings []AccessBinding) error {
	available := make(map[string]AccessBinding, len(bindings))
	referenceCount := make(map[string]int, len(bindings))
	for _, binding := range bindings {
		available[binding.ID] = binding
	}
	for targetIndex, target := range targets {
		if len(target.AccessBindingRefs) == 0 {
			continue
		}
		// AccessBinding carries one exact Site and a node subset, but it does
		// not carry a general node-to-Site topology map. Until the shared
		// contract grows explicit node/Site pairs, admitting a multi-Site
		// runtime target would let a syntactically complete binding set assert
		// an unprovable node placement. Keep the v1beta1 boundary deliberately
		// narrower: every access-bound runtime target is Site-local.
		if len(target.SiteRefs) != 1 {
			return invalidRequest(fmt.Sprintf("runtime_targets[%d].site_refs", targetIndex), "access-bound runtime target must belong to exactly one Site")
		}
		coveredSites := map[string]struct{}{}
		coveredNodes := map[string]struct{}{}
		coveredCapabilities := map[string]struct{}{}
		nodeSites := map[string]string{}
		for refIndex, ref := range target.AccessBindingRefs {
			binding, exists := available[ref]
			if !exists {
				return invalidRequest(fmt.Sprintf("runtime_targets[%d].access_binding_refs[%d]", targetIndex, refIndex), "references absent access binding %q", ref)
			}
			if err := validateAccessBindingTarget(fmt.Sprintf("runtime_targets[%d].access_binding_refs[%d]", targetIndex, refIndex), target, binding, coveredNodes, nodeSites); err != nil {
				return err
			}
			coveredSites[binding.SiteRef] = struct{}{}
			coveredCapabilities[binding.CapabilityRef] = struct{}{}
			referenceCount[ref]++
		}
		if !exactCoveredRefs(target.SiteRefs, coveredSites) || !exactCoveredRefs(target.NodeRefs, coveredNodes) || !exactCoveredCapabilities(target.AccessCapabilities, coveredCapabilities) {
			return invalidRequest(fmt.Sprintf("runtime_targets[%d].access_binding_refs", targetIndex), "bindings must exactly cover the target Site and node sets")
		}
	}
	for _, binding := range bindings {
		if referenceCount[binding.ID] != 1 {
			return invalidRequest("access_bindings."+binding.ID, "must be referenced by exactly one runtime target; got %d", referenceCount[binding.ID])
		}
	}
	return nil
}

func validateAccessBindingTarget(field string, target RuntimeTarget, binding AccessBinding, coveredNodes map[string]struct{}, nodeSites map[string]string) error {
	if !containsSorted(target.SiteRefs, binding.SiteRef) {
		return invalidRequest(field, "binding Site is outside the exact runtime target")
	}
	if binding.RuntimeRequirementID != target.RequirementID {
		return invalidRequest(field, "binding is owned by a different runtime requirement")
	}
	if binding.ContractOwnerRef != target.ProviderRef || !containsAccessCapability(target.AccessCapabilities, binding.CapabilityRef, binding.CapabilityContractHash) {
		return invalidRequest(field, "binding capability or contract owner differs from the runtime target authority")
	}
	for _, nodeRef := range binding.TargetNodeRefs {
		if !containsSorted(target.NodeRefs, nodeRef) {
			return invalidRequest(field, "binding node is outside the exact runtime target")
		}
		if previousSite, exists := nodeSites[nodeRef]; exists && previousSite != binding.SiteRef {
			return invalidRequest(field, "one target node is assigned to multiple Home Sites")
		}
		nodeSites[nodeRef] = binding.SiteRef
		coveredNodes[nodeRef] = struct{}{}
	}
	return nil
}

func containsAccessCapability(values []AccessCapability, ref, contractHash string) bool {
	index := sort.Search(len(values), func(index int) bool { return values[index].Ref >= ref })
	return index < len(values) && values[index].Ref == ref && values[index].ContractHash == contractHash
}

func exactCoveredCapabilities(values []AccessCapability, covered map[string]struct{}) bool {
	if len(values) != len(covered) {
		return false
	}
	for _, value := range values {
		if _, exists := covered[value.Ref]; !exists {
			return false
		}
	}
	return true
}

// ComputeAccessBindingProjectionHash returns the canonical content address of
// the complete shared projection. BindingHash remains the upstream StackKits
// authority hash; ProjectionHash additionally binds the runtime owner and
// target-node projection used at this adapter boundary.
func ComputeAccessBindingProjectionHash(input AccessBinding) (string, error) {
	projection := input
	projection.ProjectionHash = ""
	projection.TargetNodeRefs = append([]string(nil), input.TargetNodeRefs...)
	sort.Strings(projection.TargetNodeRefs)
	data, err := canonicalJSON(projection)
	if err != nil {
		return "", wrapError(ErrorInvalidRequest, "access_binding", "canonicalize access binding projection", err)
	}
	return hashBytes(data), nil
}

func validateBackupTargetBindings(bindings []BackupTargetBinding) error {
	if len(bindings) > MaxBackupTargetBindings {
		return invalidRequest("backup_target_bindings", "must contain at most %d bindings", MaxBackupTargetBindings)
	}
	seenIDs := make(map[string]struct{}, len(bindings))
	seenScopes := make(map[string]struct{}, len(bindings))
	for index, binding := range bindings {
		prefix := fmt.Sprintf("backup_target_bindings[%d]", index)
		if err := validateBackupTargetBindingAuthority(prefix, binding); err != nil {
			return err
		}
		if err := validateBackupTargetBindingValidity(prefix, binding); err != nil {
			return err
		}
		if _, duplicate := seenIDs[binding.ID]; duplicate {
			return invalidRequest(prefix+".id", "duplicate backup-target binding id")
		}
		seenIDs[binding.ID] = struct{}{}
		scope := binding.SiteRef + "\x00" + binding.CapabilityRef + "\x00" + strings.Join(binding.TargetNodeRefs, "\x00")
		if _, duplicate := seenScopes[scope]; duplicate {
			return invalidRequest(prefix, "duplicate Cloud Site/capability/node backup-target authority")
		}
		seenScopes[scope] = struct{}{}
	}
	return nil
}

func validateBackupTargetBindingAuthority(prefix string, binding BackupTargetBinding) error {
	for _, field := range []struct{ name, value string }{
		{"id", binding.ID}, {"runtime_requirement_id", binding.RuntimeRequirementID},
		{"stack_id", binding.StackID}, {fieldSiteRef, binding.SiteRef},
		{"contract_owner_ref", binding.ContractOwnerRef},
	} {
		if err := validateToken(prefix+"."+field.name, field.value); err != nil {
			return err
		}
	}
	if binding.Kind != "backup-target" {
		return invalidRequest(prefix+".kind", "must be backup-target")
	}
	if binding.CapabilityRef != "offsite-object-backup" {
		return invalidRequest(prefix+".capability_ref", "must be the closed Cloud backup-target capability")
	}
	for _, field := range []struct{ name, value string }{
		{"capability_contract_hash", binding.CapabilityContractHash}, {fieldRequirementsHash, binding.RequirementsHash},
		{"binding_hash", binding.BindingHash}, {"candidate_digest", binding.CandidateDigest}, {"spec_hash", binding.SpecHash},
		{"projection_hash", binding.ProjectionHash},
	} {
		if !validDigest(field.value) {
			return invalidRequest(prefix+"."+field.name, "must be a canonical SHA-256 digest")
		}
	}
	if !backupTargetBindingRefPattern.MatchString(binding.BindingRef) {
		return invalidRequest(prefix+".binding_ref", "must be an opaque backup-target binding ref")
	}
	if !backupTargetRefPattern.MatchString(binding.BackupTargetRef) {
		return invalidRequest(prefix+".backup_target_ref", "must be an opaque backup-target ref")
	}
	if !backupCustodyAttestationRefPattern.MatchString(binding.CustodyAttestationRef) {
		return invalidRequest(prefix+".custody_attestation_ref", "must be an opaque backup custody-attestation ref")
	}
	if !semanticVersionPattern.MatchString(binding.StackKitsVersion) {
		return invalidRequest(prefix+".stackkits_version", "must be a semantic version")
	}
	if err := validateRefList(prefix+".target_node_refs", binding.TargetNodeRefs, 1, MaxNodeRefsPerTarget); err != nil {
		return err
	}
	return nil
}

func validateBackupTargetBindingValidity(prefix string, binding BackupTargetBinding) error {
	if validityErr := validateExternalBindingWindow(prefix, binding.IssuedAt, binding.ValidUntil, MaxBackupTargetBindingValidity); validityErr != nil {
		return validityErr
	}
	wantProjectionHash, err := ComputeBackupTargetBindingProjectionHash(binding)
	if err != nil {
		return invalidRequest(prefix+".projection_hash", "cannot derive canonical backup-target projection")
	}
	if binding.ProjectionHash != wantProjectionHash {
		return invalidRequest(prefix+".projection_hash", "does not match the complete canonical backup-target projection")
	}
	return nil
}

func validateBackupTargetBindingClosure(targets []RuntimeTarget, bindings []BackupTargetBinding) error {
	available := make(map[string]BackupTargetBinding, len(bindings))
	referenceCount := make(map[string]int, len(bindings))
	for _, binding := range bindings {
		available[binding.ID] = binding
	}
	for targetIndex, target := range targets {
		if len(target.BackupTargetBindingRefs) == 0 {
			continue
		}
		if len(target.SiteRefs) != 1 {
			return invalidRequest(fmt.Sprintf("runtime_targets[%d].site_refs", targetIndex), "backup-target-bound runtime target must belong to exactly one Site")
		}
		coveredSites := map[string]struct{}{}
		coveredNodes := map[string]struct{}{}
		coveredCapabilities := map[string]struct{}{}
		for refIndex, ref := range target.BackupTargetBindingRefs {
			binding, exists := available[ref]
			if !exists {
				return invalidRequest(fmt.Sprintf("runtime_targets[%d].backup_target_binding_refs[%d]", targetIndex, refIndex), "references absent backup-target binding %q", ref)
			}
			if err := validateBackupTargetBindingTarget(fmt.Sprintf("runtime_targets[%d].backup_target_binding_refs[%d]", targetIndex, refIndex), target, binding, coveredNodes); err != nil {
				return err
			}
			coveredSites[binding.SiteRef] = struct{}{}
			coveredCapabilities[binding.CapabilityRef] = struct{}{}
			referenceCount[ref]++
		}
		if !exactCoveredRefs(target.SiteRefs, coveredSites) ||
			!exactCoveredRefs(target.NodeRefs, coveredNodes) ||
			!exactCoveredCapabilities(target.BackupTargetCapabilities, coveredCapabilities) {
			return invalidRequest(fmt.Sprintf("runtime_targets[%d].backup_target_binding_refs", targetIndex), "bindings must exactly cover the target Site, node, and capability sets")
		}
	}
	for _, binding := range bindings {
		if referenceCount[binding.ID] != 1 {
			return invalidRequest("backup_target_bindings."+binding.ID, "must be referenced by exactly one runtime target; got %d", referenceCount[binding.ID])
		}
	}
	return nil
}

// ComputeBackupTargetBindingProjectionHash returns the canonical content
// address of the complete shared projection. BindingHash remains the upstream
// StackKits authority hash.
func validateBackupTargetBindingTarget(field string, target RuntimeTarget, binding BackupTargetBinding, coveredNodes map[string]struct{}) error {
	if binding.RuntimeRequirementID != target.RequirementID ||
		binding.ContractOwnerRef != target.ProviderRef ||
		!containsAccessCapability(target.BackupTargetCapabilities, binding.CapabilityRef, binding.CapabilityContractHash) {
		return invalidRequest(field, "binding capability, requirement, or contract owner differs from the runtime target authority")
	}
	if !containsSorted(target.SiteRefs, binding.SiteRef) {
		return invalidRequest(field, "binding Site is outside the exact runtime target")
	}
	for _, nodeRef := range binding.TargetNodeRefs {
		if !containsSorted(target.NodeRefs, nodeRef) {
			return invalidRequest(field, "binding node is outside the exact runtime target")
		}
		coveredNodes[nodeRef] = struct{}{}
	}
	return nil
}

func ComputeBackupTargetBindingProjectionHash(input BackupTargetBinding) (string, error) {
	projection := input
	projection.ProjectionHash = ""
	projection.TargetNodeRefs = append([]string(nil), input.TargetNodeRefs...)
	sort.Strings(projection.TargetNodeRefs)
	data, err := canonicalJSON(projection)
	if err != nil {
		return "", wrapError(ErrorInvalidRequest, "backup_target_binding", "canonicalize backup-target binding projection", err)
	}
	return hashBytes(data), nil
}

func validateAccessBindingFreshness(bindings []AccessBinding, authorizationTime string, at time.Time) error {
	if at.IsZero() || at.Location() != time.UTC {
		return invalidRequest("access_bindings", "execution time must be a non-zero UTC instant")
	}
	if len(bindings) == 0 {
		if authorizationTime != "" {
			return invalidRequest("authorization_time", "must be absent without access bindings")
		}
		return nil
	}
	boundAt, err := canonicalUTCTimestamp(authorizationTime)
	if err != nil || !boundAt.Equal(at) {
		return invalidRequest("authorization_time", "must equal the exact invocation instant")
	}
	for index, binding := range bindings {
		issuedAt, err := canonicalUTCTimestamp(binding.IssuedAt)
		if err != nil {
			return invalidRequest(fmt.Sprintf("access_bindings[%d].issued_at", index), "must be a canonical RFC3339 UTC timestamp")
		}
		validUntil, err := canonicalUTCTimestamp(binding.ValidUntil)
		if err != nil {
			return invalidRequest(fmt.Sprintf("access_bindings[%d].valid_until", index), "must be a canonical RFC3339 UTC timestamp")
		}
		if at.Before(issuedAt) || !at.Before(validUntil) {
			return invalidRequest(fmt.Sprintf("access_bindings[%d].valid_until", index), "binding is not fresh at executor invocation")
		}
	}
	return nil
}

func validateExternalBindingFreshness(accessBindings []AccessBinding, backupBindings []BackupTargetBinding, authorizationTime string, at time.Time) error {
	if len(accessBindings) == 0 && len(backupBindings) == 0 {
		return validateAccessBindingFreshness(nil, authorizationTime, at)
	}
	if err := validateAccessBindingFreshness(accessBindings, authorizationTime, at); err != nil && len(accessBindings) != 0 {
		return err
	}
	if at.IsZero() || at.Location() != time.UTC {
		return invalidRequest("backup_target_bindings", "execution time must be a non-zero UTC instant")
	}
	boundAt, err := canonicalUTCTimestamp(authorizationTime)
	if err != nil || !boundAt.Equal(at) {
		return invalidRequest("authorization_time", "must equal the exact invocation instant")
	}
	for index, binding := range backupBindings {
		issuedAt, err := canonicalUTCTimestamp(binding.IssuedAt)
		if err != nil {
			return invalidRequest(fmt.Sprintf("backup_target_bindings[%d].issued_at", index), "must be a canonical RFC3339 UTC timestamp")
		}
		validUntil, err := canonicalUTCTimestamp(binding.ValidUntil)
		if err != nil {
			return invalidRequest(fmt.Sprintf("backup_target_bindings[%d].valid_until", index), "must be a canonical RFC3339 UTC timestamp")
		}
		if at.Before(issuedAt) || !at.Before(validUntil) {
			return invalidRequest(fmt.Sprintf("backup_target_bindings[%d].valid_until", index), "binding is not fresh at executor invocation")
		}
	}
	return nil
}

func canonicalUTCTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("noncanonical UTC timestamp")
	}
	return parsed, nil
}

func validateExternalBindingWindow(prefix, issuedAtText, validUntilText string, maximum time.Duration) error {
	issuedAt, err := canonicalUTCTimestamp(issuedAtText)
	if err != nil {
		return invalidRequest(prefix+".issued_at", "must be a canonical RFC3339 UTC timestamp")
	}
	validUntil, err := canonicalUTCTimestamp(validUntilText)
	if err != nil {
		return invalidRequest(prefix+".valid_until", "must be a canonical RFC3339 UTC timestamp")
	}
	if !issuedAt.Before(validUntil) || validUntil.Sub(issuedAt) > maximum {
		return invalidRequest(prefix+".valid_until", "must be after issued_at with validity no greater than %s", maximum)
	}
	return nil
}
