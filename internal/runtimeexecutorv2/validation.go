package runtimeexecutor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrorCode is a stable machine-readable runtimeexecutor failure class.
type ErrorCode string

const (
	ErrorInvalidRequest   ErrorCode = "invalid_request"
	ErrorInvalidResult    ErrorCode = "invalid_result"
	ErrorIdentityMismatch ErrorCode = "identity_mismatch"
	ErrorSetMismatch      ErrorCode = "set_mismatch"
	ErrorCancelled        ErrorCode = "cancelled"
	ErrorExecutorFailed   ErrorCode = "executor_failed"
	ErrorExecutorPanic    ErrorCode = "executor_panic"
)

// Error reports a stable code and field without exposing adapter payloads.
type Error struct {
	Code    ErrorCode
	Field   string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Field == "" {
		return "runtimeexecutor: " + e.Message
	}
	return fmt.Sprintf("runtimeexecutor: %s: %s", e.Field, e.Message)
}

// Unwrap exposes only the originating Go error, when one exists.
func (e *Error) Unwrap() error { return e.Err }

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$`)
var opaqueRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,511}$`)
var semanticVersionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
var homeAccessBindingRefPattern = regexp.MustCompile(`^home-access-binding://sha256/[a-f0-9]{64}$`)
var homeAccessFabricRefPattern = regexp.MustCompile(`^home-access-fabric://sha256/[a-f0-9]{64}$`)

// SealRequest returns a canonical defensive copy with derived artifact-set and
// request digests. The input is never mutated.
func SealRequest(input ExecutionRequest) (ExecutionRequest, error) {
	request := CloneExecutionRequest(input)
	if request.APIVersion == "" {
		request.APIVersion = APIVersion
	}
	canonicalizeRequest(&request)
	for index := range request.AccessBindings {
		request.AccessBindings[index].ProjectionHash = ""
		projectionHash, err := ComputeAccessBindingProjectionHash(request.AccessBindings[index])
		if err != nil {
			return ExecutionRequest{}, err
		}
		request.AccessBindings[index].ProjectionHash = projectionHash
	}
	request.ArtifactSetHash = ""
	request.RequestDigest = ""
	artifactSetHash, err := ComputeArtifactSetHash(request.Artifacts)
	if err != nil {
		return ExecutionRequest{}, err
	}
	request.ArtifactSetHash = artifactSetHash
	digest, err := computeRequestDigest(request)
	if err != nil {
		return ExecutionRequest{}, err
	}
	request.RequestDigest = digest
	if err := request.Validate(); err != nil {
		return ExecutionRequest{}, err
	}
	return request, nil
}

// Validate rejects noncanonical, incomplete, substituted, or secret-bearing
// execution requests.
func (request ExecutionRequest) Validate() error {
	if request.APIVersion != APIVersion {
		return invalidRequest("api_version", "must be %q", APIVersion)
	}
	if err := validateIdentity(request.Executor); err != nil {
		return err
	}
	for _, field := range []struct{ name, value string }{
		{"plan_hash", request.PlanHash}, {"manifest_hash", request.ManifestHash},
		{"generation_receipt_hash", request.GenerationReceiptHash}, {"requirements_hash", request.RequirementsHash},
		{"evidence_bundle_hash", request.EvidenceBundleHash}, {"artifact_set_hash", request.ArtifactSetHash},
		{"request_digest", request.RequestDigest},
	} {
		if !validDigest(field.value) {
			return invalidRequest(field.name, "must be a canonical SHA-256 digest")
		}
	}
	if len(request.RuntimeTargets) == 0 || len(request.RuntimeTargets) > MaxRuntimeTargets {
		return invalidRequest("runtime_targets", "must contain 1..%d targets", MaxRuntimeTargets)
	}
	if len(request.AccessBindings) == 0 {
		if request.AuthorizationTime != "" {
			return invalidRequest("authorization_time", "must be absent without access bindings")
		}
	} else if _, err := canonicalUTCTimestamp(request.AuthorizationTime); err != nil {
		return invalidRequest("authorization_time", "must be a canonical RFC3339 UTC timestamp with access bindings")
	}
	if len(request.HealthTargets) > MaxHealthTargets || len(request.Artifacts) > MaxArtifacts {
		return invalidRequest("request", "health target or artifact bound exceeded")
	}
	if err := validateRuntimeTargets(request.RuntimeTargets); err != nil {
		return err
	}
	if err := validateHealthTargets(request.HealthTargets); err != nil {
		return err
	}
	if err := validateAccessBindings(request.AccessBindings); err != nil {
		return err
	}
	if err := validateArtifacts(request.Artifacts); err != nil {
		return err
	}
	if err := validateArtifactClosure(request.RuntimeTargets, request.Artifacts, request.PlanHash); err != nil {
		return err
	}
	if err := validateAccessBindingClosure(request.RuntimeTargets, request.AccessBindings); err != nil {
		return err
	}
	canonical := CloneExecutionRequest(request)
	canonicalizeRequest(&canonical)
	if !equalRequest(request, canonical) {
		return invalidRequest("request", "must be in canonical sorted form")
	}
	artifactSetHash, err := ComputeArtifactSetHash(request.Artifacts)
	if err != nil {
		return err
	}
	if artifactSetHash != request.ArtifactSetHash {
		return invalidRequest("artifact_set_hash", "does not match exact artifacts")
	}
	digest, err := computeRequestDigest(request)
	if err != nil {
		return err
	}
	if digest != request.RequestDigest {
		return invalidRequest("request_digest", "does not match exact request")
	}
	return nil
}

