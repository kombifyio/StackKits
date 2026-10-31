package providerexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	custodyRefScheme     = "custody"
	connectionRefScheme  = "provider-connection"
	desiredSpecRefScheme = "desired-spec"
	evidenceRefScheme    = "provider-evidence"
	attestationRefScheme = "provider-attestation"

	maxIdentifierBytes = 512
	maxLookupRefBytes  = 2048
	maxDigestBytes     = len(digestPrefix) + sha256.Size*2
)

// Validation errors classify rejected commands, receipts, replays, and
// unproven absence claims.
var (
	ErrInvalidCommand       = errors.New("providerexecutor: invalid command")
	ErrInvalidReceipt       = errors.New("providerexecutor: invalid receipt")
	ErrIdempotencyConflict  = errors.New("providerexecutor: idempotency conflict")
	ErrAbsenceProofRequired = errors.New("providerexecutor: provider-native absence proof required")
)

// SealCommand normalizes a command, derives its operation ID and digest, and
// validates the resulting immutable envelope.
func SealCommand(command Command) (Command, error) {
	if err := preflightCommandBounds(command); err != nil {
		return Command{}, err
	}
	if err := validateCommandUTF8(command); err != nil {
		return Command{}, err
	}
	command = normalizeCommand(command)
	if command.APIVersion == "" {
		command.APIVersion = APIVersion
	}
	if command.OperationID == "" {
		command.OperationID = ComputeOperationID(
			command.TenantID,
			command.LeaseID,
			command.ResourceGenerationID,
			command.Operation,
			command.IdempotencyKey,
		)
	}
	resourceGraphHash, err := ComputeResourceGraphHash(command.Targets)
	if err != nil {
		return Command{}, err
	}
	command.ResourceGraphHash = resourceGraphHash
	digest, err := ComputeCommandDigest(command)
	if err != nil {
		return Command{}, err
	}
	command.CommandDigest = digest
	if err := command.Validate(); err != nil {
		return Command{}, err
	}
	return command, nil
}

// Validate checks the closed command contract and its deterministic hashes.
func (command Command) Validate() error {
	if err := preflightCommandBounds(command); err != nil {
		return err
	}
	if err := validateCommandUTF8(command); err != nil {
		return err
	}
	if !isCanonicalCommand(command) {
		return invalidCommand("command must be canonical; seal it before validation or execution")
	}
	if command.APIVersion != APIVersion {
		return invalidCommand("api_version must be %q", APIVersion)
	}
	if !validOperation(command.Operation) {
		return invalidCommand("unsupported operation %q", command.Operation)
	}
	for _, field := range []struct{ name, value string }{
		{"tenant_id", command.TenantID},
		{"lease_id", command.LeaseID},
		{"runtime_server_id", command.RuntimeServerID},
		{"idempotency_key", command.IdempotencyKey},
		{"provider_id", command.ProviderID},
		{"adapter_id", command.AdapterID},
	} {
		if err := validateIdentifier(field.name, field.value); err != nil {
			return err
		}
	}
	if command.OperationID != ComputeOperationID(
		command.TenantID,
		command.LeaseID,
		command.ResourceGenerationID,
		command.Operation,
		command.IdempotencyKey,
	) {
		return invalidCommand("operation_id does not match command identity")
	}
	if !validResourceGenerationID(command.ResourceGenerationID) {
		return invalidCommand("resource_generation_id must be a canonical lowercase UUID")
	}
	if !validJSONInteger(command.LeaseRevision) {
		return invalidCommand("lease_revision must be between 1 and %d", MaxJSONSafeInteger)
	}
	if !validJSONInteger(command.LedgerRevision) {
		return invalidCommand("ledger_revision must be between 1 and %d", MaxJSONSafeInteger)
	}
	if command.RequestedAt.IsZero() {
		return invalidCommand("requested_at required")
	}
	if err := validateLookupRef("custody_ref", command.CustodyRef, custodyRefScheme); err != nil {
		return err
	}
	if err := validateLookupRef("connection_ref", command.ConnectionRef, connectionRefScheme); err != nil {
		return err
	}
	for _, field := range []struct{ name, value string }{
		{"custody_hash", command.CustodyHash},
		{"connection_hash", command.ConnectionHash},
		{"capability_snapshot_hash", command.CapabilitySnapshotHash},
		{"execution_profile_hash", command.ExecutionProfileHash},
		{"resource_graph_hash", command.ResourceGraphHash},
	} {
		if !validDigest(field.value) {
			return invalidCommand("%s must be a sha256 digest", field.name)
		}
	}
	needsSpec := command.Operation == OperationPlan || command.Operation == OperationProvision || command.Operation == OperationReconcile
	if needsSpec {
		if err := validateLookupRef("desired_spec_ref", command.DesiredSpecRef, desiredSpecRefScheme); err != nil {
			return err
		}
		if !validDigest(command.DesiredSpecHash) {
			return invalidCommand("desired_spec_hash must be a sha256 digest")
		}
	} else if command.DesiredSpecRef != "" || command.DesiredSpecHash != "" {
		return invalidCommand("desired spec is not allowed for %q", command.Operation)
	}
	needsTargets := command.Operation == OperationObserve || command.Operation == OperationReconcile || command.Operation == OperationDecommission
	if needsTargets && len(command.Targets) == 0 {
		return invalidCommand("at least one resource target required for %q", command.Operation)
	}
	if !needsTargets && len(command.Targets) != 0 {
		return invalidCommand("resource targets are not allowed for %q", command.Operation)
	}
	if err := validateTargets(command.Targets); err != nil {
		return err
	}
	expectedResourceGraphHash, err := ComputeResourceGraphHash(command.Targets)
	if err != nil {
		return err
	}
	if command.ResourceGraphHash != expectedResourceGraphHash {
		return invalidCommand("resource_graph_hash does not match canonical targets")
	}
	expected, err := ComputeCommandDigest(command)
	if err != nil {
		return err
	}
	if command.CommandDigest != expected {
		return invalidCommand("command_digest mismatch")
	}
	return nil
}

// ValidateReplay rejects reuse of an idempotency key for a different command.
func ValidateReplay(original, replay Command) error {
	if err := original.Validate(); err != nil {
		return err
	}
	if err := replay.Validate(); err != nil {
		return err
	}
	if original.TenantID != replay.TenantID || original.LeaseID != replay.LeaseID ||
		original.ResourceGenerationID != replay.ResourceGenerationID ||
		original.Operation != replay.Operation || original.IdempotencyKey != replay.IdempotencyKey ||
		original.OperationID != replay.OperationID || original.CommandDigest != replay.CommandDigest {
		return ErrIdempotencyConflict
	}
	return nil
}

