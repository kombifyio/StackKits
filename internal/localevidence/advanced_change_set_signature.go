package localevidence

const ownerAdvancedChangeSetDomain = "stackkit.owner-signed-advanced-change-set/v1\x00"

// OwnerAdvancedChangeSetSignature authenticates the exact canonical,
// secret-free Advanced change set with the current local Owner custody.
type OwnerAdvancedChangeSetSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

// SignOwnerAdvancedChangeSet signs one canonical Advanced change set without
// exposing the Owner private key.
func SignOwnerAdvancedChangeSet(
	workspaceRoot string,
	canonicalUnsigned []byte,
) (OwnerAdvancedChangeSetSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(
		workspaceRoot,
		canonicalUnsigned,
		ownerAdvancedChangeSetDomain,
		"Advanced change set",
	)
	if err != nil {
		return OwnerAdvancedChangeSetSignature{}, err
	}
	return OwnerAdvancedChangeSetSignature{
		OwnerRef: ownerRef,
		KeyID:    keyID,
		Value:    value,
	}, nil
}

// VerifyOwnerAdvancedChangeSet verifies the exact canonical Advanced change
// set against current local Owner custody without exposing its private key.
func VerifyOwnerAdvancedChangeSet(
	workspaceRoot string,
	canonicalUnsigned []byte,
	signature OwnerAdvancedChangeSetSignature,
) error {
	return verifyOwnerRestore(
		workspaceRoot,
		canonicalUnsigned,
		ownerAdvancedChangeSetDomain,
		signature.OwnerRef,
		signature.KeyID,
		signature.Value,
		"Advanced change set",
	)
}