// Validate checks a result's canonical digest and closed outcome vocabulary.
// Exact target-set matching is performed by Invoke against its request.
func (result ExecutionResult) Validate() error {
	if result.APIVersion != APIVersion || !validDigest(result.ResultDigest) {
		return invalidResult("result", "invalid API version or result digest")
	}
	if err := validateIdentity(result.Executor); err != nil {
		return invalidResult("executor", "%s", err.Error())
	}
	for _, value := range []string{result.PlanHash, result.ManifestHash, result.GenerationReceiptHash, result.RequirementsHash, result.EvidenceBundleHash, result.ArtifactSetHash, result.RequestDigest} {
		if !validDigest(value) {
			return invalidResult("result", "all authority hashes must be canonical SHA-256 digests")
		}
	}
	if err := validateOutcomeShape(result.Runtime, result.Health); err != nil {
		return err
	}
	digest, err := computeResultDigest(result)
	if err != nil {
		return err
	}
	if digest != result.ResultDigest {
		return invalidResult("result_digest", "does not match exact result")
	}
	return nil
}

func validateIdentity(identity ExecutorIdentity) error {
	if err := validateToken("executor.id", identity.ID); err != nil {
		return err
	}
	if err := validateToken("executor.version", identity.Version); err != nil {
		return err
	}
	if !validDigest(identity.Digest) {
		return invalidRequest("executor.digest", "must be a canonical SHA-256 digest")
	}
	return nil
}

