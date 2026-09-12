package localevidence

import (
	"encoding/json"
	"errors"
	"time"
)

const workloadPeerOperationDomain = "stackkit.owner-workload-peer-operation/v1\x00"

// WorkloadPeerOperation carries exact local-owner authorization, independent of
// the management API credential. The signature is never produced by an HTTP
// endpoint. This proof is owner key custody, not a claim of human/device step-up.
type WorkloadPeerOperation struct {
	Operation     string                    `json:"operation"`
	Admission     *WorkloadPeerAdmission    `json:"admission,omitempty"`
	PeerRef       string                    `json:"peerRef,omitempty"`
	IssuedAt      time.Time                 `json:"issuedAt"`
	ExpiresAt     time.Time                 `json:"expiresAt"`
	PreviousState string                    `json:"previousState"`
	Signature     OwnerPolicyStateSignature `json:"signature"`
}

func SignWorkloadPeerOperation(workspaceRoot string, operation WorkloadPeerOperation) (WorkloadPeerOperation, error) {
	operation.Signature = OwnerPolicyStateSignature{}
	if err := validateWorkloadPeerOperation(operation, time.Now().UTC()); err != nil {
		return operation, err
	}
	peerRef := operation.PeerRef
	if operation.Admission != nil {
		peerRef = operation.Admission.PeerRef
	}
	previous, err := currentWorkloadPeerCommitment(workspaceRoot, peerRef)
	if err != nil {
		return operation, err
	}
	operation.PreviousState = previous
	raw, err := json.Marshal(operation)
	if err != nil {
		return operation, err
	}
	value, owner, key, err := signOwnerRestore(workspaceRoot, raw, workloadPeerOperationDomain, "workload peer operation")
	if err != nil {
		return operation, err
	}
	operation.Signature = OwnerPolicyStateSignature{OwnerRef: owner, KeyID: key, Value: value}
	return operation, nil
}

// ApplyWorkloadPeerOperation rejects absent, stale or body-mismatched owner
// approval before performing a custody mutation. Retired certificate tombstones
// prevent replayed admission from undoing revocation or key rotation.
func ApplyWorkloadPeerOperation(workspaceRoot string, operation WorkloadPeerOperation) error {
	if err := validateWorkloadPeerOperation(operation, time.Now().UTC()); err != nil {
		return err
	}
	signature := operation.Signature
	operation.Signature = OwnerPolicyStateSignature{}
	raw, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	if err := verifyOwnerRestore(workspaceRoot, raw, workloadPeerOperationDomain, signature.OwnerRef, signature.KeyID, signature.Value, "workload peer operation"); err != nil {
		return err
	}
	if operation.Operation == "admit" {
		return admitWorkloadPeer(workspaceRoot, *operation.Admission, &operation.PreviousState)
	}
	return revokeWorkloadPeer(workspaceRoot, operation.PeerRef, &operation.PreviousState)
}

func validateWorkloadPeerOperation(operation WorkloadPeerOperation, now time.Time) error {
	if operation.IssuedAt.IsZero() || operation.IssuedAt.After(now) || !operation.ExpiresAt.After(now) || operation.ExpiresAt.Sub(operation.IssuedAt) > 5*time.Minute {
		return errors.New("localevidence: workload peer operation requires fresh bounded owner approval")
	}
	switch operation.Operation {
	case "admit":
		if operation.Admission == nil || operation.PeerRef != "" {
			return errors.New("localevidence: admission operation is incomplete")
		}
	case "revoke":
		if operation.Admission != nil || !validWorkloadPeerRef(operation.PeerRef) {
			return errors.New("localevidence: revocation operation is incomplete")
		}
	default:
		return errors.New("localevidence: workload peer operation is unsupported")
	}
	return nil
}
