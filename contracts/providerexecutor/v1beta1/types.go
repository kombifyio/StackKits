package providerexecutor

import (
	"context"
	"time"
)

// APIVersion is the exact provider-executor contract version.
const APIVersion = "providerexecutor/v1beta1"

// The v1beta1 contract is intentionally bounded so validation, hashing, and
// graph traversal have predictable resource use. These bounds apply to one
// command or receipt envelope, not to TechStack's durable ledger retention.
const (
	MaxTargets             = 128
	MaxResources           = 256
	MaxEvidencePerResource = 32
	MaxEvidencePerReceipt  = 512
	// MaxJSONSafeInteger keeps revisions and receipt sequences exact in Go,
	// JavaScript, and JSON-backed projections.
	MaxJSONSafeInteger uint64 = 1<<53 - 1
)

// Operation identifies one provider lifecycle intent.
type Operation string

// Provider lifecycle operations accepted by the contract.
const (
	OperationPlan         Operation = "plan"
	OperationProvision    Operation = "provision"
	OperationObserve      Operation = "observe"
	OperationReconcile    Operation = "reconcile"
	OperationDecommission Operation = "decommission"
)

// Status reports the high-level outcome of one receipt.
type Status string

// Receipt outcome values accepted by the contract.
const (
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusDenied    Status = "denied"
)

// Phase is the durable lifecycle phase recorded by the owning control plane.
// Failed and denied are terminal phases. Absent is also a terminal result for
// observe and decommission, but its meaning is operation-specific: observe
// proves that every target is absent without asserting a delete workflow;
// decommission reaches absent only after delete acceptance, absence_pending,
// definitive absence evidence, and complete cleanup.
type Phase string

// Durable lifecycle phases accepted by the contract.
const (
	PhaseRequested      Phase = "requested"
	PhaseAccepted       Phase = "accepted"
	PhasePlanned        Phase = "planned"
	PhaseResourcesBound Phase = "resources_bound"
	PhasePresent        Phase = "present"
	PhaseDeleteAccepted Phase = "delete_accepted"
	PhaseAbsencePending Phase = "absence_pending"
	PhaseAbsent         Phase = "absent"
	PhaseFailed         Phase = "failed"
	PhaseDenied         Phase = "denied"
)

// ObservationState reports provider-native resource presence.
type ObservationState string

// Provider resource observation values accepted by the contract.
const (
	ObservationPresent ObservationState = "present"
	ObservationAbsent  ObservationState = "absent"
	ObservationUnknown ObservationState = "unknown"
)

// CleanupState reports whether a resource still requires cleanup work.
type CleanupState string

// Resource cleanup values accepted by the contract.
const (
	CleanupNotRequired CleanupState = "not_required"
	CleanupRequired    CleanupState = "required"
	CleanupPending     CleanupState = "pending"
	CleanupComplete    CleanupState = "complete"
	CleanupBlocked     CleanupState = "blocked"
)

// ResourceDisposition declares the cleanup action for a bound resource.
type ResourceDisposition string

const (
	// DispositionDelete requires the owning ledger to retain and delete the
	// resource before a decommission operation may complete.
	DispositionDelete ResourceDisposition = "delete"
)

// EvidenceSource identifies who made an observation. Only provider_api and
// provider_event evidence can prove absence; executor-local assertions cannot.
type EvidenceSource string

// Evidence source values accepted by the contract.
const (
	EvidenceSourceProviderAPI   EvidenceSource = "provider_api"
	EvidenceSourceProviderEvent EvidenceSource = "provider_event"
	EvidenceSourceAdapter       EvidenceSource = "adapter"
)

// ResourceTarget identifies an already-known provider resource without
// exposing credentials or provider-specific configuration.
type ResourceTarget struct {
	BindingID       string              `json:"binding_id"`
	Kind            string              `json:"kind"`
	NativeRef       string              `json:"native_ref"`
	ParentBindingID string              `json:"parent_binding_id,omitempty"`
	OwnershipHash   string              `json:"ownership_hash"`
	Disposition     ResourceDisposition `json:"disposition"`
}

