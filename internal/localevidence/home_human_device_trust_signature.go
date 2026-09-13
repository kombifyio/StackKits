package localevidence

const homeHumanDeviceTrustDomain = "stackkit.home-human-device-trust/v1\x00"

// SignHomeHumanDeviceTrust authenticates current Home public verifier material.
// It cannot mint a PocketID identity, enroll a device, or replace step-ca.
func SignHomeHumanDeviceTrust(workspaceRoot string, canonical []byte) (OwnerPolicyStateSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(workspaceRoot, canonical, homeHumanDeviceTrustDomain, "Home human-device trust")
	return OwnerPolicyStateSignature{OwnerRef: ownerRef, KeyID: keyID, Value: value}, err
}

// VerifyHomeHumanDeviceTrust checks the current local Owner signature over
// public Home verifier material.
func VerifyHomeHumanDeviceTrust(workspaceRoot string, canonical []byte, signature OwnerPolicyStateSignature) error {
	return verifyOwnerRestore(workspaceRoot, canonical, homeHumanDeviceTrustDomain, signature.OwnerRef, signature.KeyID, signature.Value, "Home human-device trust")
}