// sealReceiptEnvelope normalizes and hashes one structurally valid receipt. It stays
// private because sealing alone cannot prove that a sequence > 1 entry follows
// the durable ledger head; public mutation paths must use InitialReceipt,
// AssembleReceipt, or DenyReceipt.
func sealReceiptEnvelope(ctx context.Context, command Command, receipt Receipt, verifier EvidenceVerifier) (Receipt, error) {
	if err := validateContext(ctx); err != nil {
		return Receipt{}, err
	}
	if err := preflightReceiptBounds(receipt); err != nil {
		return Receipt{}, err
	}
	if err := validateReceiptUTF8(receipt); err != nil {
		return Receipt{}, err
	}
	receipt = normalizeReceipt(receipt)
	if receipt.APIVersion == "" {
		receipt.APIVersion = APIVersion
	}
	digest, err := ComputeReceiptDigest(receipt)
	if err != nil {
		return Receipt{}, err
	}
	receipt.ReceiptDigest = digest
	if err := receipt.ValidateFor(ctx, command, verifier); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// InitialReceipt creates the coordinator-owned head of a new lifecycle chain.
// It is the only valid sequence-1 receipt. TechStack persists it before
// calling an Executor, then passes it to the executor as read-only execution
// context in an ExecutionRequest. The executor never mutates the ledger head.
func InitialReceipt(command Command, issuedAt time.Time) (Receipt, error) {
	receipt := Receipt{
		APIVersion:             APIVersion,
		OperationID:            command.OperationID,
		Operation:              command.Operation,
		TenantID:               command.TenantID,
		LeaseID:                command.LeaseID,
		LeaseRevision:          command.LeaseRevision,
		RuntimeServerID:        command.RuntimeServerID,
		ResourceGenerationID:   command.ResourceGenerationID,
		IdempotencyKey:         command.IdempotencyKey,
		ProviderID:             command.ProviderID,
		AdapterID:              command.AdapterID,
		CapabilitySnapshotHash: command.CapabilitySnapshotHash,
		CommandDigest:          command.CommandDigest,
		DesiredSpecHash:        command.DesiredSpecHash,
		Sequence:               1,
		Status:                 StatusPending,
		Phase:                  PhaseRequested,
		PhaseEnteredAt:         issuedAt,
		IssuedAt:               issuedAt,
	}
	return sealReceiptEnvelope(context.Background(), command, receipt, nil)
}

// Validate confirms that a coordinator is giving an adapter one sealed command
// and the current, valid receipt from that command's ledger chain.
func (request ExecutionRequest) Validate(ctx context.Context, verifier EvidenceVerifier) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if err := request.Command.Validate(); err != nil {
		return err
	}
	return request.Previous.ValidateFor(ctx, request.Command, verifier)
}

// AssembleReceipt has no storage side effect. It lets TechStack's coordinator
// turn an adapter result into the next immutable receipt using the coordinator
// ledger head. Callers must persist the result only after this function
// succeeds; it seals and validates the candidate and its append transition.
func AssembleReceipt(ctx context.Context, request ExecutionRequest, result ExecutionResult, issuedAt time.Time, verifier EvidenceVerifier) (Receipt, error) {
	if err := request.Validate(ctx, verifier); err != nil {
		return Receipt{}, err
	}
	if err := validateAdapterExecutionResult(result); err != nil {
		return Receipt{}, err
	}
	return assembleReceipt(ctx, request, result, issuedAt, verifier)
}

// DenyReceipt creates the only valid denied entry without invoking an
// Executor. The durable head must still be pending/requested, proving that
// admission stopped before provider-side-effect custody began.
func DenyReceipt(ctx context.Context, request ExecutionRequest, reason Reason, issuedAt time.Time, verifier EvidenceVerifier) (Receipt, error) {
	if err := preflightReceiptBounds(Receipt{Reason: &reason}); err != nil {
		return Receipt{}, err
	}
	if err := request.Validate(ctx, verifier); err != nil {
		return Receipt{}, err
	}
	if request.Previous.Phase != PhaseRequested || request.Previous.Status != StatusPending {
		return Receipt{}, invalidReceipt("denial requires the pending/requested ledger head")
	}
	normalizedReasonCode := strings.TrimSpace(reason.Code)
	if !validCode(reason.Code) || IsProviderReasonCode(normalizedReasonCode) {
		return Receipt{}, invalidReceipt("denial requires a non-provider admission or policy reason code")
	}
	return assembleReceipt(ctx, request, ExecutionResult{
		Status: StatusDenied,
		Phase:  PhaseDenied,
		Reason: &reason,
	}, issuedAt, verifier)
}

func assembleReceipt(ctx context.Context, request ExecutionRequest, result ExecutionResult, issuedAt time.Time, verifier EvidenceVerifier) (Receipt, error) {
	command := request.Command
	previous := request.Previous
	if previous.Sequence >= MaxJSONSafeInteger {
		return Receipt{}, invalidReceipt("receipt sequence exhausted JSON-safe integer range")
	}
	phaseEnteredAt := issuedAt
	if result.Phase == previous.Phase {
		phaseEnteredAt = previous.PhaseEnteredAt
	}
	receipt := Receipt{
		APIVersion:             APIVersion,
		OperationID:            command.OperationID,
		Operation:              command.Operation,
		TenantID:               command.TenantID,
		LeaseID:                command.LeaseID,
		LeaseRevision:          command.LeaseRevision,
		RuntimeServerID:        command.RuntimeServerID,
		ResourceGenerationID:   command.ResourceGenerationID,
		IdempotencyKey:         command.IdempotencyKey,
		ProviderID:             command.ProviderID,
		AdapterID:              command.AdapterID,
		CapabilitySnapshotHash: command.CapabilitySnapshotHash,
		CommandDigest:          command.CommandDigest,
		DesiredSpecHash:        command.DesiredSpecHash,
		Sequence:               previous.Sequence + 1,
		PreviousReceiptDigest:  previous.ReceiptDigest,
		Status:                 result.Status,
		Phase:                  result.Phase,
		PhaseEnteredAt:         phaseEnteredAt,
		Resources:              result.Resources,
		Reason:                 result.Reason,
		IssuedAt:               issuedAt,
	}
	sealed, err := sealReceiptEnvelope(ctx, command, receipt, verifier)
	if err != nil {
		return Receipt{}, err
	}
	if err := validateAppendChain(command, previous, sealed); err != nil {
		return Receipt{}, err
	}
	return sealed, nil
}

// ValidateFor verifies receipt identity, graph integrity, lifecycle semantics,
// and attested provider-native evidence for every claimed absence.
func (receipt Receipt) ValidateFor(ctx context.Context, command Command, verifier EvidenceVerifier) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if err := command.Validate(); err != nil {
		return err
	}
	if err := preflightReceiptBounds(receipt); err != nil {
		return err
	}
	if err := validateReceiptUTF8(receipt); err != nil {
		return err
	}
	if !isCanonicalReceipt(receipt) {
		return invalidReceipt("receipt must be canonical; seal it before validation or persistence")
	}
	if receipt.APIVersion != APIVersion || receipt.OperationID != command.OperationID ||
		receipt.Operation != command.Operation || receipt.TenantID != command.TenantID ||
		receipt.LeaseID != command.LeaseID || receipt.LeaseRevision != command.LeaseRevision ||
		receipt.RuntimeServerID != command.RuntimeServerID ||
		receipt.ResourceGenerationID != command.ResourceGenerationID ||
		receipt.IdempotencyKey != command.IdempotencyKey || receipt.ProviderID != command.ProviderID ||
		receipt.AdapterID != command.AdapterID ||
		receipt.CapabilitySnapshotHash != command.CapabilitySnapshotHash ||
		receipt.CommandDigest != command.CommandDigest {
		return invalidReceipt("receipt identity does not match command")
	}
	if receipt.DesiredSpecHash != command.DesiredSpecHash {
		return invalidReceipt("desired_spec_hash does not match command")
	}
	if receipt.IssuedAt.IsZero() || receipt.IssuedAt.Before(command.RequestedAt) {
		return invalidReceipt("issued_at must be at or after requested_at")
	}
	if receipt.PhaseEnteredAt.IsZero() || receipt.PhaseEnteredAt.Before(command.RequestedAt) || receipt.PhaseEnteredAt.After(receipt.IssuedAt) {
		return invalidReceipt("phase_entered_at must be within command and receipt time bounds")
	}
	if !validJSONInteger(receipt.LeaseRevision) {
		return invalidReceipt("lease_revision must be between 1 and %d", MaxJSONSafeInteger)
	}
	if !validJSONInteger(receipt.Sequence) {
		return invalidReceipt("sequence must be between 1 and %d", MaxJSONSafeInteger)
	}
	if receipt.Sequence == 1 && receipt.PreviousReceiptDigest != "" {
		return invalidReceipt("first receipt must not have previous_receipt_digest")
	}
	if receipt.Sequence > 1 && !validDigest(receipt.PreviousReceiptDigest) {
		return invalidReceipt("previous_receipt_digest required after first receipt")
	}
	if receipt.Sequence == 1 && (receipt.Status != StatusPending || receipt.Phase != PhaseRequested || len(receipt.Resources) != 0) {
		return invalidReceipt("first receipt must be pending/requested without resource bindings")
	}
	if receipt.Sequence == 1 && !receipt.PhaseEnteredAt.Equal(receipt.IssuedAt) {
		return invalidReceipt("first receipt phase_entered_at must equal issued_at")
	}
	if receipt.Sequence > 1 && receipt.Phase == PhaseRequested {
		return invalidReceipt("requested phase is reserved for the first receipt")
	}
	if !validStatus(receipt.Status) || !phaseAllowedForOperation(receipt.Operation, receipt.Phase) {
		return invalidReceipt("invalid status or phase")
	}
	if err := validateStatusPhase(receipt); err != nil {
		return err
	}
	// Plan is a read-only operation. It never authorizes provider resource
	// creation, even when the adapter reports pending, denied, or failed.
	if command.Operation == OperationPlan && len(receipt.Resources) != 0 {
		return invalidReceipt("plan receipts must not contain resource bindings")
	}
	if err := validateResourceGraph(ctx, command, receipt, verifier); err != nil {
		return err
	}
	// Observe, reconcile, and decommission act only on an already-authorized
	// target graph. Intermediate and failed receipts may retain a subset (for
	// example when a provider call stops part-way through), but they must never
	// introduce a new cleanup handle outside that command scope.
	if receipt.Sequence > 1 && targetBoundOperation(command.Operation) {
		if err := validateTargetSubset(command.Targets, receipt.Resources); err != nil {
			return err
		}
	}
	if err := validateOperationResult(command, receipt); err != nil {
		return err
	}
	expected, err := ComputeReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != expected {
		return invalidReceipt("receipt_digest mismatch")
	}
	return nil
}

