package localevidence

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
)

const remoteActionDomain = "stackkit.home-remote-action/v1\x00"

// SignRemoteAction uses the established Home owner key with a dedicated domain.
// It is authorization of exact bytes, never evidence of human step-up.
func SignRemoteAction(root string, canonical []byte) (OwnerPolicyStateSignature, error) {
	value, owner, key, err := signOwnerRestore(root, canonical, remoteActionDomain, "remote action")
	return OwnerPolicyStateSignature{OwnerRef: owner, KeyID: key, Value: value}, err
}

// VerifyRemoteAction accepts only the public Home key admitted by the receiver's
// local owner. Callers must re-read that admission and its withdrawal state.
func VerifyRemoteAction(canonical []byte, signature OwnerPolicyStateSignature, ownerRef, keyID string, public ed25519.PublicKey) error {
	value, err := base64.RawStdEncoding.Strict().DecodeString(signature.Value)
	if len(canonical) == 0 || len(public) != ed25519.PublicKeySize || err != nil ||
		signature.OwnerRef != ownerRef || signature.KeyID != keyID ||
		!ed25519.Verify(public, ownerRestoreDigest(remoteActionDomain, canonical), value) {
		return errors.New("localevidence: remote action does not verify against admitted Home authority")
	}
	return nil
}
