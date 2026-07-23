// Package rilaction defines the provider-free approved-action handoff shared
// by TechStack and StackKits. It carries authority references only; it does not
// select an executor, transport, provider, lease, endpoint, or credential.
package rilaction

const (
	// APIVersionV1Alpha1 identifies the first closed RIL action-handoff wire.
	APIVersionV1Alpha1 = "stackkit.ril-action-handoff/v1alpha1"
	// EvidenceAPIVersionV1 identifies the stable public-safe action result wire.
	EvidenceAPIVersionV1 = "stackkit.ril-action-evidence/v1"
)

// TargetScope selects the governed StackKits identity depth without selecting
// an executor or infrastructure provider.
type TargetScope string

const (
	TargetScopeStack           TargetScope = "stack"
	TargetScopeModuleInstance  TargetScope = "module-instance"
	TargetScopeRuntimeInstance TargetScope = "runtime-instance"
)

// InputType identifies one catalog-declared input representation.
type InputType string

const (
	InputTypeOpaqueReference InputType = "opaque-reference"
	InputTypeBoolean         InputType = "boolean"
	InputTypeInteger         InputType = "integer"
	InputTypeStringEnum      InputType = "string-enum"
)

// ApprovalClass identifies the upstream approval ceremony that authorized the
// action. It does not grant StackKits additional execution capability.
type ApprovalClass string

const (
	ApprovalClassOwnerStepUp ApprovalClass = "owner-step-up"
	ApprovalClassBreakGlass  ApprovalClass = "break-glass"
)

// Request is one already-approved, short-lived action admission. StackKits
// must still resolve PrimitiveID and PrimitiveContractHash through its own CUE
// product authority and choose the runtime owner itself.
type Request struct {
	APIVersion       string           `json:"api_version"`
	ActionCardID     string           `json:"action_card_id"`
	ExecutionID      string           `json:"execution_id"`
	TraceID          string           `json:"trace_id"`
	TenantID         string           `json:"tenant_id"`
	StackID          string           `json:"stack_id"`
	Primitive        PrimitiveBinding `json:"primitive"`
	ResolvedPlanHash string           `json:"resolved_plan_hash"`
	Approval         ApprovalBinding  `json:"approval"`
	Grant            GrantBinding     `json:"grant"`
	Target           TargetBinding    `json:"target"`
	Inputs           []Input          `json:"inputs"`
	EvidenceSinkRef  string           `json:"evidence_sink_ref"`
	IssuedAt         string           `json:"issued_at"`
	ValidUntil       string           `json:"valid_until"`
	Nonce            string           `json:"nonce"`
	IdempotencyKey   string           `json:"idempotency_key"`
}

// PrimitiveBinding binds a request to one exact CUE-governed RIL primitive.
type PrimitiveBinding struct {
	ID             string `json:"id"`
	ContractHash   string `json:"contract_hash"`
	OperationClass string `json:"operation_class"`
}

// ApprovalBinding references one short-lived TechStack approval receipt.
type ApprovalBinding struct {
	ReceiptRef  string        `json:"receipt_ref"`
	ReceiptHash string        `json:"receipt_hash"`
	Decision    string        `json:"decision"`
	Class       ApprovalClass `json:"class"`
	ApprovedAt  string        `json:"approved_at"`
	ValidUntil  string        `json:"valid_until"`
}

// GrantBinding references one short-lived Gateway policy grant for StackKits.
type GrantBinding struct {
	BindingRef  string   `json:"binding_ref"`
	BindingHash string   `json:"binding_hash"`
	Audience    string   `json:"audience"`
	Scopes      []string `json:"scopes"`
	GrantedAt   string   `json:"granted_at"`
	ValidUntil  string   `json:"valid_until"`
}

// TargetBinding names only governed StackKits identity. ExecutionChannelRef
// is an opaque, already-authorized routing reference, never an address,
// endpoint, transport hint, credential, or permission to discover a node.
type TargetBinding struct {
	Scope               TargetScope `json:"scope"`
	SiteRef             string      `json:"site_ref,omitempty"`
	NodeRef             string      `json:"node_ref,omitempty"`
	ModuleInstanceRef   string      `json:"module_instance_ref,omitempty"`
	RuntimeInstanceRef  string      `json:"runtime_instance_ref,omitempty"`
	ExecutionChannelRef string      `json:"execution_channel_ref,omitempty"`
}

// Input is one typed, catalog-declared approved-action input. OpaqueRef is a
// non-secret identity reference; it cannot use URL or secret-store schemes.
type Input struct {
	ID        string    `json:"id"`
	Type      InputType `json:"type"`
	OpaqueRef string    `json:"opaque_ref,omitempty"`
	Boolean   *bool     `json:"boolean,omitempty"`
	Integer   *int64    `json:"integer,omitempty"`
	Enum      string    `json:"enum,omitempty"`
}

// Evidence is the provider-free public result of one action invocation. It
// contains correlation, stable status codes, and verification/recovery facts;
// protected logs and diagnostics stay behind opaque references.
type Evidence struct {
	APIVersion             string               `json:"api_version"`
	EvidenceID             string               `json:"evidence_id"`
	EvidenceSinkRef        string               `json:"evidence_sink_ref"`
	ActionCardID           string               `json:"action_card_id"`
	ExecutionID            string               `json:"execution_id"`
	TraceID                string               `json:"trace_id"`
	TenantID               string               `json:"tenant_id"`
	StackID                string               `json:"stack_id"`
	PrimitiveID            string               `json:"primitive_id"`
	PrimitiveContractHash  string               `json:"primitive_contract_hash"`
	ResolvedPlanHash       string               `json:"resolved_plan_hash"`
	RequestDigest          string               `json:"request_digest"`
	ExecutorRef            string               `json:"executor_ref"`
	TargetRef              string               `json:"target_ref"`
	Status                 string               `json:"status"`
	Verification           VerificationEvidence `json:"verification"`
	Recovery               RecoveryEvidence     `json:"recovery"`
	SummaryCodes           []string             `json:"summary_codes"`
	ProtectedDiagnosticRef string               `json:"protected_diagnostic_ref,omitempty"`
	EvaluatedAt            string               `json:"evaluated_at"`
}

// VerificationEvidence carries only closed check IDs and statuses. It cannot
// contain arbitrary runtime output.
type VerificationEvidence struct {
	Kind                 string              `json:"kind"`
	Status               string              `json:"status"`
	RuntimeStateObserved bool                `json:"runtime_state_observed"`
	Checks               []VerificationCheck `json:"checks"`
}

// VerificationCheck is one closed, public-safe check result.
type VerificationCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// RecoveryEvidence reports only the governed recovery disposition. A named
// primitive with status "required" must be submitted as a separate approved
// rilaction.Request and returns its own top-level Evidence; this record cannot
// claim that separately authorized recovery succeeded or failed.
type RecoveryEvidence struct {
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	PrimitiveRef string `json:"primitive_ref,omitempty"`
}