// ValidateAppend verifies a receipt before appending it after previous. It
// enforces hash-chain, sequence, time, phase-transition, and resource-graph
// continuity in addition to the standalone receipt invariants.
func ValidateAppend(ctx context.Context, command Command, previous, next Receipt, verifier EvidenceVerifier) error {
	if err := previous.ValidateFor(ctx, command, verifier); err != nil {
		return err
	}
	if err := next.ValidateFor(ctx, command, verifier); err != nil {
		return err
	}
	return validateAppendChain(command, previous, next)
}

func validateAppendChain(command Command, previous, next Receipt) error {
	previous = normalizeReceipt(previous)
	next = normalizeReceipt(next)
	if next.Sequence != previous.Sequence+1 {
		return invalidReceipt("sequence must increase by exactly one")
	}
	if next.PreviousReceiptDigest != previous.ReceiptDigest {
		return invalidReceipt("previous_receipt_digest does not match prior receipt")
	}
	if next.IssuedAt.Before(previous.IssuedAt) {
		return invalidReceipt("issued_at must be monotone")
	}
	if next.Phase == previous.Phase {
		if !next.PhaseEnteredAt.Equal(previous.PhaseEnteredAt) {
			return invalidReceipt("same-phase append must retain phase_entered_at")
		}
	} else if !next.PhaseEnteredAt.Equal(next.IssuedAt) {
		return invalidReceipt("phase transition must set phase_entered_at to issued_at")
	}
	if next.Phase == previous.Phase &&
		(previous.Status != StatusPending || next.Status != StatusPending) {
		return invalidReceipt("same-phase transitions require pending status on both receipts")
	}
	if err := ValidateTransition(command.Operation, previous.Phase, next.Phase); err != nil {
		return invalidReceipt("%v", err)
	}
	if err := validateGraphContinuity(previous.Resources, next.Resources); err != nil {
		return err
	}
	if command.Operation == OperationDecommission && previous.Phase == PhaseAbsencePending && next.Phase == PhaseAbsent {
		for _, resource := range next.Resources {
			if !hasDefinitiveAbsenceEvidenceAtOrAfter(resource.Evidence, previous.PhaseEnteredAt) {
				return fmt.Errorf("%w: resource %q has no provider-native absence evidence collected after absence_pending began", ErrAbsenceProofRequired, resource.BindingID)
			}
		}
	}
	return nil
}

func validateOperationResult(command Command, receipt Receipt) error {
	if receipt.Status != StatusSucceeded {
		return nil
	}
	switch command.Operation {
	case OperationPlan:
		if receipt.Phase != PhasePlanned || len(receipt.Resources) != 0 {
			return invalidReceipt("successful plan must be planned without resource bindings")
		}
	case OperationProvision:
		if receipt.Phase != PhasePresent || len(receipt.Resources) == 0 {
			return invalidReceipt("successful provision must retain a present resource graph")
		}
		if err := requireUniformObservation(receipt.Resources, ObservationPresent); err != nil {
			return err
		}
	case OperationReconcile:
		if receipt.Phase != PhasePresent {
			return invalidReceipt("successful reconcile must be in present phase")
		}
		if err := validateExactTargetCoverage(command.Targets, receipt.Resources); err != nil {
			return err
		}
		if err := requireUniformObservation(receipt.Resources, ObservationPresent); err != nil {
			return err
		}
	case OperationObserve:
		if receipt.Phase != PhasePresent && receipt.Phase != PhaseAbsent {
			return invalidReceipt("successful observe must resolve presence or absence")
		}
		if err := validateExactTargetCoverage(command.Targets, receipt.Resources); err != nil {
			return err
		}
		want := ObservationPresent
		if receipt.Phase == PhaseAbsent {
			want = ObservationAbsent
		}
		if err := requireUniformObservation(receipt.Resources, want); err != nil {
			return err
		}
		for _, resource := range receipt.Resources {
			if !hasDefinitiveEvidence(resource.Evidence, want) {
				return invalidReceipt("observed resource %q requires definitive evidence", resource.BindingID)
			}
		}
	case OperationDecommission:
		if receipt.Phase != PhaseAbsent {
			return invalidReceipt("successful decommission must be in absent phase")
		}
		if err := validateExactTargetCoverage(command.Targets, receipt.Resources); err != nil {
			return err
		}
		if err := requireUniformObservation(receipt.Resources, ObservationAbsent); err != nil {
			return err
		}
		for _, resource := range receipt.Resources {
			if resource.Cleanup != CleanupComplete {
				return invalidReceipt("decommissioned resource %q must have complete cleanup", resource.BindingID)
			}
		}
	}
	return nil
}

func requireUniformObservation(resources []ResourceBinding, want ObservationState) error {
	for _, resource := range resources {
		if resource.Observation != want {
			return invalidReceipt("resource %q observation is %q, want %q", resource.BindingID, resource.Observation, want)
		}
	}
	return nil
}

func validateExactTargetCoverage(targets []ResourceTarget, resources []ResourceBinding) error {
	if len(resources) != len(targets) {
		return invalidReceipt("resource graph must exactly cover command targets")
	}
	byBinding := make(map[string]ResourceBinding, len(resources))
	for _, resource := range resources {
		byBinding[resource.BindingID] = resource
	}
	for _, target := range targets {
		resource, ok := byBinding[target.BindingID]
		if !ok || !sameResourceIdentity(target, resource) {
			return invalidReceipt("resource %q does not exactly match its command target", target.BindingID)
		}
	}
	return nil
}

// validateTargetSubset permits partial progress for a target-bound operation,
// but never lets a receipt bind a resource not explicitly authorized by the
// command. Successful terminal operations apply the stricter exact-coverage
// rule above.
func validateTargetSubset(targets []ResourceTarget, resources []ResourceBinding) error {
	byBinding := make(map[string]ResourceTarget, len(targets))
	for _, target := range targets {
		byBinding[target.BindingID] = target
	}
	for _, resource := range resources {
		target, ok := byBinding[resource.BindingID]
		if !ok || !sameResourceIdentity(target, resource) {
			return invalidReceipt("resource %q is not an authorized command target", resource.BindingID)
		}
	}
	return nil
}

