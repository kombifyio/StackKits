package localevidence

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

const ownerSnapshotAnchorDomain = "stackkit.owner-signed-snapshot-anchor/v1\x00"

// OwnerSnapshotAnchorSignature authenticates one canonical local backup
// snapshot anchor with the established owner custody key.
type OwnerSnapshotAnchorSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

// SignOwnerSnapshotAnchor signs only snapshot anchors. Its dedicated domain
// prevents a valid signature from being replayed as another evidence type.
func SignOwnerSnapshotAnchor(
	workspaceRoot string,
	canonicalAnchor []byte,
) (OwnerSnapshotAnchorSignature, error) {
	if len(canonicalAnchor) == 0 {
		return OwnerSnapshotAnchorSignature{}, errors.New("localevidence: owner snapshot anchor is empty")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return OwnerSnapshotAnchorSignature{}, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return OwnerSnapshotAnchorSignature{}, err
	}
	if owner.OwnerRef != key.OwnerRef || owner.KeyID != key.KeyID {
		return OwnerSnapshotAnchorSignature{}, errors.New("localevidence: snapshot anchor signer differs from established owner")
	}
	return OwnerSnapshotAnchorSignature{
		OwnerRef: owner.OwnerRef,
		KeyID:    owner.KeyID,
		Value: base64.RawStdEncoding.EncodeToString(
			ed25519.Sign(key.private, ownerSnapshotAnchorDigest(canonicalAnchor)),
		),
	}, nil
}

// VerifyOwnerSnapshotAnchor verifies a canonical snapshot anchor against the
// current local owner custody.
func VerifyOwnerSnapshotAnchor(
	workspaceRoot string,
	canonicalAnchor []byte,
	signature OwnerSnapshotAnchorSignature,
) error {
	if len(canonicalAnchor) == 0 || signature.OwnerRef == "" || signature.KeyID == "" {
		return errors.New("localevidence: owner snapshot anchor signature is incomplete")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return err
	}
	if signature.OwnerRef != owner.OwnerRef ||
		signature.KeyID != owner.KeyID ||
		key.OwnerRef != owner.OwnerRef ||
		key.KeyID != owner.KeyID {
		return errors.New("localevidence: owner snapshot anchor signature is not bound to established custody")
	}
	value, err := base64.RawStdEncoding.Strict().DecodeString(signature.Value)
	if err != nil ||
		len(value) != ed25519.SignatureSize ||
		!ed25519.Verify(key.Public(), ownerSnapshotAnchorDigest(canonicalAnchor), value) {
		return errors.New("localevidence: owner snapshot anchor signature does not verify")
	}
	return nil
}

func ownerSnapshotAnchorDigest(canonicalAnchor []byte) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte(ownerSnapshotAnchorDomain))
	_, _ = digest.Write(canonicalAnchor)
	return digest.Sum(nil)
}
