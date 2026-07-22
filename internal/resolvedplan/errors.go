package resolvedplan

import "fmt"

// ErrorCode is stable enough for CLI/API adapters to map without parsing text.
type ErrorCode string

const (
	ErrInvalidInput               ErrorCode = "invalid_input"
	ErrContractValidation         ErrorCode = "contract_validation"
	ErrProfileMismatch            ErrorCode = "profile_spec_mismatch"
	ErrUnknownCapability          ErrorCode = "unknown_capability"
	ErrForbiddenCapability        ErrorCode = "forbidden_capability"
	ErrUnrealizedCapability       ErrorCode = "unrealized_capability"
	ErrAmbiguousProvider          ErrorCode = "ambiguous_provider"
	ErrUnknownProvider            ErrorCode = "unknown_provider"
	ErrUnknownAddOn               ErrorCode = "unknown_addon"
	ErrUnsupportedAddOn           ErrorCode = "unsupported_addon"
	ErrUnknownModule              ErrorCode = "unknown_module"
	ErrUnrealizedModule           ErrorCode = "unrealized_module"
	ErrUnknownWorkload            ErrorCode = "unknown_workload"
	ErrForbiddenWorkload          ErrorCode = "forbidden_workload"
	ErrUnknownWorkloadAlternative ErrorCode = "unknown_workload_alternative"
	ErrContractConflict           ErrorCode = "contract_conflict"
	ErrUnresolvedPlacement        ErrorCode = "unresolved_placement"
	ErrUnsafeSecretReference      ErrorCode = "unsafe_secret_reference"
	ErrPlanHashMismatch           ErrorCode = "plan_hash_mismatch"
	ErrNonCanonicalPlan           ErrorCode = "non_canonical_plan"
)

const (
	ErrExternalHostBindingStale                ErrorCode = "external_host_binding_stale"
	ErrHostConformanceReceiptMissing           ErrorCode = "host_conformance_receipt_missing"
	ErrHostConformanceReceiptStale             ErrorCode = "host_conformance_receipt_stale"
	ErrHostConformanceReceiptRejected          ErrorCode = "host_conformance_receipt_rejected"
	ErrExternalHomeAccessBindingStale          ErrorCode = "external_home_access_binding_stale"
	ErrExternalHomeAccessBindingMismatch       ErrorCode = "external_home_access_binding_mismatch"
	ErrExternalBackupTargetBindingStale        ErrorCode = "external_backup_target_binding_stale"
	ErrExternalBackupTargetBindingMismatch     ErrorCode = "external_backup_target_binding_mismatch"
	ErrExternalHomeBackupTargetBindingStale    ErrorCode = "external_home_backup_target_binding_stale"
	ErrExternalHomeBackupTargetBindingMismatch ErrorCode = "external_home_backup_target_binding_mismatch"
	ErrExternalFederationLinkBindingStale      ErrorCode = "external_federation_link_binding_stale"
	ErrExternalFederationLinkBindingMismatch   ErrorCode = "external_federation_link_binding_mismatch"
)

// CompileError identifies a fail-closed compiler decision.
type CompileError struct {
	Code    ErrorCode
	Path    string
	Message string
}

func (e *CompileError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("resolvedplan %s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("resolvedplan %s at %s: %s", e.Code, e.Path, e.Message)
}

func fail(code ErrorCode, path, format string, args ...any) error {
	return &CompileError{Code: code, Path: path, Message: fmt.Sprintf(format, args...)}
}