// Command is authorized and persisted by the owning product before it crosses
// an Executor boundary. AdapterID selects a TechStack-owned adapter; it is not
// a StackKit compatibility or architecture field. CustodyRef, ConnectionRef,
// and DesiredSpecRef are hierarchical server-side lookup references whose
// identifiers are opaque to this package; they must never contain raw
// credentials. LeaseRevision, RuntimeServerID, and ResourceGenerationID fence
// execution to the exact lease/server/generation aggregate that TechStack
// admitted. ProviderID is the stable vendor identity; AdapterID selects the
// TechStack implementation. CapabilitySnapshotHash and ExecutionProfileHash
// retain the exact catalog/admission decision used for this operation.
type Command struct {
	APIVersion             string           `json:"api_version"`
	OperationID            string           `json:"operation_id"`
	Operation              Operation        `json:"operation"`
	TenantID               string           `json:"tenant_id"`
	LeaseID                string           `json:"lease_id"`
	LeaseRevision          uint64           `json:"lease_revision"`
	RuntimeServerID        string           `json:"runtime_server_id"`
	ResourceGenerationID   string           `json:"resource_generation_id"`
	IdempotencyKey         string           `json:"idempotency_key"`
	ProviderID             string           `json:"provider_id"`
	AdapterID              string           `json:"adapter_id"`
	CustodyRef             string           `json:"custody_ref"`
	CustodyHash            string           `json:"custody_hash"`
	ConnectionRef          string           `json:"connection_ref"`
	ConnectionHash         string           `json:"connection_hash"`
	CapabilitySnapshotHash string           `json:"capability_snapshot_hash"`
	ExecutionProfileHash   string           `json:"execution_profile_hash"`
	ResourceGraphHash      string           `json:"resource_graph_hash"`
	LedgerRevision         uint64           `json:"ledger_revision"`
	DesiredSpecRef         string           `json:"desired_spec_ref,omitempty"`
	DesiredSpecHash        string           `json:"desired_spec_hash,omitempty"`
	Targets                []ResourceTarget `json:"targets,omitempty"`
	RequestedAt            time.Time        `json:"requested_at"`
	CommandDigest          string           `json:"command_digest"`
}

// Evidence is immutable observation evidence. Digest hashes the evidence
// payload retained by the owning product; Ref locates that payload without
// embedding it in the shared contract.
type Evidence struct {
	Ref                    string           `json:"ref"`
	Digest                 string           `json:"digest"`
	Source                 EvidenceSource   `json:"source"`
	OperationID            string           `json:"operation_id"`
	LeaseRevision          uint64           `json:"lease_revision"`
	RuntimeServerID        string           `json:"runtime_server_id"`
	ProviderID             string           `json:"provider_id"`
	CapabilitySnapshotHash string           `json:"capability_snapshot_hash"`
	BindingID              string           `json:"binding_id"`
	NativeRefHash          string           `json:"native_ref_hash"`
	ConnectionHash         string           `json:"connection_hash"`
	ExecutionProfileHash   string           `json:"execution_profile_hash"`
	ResourceGraphHash      string           `json:"resource_graph_hash"`
	SubjectHash            string           `json:"subject_hash"`
	Observation            ObservationState `json:"observation"`
	Definitive             bool             `json:"definitive"`
	AttestationRef         string           `json:"attestation_ref"`
	AttestationDigest      string           `json:"attestation_digest"`
	CollectedAt            time.Time        `json:"collected_at"`
}

// ResourceBinding is one node in the authoritative resource graph. NativeRef
// is retained through cleanup so a partially-created operation remains
// recoverable and an absent resource can still be audited.
type ResourceBinding struct {
	BindingID       string              `json:"binding_id"`
	Kind            string              `json:"kind"`
	NativeRef       string              `json:"native_ref"`
	ParentBindingID string              `json:"parent_binding_id,omitempty"`
	OwnershipHash   string              `json:"ownership_hash"`
	Disposition     ResourceDisposition `json:"disposition"`
	Observation     ObservationState    `json:"observation"`
	Cleanup         CleanupState        `json:"cleanup"`
	Evidence        []Evidence          `json:"evidence,omitempty"`
}

// Canonical provider failure reason codes. Provider adapters must map native
// SDK and API failures into this closed taxonomy before returning a result;
// provider-specific diagnostics belong in secret-free evidence, logs, or
// support references rather than new wire-level reason codes.
const (
	ReasonCodeProviderAuth            = "provider.auth"
	ReasonCodeProviderPermission      = "provider.permission"
	ReasonCodeProviderInvalidSpec     = "provider.invalid_spec"
	ReasonCodeProviderQuota           = "provider.quota"
	ReasonCodeProviderRateLimit       = "provider.rate_limit"
	ReasonCodeProviderConflict        = "provider.conflict"
	ReasonCodeProviderNotFound        = "provider.not_found"
	ReasonCodeProviderTransient       = "provider.transient"
	ReasonCodeProviderTimeout         = "provider.timeout"
	ReasonCodeProviderPartialCreate   = "provider.partial_create"
	ReasonCodeProviderCleanupRequired = "provider.cleanup_required"
)

