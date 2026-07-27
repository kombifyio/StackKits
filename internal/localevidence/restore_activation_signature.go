package localevidence

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
)

const ownerRestoreActivationDomain = "stackkit.owner-signed-restore-activation-result/v1\x00"

// OwnerRestoreActivationSignature authenticates the terminal result of moving
// one verified staged restore into the exact plan-owned live volumes.
type OwnerRestoreActivationSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

func SignOwnerRestoreActivation(
	workspaceRoot string,
	canonicalResult []byte,
) (OwnerRestoreActivationSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(
		workspaceRoot,
		canonicalResult,
		ownerRestoreActivationDomain,
		"restore activation result",
	)
	if err != nil {
		return OwnerRestoreActivationSignature{}, err
	}
	return OwnerRestoreActivationSignature{
		OwnerRef: ownerRef,
		KeyID:    keyID,
		Value:    value,
	}, nil
}

func VerifyOwnerRestoreActivation(
	workspaceRoot string,
	canonicalResult []byte,
	signature OwnerRestoreActivationSignature,
) error {
	if len(canonicalResult) == 0 {
		return errors.New("localevidence: owner restore activation result is empty")
	}
	return verifyOwnerRestore(
		workspaceRoot,
		canonicalResult,
		ownerRestoreActivationDomain,
		signature.OwnerRef,
		signature.KeyID,
		signature.Value,
		"restore activation result",
	)
}

// DecodeOwnerRestoreActivationSignature verifies the wire encoding without
// loading custody. It is used by strict contract decoders before local binding.
func DecodeOwnerRestoreActivationSignature(value string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != ed25519.SignatureSize {
		return nil, errors.New("localevidence: restore activation signature encoding is invalid")
	}
	return decoded, nil
}
