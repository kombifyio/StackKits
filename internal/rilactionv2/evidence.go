package rilaction

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	MaxEvidenceBytes  = 64 * 1024
	MaxEvidenceChecks = 16
	MaxSummaryCodes   = 16
)

// DecodeEvidenceForRequest rejects ambiguous JSON and validates an evidence
// record against the exact request it completes. Request freshness is not
// re-evaluated at read time; evaluated_at proves the execution instant fell
// inside every bound authority window.
func DecodeEvidenceForRequest(data []byte, request Request) (Evidence, error) {
	if len(data) == 0 || len(data) > MaxEvidenceBytes {
		return Evidence{}, fmt.Errorf("rilaction evidence size is invalid")
	}
	if err := rejectDuplicateOrTrailingJSON(data); err != nil {
		return Evidence{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var evidence Evidence
	if err := decoder.Decode(&evidence); err != nil {
		return Evidence{}, fmt.Errorf("rilaction evidence is invalid: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return Evidence{}, err
	}
	if err := ValidateEvidenceForRequest(request, evidence); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}

// ValidateEvidenceForRequest proves exact request/result correlation without
// requiring a live clock or any provider, transport, credential, or log data.
func ValidateEvidenceForRequest(request Request, evidence Evidence) error {
	if err := ValidateRequestShape(request); err != nil {
		return err
	}
	requestDigest, err := ComputeRequestDigest(request)
	if err != nil {
		return err
	}
	if evidence.APIVersion != EvidenceAPIVersionV1 {
		return invalid("evidence.api_version", "is unsupported")
	}
	if err := validateContractID("evidence.executor_ref", evidence.ExecutorRef); err != nil {
		return err
	}
	evidenceID, err := ComputeEvidenceID(requestDigest, evidence.ExecutorRef)
	if err != nil {
		return err
	}
	targetRef, err := TargetReference(request)
	if err != nil {
		return err
	}
	exact := map[string][2]string{
		"evidence_id":             {evidence.EvidenceID, evidenceID},
		"evidence_sink_ref":       {evidence.EvidenceSinkRef, request.EvidenceSinkRef},
		"action_card_id":          {evidence.ActionCardID, request.ActionCardID},
		"execution_id":            {evidence.ExecutionID, request.ExecutionID},
		"trace_id":                {evidence.TraceID, request.TraceID},
		"tenant_id":               {evidence.TenantID, request.TenantID},
		"stack_id":                {evidence.StackID, request.StackID},
		"primitive_id":            {evidence.PrimitiveID, request.Primitive.ID},
		"primitive_contract_hash": {evidence.PrimitiveContractHash, request.Primitive.ContractHash},
		"resolved_plan_hash":      {evidence.ResolvedPlanHash, request.ResolvedPlanHash},
		"request_digest":          {evidence.RequestDigest, requestDigest},
		"target_ref":              {evidence.TargetRef, targetRef},
	}
	for field, values := range exact {
		if values[0] != values[1] {
			return invalid("evidence."+field, "does not match the approved request")
		}
	}
	if evidence.Status != "succeeded" && evidence.Status != "failed" {
		return invalid("evidence.status", "is unsupported")
	}
	if err := validateVerificationEvidence(evidence.Verification); err != nil {
		return err
	}
	if evidence.Status == "succeeded" && evidence.Verification.Status != "passed" {
		return invalid("evidence.verification.status", "must pass for a succeeded action")
	}
	if evidence.Status == "failed" && evidence.Verification.Status != "failed" {
		return invalid("evidence.verification.status", "must fail for a failed action")
	}
	if err := validateRecoveryEvidence(evidence.Recovery); err != nil {
		return err
	}
	if len(evidence.SummaryCodes) == 0 || len(evidence.SummaryCodes) > MaxSummaryCodes {
		return invalid("evidence.summary_codes", "must contain a bounded non-empty set")
	}
	for index, code := range evidence.SummaryCodes {
		if err := validateContractID(fmt.Sprintf("evidence.summary_codes[%d]", index), code); err != nil {
			return err
		}
		if index > 0 && evidence.SummaryCodes[index-1] >= code {
			return invalid("evidence.summary_codes", "must be strictly sorted and unique")
		}
	}
	if evidence.ProtectedDiagnosticRef != "" {
		if err := validateOpaqueRef("evidence.protected_diagnostic_ref", evidence.ProtectedDiagnosticRef, "diagnostic"); err != nil {
			return err
		}
	}
	evaluatedAt, err := parseTimestamp("evidence.evaluated_at", evidence.EvaluatedAt)
	if err != nil {
		return err
	}
	issuedAt, _ := parseCanonicalUTC(request.IssuedAt)
	validUntil, _ := parseCanonicalUTC(request.ValidUntil)
	approvalUntil, _ := parseCanonicalUTC(request.Approval.ValidUntil)
	grantUntil, _ := parseCanonicalUTC(request.Grant.ValidUntil)
	if evaluatedAt.Before(issuedAt) || !evaluatedAt.Before(validUntil) || !evaluatedAt.Before(approvalUntil) || !evaluatedAt.Before(grantUntil) {
		return invalid("evidence.evaluated_at", "falls outside the approved authority window")
	}
	return nil
}

func validateVerificationEvidence(evidence VerificationEvidence) error {
	if err := validateContractID("evidence.verification.kind", evidence.Kind); err != nil {
		return err
	}
	if evidence.Status != "passed" && evidence.Status != "failed" {
		return invalid("evidence.verification.status", "is unsupported")
	}
	if len(evidence.Checks) == 0 || len(evidence.Checks) > MaxEvidenceChecks {
		return invalid("evidence.verification.checks", "must contain a bounded non-empty set")
	}
	failed := 0
	for index, check := range evidence.Checks {
		if err := validateContractID(fmt.Sprintf("evidence.verification.checks[%d].id", index), check.ID); err != nil {
			return err
		}
		if check.Status != "passed" && check.Status != "failed" {
			return invalid(fmt.Sprintf("evidence.verification.checks[%d].status", index), "is unsupported")
		}
		if index > 0 && evidence.Checks[index-1].ID >= check.ID {
			return invalid("evidence.verification.checks", "must be strictly sorted and unique")
		}
		if evidence.Status == "passed" && check.Status != "passed" {
			return invalid("evidence.verification.checks", "cannot contain a failed check when verification passed")
		}
		if check.Status == "failed" {
			failed++
		}
	}
	if evidence.Status == "failed" && failed == 0 {
		return invalid("evidence.verification.checks", "must contain a failed check when verification failed")
	}
	return nil
}

func validateRecoveryEvidence(evidence RecoveryEvidence) error {
	switch evidence.Kind {
	case "none":
		if evidence.Status != "not-required" || evidence.PrimitiveRef != "" {
			return invalid("evidence.recovery", "none requires not-required status and no primitive")
		}
	case "primitive":
		if evidence.Status != "succeeded" && evidence.Status != "failed" {
			return invalid("evidence.recovery.status", "is unsupported for primitive recovery")
		}
		if err := validateContractID("evidence.recovery.primitive_ref", evidence.PrimitiveRef); err != nil {
			return err
		}
	case "manual":
		if evidence.Status != "manual-required" || evidence.PrimitiveRef != "" {
			return invalid("evidence.recovery", "manual requires manual-required status and no primitive")
		}
	default:
		return invalid("evidence.recovery.kind", "is unsupported")
	}
	return nil
}

// ComputeEvidenceID binds one result identity to the complete approved request
// and the product-selected executor without exposing executor selection in the
// request wire.
func ComputeEvidenceID(requestDigest, executorRef string) (string, error) {
	if err := validateDigest("request_digest", requestDigest); err != nil {
		return "", err
	}
	if err := validateContractID("executor_ref", executorRef); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(requestDigest + "\x00" + executorRef))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// TargetReference derives the only public evidence target identity allowed by
// the closed request target. It cannot introduce a wider target.
func TargetReference(request Request) (string, error) {
	if err := ValidateRequestShape(request); err != nil {
		return "", err
	}
	switch request.Target.Scope {
	case TargetScopeStack:
		return "stack:" + request.StackID, nil
	case TargetScopeModuleInstance:
		return "module:" + strings.Join([]string{request.Target.SiteRef, request.Target.ModuleInstanceRef}, "/"), nil
	case TargetScopeRuntimeInstance:
		return "runtime:" + strings.Join([]string{request.Target.SiteRef, request.Target.NodeRef, request.Target.RuntimeInstanceRef}, "/"), nil
	default:
		return "", fmt.Errorf("unsupported target scope")
	}
}
