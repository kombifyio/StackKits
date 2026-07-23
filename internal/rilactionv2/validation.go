package rilaction

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kombifyio/stackkits/internal/referenceidv2"
)

const (
	MaxRequestBytes      = 64 * 1024
	MaxRequestValidity   = 5 * time.Minute
	MaxAuthorityValidity = 15 * time.Minute
	MaxInputs            = 16
	MaxGrantScopes       = 16
)

var (
	digestPattern     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	contractIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,127}$`)
	stableIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	opaqueRefPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}:[A-Za-z0-9][A-Za-z0-9._/-]{0,190}$`)
)

var forbiddenOpaqueSchemes = map[string]struct{}{
	"credential": {}, "doppler": {}, "key": {}, "password": {}, "secret": {}, "token": {}, "vault": {},
}

var forbiddenOperationClassTerms = []string{
	"command", "credential", "docker", "executor", "opentofu", "path", "provider", "secret", "shell", "ssh", "tofu", "transport",
}

// DecodeRequestAt performs strict duplicate/unknown/trailing rejection and
// validates freshness against one caller-supplied trusted UTC instant. The
// instant is an execution-side clock sample, never a request field.
func DecodeRequestAt(data []byte, now time.Time) (Request, error) {
	if len(data) == 0 || len(data) > MaxRequestBytes {
		return Request{}, fmt.Errorf("rilaction request size is invalid")
	}
	if err := rejectDuplicateOrTrailingJSON(data); err != nil {
		return Request{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, fmt.Errorf("rilaction request is invalid: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return Request{}, err
	}
	if err := ValidateRequestAt(request, now); err != nil {
		return Request{}, err
	}
	return request, nil
}

// ValidateRequestAt validates shape, authority windows, and request freshness
// at one trusted UTC instant immediately before admission or invocation.
func ValidateRequestAt(request Request, now time.Time) error {
	if err := ValidateRequestShape(request); err != nil {
		return err
	}
	if now.Location() != time.UTC {
		return invalid("clock", "must be UTC")
	}
	issuedAt, _ := parseCanonicalUTC(request.IssuedAt)
	validUntil, _ := parseCanonicalUTC(request.ValidUntil)
	approvalUntil, _ := parseCanonicalUTC(request.Approval.ValidUntil)
	grantUntil, _ := parseCanonicalUTC(request.Grant.ValidUntil)
	if now.Before(issuedAt) {
		return invalid("issued_at", "is in the future")
	}
	if !now.Before(validUntil) || !now.Before(approvalUntil) || !now.Before(grantUntil) {
		return invalid("valid_until", "authority is expired")
	}
	return nil
}

// ValidateRequestShape validates all deterministic closure without using a
// clock. Consumers must still call ValidateRequestAt immediately before use.
func ValidateRequestShape(request Request) error {
	if request.APIVersion != APIVersionV1Alpha1 {
		return invalid("api_version", "is unsupported")
	}
	for field, value := range map[string]string{
		"action_card_id": request.ActionCardID, "execution_id": request.ExecutionID, "trace_id": request.TraceID,
		"tenant_id": request.TenantID, "stack_id": request.StackID, "nonce": request.Nonce,
		"idempotency_key": request.IdempotencyKey,
	} {
		if err := validateStableID(field, value); err != nil {
			return err
		}
	}
	if len(request.Nonce) < 16 || len(request.IdempotencyKey) < 16 {
		return invalid("replay_identity", "nonce and idempotency_key must contain at least 16 characters")
	}
	if err := validateContractID("primitive.id", request.Primitive.ID); err != nil {
		return err
	}
	if err := validateDigest("primitive.contract_hash", request.Primitive.ContractHash); err != nil {
		return err
	}
	if err := validateContractID("primitive.operation_class", request.Primitive.OperationClass); err != nil {
		return err
	}
	for _, term := range forbiddenOperationClassTerms {
		if strings.Contains(request.Primitive.OperationClass, term) {
			return invalid("primitive.operation_class", "cannot select raw execution authority")
		}
	}
	if err := validateDigest("resolved_plan_hash", request.ResolvedPlanHash); err != nil {
		return err
	}
	if err := validateApproval(request.Approval); err != nil {
		return err
	}
	if err := validateGrant(request.Grant); err != nil {
		return err
	}
	if err := validateTarget(request.Target); err != nil {
		return err
	}
	if err := validateInputs(request.Inputs); err != nil {
		return err
	}
	if err := validateOpaqueRef("evidence_sink_ref", request.EvidenceSinkRef, "evidence"); err != nil {
		return err
	}
	issuedAt, err := parseTimestamp("issued_at", request.IssuedAt)
	if err != nil {
		return err
	}
	validUntil, err := parseTimestamp("valid_until", request.ValidUntil)
	if err != nil {
		return err
	}
	approvedAt, _ := parseCanonicalUTC(request.Approval.ApprovedAt)
	approvalUntil, _ := parseCanonicalUTC(request.Approval.ValidUntil)
	grantedAt, _ := parseCanonicalUTC(request.Grant.GrantedAt)
	grantUntil, _ := parseCanonicalUTC(request.Grant.ValidUntil)
	if !issuedAt.Before(validUntil) || validUntil.Sub(issuedAt) > MaxRequestValidity {
		return invalid("valid_until", "must be after issued_at within the maximum request validity")
	}
	if issuedAt.Before(approvedAt) || issuedAt.Before(grantedAt) {
		return invalid("issued_at", "must not predate approval or grant authority")
	}
	if validUntil.After(approvalUntil) || validUntil.After(grantUntil) {
		return invalid("valid_until", "must not exceed approval or grant authority")
	}
	return nil
}

func validateApproval(binding ApprovalBinding) error {
	if err := validateOpaqueRef("approval.receipt_ref", binding.ReceiptRef, "approval"); err != nil {
		return err
	}
	if err := validateDigest("approval.receipt_hash", binding.ReceiptHash); err != nil {
		return err
	}
	if binding.Decision != "approved" {
		return invalid("approval.decision", "must be approved")
	}
	if binding.Class != ApprovalClassOwnerStepUp && binding.Class != ApprovalClassBreakGlass {
		return invalid("approval.class", "is unsupported")
	}
	approvedAt, err := parseTimestamp("approval.approved_at", binding.ApprovedAt)
	if err != nil {
		return err
	}
	validUntil, err := parseTimestamp("approval.valid_until", binding.ValidUntil)
	if err != nil {
		return err
	}
	if !approvedAt.Before(validUntil) || validUntil.Sub(approvedAt) > MaxAuthorityValidity {
		return invalid("approval.valid_until", "must be after approved_at within the maximum authority validity")
	}
	return nil
}

func validateGrant(binding GrantBinding) error {
	if err := validateOpaqueRef("grant.binding_ref", binding.BindingRef, "grant"); err != nil {
		return err
	}
	if err := validateDigest("grant.binding_hash", binding.BindingHash); err != nil {
		return err
	}
	if binding.Audience != "stackkits" {
		return invalid("grant.audience", "must be stackkits")
	}
	if len(binding.Scopes) == 0 || len(binding.Scopes) > MaxGrantScopes {
		return invalid("grant.scopes", "must contain a bounded non-empty set")
	}
	for index, scope := range binding.Scopes {
		if err := validateContractID(fmt.Sprintf("grant.scopes[%d]", index), scope); err != nil {
			return err
		}
		if index > 0 && binding.Scopes[index-1] >= scope {
			return invalid("grant.scopes", "must be strictly sorted and unique")
		}
	}
	grantedAt, err := parseTimestamp("grant.granted_at", binding.GrantedAt)
	if err != nil {
		return err
	}
	validUntil, err := parseTimestamp("grant.valid_until", binding.ValidUntil)
	if err != nil {
		return err
	}
	if !grantedAt.Before(validUntil) || validUntil.Sub(grantedAt) > MaxAuthorityValidity {
		return invalid("grant.valid_until", "must be after granted_at within the maximum authority validity")
	}
	return nil
}

func validateTarget(target TargetBinding) error {
	for field, value := range map[string]string{
		"target.site_ref": target.SiteRef, "target.node_ref": target.NodeRef,
		"target.module_instance_ref": target.ModuleInstanceRef, "target.runtime_instance_ref": target.RuntimeInstanceRef,
	} {
		if value != "" {
			if err := validateStableID(field, value); err != nil {
				return err
			}
		}
	}
	if target.ExecutionChannelRef != "" && !referenceid.ValidExecutionChannel(target.ExecutionChannelRef) {
		return invalid("target.execution_channel_ref", "must be a canonical non-secret execution-channel identity")
	}
	switch target.Scope {
	case TargetScopeStack:
		if target.SiteRef != "" || target.NodeRef != "" || target.ModuleInstanceRef != "" || target.RuntimeInstanceRef != "" || target.ExecutionChannelRef != "" {
			return invalid("target", "stack scope cannot carry narrower target or channel fields")
		}
	case TargetScopeModuleInstance:
		if target.SiteRef == "" || target.ModuleInstanceRef == "" || target.RuntimeInstanceRef != "" {
			return invalid("target", "module-instance scope requires site_ref and module_instance_ref only")
		}
		if (target.NodeRef == "") != (target.ExecutionChannelRef == "") {
			return invalid("target", "node_ref and execution_channel_ref must appear together")
		}
	case TargetScopeRuntimeInstance:
		if target.SiteRef == "" || target.NodeRef == "" || target.RuntimeInstanceRef == "" || target.ExecutionChannelRef == "" || target.ModuleInstanceRef != "" {
			return invalid("target", "runtime-instance scope requires exact site, node, runtime instance, and channel")
		}
	default:
		return invalid("target.scope", "is unsupported")
	}
	return nil
}

func validateInputs(inputs []Input) error {
	if len(inputs) > MaxInputs {
		return invalid("inputs", "exceeds maximum count")
	}
	for index, input := range inputs {
		prefix := fmt.Sprintf("inputs[%d]", index)
		if err := validateContractID(prefix+".id", input.ID); err != nil {
			return err
		}
		if index > 0 && inputs[index-1].ID >= input.ID {
			return invalid("inputs", "must be strictly sorted and unique by id")
		}
		set := 0
		if input.OpaqueRef != "" {
			set++
		}
		if input.Boolean != nil {
			set++
		}
		if input.Integer != nil {
			set++
		}
		if input.Enum != "" {
			set++
		}
		if set != 1 {
			return invalid(prefix, "must contain exactly one typed value")
		}
		switch input.Type {
		case InputTypeOpaqueReference:
			if input.OpaqueRef == "" {
				return invalid(prefix+".opaque_ref", "is required for opaque-reference")
			}
			if err := validateOpaqueRef(prefix+".opaque_ref", input.OpaqueRef, ""); err != nil {
				return err
			}
		case InputTypeBoolean:
			if input.Boolean == nil {
				return invalid(prefix+".boolean", "is required for boolean")
			}
		case InputTypeInteger:
			if input.Integer == nil {
				return invalid(prefix+".integer", "is required for integer")
			}
		case InputTypeStringEnum:
			if err := validateContractID(prefix+".enum", input.Enum); err != nil {
				return err
			}
		default:
			return invalid(prefix+".type", "is unsupported")
		}
	}
	return nil
}

// ComputeRequestDigest returns the deterministic digest of the complete
// provider-free handoff. It intentionally performs shape, but not freshness,
// validation so stored evidence can be reverified after expiry.
func ComputeRequestDigest(request Request) (string, error) {
	if err := ValidateRequestShape(request); err != nil {
		return "", err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("marshal rilaction request: %w", err)
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateStableID(field, value string) error {
	if !utf8.ValidString(value) || !stableIDPattern.MatchString(value) || strings.Contains(value, "://") {
		return invalid(field, "must be a safe opaque identifier")
	}
	return nil
}

func validateContractID(field, value string) error {
	if !contractIDPattern.MatchString(value) {
		return invalid(field, "must be a canonical contract id")
	}
	return nil
}

func validateDigest(field, value string) error {
	if !digestPattern.MatchString(value) {
		return invalid(field, "must be a canonical sha256 digest")
	}
	return nil
}

func validateOpaqueRef(field, value, requiredScheme string) error {
	if !utf8.ValidString(value) || !opaqueRefPattern.MatchString(value) || strings.Contains(value, "://") {
		return invalid(field, "must be an opaque non-URL reference")
	}
	scheme, _, _ := strings.Cut(value, ":")
	if _, forbidden := forbiddenOpaqueSchemes[scheme]; forbidden {
		return invalid(field, "uses a forbidden material-bearing scheme")
	}
	if requiredScheme != "" && scheme != requiredScheme {
		return invalid(field, "uses an unsupported reference scheme")
	}
	return nil
}

func parseTimestamp(field, value string) (time.Time, error) {
	parsed, err := parseCanonicalUTC(value)
	if err != nil {
		return time.Time{}, invalid(field, "must be a canonical RFC3339Nano UTC timestamp")
	}
	return parsed, nil
}

func parseCanonicalUTC(value string) (time.Time, error) {
	if !strings.HasSuffix(value, "Z") {
		return time.Time{}, fmt.Errorf("not UTC")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("not canonical")
	}
	return parsed, nil
}

func invalid(field, reason string) error {
	return fmt.Errorf("rilaction field %s %s", field, reason)
}

func rejectDuplicateOrTrailingJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	return requireEOF(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("rilaction request is invalid JSON")
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("rilaction request is invalid JSON")
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("rilaction request contains an invalid object key")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("rilaction request contains a duplicate field")
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("rilaction request is invalid JSON")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("rilaction request is invalid JSON")
		}
	default:
		return fmt.Errorf("rilaction request is invalid JSON")
	}
	return nil
}

func requireEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("rilaction request contains trailing JSON")
	}
	return nil
}

// SortedInputs returns a defensive canonical copy for producers. Validation
// still requires consumers to reject unsorted input rather than silently
// changing the signed/digested wire.
func SortedInputs(inputs []Input) []Input {
	result := append([]Input(nil), inputs...)
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