func validateRuntimeTargets(targets []RuntimeTarget) error {
	seen := map[string]struct{}{}
	for i, target := range targets {
		prefix := fmt.Sprintf("runtime_targets[%d]", i)
		for _, field := range []struct{ name, value string }{
			{"requirement_id", target.RequirementID}, {"owner_kind", target.OwnerKind}, {"owner_ref", target.OwnerRef},
			{"provider_ref", target.ProviderRef}, {"runtime_kind", target.RuntimeKind}, {"runtime_delivery", target.RuntimeDelivery},
			{"instance_ref", target.InstanceRef},
		} {
			if err := validateToken(prefix+"."+field.name, field.value); err != nil {
				return err
			}
		}
		switch target.OwnerKind {
		case "module":
			if target.OwnerVersion != "" {
				return invalidRequest(prefix+".owner_version", "module runtime target must not carry provider-owner version authority")
			}
		case "provider-owner":
			if err := validateToken(prefix+".owner_version", target.OwnerVersion); err != nil {
				return err
			}
		default:
			return invalidRequest(prefix+".owner_kind", "must be module or provider-owner")
		}
		for _, field := range []struct{ name, value string }{
			{"owner_contract_hash", target.OwnerContractHash}, {"provider_contract_hash", target.ProviderContractHash},
		} {
			if !validDigest(field.value) {
				return invalidRequest(prefix+"."+field.name, "must be a canonical SHA-256 digest")
			}
		}
		if (target.ModuleRef == "") != (target.ModuleContractHash == "") || (target.UnitRef == "") != (target.UnitContractHash == "") {
			return invalidRequest(prefix, "module/unit refs and contract hashes must be paired")
		}
		if target.ModuleRef != "" {
			if err := validateToken(prefix+".module_ref", target.ModuleRef); err != nil {
				return err
			}
			if !validDigest(target.ModuleContractHash) {
				return invalidRequest(prefix+".module_contract_hash", "must be a canonical SHA-256 digest")
			}
		}
		if target.UnitRef != "" {
			if err := validateToken(prefix+".unit_ref", target.UnitRef); err != nil {
				return err
			}
			if !validDigest(target.UnitContractHash) {
				return invalidRequest(prefix+".unit_contract_hash", "must be a canonical SHA-256 digest")
			}
		}
		if target.RuntimeEngine != "" {
			if err := validateToken(prefix+".runtime_engine", target.RuntimeEngine); err != nil {
				return err
			}
		}
		if target.ExecutionChannelRef != "" {
			if err := validateToken(prefix+".execution_channel_ref", target.ExecutionChannelRef); err != nil {
				return err
			}
		}
		if target.WorkloadRef != "" {
			if err := validateToken(prefix+".workload_ref", target.WorkloadRef); err != nil {
				return err
			}
		}
		if target.ImageRef != "" {
			if err := validateOpaqueRef(prefix+".image_ref", target.ImageRef); err != nil {
				return err
			}
		}
		if (target.ImageRef == "") != (target.ImageDigest == "") {
			return invalidRequest(prefix+".image", "image ref and digest must appear together")
		}
		if target.ImageDigest != "" && !validDigest(target.ImageDigest) {
			return invalidRequest(prefix+".image_digest", "must be a canonical SHA-256 digest")
		}
		if target.UnitRef != "" && target.ModuleRef == "" {
			return invalidRequest(prefix, "unit authority requires module authority")
		}
		if err := validateRefList(prefix+".site_refs", target.SiteRefs, 1, MaxSiteRefsPerTarget); err != nil {
			return err
		}
		if err := validateRefList(prefix+".node_refs", target.NodeRefs, 1, MaxNodeRefsPerTarget); err != nil {
			return err
		}
		if err := validateDaemonTargets(prefix+".daemon_bindings", target.DaemonBindings); err != nil {
			return err
		}
		for daemonIndex, daemon := range target.DaemonBindings {
			if target.RuntimeEngine == "" || daemon.Engine != target.RuntimeEngine {
				return invalidRequest(fmt.Sprintf("%s.daemon_bindings[%d].engine", prefix, daemonIndex), "must match the governed runtime engine")
			}
		}
		minimumArtifacts := 0
		if target.ModuleRef != "" || target.UnitRef != "" {
			minimumArtifacts = 1
		}
		if err := validateRefList(prefix+".artifact_refs", target.ArtifactRefs, minimumArtifacts, MaxArtifacts); err != nil {
			return err
		}
		if err := validateRefList(prefix+".access_binding_refs", target.AccessBindingRefs, 0, MaxAccessBindings); err != nil {
			return err
		}
		if err := validateAccessCapabilities(prefix+".access_capabilities", target.AccessCapabilities); err != nil {
			return err
		}
		if (len(target.AccessBindingRefs) == 0) != (len(target.AccessCapabilities) == 0) {
			return invalidRequest(prefix, "access bindings and capability authority must appear together")
		}
		key := target.RequirementID + "\x00" + target.InstanceRef
		if _, exists := seen[key]; exists {
			return invalidRequest(prefix, "duplicate governed runtime target")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateAccessCapabilities(field string, capabilities []AccessCapability) error {
	if len(capabilities) > MaxAccessBindings {
		return invalidRequest(field, "must contain at most %d capabilities", MaxAccessBindings)
	}
	for index, capability := range capabilities {
		prefix := fmt.Sprintf("%s[%d]", field, index)
		if capability.Ref != "private-remote-access" && capability.Ref != "public-publish-egress" {
			return invalidRequest(prefix+".ref", "must be a closed Home access capability")
		}
		if !validDigest(capability.ContractHash) {
			return invalidRequest(prefix+".contract_hash", "must be a canonical SHA-256 digest")
		}
		if index > 0 && capabilities[index-1].Ref >= capability.Ref {
			return invalidRequest(field, "must be sorted and unique")
		}
	}
	return nil
}

func validateAccessBindings(bindings []AccessBinding) error {
	if len(bindings) > MaxAccessBindings {
		return invalidRequest("access_bindings", "must contain at most %d bindings", MaxAccessBindings)
	}
	seenIDs := make(map[string]struct{}, len(bindings))
	seenScopes := make(map[string]struct{}, len(bindings))
	for index, binding := range bindings {
		prefix := fmt.Sprintf("access_bindings[%d]", index)
		for _, field := range []struct{ name, value string }{
			{"id", binding.ID}, {"runtime_requirement_id", binding.RuntimeRequirementID},
			{"stack_id", binding.StackID}, {"site_ref", binding.SiteRef},
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
			{"capability_contract_hash", binding.CapabilityContractHash}, {"requirements_hash", binding.RequirementsHash},
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
		issuedAt, err := canonicalUTCTimestamp(binding.IssuedAt)
		if err != nil {
			return invalidRequest(prefix+".issued_at", "must be a canonical RFC3339 UTC timestamp")
		}
		validUntil, err := canonicalUTCTimestamp(binding.ValidUntil)
		if err != nil {
			return invalidRequest(prefix+".valid_until", "must be a canonical RFC3339 UTC timestamp")
		}
		if !issuedAt.Before(validUntil) || validUntil.Sub(issuedAt) > MaxAccessBindingValidity {
			return invalidRequest(prefix+".valid_until", "must be after issued_at with validity no greater than %s", MaxAccessBindingValidity)
		}
		wantProjectionHash, err := ComputeAccessBindingProjectionHash(binding)
		if err != nil {
			return invalidRequest(prefix+".projection_hash", "cannot derive canonical access projection")
		}
		if binding.ProjectionHash != wantProjectionHash {
			return invalidRequest(prefix+".projection_hash", "does not match the complete canonical access projection")
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
			if !containsSorted(target.SiteRefs, binding.SiteRef) {
				return invalidRequest(fmt.Sprintf("runtime_targets[%d].access_binding_refs[%d]", targetIndex, refIndex), "binding Site is outside the exact runtime target")
			}
			if binding.RuntimeRequirementID != target.RequirementID {
				return invalidRequest(fmt.Sprintf("runtime_targets[%d].access_binding_refs[%d]", targetIndex, refIndex), "binding is owned by a different runtime requirement")
			}
			if binding.ContractOwnerRef != target.ProviderRef || !containsAccessCapability(target.AccessCapabilities, binding.CapabilityRef, binding.CapabilityContractHash) {
				return invalidRequest(fmt.Sprintf("runtime_targets[%d].access_binding_refs[%d]", targetIndex, refIndex), "binding capability or contract owner differs from the runtime target authority")
			}
			for _, nodeRef := range binding.TargetNodeRefs {
				if !containsSorted(target.NodeRefs, nodeRef) {
					return invalidRequest(fmt.Sprintf("runtime_targets[%d].access_binding_refs[%d]", targetIndex, refIndex), "binding node is outside the exact runtime target")
				}
				if previousSite, exists := nodeSites[nodeRef]; exists && previousSite != binding.SiteRef {
					return invalidRequest(fmt.Sprintf("runtime_targets[%d].access_binding_refs[%d]", targetIndex, refIndex), "one target node is assigned to multiple Home Sites")
				}
				nodeSites[nodeRef] = binding.SiteRef
				coveredNodes[nodeRef] = struct{}{}
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

func canonicalUTCTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("noncanonical UTC timestamp")
	}
	return parsed, nil
}

func containsSorted(values []string, candidate string) bool {
	index := sort.SearchStrings(values, candidate)
	return index < len(values) && values[index] == candidate
}

func exactCoveredRefs(values []string, covered map[string]struct{}) bool {
	if len(values) != len(covered) {
		return false
	}
	for _, value := range values {
		if _, exists := covered[value]; !exists {
			return false
		}
	}
	return true
}

func validateHealthTargets(targets []HealthTarget) error {
	seen := map[string]struct{}{}
	for i, target := range targets {
		prefix := fmt.Sprintf("health_targets[%d]", i)
		for _, value := range []struct{ name, value string }{{"requirement_id", target.RequirementID}, {"source_ref", target.SourceRef}, {"phase", target.Phase}, {"kind", target.Kind}, {"target_kind", target.TargetKind}, {"target_ref", target.TargetRef}} {
			if err := validateToken(prefix+"."+value.name, value.value); err != nil {
				return err
			}
		}
		if !validDigest(target.ContractHash) {
			return invalidRequest(prefix+".contract_hash", "must be a canonical SHA-256 digest")
		}
		if target.RouteRef != "" {
			if err := validateToken(prefix+".route_ref", target.RouteRef); err != nil {
				return err
			}
		}
		if target.BackendPoolRef != "" {
			if err := validateToken(prefix+".backend_pool_ref", target.BackendPoolRef); err != nil {
				return err
			}
		}
		if err := validateRefList(prefix+".site_refs", target.SiteRefs, 0, MaxSiteRefsPerTarget); err != nil {
			return err
		}
		if err := validateRefList(prefix+".node_refs", target.NodeRefs, 0, MaxNodeRefsPerTarget); err != nil {
			return err
		}
		if _, exists := seen[target.RequirementID]; exists {
			return invalidRequest(prefix, "duplicate health target")
		}
		seen[target.RequirementID] = struct{}{}
	}
	return nil
}

func validateArtifacts(artifacts []Artifact) error {
	seen := map[string]struct{}{}
	total := 0
	for i, artifact := range artifacts {
		prefix := fmt.Sprintf("artifacts[%d]", i)
		for _, value := range []struct{ name, value string }{{"id", artifact.ID}, {"kind", artifact.Kind}, {"format", artifact.Format}, {"mode", artifact.Mode}} {
			if err := validateToken(prefix+"."+value.name, value.value); err != nil {
				return err
			}
		}
		if !validDigest(artifact.OwnerContractHash) {
			return invalidRequest(prefix+".owner_contract_hash", "must be a canonical SHA-256 digest")
		}
		if artifact.OwnerKind != "plan" && artifact.OwnerKind != "render-instance" {
			return invalidRequest(prefix+".owner_kind", "must be plan or render-instance")
		}
		if artifact.OwnerKind == "plan" {
			if !validDigest(artifact.OwnerRef) {
				return invalidRequest(prefix+".owner_ref", "plan owner ref must be a canonical SHA-256 plan digest")
			}
		} else if err := validateToken(prefix+".owner_ref", artifact.OwnerRef); err != nil {
			return err
		}
		if (artifact.ProviderRef == "") != (artifact.ProviderContractHash == "") {
			return invalidRequest(prefix, "provider ref and contract hash must be paired")
		}
		if artifact.ProviderRef != "" {
			if err := validateToken(prefix+".provider_ref", artifact.ProviderRef); err != nil {
				return err
			}
			if !validDigest(artifact.ProviderContractHash) {
				return invalidRequest(prefix+".provider_contract_hash", "must be a canonical SHA-256 digest")
			}
		}
		if (artifact.ModuleRef == "") != (artifact.ModuleContractHash == "") || (artifact.UnitRef == "") != (artifact.UnitContractHash == "") {
			return invalidRequest(prefix, "module/unit refs and contract hashes must be paired")
		}
		if artifact.ModuleRef != "" {
			if err := validateToken(prefix+".module_ref", artifact.ModuleRef); err != nil {
				return err
			}
			if !validDigest(artifact.ModuleContractHash) {
				return invalidRequest(prefix+".module_contract_hash", "must be a canonical SHA-256 digest")
			}
		}
		if artifact.UnitRef != "" {
			if artifact.ModuleRef == "" {
				return invalidRequest(prefix, "unit authority requires module authority")
			}
			if err := validateToken(prefix+".unit_ref", artifact.UnitRef); err != nil {
				return err
			}
			if !validDigest(artifact.UnitContractHash) {
				return invalidRequest(prefix+".unit_contract_hash", "must be a canonical SHA-256 digest")
			}
		}
		if artifact.InstanceRef != "" {
			if artifact.UnitRef == "" {
				return invalidRequest(prefix, "instance authority requires unit authority")
			}
			if err := validateToken(prefix+".instance_ref", artifact.InstanceRef); err != nil {
				return err
			}
		}
		minimumPlacement := 0
		if artifact.OwnerKind == "render-instance" {
			minimumPlacement = 1
		}
		if err := validateRefList(prefix+".site_refs", artifact.SiteRefs, minimumPlacement, MaxSiteRefsPerTarget); err != nil {
			return err
		}
		if err := validateRefList(prefix+".node_refs", artifact.NodeRefs, minimumPlacement, MaxNodeRefsPerTarget); err != nil {
			return err
		}
		if artifact.OwnerKind == "plan" {
			if artifact.ProviderRef != "" || artifact.ProviderContractHash != "" || artifact.ModuleRef != "" || artifact.ModuleContractHash != "" || artifact.UnitRef != "" || artifact.UnitContractHash != "" || artifact.InstanceRef != "" || artifact.OutputRef != "" || len(artifact.SiteRefs) != 0 || len(artifact.NodeRefs) != 0 {
				return invalidRequest(prefix, "plan-owned artifact must not carry runtime owner, placement, or output authority")
			}
		} else {
			if artifact.ProviderRef == "" || artifact.ModuleRef == "" || artifact.UnitRef == "" || artifact.InstanceRef == "" || artifact.OutputRef == "" {
				return invalidRequest(prefix, "render-instance artifact requires full provider/module/unit/instance/output authority")
			}
			if err := validateToken(prefix+".output_ref", artifact.OutputRef); err != nil {
				return err
			}
		}
		if artifact.Content == nil || len(artifact.Content) > MaxArtifactBytes {
			return invalidRequest(prefix+".content", "must be present and no larger than %d bytes", MaxArtifactBytes)
		}
		total += len(artifact.Content)
		if total > MaxTotalArtifactBytes {
			return invalidRequest("artifacts", "total content exceeds %d bytes", MaxTotalArtifactBytes)
		}
		if hashBytes(artifact.Content) != artifact.Digest {
			return invalidRequest(prefix+".digest", "does not match content")
		}
		if _, exists := seen[artifact.ID]; exists {
			return invalidRequest(prefix+".id", "duplicate artifact")
		}
		seen[artifact.ID] = struct{}{}
	}
	return nil
}

func validateArtifactClosure(targets []RuntimeTarget, artifacts []Artifact, planHash string) error {
	available := make(map[string]Artifact, len(artifacts))
	referenceCount := make(map[string]int, len(artifacts))
	for _, artifact := range artifacts {
		available[artifact.ID] = artifact
		if artifact.OwnerKind == "plan" && (artifact.OwnerRef != planHash || artifact.OwnerContractHash != planHash) {
			return invalidRequest("artifacts."+artifact.ID, "plan-owned artifact owner ref and contract hash must both bind the exact plan hash")
		}
	}
	for targetIndex, target := range targets {
		for refIndex, ref := range target.ArtifactRefs {
			artifact, exists := available[ref]
			if !exists {
				return invalidRequest(fmt.Sprintf("runtime_targets[%d].artifact_refs[%d]", targetIndex, refIndex), "references absent artifact %q", ref)
			}
			if artifact.OwnerKind == "plan" {
				return invalidRequest(fmt.Sprintf("runtime_targets[%d].artifact_refs[%d]", targetIndex, refIndex), "plan-owned artifact %q must not be runtime-referenced", ref)
			}
			if !artifactMatchesRuntimeTarget(artifact, target) {
				return invalidRequest(fmt.Sprintf("runtime_targets[%d].artifact_refs[%d]", targetIndex, refIndex), "render-instance artifact %q identity does not exactly match runtime target", ref)
			}
			referenceCount[ref]++
		}
	}
	for _, artifact := range artifacts {
		if artifact.OwnerKind == "render-instance" && referenceCount[artifact.ID] != 1 {
			return invalidRequest("artifacts."+artifact.ID, "render-instance artifact must be referenced exactly once; got %d", referenceCount[artifact.ID])
		}
		if artifact.OwnerKind == "plan" && referenceCount[artifact.ID] != 0 {
			return invalidRequest("artifacts."+artifact.ID, "plan-owned artifact must not be runtime-referenced")
		}
	}
	return nil
}

func artifactMatchesRuntimeTarget(artifact Artifact, target RuntimeTarget) bool {
	return artifact.OwnerKind == "render-instance" && artifact.OwnerRef == target.InstanceRef && artifact.OwnerContractHash == target.UnitContractHash &&
		artifact.ProviderRef == target.ProviderRef && artifact.ProviderContractHash == target.ProviderContractHash &&
		artifact.ModuleRef == target.ModuleRef && artifact.ModuleContractHash == target.ModuleContractHash &&
		artifact.UnitRef == target.UnitRef && artifact.UnitContractHash == target.UnitContractHash && artifact.InstanceRef == target.InstanceRef &&
		equalStrings(artifact.SiteRefs, target.SiteRefs) && equalStrings(artifact.NodeRefs, target.NodeRefs)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateOutcomeShape(runtime []RuntimeOutcome, health []HealthOutcome) error {
	seenRuntime := map[string]struct{}{}
	for i, outcome := range runtime {
		prefix := fmt.Sprintf("runtime[%d]", i)
		if outcome.Status != RuntimeStatusApplied {
			return invalidResult(prefix+".status", "must be %q", RuntimeStatusApplied)
		}
		if err := validateToken(prefix+".requirement_id", outcome.RequirementID); err != nil {
			return invalidResult(prefix, "%s", err.Error())
		}
		if err := validateToken(prefix+".instance_ref", outcome.InstanceRef); err != nil {
			return invalidResult(prefix, "%s", err.Error())
		}
		if err := validateObservation(prefix, outcome.ObservationRef, outcome.ObservationDigest, "runtime-observation"); err != nil {
			return err
		}
		key := outcome.RequirementID + "\x00" + outcome.InstanceRef
		if i > 0 {
			previous := runtime[i-1].RequirementID + "\x00" + runtime[i-1].InstanceRef
			if previous >= key {
				return invalidResult("runtime", "must be sorted and unique")
			}
		}
		if _, exists := seenRuntime[key]; exists {
			return invalidResult(prefix, "duplicate runtime outcome")
		}
		seenRuntime[key] = struct{}{}
	}
	seenHealth := map[string]struct{}{}
	for i, outcome := range health {
		prefix := fmt.Sprintf("health[%d]", i)
		if outcome.Status != HealthStatusHealthy {
			return invalidResult(prefix+".status", "must be %q", HealthStatusHealthy)
		}
		if err := validateToken(prefix+".requirement_id", outcome.RequirementID); err != nil {
			return invalidResult(prefix, "%s", err.Error())
		}
		if err := validateToken(prefix+".target_ref", outcome.TargetRef); err != nil {
			return invalidResult(prefix, "%s", err.Error())
		}
		if err := validateObservation(prefix, outcome.ObservationRef, outcome.ObservationDigest, "health-observation"); err != nil {
			return err
		}
		if _, exists := seenHealth[outcome.RequirementID]; exists {
			return invalidResult(prefix, "duplicate health outcome")
		}
		if i > 0 && health[i-1].RequirementID >= outcome.RequirementID {
			return invalidResult("health", "must be sorted and unique")
		}
		seenHealth[outcome.RequirementID] = struct{}{}
	}
	return nil
}

func validateObservation(field, ref, digest, scheme string) error {
	parsed, err := url.Parse(ref)
	if err != nil || parsed.Scheme != scheme || parsed.Host == "" || parsed.Path == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return invalidResult(field+".observation_ref", "must be a safe opaque %s URI", scheme)
	}
	if !validDigest(digest) {
		return invalidResult(field+".observation_digest", "must be a canonical SHA-256 digest")
	}
	return nil
}

func validateRefList(field string, values []string, min, max int) error {
	if len(values) < min || len(values) > max {
		return invalidRequest(field, "must contain %d..%d refs", min, max)
	}
	for i, value := range values {
		if err := validateToken(fmt.Sprintf("%s[%d]", field, i), value); err != nil {
			return err
		}
		if i > 0 && values[i-1] >= value {
			return invalidRequest(field, "must be sorted and unique")
		}
	}
	return nil
}

func validateDaemonTargets(field string, values []DaemonTarget) error {
	if len(values) > MaxNodeRefsPerTarget {
		return invalidRequest(field, "must contain at most %d daemon bindings", MaxNodeRefsPerTarget)
	}
	for i, value := range values {
		prefix := fmt.Sprintf("%s[%d]", field, i)
		for _, item := range []struct{ name, value string }{{"ref", value.Ref}, {"instance_ref", value.InstanceRef}, {"engine", value.Engine}} {
			if err := validateToken(prefix+"."+item.name, item.value); err != nil {
				return err
			}
		}
		if err := validateSocketPath(prefix+".socket_path", value.SocketPath); err != nil {
			return err
		}
		if i > 0 {
			previous, current := values[i-1], value
			previousKey, currentKey := previous.Ref+"\x00"+previous.InstanceRef, current.Ref+"\x00"+current.InstanceRef
			if previousKey >= currentKey {
				return invalidRequest(field, "must be sorted and unique")
			}
		}
	}
	return nil
}

func validateSocketPath(field, value string) error {
	if len(value) > 4096 || !utf8.ValidString(value) || containsControl(value) || secretLikeRef(value) || !strings.HasPrefix(value, "/") || path.Clean(value) != value || strings.Contains(value, "\\") {
		return invalidRequest(field, "must be a canonical absolute runtime socket path")
	}
	return nil
}

func validateOpaqueRef(field, value string) error {
	if !utf8.ValidString(value) || !opaqueRefPattern.MatchString(value) || containsControl(value) || secretLikeRef(value) {
		return invalidRequest(field, "must be a canonical non-secret opaque ref")
	}
	return nil
}

func validateToken(field, value string) error {
	if !utf8.ValidString(value) || !tokenPattern.MatchString(value) || containsControl(value) || secretLikeRef(value) {
		return invalidRequest(field, "must be a canonical non-secret identifier")
	}
	return nil
}

func secretLikeRef(value string) bool {
	lower := strings.ToLower(value)
	for _, prefix := range []string{"secret://", "credential://", "password://", "token://", "bearer://", "private-key://"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func validDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func canonicalizeRequest(request *ExecutionRequest) {
	for i := range request.RuntimeTargets {
		sort.Strings(request.RuntimeTargets[i].SiteRefs)
		sort.Strings(request.RuntimeTargets[i].NodeRefs)
		sort.Strings(request.RuntimeTargets[i].ArtifactRefs)
		sort.Strings(request.RuntimeTargets[i].AccessBindingRefs)
		sort.Slice(request.RuntimeTargets[i].AccessCapabilities, func(left, right int) bool {
			return request.RuntimeTargets[i].AccessCapabilities[left].Ref < request.RuntimeTargets[i].AccessCapabilities[right].Ref
		})
		sort.Slice(request.RuntimeTargets[i].DaemonBindings, func(left, right int) bool {
			if request.RuntimeTargets[i].DaemonBindings[left].Ref == request.RuntimeTargets[i].DaemonBindings[right].Ref {
				return request.RuntimeTargets[i].DaemonBindings[left].InstanceRef < request.RuntimeTargets[i].DaemonBindings[right].InstanceRef
			}
			return request.RuntimeTargets[i].DaemonBindings[left].Ref < request.RuntimeTargets[i].DaemonBindings[right].Ref
		})
	}
	for i := range request.HealthTargets {
		sort.Strings(request.HealthTargets[i].SiteRefs)
		sort.Strings(request.HealthTargets[i].NodeRefs)
	}
	for i := range request.Artifacts {
		sort.Strings(request.Artifacts[i].SiteRefs)
		sort.Strings(request.Artifacts[i].NodeRefs)
	}
	for i := range request.AccessBindings {
		sort.Strings(request.AccessBindings[i].TargetNodeRefs)
	}
	sort.Slice(request.RuntimeTargets, func(i, j int) bool {
		if request.RuntimeTargets[i].RequirementID == request.RuntimeTargets[j].RequirementID {
			return request.RuntimeTargets[i].InstanceRef < request.RuntimeTargets[j].InstanceRef
		}
		return request.RuntimeTargets[i].RequirementID < request.RuntimeTargets[j].RequirementID
	})
	sort.Slice(request.HealthTargets, func(i, j int) bool {
		return request.HealthTargets[i].RequirementID < request.HealthTargets[j].RequirementID
	})
	sort.Slice(request.AccessBindings, func(i, j int) bool { return request.AccessBindings[i].ID < request.AccessBindings[j].ID })
	sort.Slice(request.Artifacts, func(i, j int) bool { return request.Artifacts[i].ID < request.Artifacts[j].ID })
}

func equalRequest(left, right ExecutionRequest) bool {
	leftJSON, leftErr := canonicalJSON(left)
	rightJSON, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func invalidRequest(field, format string, args ...any) error {
	return &Error{Code: ErrorInvalidRequest, Field: field, Message: fmt.Sprintf(format, args...)}
}
func invalidResult(field, format string, args ...any) error {
	return &Error{Code: ErrorInvalidResult, Field: field, Message: fmt.Sprintf(format, args...)}
}
func wrapError(code ErrorCode, field, message string, err error) error {
	return &Error{Code: code, Field: field, Message: message, Err: err}
}
