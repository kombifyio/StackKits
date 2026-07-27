package localevidence

const ownerAdvancedTrustDomain = "stackkit.owner-signed-local-advanced-trust/v1\x00"

// OwnerAdvancedTrustSignature authenticates the exact locally accepted
// Advanced trust record. Its domain prevents replay as another lifecycle
// approval or evidence record.
type OwnerAdvancedTrustSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

// SignOwnerAdvancedTrust signs one canonical, secret-free Advanced trust
// record with the established local Owner evidence key.
func SignOwnerAdvancedTrust(
	workspaceRoot string,
	canonicalRecord []byte,
) (OwnerAdvancedTrustSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(
		workspaceRoot,
		canonicalRecord,
		ownerAdvancedTrustDomain,
		"Advanced trust record",
	)
	if err != nil {
		return OwnerAdvancedTrustSignature{}, err
	}
	return OwnerAdvancedTrustSignature{OwnerRef: ownerRef, KeyID: keyID, Value: value}, nil
}

// VerifyOwnerAdvancedTrust verifies the record against current local Owner
// custody without exposing the Owner private key.
func VerifyOwnerAdvancedTrust(
	workspaceRoot string,
	canonicalRecord []byte,
	signature OwnerAdvancedTrustSignature,
) error {
	return verifyOwnerRestore(
		workspaceRoot,
		canonicalRecord,
		ownerAdvancedTrustDomain,
		signature.OwnerRef,
		signature.KeyID,
		signature.Value,
		"Advanced trust record",
	)
}