func targetBoundOperation(operation Operation) bool {
	return operation == OperationObserve || operation == OperationReconcile || operation == OperationDecommission
}

func sameResourceIdentity(target ResourceTarget, resource ResourceBinding) bool {
	return target.BindingID == resource.BindingID && target.Kind == resource.Kind &&
		target.NativeRef == resource.NativeRef && target.ParentBindingID == resource.ParentBindingID &&
		target.OwnershipHash == resource.OwnershipHash && target.Disposition == resource.Disposition
}

func validateStatusPhase(receipt Receipt) error {
	if receipt.Reason != nil {
		if !validCode(receipt.Reason.Code) {
			return invalidReceipt("reason code is invalid")
		}
	}
	switch receipt.Status {
	case StatusPending:
		if receipt.Phase == PhaseFailed || receipt.Phase == PhaseDenied || receipt.Phase == PhasePresent || receipt.Phase == PhaseAbsent || receipt.Phase == PhasePlanned {
			return invalidReceipt("pending status cannot use terminal phase %q", receipt.Phase)
		}
		if receipt.Reason != nil {
			return invalidReceipt("pending receipt must not contain a terminal reason")
		}
	case StatusSucceeded:
		if receipt.Reason != nil {
			return invalidReceipt("successful receipt must not contain a failure reason")
		}
	case StatusFailed:
		if receipt.Phase != PhaseFailed || receipt.Reason == nil {
			return invalidReceipt("failed receipt requires failed phase and reason")
		}
	case StatusDenied:
		if receipt.Phase != PhaseDenied || receipt.Reason == nil || len(receipt.Resources) != 0 {
			return invalidReceipt("denied receipt requires denied phase, reason, and no resource bindings")
		}
	}
	return nil
}

func validateAdapterExecutionResult(result ExecutionResult) error {
	if err := preflightReceiptBounds(Receipt{Resources: result.Resources, Reason: result.Reason}); err != nil {
		return err
	}
	if result.Status == StatusDenied {
		return invalidReceipt("executors must not return denied; admission denial must use DenyReceipt before invocation")
	}
	switch result.Status {
	case StatusPending, StatusSucceeded:
		if result.Reason != nil {
			return invalidReceipt("non-failed executor result must not contain a reason")
		}
	case StatusFailed:
		if result.Reason == nil || !IsProviderReasonCode(strings.TrimSpace(result.Reason.Code)) {
			return invalidReceipt("failed executor result requires a canonical provider reason")
		}
	default:
		return invalidReceipt("executor returned invalid status %q", result.Status)
	}
	return nil
}

func preflightCommandBounds(command Command) error {
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{"api_version", command.APIVersion, maxIdentifierBytes},
		{"operation_id", command.OperationID, maxIdentifierBytes},
		{"operation", string(command.Operation), maxIdentifierBytes},
		{"tenant_id", command.TenantID, maxIdentifierBytes},
		{"lease_id", command.LeaseID, maxIdentifierBytes},
		{"runtime_server_id", command.RuntimeServerID, maxIdentifierBytes},
		{"resource_generation_id", command.ResourceGenerationID, maxIdentifierBytes},
		{"idempotency_key", command.IdempotencyKey, maxIdentifierBytes},
		{"provider_id", command.ProviderID, maxIdentifierBytes},
		{"adapter_id", command.AdapterID, maxIdentifierBytes},
		{"custody_ref", command.CustodyRef, maxLookupRefBytes},
		{"custody_hash", command.CustodyHash, maxDigestBytes},
		{"connection_ref", command.ConnectionRef, maxLookupRefBytes},
		{"connection_hash", command.ConnectionHash, maxDigestBytes},
		{"capability_snapshot_hash", command.CapabilitySnapshotHash, maxDigestBytes},
		{"execution_profile_hash", command.ExecutionProfileHash, maxDigestBytes},
		{"resource_graph_hash", command.ResourceGraphHash, maxDigestBytes},
		{"desired_spec_ref", command.DesiredSpecRef, maxLookupRefBytes},
		{"desired_spec_hash", command.DesiredSpecHash, maxDigestBytes},
		{"command_digest", command.CommandDigest, maxDigestBytes},
	} {
		if len(field.value) > field.limit {
			return invalidCommand("%s exceeds %d bytes", field.name, field.limit)
		}
	}
	return preflightTargetBounds(command.Targets)
}

func preflightTargetBounds(targets []ResourceTarget) error {
	if len(targets) > MaxTargets {
		return invalidCommand("resource targets must not exceed %d", MaxTargets)
	}
	for i, target := range targets {
		for _, field := range []struct {
			name  string
			value string
			limit int
		}{
			{"binding_id", target.BindingID, maxIdentifierBytes},
			{"kind", target.Kind, maxIdentifierBytes},
			{"native_ref", target.NativeRef, maxIdentifierBytes},
			{"parent_binding_id", target.ParentBindingID, maxIdentifierBytes},
			{"ownership_hash", target.OwnershipHash, maxDigestBytes},
			{"disposition", string(target.Disposition), maxIdentifierBytes},
		} {
			if len(field.value) > field.limit {
				return invalidCommand("targets[%d].%s exceeds %d bytes", i, field.name, field.limit)
			}
		}
	}
	return nil
}

