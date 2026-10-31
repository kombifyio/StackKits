package localevidence

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"

	"github.com/kombifyio/stackkits/contracts/applyevidence"
)

const applicationTransferDomain = "stackkit.owner-application-transfer/v1\x00"

// ApplicationTransferSignature travels with application data, never with an
// Owner private key. The receiving Owner must independently pin OwnerRef/KeyID.
type ApplicationTransferSignature struct {
	OwnerRef  string `json:"ownerRef"`
	KeyID     string `json:"keyId"`
	PublicKey string `json:"publicKey"`
	Value     string `json:"value"`
}

func SignApplicationTransfer(workspace string, canonical []byte) (ApplicationTransferSignature, error) {
	value, owner, keyID, err := signOwnerRestore(workspace, canonical, applicationTransferDomain, "application transfer")
	if err != nil {
		return ApplicationTransferSignature{}, err
	}
	key, err := LoadOwnerKey(workspace)
	if err != nil {
		return ApplicationTransferSignature{}, err
	}
	return ApplicationTransferSignature{OwnerRef: owner, KeyID: keyID, PublicKey: base64.RawStdEncoding.EncodeToString(key.Public()), Value: value}, nil
}

func VerifyApplicationTransfer(canonical []byte, signature ApplicationTransferSignature, expectedOwner, expectedFingerprint string) error {
	public, err := base64.RawStdEncoding.Strict().DecodeString(signature.PublicKey)
	if err != nil || len(public) != ed25519.PublicKeySize || expectedOwner == "" || expectedFingerprint == "" || signature.OwnerRef != expectedOwner || ApplicationTransferFingerprint(public) != expectedFingerprint || applyevidence.ProducerKeyID(ed25519.PublicKey(public)) != signature.KeyID {
		return errors.New("application transfer source differs from independently pinned Owner/key")
	}
	value, err := base64.RawStdEncoding.Strict().DecodeString(signature.Value)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(public), ownerRestoreDigest(applicationTransferDomain, canonical), value) {
		return errors.New("application transfer source signature does not verify")
	}
	return nil
}

func ApplicationTransferFingerprint(public []byte) string {
	sum := sha256.Sum256(public)
	return "sha256:" + hex.EncodeToString(sum[:])
}