// IsProviderReasonCode reports whether code is in the closed provider failure
// taxonomy. It returns false for policy, admission, or coordinator codes,
// which use their own non-provider namespaces.
func IsProviderReasonCode(code string) bool {
	switch code {
	case ReasonCodeProviderAuth,
		ReasonCodeProviderPermission,
		ReasonCodeProviderInvalidSpec,
		ReasonCodeProviderQuota,
		ReasonCodeProviderRateLimit,
		ReasonCodeProviderConflict,
		ReasonCodeProviderNotFound,
		ReasonCodeProviderTransient,
		ReasonCodeProviderTimeout,
		ReasonCodeProviderPartialCreate,
		ReasonCodeProviderCleanupRequired:
		return true
	default:
		return false
	}
}

// Reason is a secret-free, machine-readable failure envelope. Human-readable
// diagnostics and raw provider responses remain in TechStack-owned logs or
// evidence stores and never cross this contract.
type Reason struct {
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

// Receipt is an immutable lifecycle-ledger entry. TechStack's coordinator
// first persists sequence 1 as pending/requested with no resources, then asks
// an Executor to return later entries (normally starting at sequence 2). The
// coordinator seals, validates, and appends each entry. Sequence and
// PreviousReceiptDigest form the validated append chain. ReceiptDigest detects
// accidental mutation; it is not a replacement for transport authentication
// or durable-ledger integrity. Every receipt repeats the command's exact
// lease revision, runtime server, provider, capability snapshot, and resource
// generation so a ledger chain cannot cross an admitted execution fence.
type Receipt struct {
	APIVersion             string            `json:"api_version"`
	OperationID            string            `json:"operation_id"`
	Operation              Operation         `json:"operation"`
	TenantID               string            `json:"tenant_id"`
	LeaseID                string            `json:"lease_id"`
	LeaseRevision          uint64            `json:"lease_revision"`
	RuntimeServerID        string            `json:"runtime_server_id"`
	ResourceGenerationID   string            `json:"resource_generation_id"`
	IdempotencyKey         string            `json:"idempotency_key"`
	ProviderID             string            `json:"provider_id"`
	AdapterID              string            `json:"adapter_id"`
	CapabilitySnapshotHash string            `json:"capability_snapshot_hash"`
	CommandDigest          string            `json:"command_digest"`
	DesiredSpecHash        string            `json:"desired_spec_hash,omitempty"`
	Sequence               uint64            `json:"sequence"`
	PreviousReceiptDigest  string            `json:"previous_receipt_digest,omitempty"`
	Status                 Status            `json:"status"`
	Phase                  Phase             `json:"phase"`
	PhaseEnteredAt         time.Time         `json:"phase_entered_at"`
	Resources              []ResourceBinding `json:"resources,omitempty"`
	Reason                 *Reason           `json:"reason,omitempty"`
	IssuedAt               time.Time         `json:"issued_at"`
	ReceiptDigest          string            `json:"receipt_digest"`
}

// EvidenceVerifier verifies the attestation behind definitive provider-native
// evidence. TechStack owns the implementation and its trust configuration.
// Implementations must honor context cancellation. The shared package invokes
// it before accepting an absent lifecycle state and rejects typed-nil verifiers.
type EvidenceVerifier interface {
	VerifyEvidence(context.Context, Command, ResourceTarget, Evidence) error
}

// ExecutionResult is the unchained result of one adapter invocation. It has no
// receipt identity, sequence, or digest because an adapter cannot safely own
// TechStack's ledger head. A provider failure is represented by a failed
// result with a closed provider reason and every known resource binding.
// Admission and policy failures use DenyReceipt before invocation. The
// single-return shape prevents callers from discarding partial-create cleanup
// handles on a conventional error path.
type ExecutionResult struct {
	Status    Status            `json:"status"`
	Phase     Phase             `json:"phase"`
	Resources []ResourceBinding `json:"resources,omitempty"`
	Reason    *Reason           `json:"reason,omitempty"`
}

// ExecutionRequest gives an adapter the command and the validated current
// ledger head. The adapter therefore has the prior lifecycle phase, complete
// resource graph, and immutable evidence history it needs to return exactly
// one next transition. The request does not transfer ledger authority: only
// TechStack's coordinator may seal and persist the resulting receipt.
type ExecutionRequest struct {
	Command  Command `json:"command"`
	Previous Receipt `json:"previous"`
}

// Executor is intentionally transport-agnostic. It returns an unchained
// ExecutionResult, never an authoritative ledger receipt. TechStack's
// coordinator owns the ledger head, calls AssembleReceipt, persists the
// sealed receipt, and validates the append before advancing projections.
type Executor interface {
	Execute(context.Context, ExecutionRequest) ExecutionResult
}