func preflightReceiptBounds(receipt Receipt) error {
	if len(receipt.Resources) > MaxResources {
		return invalidReceipt("resources must not exceed %d", MaxResources)
	}
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{"api_version", receipt.APIVersion, maxIdentifierBytes},
		{"operation_id", receipt.OperationID, maxIdentifierBytes},
		{"operation", string(receipt.Operation), maxIdentifierBytes},
		{"tenant_id", receipt.TenantID, maxIdentifierBytes},
		{"lease_id", receipt.LeaseID, maxIdentifierBytes},
		{"runtime_server_id", receipt.RuntimeServerID, maxIdentifierBytes},
		{"resource_generation_id", receipt.ResourceGenerationID, maxIdentifierBytes},
		{"idempotency_key", receipt.IdempotencyKey, maxIdentifierBytes},
		{"provider_id", receipt.ProviderID, maxIdentifierBytes},
		{"adapter_id", receipt.AdapterID, maxIdentifierBytes},
		{"capability_snapshot_hash", receipt.CapabilitySnapshotHash, maxDigestBytes},
		{"command_digest", receipt.CommandDigest, maxDigestBytes},
		{"desired_spec_hash", receipt.DesiredSpecHash, maxDigestBytes},
		{"previous_receipt_digest", receipt.PreviousReceiptDigest, maxDigestBytes},
		{"status", string(receipt.Status), maxIdentifierBytes},
		{"phase", string(receipt.Phase), maxIdentifierBytes},
		{"receipt_digest", receipt.ReceiptDigest, maxDigestBytes},
	} {
		if len(field.value) > field.limit {
			return invalidReceipt("%s exceeds %d bytes", field.name, field.limit)
		}
	}
	if receipt.Reason != nil && len(receipt.Reason.Code) > 160 {
		return invalidReceipt("reason.code exceeds 160 bytes")
	}
	totalEvidence := 0
	for i, resource := range receipt.Resources {
		if len(resource.Evidence) > MaxEvidencePerResource {
			return invalidReceipt("resources[%d].evidence must not exceed %d entries", i, MaxEvidencePerResource)
		}
		if len(resource.Evidence) > MaxEvidencePerReceipt-totalEvidence {
			return invalidReceipt("receipt evidence must not exceed %d entries", MaxEvidencePerReceipt)
		}
		totalEvidence += len(resource.Evidence)
		for _, field := range []struct {
			name  string
			value string
			limit int
		}{
			{"binding_id", resource.BindingID, maxIdentifierBytes},
			{"kind", resource.Kind, maxIdentifierBytes},
			{"native_ref", resource.NativeRef, maxIdentifierBytes},
			{"parent_binding_id", resource.ParentBindingID, maxIdentifierBytes},
			{"ownership_hash", resource.OwnershipHash, maxDigestBytes},
			{"disposition", string(resource.Disposition), maxIdentifierBytes},
			{"observation", string(resource.Observation), maxIdentifierBytes},
			{"cleanup", string(resource.Cleanup), maxIdentifierBytes},
		} {
			if len(field.value) > field.limit {
				return invalidReceipt("resources[%d].%s exceeds %d bytes", i, field.name, field.limit)
			}
		}
		for j, evidence := range resource.Evidence {
			for _, field := range []struct {
				name  string
				value string
				limit int
			}{
				{"ref", evidence.Ref, maxLookupRefBytes},
				{"digest", evidence.Digest, maxDigestBytes},
				{"source", string(evidence.Source), maxIdentifierBytes},
				{"operation_id", evidence.OperationID, maxIdentifierBytes},
				{"runtime_server_id", evidence.RuntimeServerID, maxIdentifierBytes},
				{"provider_id", evidence.ProviderID, maxIdentifierBytes},
				{"capability_snapshot_hash", evidence.CapabilitySnapshotHash, maxDigestBytes},
				{"binding_id", evidence.BindingID, maxIdentifierBytes},
				{"native_ref_hash", evidence.NativeRefHash, maxDigestBytes},
				{"connection_hash", evidence.ConnectionHash, maxDigestBytes},
				{"execution_profile_hash", evidence.ExecutionProfileHash, maxDigestBytes},
				{"resource_graph_hash", evidence.ResourceGraphHash, maxDigestBytes},
				{"subject_hash", evidence.SubjectHash, maxDigestBytes},
				{"observation", string(evidence.Observation), maxIdentifierBytes},
				{"attestation_ref", evidence.AttestationRef, maxLookupRefBytes},
				{"attestation_digest", evidence.AttestationDigest, maxDigestBytes},
			} {
				if len(field.value) > field.limit {
					return invalidReceipt("resources[%d].evidence[%d].%s exceeds %d bytes", i, j, field.name, field.limit)
				}
			}
		}
	}
	return nil
}

func validateTargets(targets []ResourceTarget) error {
	if len(targets) > MaxTargets {
		return invalidCommand("resource targets must not exceed %d", MaxTargets)
	}
	seen := make(map[string]bool, len(targets))
	parents := make(map[string]string, len(targets))
	for _, target := range targets {
		if err := validateIdentifier("target.binding_id", target.BindingID); err != nil {
			return err
		}
		if seen[target.BindingID] {
			return invalidCommand("duplicate target binding_id %q", target.BindingID)
		}
		seen[target.BindingID] = true
		parents[target.BindingID] = target.ParentBindingID
		if err := validateIdentifier("target.kind", target.Kind); err != nil {
			return err
		}
		if err := validateIdentifier("target.native_ref", target.NativeRef); err != nil {
			return err
		}
		if !validDigest(target.OwnershipHash) {
			return invalidCommand("target.ownership_hash must be a sha256 digest")
		}
		if target.Disposition != DispositionDelete {
			return invalidCommand("target.disposition must be %q", DispositionDelete)
		}
	}
	for bindingID, parentID := range parents {
		if parentID != "" && !seen[parentID] {
			return invalidCommand("target %q has unknown parent %q", bindingID, parentID)
		}
	}
	if cycle := findParentCycle(parents); cycle != "" {
		return invalidCommand("target graph contains a cycle at %q", cycle)
	}
	return nil
}

func validateResourceGraph(ctx context.Context, command Command, receipt Receipt, verifier EvidenceVerifier) error {
	if len(receipt.Resources) > MaxResources {
		return invalidReceipt("resources must not exceed %d", MaxResources)
	}
	seen := make(map[string]bool, len(receipt.Resources))
	parents := make(map[string]string, len(receipt.Resources))
	evidenceRefs := make(map[string]bool)
	evidenceDigests := make(map[string]bool)
	attestationRefs := make(map[string]bool)
	attestationDigests := make(map[string]bool)
	totalEvidence := 0
	for _, resource := range receipt.Resources {
		if len(resource.Evidence) > MaxEvidencePerResource {
			return invalidReceipt("resource %q evidence must not exceed %d entries", resource.BindingID, MaxEvidencePerResource)
		}
		totalEvidence += len(resource.Evidence)
		if totalEvidence > MaxEvidencePerReceipt {
			return invalidReceipt("receipt evidence must not exceed %d entries", MaxEvidencePerReceipt)
		}
		if err := validateIdentifier("resource.binding_id", resource.BindingID); err != nil {
			return invalidReceipt("%v", err)
		}
		if seen[resource.BindingID] {
			return invalidReceipt("duplicate resource binding_id %q", resource.BindingID)
		}
		seen[resource.BindingID] = true
		parents[resource.BindingID] = resource.ParentBindingID
		if err := validateIdentifier("resource.kind", resource.Kind); err != nil {
			return invalidReceipt("%v", err)
		}
		if err := validateIdentifier("resource.native_ref", resource.NativeRef); err != nil {
			return invalidReceipt("%v", err)
		}
		if !validDigest(resource.OwnershipHash) || resource.Disposition != DispositionDelete {
			return invalidReceipt("resource %q has invalid ownership or disposition", resource.BindingID)
		}
		if !validObservationCleanup(resource.Observation, resource.Cleanup) {
			return invalidReceipt("resource %q has invalid observation-cleanup combination %q/%q", resource.BindingID, resource.Observation, resource.Cleanup)
		}
		target := targetFromResource(resource)
		for _, evidence := range resource.Evidence {
			if err := validateEvidence(ctx, command, receipt, target, resource, evidence, verifier); err != nil {
				return err
			}
			if evidenceRefs[evidence.Ref] || evidenceDigests[evidence.Digest] ||
				attestationRefs[evidence.AttestationRef] || attestationDigests[evidence.AttestationDigest] {
				return invalidReceipt("evidence and attestations must be unique within a receipt")
			}
			evidenceRefs[evidence.Ref] = true
			evidenceDigests[evidence.Digest] = true
			attestationRefs[evidence.AttestationRef] = true
			attestationDigests[evidence.AttestationDigest] = true
		}
		if observation, ok, err := latestDefinitiveObservation(resource.Evidence); err != nil {
			return err
		} else if ok && resource.Observation != observation {
			return invalidReceipt("resource %q observation %q does not match latest definitive evidence %q", resource.BindingID, resource.Observation, observation)
		}
		if resource.Observation == ObservationAbsent && !hasDefinitiveAbsenceEvidence(resource.Evidence) {
			return fmt.Errorf("%w: resource %q", ErrAbsenceProofRequired, resource.BindingID)
		}
	}
	for bindingID, parentID := range parents {
		if parentID != "" && !seen[parentID] {
			return invalidReceipt("resource %q has unknown parent %q", bindingID, parentID)
		}
	}
	if cycle := findParentCycle(parents); cycle != "" {
		return invalidReceipt("resource graph contains a cycle at %q", cycle)
	}
	return nil
}

