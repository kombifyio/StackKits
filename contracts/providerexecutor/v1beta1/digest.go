package providerexecutor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const digestPrefix = "sha256:"

// ComputeOperationID derives the stable, generation-bound operation identity
// used for retries. Reusing an idempotency key in a later resource generation
// therefore cannot address or claim an earlier generation's ledger.
func ComputeOperationID(tenantID, leaseID, resourceGenerationID string, operation Operation, idempotencyKey string) string {
	values := []string{
		strings.TrimSpace(tenantID),
		strings.TrimSpace(leaseID),
		normalizeResourceGenerationID(resourceGenerationID),
		strings.TrimSpace(string(operation)),
		strings.TrimSpace(idempotencyKey),
	}
	digest := sha256ScopeBytes(values...)
	return "op_" + hex.EncodeToString(digest[:16])
}

// ComputeCommandDigest hashes the immutable, idempotency-relevant command
// body. The digest field itself is excluded; RequestedAt remains part of the
// immutable authorization envelope and must not drift across retries.
func ComputeCommandDigest(command Command) (string, error) {
	if err := preflightCommandBounds(command); err != nil {
		return "", err
	}
	if err := validateCommandUTF8(command); err != nil {
		return "", err
	}
	command = normalizeCommand(command)
	command.RequestedAt = command.RequestedAt.UTC()
	command.CommandDigest = ""
	type digestEnvelope struct {
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
	}
	payload, err := json.Marshal(digestEnvelope{
		APIVersion: command.APIVersion, OperationID: command.OperationID, Operation: command.Operation,
		TenantID: command.TenantID, LeaseID: command.LeaseID, LeaseRevision: command.LeaseRevision,
		RuntimeServerID: command.RuntimeServerID, ResourceGenerationID: command.ResourceGenerationID,
		IdempotencyKey: command.IdempotencyKey, ProviderID: command.ProviderID, AdapterID: command.AdapterID,
		CustodyRef: command.CustodyRef, CustodyHash: command.CustodyHash,
		ConnectionRef: command.ConnectionRef, ConnectionHash: command.ConnectionHash,
		CapabilitySnapshotHash: command.CapabilitySnapshotHash, ExecutionProfileHash: command.ExecutionProfileHash,
		ResourceGraphHash: command.ResourceGraphHash,
		LedgerRevision:    command.LedgerRevision, DesiredSpecRef: command.DesiredSpecRef,
		DesiredSpecHash: command.DesiredSpecHash, Targets: command.Targets, RequestedAt: command.RequestedAt,
	})
	if err != nil {
		return "", fmt.Errorf("providerexecutor: marshal command digest: %w", err)
	}
	return sha256Digest(payload), nil
}

// ComputeNativeRefHash binds evidence to one provider-native resource handle
// without copying that handle into an attestation subject.
func ComputeNativeRefHash(nativeRef string) string {
	return sha256String(strings.TrimSpace(nativeRef))
}

// ComputeResourceGraphHash derives the canonical digest of the immutable
// resource targets authorized by a command. Target order and surrounding
// whitespace do not affect the result; malformed targets are rejected before
// a digest is returned. An empty graph has a stable digest of the empty JSON
// array rather than a caller-selected placeholder.
func ComputeResourceGraphHash(targets []ResourceTarget) (string, error) {
	if err := preflightTargetBounds(targets); err != nil {
		return "", err
	}
	canonical := normalizeCommand(Command{Targets: targets}).Targets
	if err := validateTargets(canonical); err != nil {
		return "", err
	}
	if canonical == nil {
		canonical = []ResourceTarget{}
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("providerexecutor: marshal resource graph digest: %w", err)
	}
	return sha256Digest(payload), nil
}

// ComputeEvidenceSubjectHash binds definitive evidence to the immutable
// command, resource ownership, and requested observation.
func ComputeEvidenceSubjectHash(command Command, target ResourceTarget, observation ObservationState) string {
	return sha256Scope(
		command.CommandDigest,
		command.OperationID,
		string(command.Operation),
		command.TenantID,
		command.LeaseID,
		strconv.FormatUint(command.LeaseRevision, 10),
		command.RuntimeServerID,
		command.ResourceGenerationID,
		command.ProviderID,
		command.CapabilitySnapshotHash,
		target.BindingID,
		ComputeNativeRefHash(target.NativeRef),
		target.OwnershipHash,
		string(target.Disposition),
		command.ConnectionHash,
		command.ExecutionProfileHash,
		command.ResourceGraphHash,
		string(observation),
	)
}

// ComputeReceiptDigest hashes a receipt excluding ReceiptDigest.
func ComputeReceiptDigest(receipt Receipt) (string, error) {
	if err := preflightReceiptBounds(receipt); err != nil {
		return "", err
	}
	if err := validateReceiptUTF8(receipt); err != nil {
		return "", err
	}
	receipt = normalizeReceipt(receipt)
	receipt.ReceiptDigest = ""
	payload, err := json.Marshal(receipt)
	if err != nil {
		return "", fmt.Errorf("providerexecutor: marshal receipt digest: %w", err)
	}
	return sha256Digest(payload), nil
}

func sha256Digest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return digestPrefix + hex.EncodeToString(digest[:])
}

func sha256String(value string) string {
	digest := sha256.New()
	_, _ = io.WriteString(digest, value)
	return digestPrefix + hex.EncodeToString(digest.Sum(nil))
}

func sha256Scope(values ...string) string {
	digest := sha256ScopeBytes(values...)
	return digestPrefix + hex.EncodeToString(digest[:])
}

func sha256ScopeBytes(values ...string) [sha256.Size]byte {
	hash := sha256.New()
	separator := [1]byte{0}
	for i, value := range values {
		if i > 0 {
			_, _ = hash.Write(separator[:])
		}
		_, _ = io.WriteString(hash, value)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}
