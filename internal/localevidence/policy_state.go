package localevidence

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

const ownerPolicyStateDomain = "stackkit.owner-bound-policy-state/v1\x00"

type OwnerPolicyStateSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

// SignOwnerPolicyState signs one canonical, secret-free local policy state
// without exposing the owner's private key outside this package.
func SignOwnerPolicyState(workspaceRoot string, canonicalState []byte) (OwnerPolicyStateSignature, error) {
	if len(canonicalState) == 0 {
		return OwnerPolicyStateSignature{}, errors.New("localevidence: owner policy state is empty")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return OwnerPolicyStateSignature{}, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return OwnerPolicyStateSignature{}, err
	}
	if owner.OwnerRef != key.OwnerRef || owner.KeyID != key.KeyID {
		return OwnerPolicyStateSignature{}, errors.New("localevidence: policy state signer differs from established owner")
	}
	digest := ownerPolicyStateDigest(canonicalState)
	return OwnerPolicyStateSignature{
		OwnerRef: owner.OwnerRef, KeyID: owner.KeyID,
		Value: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, digest)),
	}, nil
}

// VerifyOwnerPolicyState verifies the signature and current owner binding.
func VerifyOwnerPolicyState(workspaceRoot string, canonicalState []byte, signature OwnerPolicyStateSignature) error {
	if len(canonicalState) == 0 || signature.OwnerRef == "" || signature.KeyID == "" {
		return errors.New("localevidence: owner policy state signature is incomplete")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return err
	}
	if signature.OwnerRef != owner.OwnerRef || signature.KeyID != owner.KeyID ||
		key.OwnerRef != owner.OwnerRef || key.KeyID != owner.KeyID {
		return errors.New("localevidence: owner policy state signature is not bound to the established owner")
	}
	value, err := base64.RawStdEncoding.Strict().DecodeString(signature.Value)
	if err != nil || len(value) != ed25519.SignatureSize ||
		!ed25519.Verify(key.Public(), ownerPolicyStateDigest(canonicalState), value) {
		return errors.New("localevidence: owner policy state signature does not verify")
	}
	return nil
}

func ownerPolicyStateDigest(canonicalState []byte) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte(ownerPolicyStateDomain))
	_, _ = digest.Write(canonicalState)
	return digest.Sum(nil)
}