func validateEvidence(ctx context.Context, command Command, receipt Receipt, target ResourceTarget, resource ResourceBinding, evidence Evidence, verifier EvidenceVerifier) error {
	if err := validateLookupRef("evidence.ref", evidence.Ref, evidenceRefScheme); err != nil {
		return invalidReceipt("%v", err)
	}
	if err := validateLookupRef("evidence.attestation_ref", evidence.AttestationRef, attestationRefScheme); err != nil {
		return invalidReceipt("%v", err)
	}
	if !validDigest(evidence.Digest) || !validDigest(evidence.AttestationDigest) ||
		!validObservation(evidence.Observation) || evidence.CollectedAt.IsZero() || !evidence.Definitive {
		return invalidReceipt("invalid or non-definitive evidence envelope")
	}
	if evidence.CollectedAt.Before(command.RequestedAt) || evidence.CollectedAt.After(receipt.IssuedAt) {
		return invalidReceipt("evidence collected_at must be within command and receipt time bounds")
	}
	if evidence.OperationID != command.OperationID || evidence.LeaseRevision != command.LeaseRevision ||
		evidence.RuntimeServerID != command.RuntimeServerID || evidence.ProviderID != command.ProviderID ||
		evidence.CapabilitySnapshotHash != command.CapabilitySnapshotHash ||
		evidence.BindingID != resource.BindingID ||
		evidence.NativeRefHash != ComputeNativeRefHash(resource.NativeRef) ||
		evidence.ConnectionHash != command.ConnectionHash ||
		evidence.ExecutionProfileHash != command.ExecutionProfileHash ||
		evidence.ResourceGraphHash != command.ResourceGraphHash ||
		evidence.SubjectHash != ComputeEvidenceSubjectHash(command, target, evidence.Observation) {
		return invalidReceipt("evidence binding does not match command and resource")
	}
	switch evidence.Source {
	case EvidenceSourceProviderAPI, EvidenceSourceProviderEvent:
	case EvidenceSourceAdapter:
		if evidence.Observation == ObservationAbsent {
			return fmt.Errorf("%w: adapter evidence cannot prove resource %q absent", ErrAbsenceProofRequired, resource.BindingID)
		}
	default:
		return invalidReceipt("invalid evidence source %q", evidence.Source)
	}
	if evidence.Observation == ObservationAbsent {
		if isNilEvidenceVerifier(verifier) {
			return fmt.Errorf("%w: evidence verifier required for resource %q", ErrAbsenceProofRequired, resource.BindingID)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: evidence verification context: %w", ErrAbsenceProofRequired, err)
		}
		if err := verifier.VerifyEvidence(ctx, command, target, evidence); err != nil {
			return fmt.Errorf("%w: resource %q: %v", ErrAbsenceProofRequired, resource.BindingID, err)
		}
	}
	return nil
}

func hasDefinitiveAbsenceEvidence(evidence []Evidence) bool {
	for _, item := range evidence {
		if item.Observation == ObservationAbsent && item.Definitive &&
			(item.Source == EvidenceSourceProviderAPI || item.Source == EvidenceSourceProviderEvent) {
			return true
		}
	}
	return false
}

func hasDefinitiveAbsenceEvidenceAtOrAfter(evidence []Evidence, notBefore time.Time) bool {
	for _, item := range evidence {
		if item.Observation == ObservationAbsent && item.Definitive && !item.CollectedAt.Before(notBefore) &&
			(item.Source == EvidenceSourceProviderAPI || item.Source == EvidenceSourceProviderEvent) {
			return true
		}
	}
	return false
}

func hasDefinitiveEvidence(evidence []Evidence, observation ObservationState) bool {
	for _, item := range evidence {
		if item.Observation == observation && item.Definitive {
			return true
		}
	}
	return false
}

// latestDefinitiveObservation returns the observation at the latest evidence
// collection timestamp. Equal timestamps with conflicting observations are
// intentionally rejected: a ledger cannot safely choose a current state from
// an ambiguous observation race.
func latestDefinitiveObservation(evidence []Evidence) (ObservationState, bool, error) {
	var latest Evidence
	found := false
	for _, item := range evidence {
		if !item.Definitive {
			continue
		}
		if !found || item.CollectedAt.After(latest.CollectedAt) {
			latest, found = item, true
			continue
		}
		if item.CollectedAt.Equal(latest.CollectedAt) && item.Observation != latest.Observation {
			return "", false, invalidReceipt("conflicting definitive evidence shares collected_at %s", item.CollectedAt.Format(time.RFC3339Nano))
		}
	}
	if !found {
		return "", false, nil
	}
	return latest.Observation, true, nil
}

func targetFromResource(resource ResourceBinding) ResourceTarget {
	return ResourceTarget{
		BindingID:       resource.BindingID,
		Kind:            resource.Kind,
		NativeRef:       resource.NativeRef,
		ParentBindingID: resource.ParentBindingID,
		OwnershipHash:   resource.OwnershipHash,
		Disposition:     resource.Disposition,
	}
}

func validateGraphContinuity(previous, next []ResourceBinding) error {
	nextByID := make(map[string]ResourceBinding, len(next))
	for _, resource := range next {
		nextByID[resource.BindingID] = resource
	}
	for _, prior := range previous {
		current, ok := nextByID[prior.BindingID]
		if !ok || !sameResourceIdentity(targetFromResource(prior), current) {
			return invalidReceipt("resource graph continuity lost binding %q", prior.BindingID)
		}
		currentEvidence := make(map[Evidence]bool, len(current.Evidence))
		for _, evidence := range current.Evidence {
			currentEvidence[normalizeEvidence(evidence)] = true
		}
		for _, evidence := range prior.Evidence {
			if !currentEvidence[normalizeEvidence(evidence)] {
				return invalidReceipt("resource graph continuity lost evidence for binding %q", prior.BindingID)
			}
		}
	}
	return nil
}

func findParentCycle(parents map[string]string) string {
	const (
		unseen = iota
		visiting
		done
	)
	state := make(map[string]int, len(parents))
	for start := range parents {
		if state[start] != unseen {
			continue
		}
		path := make([]string, 0, len(parents))
		current := start
		for steps := 0; current != "" && steps <= len(parents); steps++ {
			switch state[current] {
			case visiting:
				return current
			case done:
				current = ""
				continue
			}
			state[current] = visiting
			path = append(path, current)
			current = parents[current]
		}
		if current != "" {
			// Parent maps are validated as closed before this helper is called.
			// Keep the traversal fail-closed even if this helper is reused.
			return current
		}
		for _, bindingID := range path {
			state[bindingID] = done
		}
	}
	return ""
}

func isCanonicalCommand(command Command) bool {
	return reflect.DeepEqual(command, normalizeCommand(command))
}

func isCanonicalReceipt(receipt Receipt) bool {
	return reflect.DeepEqual(receipt, normalizeReceipt(receipt))
}

func normalizeCommand(command Command) Command {
	command.APIVersion = strings.TrimSpace(command.APIVersion)
	command.OperationID = strings.TrimSpace(command.OperationID)
	command.Operation = Operation(strings.TrimSpace(string(command.Operation)))
	command.TenantID = strings.TrimSpace(command.TenantID)
	command.LeaseID = strings.TrimSpace(command.LeaseID)
	command.RuntimeServerID = strings.TrimSpace(command.RuntimeServerID)
	command.ResourceGenerationID = normalizeResourceGenerationID(command.ResourceGenerationID)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.ProviderID = strings.TrimSpace(command.ProviderID)
	command.AdapterID = strings.TrimSpace(command.AdapterID)
	command.CustodyRef = strings.TrimSpace(command.CustodyRef)
	command.CustodyHash = strings.TrimSpace(strings.ToLower(command.CustodyHash))
	command.ConnectionRef = strings.TrimSpace(command.ConnectionRef)
	command.ConnectionHash = strings.TrimSpace(strings.ToLower(command.ConnectionHash))
	command.CapabilitySnapshotHash = strings.TrimSpace(strings.ToLower(command.CapabilitySnapshotHash))
	command.ExecutionProfileHash = strings.TrimSpace(strings.ToLower(command.ExecutionProfileHash))
	command.ResourceGraphHash = strings.TrimSpace(strings.ToLower(command.ResourceGraphHash))
	command.DesiredSpecRef = strings.TrimSpace(command.DesiredSpecRef)
	command.DesiredSpecHash = strings.TrimSpace(strings.ToLower(command.DesiredSpecHash))
	command.CommandDigest = strings.TrimSpace(strings.ToLower(command.CommandDigest))
	command.RequestedAt = command.RequestedAt.UTC()
	command.Targets = append([]ResourceTarget(nil), command.Targets...)
	for i := range command.Targets {
		target := &command.Targets[i]
		target.BindingID = strings.TrimSpace(target.BindingID)
		target.Kind = strings.TrimSpace(target.Kind)
		target.NativeRef = strings.TrimSpace(target.NativeRef)
		target.ParentBindingID = strings.TrimSpace(target.ParentBindingID)
		target.OwnershipHash = strings.TrimSpace(strings.ToLower(target.OwnershipHash))
		target.Disposition = ResourceDisposition(strings.TrimSpace(string(target.Disposition)))
	}
	sort.Slice(command.Targets, func(i, j int) bool { return command.Targets[i].BindingID < command.Targets[j].BindingID })
	return command
}

