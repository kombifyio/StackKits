package localevidence

import "errors"

const ownerLifecycleMutationDomain = "stackkit.owner-signed-lifecycle-mutation/v1\x00"

// OwnerLifecycleMutationSignature authenticates one local lifecycle mutation
// journal. The domain is distinct from Apply, backup, and recovery evidence.
type OwnerLifecycleMutationSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

func SignOwnerLifecycleMutation(
	workspaceRoot string,
	canonicalRecord []byte,
) (OwnerLifecycleMutationSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(
		workspaceRoot,
		canonicalRecord,
		ownerLifecycleMutationDomain,
		"lifecycle mutation",
	)
	if err != nil {
		return OwnerLifecycleMutationSignature{}, err
	}
	return OwnerLifecycleMutationSignature{
		OwnerRef: ownerRef,
		KeyID:    keyID,
		Value:    value,
	}, nil
}

func VerifyOwnerLifecycleMutation(
	workspaceRoot string,
	canonicalRecord []byte,
	signature OwnerLifecycleMutationSignature,
) error {
	return verifyOwnerRestore(
		workspaceRoot,
		canonicalRecord,
		ownerLifecycleMutationDomain,
		signature.OwnerRef,
		signature.KeyID,
		signature.Value,
		"lifecycle mutation",
	)
}

// VerifyLifecycleMutation verifies node custody in the restricted Home core,
// which holds the pinned public Authority record and never the Owner key.
func (r HomeAuthorityPublic) VerifyLifecycleMutation(canonical []byte, signature OwnerLifecycleMutationSignature) error {
	public, err := r.ownerKey()
	if err != nil {
		return err
	}
	if !verifyPinnedOwnerSignature(ownerLifecycleMutationDomain, canonical, OwnerPolicyStateSignature{OwnerRef: signature.OwnerRef, KeyID: signature.KeyID, Value: signature.Value}, r.OwnerRef, r.KeyID, public) {
		return errors.New("localevidence: own application custody signature does not verify under the pinned Authority")
	}
	return nil
}
