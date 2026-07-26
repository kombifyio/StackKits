package localevidence

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

const ownerApplyResultDomain = "stackkit.owner-signed-apply-result/v1\x00"

type OwnerApplyResultSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

// SignOwnerApplyResult authenticates the complete canonical runtime result,
// including its post-Apply observations, without exposing the private key.
func SignOwnerApplyResult(workspaceRoot string, canonicalResult []byte) (OwnerApplyResultSignature, error) {
	if len(canonicalResult) == 0 {
		return OwnerApplyResultSignature{}, errors.New("localevidence: owner Apply result is empty")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return OwnerApplyResultSignature{}, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return OwnerApplyResultSignature{}, err
	}
	if owner.OwnerRef != key.OwnerRef || owner.KeyID != key.KeyID {
		return OwnerApplyResultSignature{}, errors.New("localevidence: Apply result signer differs from established owner")
	}
	return OwnerApplyResultSignature{
		OwnerRef: owner.OwnerRef, KeyID: owner.KeyID,
		Value: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, ownerApplyResultDigest(canonicalResult))),
	}, nil
}

// VerifyOwnerApplyResult verifies the complete result against current custody.
func VerifyOwnerApplyResult(workspaceRoot string, canonicalResult []byte, signature OwnerApplyResultSignature) error {
	if len(canonicalResult) == 0 || signature.OwnerRef == "" || signature.KeyID == "" {
		return errors.New("localevidence: owner Apply result signature is incomplete")
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
		return errors.New("localevidence: owner Apply result signature is not bound to established custody")
	}
	value, err := base64.RawStdEncoding.Strict().DecodeString(signature.Value)
	if err != nil || len(value) != ed25519.SignatureSize ||
		!ed25519.Verify(key.Public(), ownerApplyResultDigest(canonicalResult), value) {
		return errors.New("localevidence: owner Apply result signature does not verify")
	}
	return nil
}

func ownerApplyResultDigest(canonicalResult []byte) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte(ownerApplyResultDomain))
	_, _ = digest.Write(canonicalResult)
	return digest.Sum(nil)
}