func normalizeReceipt(receipt Receipt) Receipt {
	receipt.APIVersion = strings.TrimSpace(receipt.APIVersion)
	receipt.OperationID = strings.TrimSpace(receipt.OperationID)
	receipt.Operation = Operation(strings.TrimSpace(string(receipt.Operation)))
	receipt.TenantID = strings.TrimSpace(receipt.TenantID)
	receipt.LeaseID = strings.TrimSpace(receipt.LeaseID)
	receipt.RuntimeServerID = strings.TrimSpace(receipt.RuntimeServerID)
	receipt.ResourceGenerationID = normalizeResourceGenerationID(receipt.ResourceGenerationID)
	receipt.IdempotencyKey = strings.TrimSpace(receipt.IdempotencyKey)
	receipt.ProviderID = strings.TrimSpace(receipt.ProviderID)
	receipt.AdapterID = strings.TrimSpace(receipt.AdapterID)
	receipt.CapabilitySnapshotHash = strings.TrimSpace(strings.ToLower(receipt.CapabilitySnapshotHash))
	receipt.CommandDigest = strings.TrimSpace(strings.ToLower(receipt.CommandDigest))
	receipt.DesiredSpecHash = strings.TrimSpace(strings.ToLower(receipt.DesiredSpecHash))
	receipt.PreviousReceiptDigest = strings.TrimSpace(strings.ToLower(receipt.PreviousReceiptDigest))
	receipt.Status = Status(strings.TrimSpace(string(receipt.Status)))
	receipt.Phase = Phase(strings.TrimSpace(string(receipt.Phase)))
	receipt.PhaseEnteredAt = receipt.PhaseEnteredAt.UTC()
	receipt.IssuedAt = receipt.IssuedAt.UTC()
	receipt.ReceiptDigest = strings.TrimSpace(strings.ToLower(receipt.ReceiptDigest))
	receipt.Resources = append([]ResourceBinding(nil), receipt.Resources...)
	for i := range receipt.Resources {
		resource := &receipt.Resources[i]
		resource.BindingID = strings.TrimSpace(resource.BindingID)
		resource.Kind = strings.TrimSpace(resource.Kind)
		resource.NativeRef = strings.TrimSpace(resource.NativeRef)
		resource.ParentBindingID = strings.TrimSpace(resource.ParentBindingID)
		resource.OwnershipHash = strings.TrimSpace(strings.ToLower(resource.OwnershipHash))
		resource.Disposition = ResourceDisposition(strings.TrimSpace(string(resource.Disposition)))
		resource.Observation = ObservationState(strings.TrimSpace(string(resource.Observation)))
		resource.Cleanup = CleanupState(strings.TrimSpace(string(resource.Cleanup)))
		resource.Evidence = append([]Evidence(nil), resource.Evidence...)
		for j := range resource.Evidence {
			resource.Evidence[j] = normalizeEvidence(resource.Evidence[j])
		}
		sort.Slice(resource.Evidence, func(i, j int) bool { return resource.Evidence[i].Ref < resource.Evidence[j].Ref })
	}
	sort.Slice(receipt.Resources, func(i, j int) bool { return receipt.Resources[i].BindingID < receipt.Resources[j].BindingID })
	if receipt.Reason != nil {
		copyReason := *receipt.Reason
		copyReason.Code = strings.TrimSpace(copyReason.Code)
		receipt.Reason = &copyReason
	}
	return receipt
}

func normalizeEvidence(evidence Evidence) Evidence {
	evidence.Ref = strings.TrimSpace(evidence.Ref)
	evidence.Digest = strings.TrimSpace(strings.ToLower(evidence.Digest))
	evidence.Source = EvidenceSource(strings.TrimSpace(string(evidence.Source)))
	evidence.OperationID = strings.TrimSpace(evidence.OperationID)
	evidence.RuntimeServerID = strings.TrimSpace(evidence.RuntimeServerID)
	evidence.ProviderID = strings.TrimSpace(evidence.ProviderID)
	evidence.CapabilitySnapshotHash = strings.TrimSpace(strings.ToLower(evidence.CapabilitySnapshotHash))
	evidence.BindingID = strings.TrimSpace(evidence.BindingID)
	evidence.NativeRefHash = strings.TrimSpace(strings.ToLower(evidence.NativeRefHash))
	evidence.ConnectionHash = strings.TrimSpace(strings.ToLower(evidence.ConnectionHash))
	evidence.ExecutionProfileHash = strings.TrimSpace(strings.ToLower(evidence.ExecutionProfileHash))
	evidence.ResourceGraphHash = strings.TrimSpace(strings.ToLower(evidence.ResourceGraphHash))
	evidence.SubjectHash = strings.TrimSpace(strings.ToLower(evidence.SubjectHash))
	evidence.Observation = ObservationState(strings.TrimSpace(string(evidence.Observation)))
	evidence.AttestationRef = strings.TrimSpace(evidence.AttestationRef)
	evidence.AttestationDigest = strings.TrimSpace(strings.ToLower(evidence.AttestationDigest))
	evidence.CollectedAt = evidence.CollectedAt.UTC()
	return evidence
}

func validateCommandUTF8(command Command) error {
	fields := []struct{ name, value string }{
		{"api_version", command.APIVersion},
		{"operation_id", command.OperationID},
		{"operation", string(command.Operation)},
		{"tenant_id", command.TenantID},
		{"lease_id", command.LeaseID},
		{"runtime_server_id", command.RuntimeServerID},
		{"resource_generation_id", command.ResourceGenerationID},
		{"idempotency_key", command.IdempotencyKey},
		{"provider_id", command.ProviderID},
		{"adapter_id", command.AdapterID},
		{"custody_ref", command.CustodyRef},
		{"custody_hash", command.CustodyHash},
		{"connection_ref", command.ConnectionRef},
		{"connection_hash", command.ConnectionHash},
		{"capability_snapshot_hash", command.CapabilitySnapshotHash},
		{"execution_profile_hash", command.ExecutionProfileHash},
		{"resource_graph_hash", command.ResourceGraphHash},
		{"desired_spec_ref", command.DesiredSpecRef},
		{"desired_spec_hash", command.DesiredSpecHash},
		{"command_digest", command.CommandDigest},
	}
	for _, field := range fields {
		if !utf8.ValidString(field.value) {
			return invalidCommand("%s must be valid UTF-8", field.name)
		}
	}
	for i, target := range command.Targets {
		for _, field := range []struct{ name, value string }{
			{"binding_id", target.BindingID},
			{"kind", target.Kind},
			{"native_ref", target.NativeRef},
			{"parent_binding_id", target.ParentBindingID},
			{"ownership_hash", target.OwnershipHash},
			{"disposition", string(target.Disposition)},
		} {
			if !utf8.ValidString(field.value) {
				return invalidCommand("targets[%d].%s must be valid UTF-8", i, field.name)
			}
		}
	}
	return nil
}

