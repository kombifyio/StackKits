package localevidence

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
