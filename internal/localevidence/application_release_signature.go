package localevidence

const ownerApplicationReleaseDomain = "stackkit.owner-signed-application-release/v1\x00"

// OwnerApplicationReleaseSignature binds one scoped application release
// checkpoint or rollback record to the established local Owner key.
type OwnerApplicationReleaseSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

func SignOwnerApplicationRelease(workspace string, canonical []byte) (OwnerApplicationReleaseSignature, error) {
	value, owner, key, err := signOwnerRestore(workspace, canonical, ownerApplicationReleaseDomain, "application release record")
	return OwnerApplicationReleaseSignature{OwnerRef: owner, KeyID: key, Value: value}, err
}

func VerifyOwnerApplicationRelease(workspace string, canonical []byte, signature OwnerApplicationReleaseSignature) error {
	return verifyOwnerRestore(workspace, canonical, ownerApplicationReleaseDomain, signature.OwnerRef, signature.KeyID, signature.Value, "application release record")
}
