package localevidence

const ownerRelocationDomain = "stackkit.owner-signed-relocation-receipt/v1\x00"

type OwnerRelocationSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

func SignOwnerRelocation(workspace string, canonical []byte) (OwnerRelocationSignature, error) {
	value, owner, key, err := signOwnerRestore(workspace, canonical, ownerRelocationDomain, "relocation receipt")
	return OwnerRelocationSignature{OwnerRef: owner, KeyID: key, Value: value}, err
}

func VerifyOwnerRelocation(workspace string, canonical []byte, signature OwnerRelocationSignature) error {
	return verifyOwnerRestore(workspace, canonical, ownerRelocationDomain, signature.OwnerRef, signature.KeyID, signature.Value, "relocation receipt")
}
