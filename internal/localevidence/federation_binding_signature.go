package localevidence

const ownerFederationBindingAdmissionDomain = "stackkit.owner-federation-link-binding-admission/v1\x00"

// OwnerFederationBindingAdmissionSignature authenticates one exact local
// adoption of an opaque external Federation-link binding. It cannot be
// replayed as Apply, backup, restore, lifecycle, or Advanced evidence.
type OwnerFederationBindingAdmissionSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

func SignOwnerFederationBindingAdmission(workspaceRoot string, canonicalAdmission []byte) (OwnerFederationBindingAdmissionSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(
		workspaceRoot,
		canonicalAdmission,
		ownerFederationBindingAdmissionDomain,
		"Federation-link binding admission",
	)
	if err != nil {
		return OwnerFederationBindingAdmissionSignature{}, err
	}
	return OwnerFederationBindingAdmissionSignature{OwnerRef: ownerRef, KeyID: keyID, Value: value}, nil
}

func VerifyOwnerFederationBindingAdmission(workspaceRoot string, canonicalAdmission []byte, signature OwnerFederationBindingAdmissionSignature) error {
	return verifyOwnerRestore(
		workspaceRoot,
		canonicalAdmission,
		ownerFederationBindingAdmissionDomain,
		signature.OwnerRef,
		signature.KeyID,
		signature.Value,
		"Federation-link binding admission",
	)
}
