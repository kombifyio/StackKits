package localevidence

const (
	ownerIdentityProjectionApprovalDomain = "stackkit.owner-identity-projection-approval/v1\x00"
	ownerIdentityProjectionReceiptDomain  = "stackkit.owner-identity-projection-receipt/v1\x00"
)

// OwnerIdentityProjectionSignature binds a local approval or terminal receipt
// to current Owner custody without exporting the Owner private key.
type OwnerIdentityProjectionSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

func SignOwnerIdentityProjectionApproval(
	workspaceRoot string,
	canonicalApproval []byte,
) (OwnerIdentityProjectionSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(
		workspaceRoot,
		canonicalApproval,
		ownerIdentityProjectionApprovalDomain,
		"identity projection approval",
	)
	if err != nil {
		return OwnerIdentityProjectionSignature{}, err
	}
	return OwnerIdentityProjectionSignature{
		OwnerRef: ownerRef, KeyID: keyID, Value: value,
	}, nil
}

func VerifyOwnerIdentityProjectionApproval(
	workspaceRoot string,
	canonicalApproval []byte,
	signature OwnerIdentityProjectionSignature,
) error {
	return verifyOwnerRestore(
		workspaceRoot,
		canonicalApproval,
		ownerIdentityProjectionApprovalDomain,
		signature.OwnerRef,
		signature.KeyID,
		signature.Value,
		"identity projection approval",
	)
}

func SignOwnerIdentityProjectionReceipt(
	workspaceRoot string,
	canonicalReceipt []byte,
) (OwnerIdentityProjectionSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(
		workspaceRoot,
		canonicalReceipt,
		ownerIdentityProjectionReceiptDomain,
		"identity projection receipt",
	)
	if err != nil {
		return OwnerIdentityProjectionSignature{}, err
	}
	return OwnerIdentityProjectionSignature{
		OwnerRef: ownerRef, KeyID: keyID, Value: value,
	}, nil
}

func VerifyOwnerIdentityProjectionReceipt(
	workspaceRoot string,
	canonicalReceipt []byte,
	signature OwnerIdentityProjectionSignature,
) error {
	return verifyOwnerRestore(
		workspaceRoot,
		canonicalReceipt,
		ownerIdentityProjectionReceiptDomain,
		signature.OwnerRef,
		signature.KeyID,
		signature.Value,
		"identity projection receipt",
	)
}
