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
	"unicode"
	"unicode/utf8"
)

const (
	artifactOwnerPlan           = "plan"
	artifactOwnerRenderInstance = "render-instance"
	fieldSiteRef                = "site_ref"
	fieldRequirementsHash       = "requirements_hash"
)

// ErrorCode is a stable machine-readable runtimeexecutor failure class.
type ErrorCode string

const (
	ErrorInvalidRequest   ErrorCode = "invalid_request"
	ErrorInvalidResult    ErrorCode = "invalid_result"
	ErrorIdentityMismatch ErrorCode = "identity_mismatch"
	ErrorSetMismatch      ErrorCode = "set_mismatch"
	ErrorCancelled        ErrorCode = "cancelled" //nolint:misspell // Published error-code wire value.
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
var backupTargetBindingRefPattern = regexp.MustCompile(`^backup-target-binding://sha256/[a-f0-9]{64}$`)
var backupTargetRefPattern = regexp.MustCompile(`^backup-target://sha256/[a-f0-9]{64}$`)
var backupCustodyAttestationRefPattern = regexp.MustCompile(`^backup-custody-attestation://sha256/[a-f0-9]{64}$`)

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
	for index := range request.BackupTargetBindings {
		request.BackupTargetBindings[index].ProjectionHash = ""
		projectionHash, err := ComputeBackupTargetBindingProjectionHash(request.BackupTargetBindings[index])
		if err != nil {
			return ExecutionRequest{}, err
		}
		request.BackupTargetBindings[index].ProjectionHash = projectionHash
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
	if err := request.validateAuthority(); err != nil {
		return err
	}
	if err := request.validateTopology(); err != nil {
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

func (request ExecutionRequest) validateAuthority() error {
	if request.APIVersion != APIVersion {
		return invalidRequest("api_version", "must be %q", APIVersion)
	}
	if err := validateIdentity(request.Executor); err != nil {
		return err
	}
	for _, field := range []struct{ name, value string }{
		{"plan_hash", request.PlanHash}, {"manifest_hash", request.ManifestHash},
		{"generation_receipt_hash", request.GenerationReceiptHash}, {fieldRequirementsHash, request.RequirementsHash},
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
	if len(request.AccessBindings) == 0 && len(request.BackupTargetBindings) == 0 {
		if request.AuthorizationTime != "" {
			return invalidRequest("authorization_time", "must be absent without expiring external bindings")
		}
	} else if _, err := canonicalUTCTimestamp(request.AuthorizationTime); err != nil {
		return invalidRequest("authorization_time", "must be a canonical RFC3339 UTC timestamp with expiring external bindings")
	}
	if len(request.HealthTargets) > MaxHealthTargets || len(request.Artifacts) > MaxArtifacts {
		return invalidRequest("request", "health target or artifact bound exceeded")
	}
	return nil
}

func (request ExecutionRequest) validateTopology() error {
	if err := validateRuntimeTargets(request.RuntimeTargets); err != nil {
		return err
	}
	if err := validateHealthTargets(request.HealthTargets); err != nil {
		return err
	}
	if err := validateHealthTargetClosure(request.RuntimeTargets, request.HealthTargets); err != nil {
		return err
	}
	if err := validateAccessBindings(request.AccessBindings); err != nil {
		return err
	}
	if err := validateBackupTargetBindings(request.BackupTargetBindings); err != nil {
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
	if err := validateBackupTargetBindingClosure(request.RuntimeTargets, request.BackupTargetBindings); err != nil {
		return err
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
		if err := validateHealthTargetPlacement(prefix, target); err != nil {
			return err
		}
		if err := validateHealthTargetProbe(prefix, target); err != nil {
			return err
		}
		if _, exists := seen[target.RequirementID]; exists {
			return invalidRequest(prefix, "duplicate health target")
		}
		seen[target.RequirementID] = struct{}{}
	}
	return nil
}

func validateHealthTargetPlacement(prefix string, target HealthTarget) error {
	for _, value := range []struct{ name, value string }{{"requirement_id", target.RequirementID}, {"source_ref", target.SourceRef}, {"phase", target.Phase}, {"kind", target.Kind}, {"target_kind", target.TargetKind}, {"target_ref", target.TargetRef}} {
		if err := validateToken(prefix+"."+value.name, value.value); err != nil {
			return err
		}
	}
	if !validDigest(target.ContractHash) {
		return invalidRequest(prefix+".contract_hash", "must be a canonical SHA-256 digest")
	}
	if target.RuntimeRequirementID != "" {
		if err := validateToken(prefix+".runtime_requirement_id", target.RuntimeRequirementID); err != nil {
			return err
		}
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
	return nil
}

func validateHealthTargetProbe(prefix string, target HealthTarget) error {
	if target.TargetKind == "route" {
		if target.RuntimeRequirementID == "" || target.RouteRef != target.TargetRef || target.BackendPoolRef == "" || target.Probe == nil {
			return invalidRequest(prefix, "route health requires exact runtime, route, backend-pool, and probe bindings")
		}
		if err := validateHealthProbe(prefix+".probe", target.Kind, *target.Probe); err != nil {
			return err
		}
	} else if target.Probe != nil {
		return invalidRequest(prefix+".probe", "is valid only for route health")
	}
	return nil
}

func validateHealthProbe(prefix, kind string, probe HealthProbe) error {
	if probe.Port < 1 || probe.Port > 65535 || probe.TimeoutSeconds < 1 || probe.TimeoutSeconds > 300 {
		return invalidRequest(prefix, "port or timeout is outside the closed probe bounds")
	}
	switch kind {
	case "http":
		return validateHTTPHealthProbe(prefix, probe)
	case "tcp":
		if probe.Protocol != "tcp" || probe.Method != "" || probe.FollowRedirects || probe.Path != "" || len(probe.ExpectedStatuses) != 0 {
			return invalidRequest(prefix, "TCP route probe must not carry HTTP fields")
		}
	default:
		return invalidRequest(prefix, "route probe kind must be http or tcp")
	}
	return nil
}

func validateHTTPHealthProbe(prefix string, probe HealthProbe) error {
	if probe.Protocol != "http" || probe.Method != "GET" || probe.FollowRedirects || !strings.HasPrefix(probe.Path, "/") || len(probe.ExpectedStatuses) == 0 {
		return invalidRequest(prefix, "HTTP route probe must be address-free HTTP GET with redirects disabled, an absolute path, and expected statuses")
	}
	if err := validateExpectedStatuses(prefix, probe.ExpectedStatuses); err != nil {
		return err
	}
	return nil
}

func validateExpectedStatuses(prefix string, statuses []int) error {
	seen := map[int]struct{}{}
	for _, status := range statuses {
		if status < 100 || status > 599 {
			return invalidRequest(prefix+".expected_statuses", "contains an invalid HTTP status")
		}
		if _, duplicate := seen[status]; duplicate {
			return invalidRequest(prefix+".expected_statuses", "must be unique")
		}
		seen[status] = struct{}{}
	}
	return nil
}

func validateHealthTargetClosure(runtimeTargets []RuntimeTarget, healthTargets []HealthTarget) error {
	byRequirement := make(map[string]RuntimeTarget, len(runtimeTargets))
	for _, target := range runtimeTargets {
		byRequirement[target.RequirementID] = target
	}
	for index, health := range healthTargets {
		if health.RuntimeRequirementID == "" {
			continue
		}
		target, exists := byRequirement[health.RuntimeRequirementID]
		if !exists || !equalStrings(health.SiteRefs, target.SiteRefs) || !equalStrings(health.NodeRefs, target.NodeRefs) {
			return invalidRequest(fmt.Sprintf("health_targets[%d].runtime_requirement_id", index), "does not bind the exact runtime placement")
		}
	}
	return nil
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
	if err := validateRuntimeOutcomes(runtime); err != nil {
		return err
	}
	return validateHealthOutcomes(health)
}

func validateRuntimeOutcomes(runtime []RuntimeOutcome) error {
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
	return nil
}

func validateHealthOutcomes(health []HealthOutcome) error {
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
		if request.RuntimeTargets[i].RuntimeAdapter != nil {
			sort.Strings(request.RuntimeTargets[i].RuntimeAdapter.ArtifactRefs)
			for agentIndex := range request.RuntimeTargets[i].RuntimeAdapter.Agents {
				sort.Strings(request.RuntimeTargets[i].RuntimeAdapter.Agents[agentIndex].ArtifactRefs)
			}
			sort.Slice(request.RuntimeTargets[i].RuntimeAdapter.Agents, func(left, right int) bool {
				return request.RuntimeTargets[i].RuntimeAdapter.Agents[left].ID < request.RuntimeTargets[i].RuntimeAdapter.Agents[right].ID
			})
		}
		sort.Strings(request.RuntimeTargets[i].AccessBindingRefs)
		sort.Slice(request.RuntimeTargets[i].AccessCapabilities, func(left, right int) bool {
			return request.RuntimeTargets[i].AccessCapabilities[left].Ref < request.RuntimeTargets[i].AccessCapabilities[right].Ref
		})
		sort.Strings(request.RuntimeTargets[i].BackupTargetBindingRefs)
		sort.Slice(request.RuntimeTargets[i].BackupTargetCapabilities, func(left, right int) bool {
			return request.RuntimeTargets[i].BackupTargetCapabilities[left].Ref < request.RuntimeTargets[i].BackupTargetCapabilities[right].Ref
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
		if request.HealthTargets[i].Probe != nil {
			sort.Ints(request.HealthTargets[i].Probe.ExpectedStatuses)
		}
	}
	for i := range request.Artifacts {
		if request.Artifacts[i].ExecutionClass == "" {
			if request.Artifacts[i].OwnerKind == artifactOwnerPlan {
				request.Artifacts[i].ExecutionClass = ArtifactExecutionClassPlan
			} else {
				request.Artifacts[i].ExecutionClass = ArtifactExecutionClassExecutable
			}
		}
		sort.Strings(request.Artifacts[i].SiteRefs)
		sort.Strings(request.Artifacts[i].NodeRefs)
	}
	for i := range request.AccessBindings {
		sort.Strings(request.AccessBindings[i].TargetNodeRefs)
	}
	for i := range request.BackupTargetBindings {
		sort.Strings(request.BackupTargetBindings[i].TargetNodeRefs)
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
	sort.Slice(request.BackupTargetBindings, func(i, j int) bool { return request.BackupTargetBindings[i].ID < request.BackupTargetBindings[j].ID })
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