func validateReceiptUTF8(receipt Receipt) error {
	fields := []struct{ name, value string }{
		{"api_version", receipt.APIVersion},
		{"operation_id", receipt.OperationID},
		{"operation", string(receipt.Operation)},
		{"tenant_id", receipt.TenantID},
		{"lease_id", receipt.LeaseID},
		{"runtime_server_id", receipt.RuntimeServerID},
		{"resource_generation_id", receipt.ResourceGenerationID},
		{"idempotency_key", receipt.IdempotencyKey},
		{"provider_id", receipt.ProviderID},
		{"adapter_id", receipt.AdapterID},
		{"capability_snapshot_hash", receipt.CapabilitySnapshotHash},
		{"command_digest", receipt.CommandDigest},
		{"desired_spec_hash", receipt.DesiredSpecHash},
		{"previous_receipt_digest", receipt.PreviousReceiptDigest},
		{"status", string(receipt.Status)},
		{"phase", string(receipt.Phase)},
		{"receipt_digest", receipt.ReceiptDigest},
	}
	for _, field := range fields {
		if !utf8.ValidString(field.value) {
			return invalidReceipt("%s must be valid UTF-8", field.name)
		}
	}
	if receipt.Reason != nil {
		if !utf8.ValidString(receipt.Reason.Code) {
			return invalidReceipt("reason.code must be valid UTF-8")
		}
	}
	for i, resource := range receipt.Resources {
		for _, field := range []struct{ name, value string }{
			{"binding_id", resource.BindingID},
			{"kind", resource.Kind},
			{"native_ref", resource.NativeRef},
			{"parent_binding_id", resource.ParentBindingID},
			{"ownership_hash", resource.OwnershipHash},
			{"disposition", string(resource.Disposition)},
			{"observation", string(resource.Observation)},
			{"cleanup", string(resource.Cleanup)},
		} {
			if !utf8.ValidString(field.value) {
				return invalidReceipt("resources[%d].%s must be valid UTF-8", i, field.name)
			}
		}
		for j, evidence := range resource.Evidence {
			for _, field := range []struct{ name, value string }{
				{"ref", evidence.Ref},
				{"digest", evidence.Digest},
				{"source", string(evidence.Source)},
				{"operation_id", evidence.OperationID},
				{"runtime_server_id", evidence.RuntimeServerID},
				{"provider_id", evidence.ProviderID},
				{"capability_snapshot_hash", evidence.CapabilitySnapshotHash},
				{"binding_id", evidence.BindingID},
				{"native_ref_hash", evidence.NativeRefHash},
				{"connection_hash", evidence.ConnectionHash},
				{"execution_profile_hash", evidence.ExecutionProfileHash},
				{"resource_graph_hash", evidence.ResourceGraphHash},
				{"subject_hash", evidence.SubjectHash},
				{"observation", string(evidence.Observation)},
				{"attestation_ref", evidence.AttestationRef},
				{"attestation_digest", evidence.AttestationDigest},
			} {
				if !utf8.ValidString(field.value) {
					return invalidReceipt("resources[%d].evidence[%d].%s must be valid UTF-8", i, j, field.name)
				}
			}
		}
	}
	return nil
}

func validateIdentifier(name, value string) error {
	if containsControlOrFormat(value) {
		return invalidCommand("%s must not contain control or format characters", name)
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxIdentifierBytes {
		return invalidCommand("%s invalid or missing", name)
	}
	return nil
}

func validateLookupRef(name, value, scheme string) error {
	if containsControlOrFormat(value) {
		return invalidCommand("%s must not contain control or format characters", name)
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxLookupRefBytes {
		return invalidCommand("%s invalid or missing", name)
	}
	// Lookup references must be hierarchical server-side identifiers. Rejecting
	// opaque URIs and all escaping keeps raw tokens/credentials out of the
	// command envelope rather than merely excluding URI userinfo.
	if strings.Contains(value, "%") || strings.Contains(value, "#") {
		return invalidCommand("%s must not contain URI escapes or fragment delimiters", name)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != scheme || parsed.Opaque != "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawPath != "" ||
		parsed.Host == "" || parsed.Port() != "" || parsed.Host != parsed.Hostname() {
		return invalidCommand("%s must be a hierarchical %s server lookup reference without credentials, query, fragment, port, or escapes", name, scheme)
	}
	if err := validateLookupHost(parsed.Host); err != nil {
		return invalidCommand("%s invalid lookup host: %v", name, err)
	}
	if err := validateLookupPath(parsed.Path); err != nil {
		return invalidCommand("%s invalid lookup path: %v", name, err)
	}
	return nil
}

const (
	maxLookupHostLength    = 253
	maxLookupSegmentLength = 128
)

func validateLookupHost(host string) error {
	if len(host) == 0 || len(host) > maxLookupHostLength {
		return errors.New("host length")
	}
	for _, segment := range strings.Split(host, ".") {
		if err := validateLookupSegment(segment, 63); err != nil {
			return err
		}
	}
	return nil
}

func validateLookupPath(path string) error {
	if path == "" || path == "/" || !strings.HasPrefix(path, "/") {
		return errors.New("path required")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if err := validateLookupSegment(segment, maxLookupSegmentLength); err != nil {
			return err
		}
	}
	return nil
}

func validateLookupSegment(segment string, maximum int) error {
	if segment == "" || segment == "." || segment == ".." || len(segment) > maximum {
		return errors.New("empty, dot, or oversized segment")
	}
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		if c > 0x7f || !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || (i > 0 && (c == '.' || c == '_' || c == '-'))) {
			return errors.New("segment must match [A-Za-z0-9][A-Za-z0-9._-]*")
		}
	}
	return nil
}

func containsControlOrFormat(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return true
		}
	}
	return false
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, digestPrefix) || len(value) != len(digestPrefix)+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, digestPrefix))
	return err == nil
}

func validJSONInteger(value uint64) bool {
	return value > 0 && value <= MaxJSONSafeInteger
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return invalidReceipt("validation context required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: validation context: %w", ErrInvalidReceipt, err)
	}
	return nil
}

func isNilEvidenceVerifier(verifier EvidenceVerifier) bool {
	if verifier == nil {
		return true
	}
	value := reflect.ValueOf(verifier)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func normalizeResourceGenerationID(value string) string {
	value = strings.TrimSpace(value)
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil {
		return value
	}
	return parsed.String()
}

func validResourceGenerationID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}

func validOperation(value Operation) bool {
	switch value {
	case OperationPlan, OperationProvision, OperationObserve, OperationReconcile, OperationDecommission:
		return true
	default:
		return false
	}
}

func validStatus(value Status) bool {
	switch value {
	case StatusPending, StatusSucceeded, StatusFailed, StatusDenied:
		return true
	default:
		return false
	}
}

func validObservation(value ObservationState) bool {
	return value == ObservationPresent || value == ObservationAbsent || value == ObservationUnknown
}

func validObservationCleanup(observation ObservationState, cleanup CleanupState) bool {
	switch observation {
	case ObservationPresent:
		return cleanup == CleanupRequired || cleanup == CleanupPending || cleanup == CleanupBlocked
	case ObservationAbsent:
		return cleanup == CleanupComplete || cleanup == CleanupNotRequired
	case ObservationUnknown:
		return cleanup == CleanupRequired || cleanup == CleanupPending || cleanup == CleanupBlocked
	default:
		return false
	}
}

func validCode(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 160 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return !strings.HasPrefix(value, "provider.") || IsProviderReasonCode(value)
}

func invalidCommand(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidCommand, fmt.Sprintf(format, args...))
}

func invalidReceipt(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidReceipt, fmt.Sprintf(format, args...))
}
