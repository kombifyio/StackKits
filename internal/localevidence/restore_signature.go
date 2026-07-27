package localevidence

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

const (
	ownerRestoreRecoveryDomain = "stackkit.owner-signed-restore-recovery-anchor/v1\x00"
	ownerRestoreResultDomain   = "stackkit.owner-signed-restore-result/v1\x00"
)

// OwnerRestoreRecoverySignature authenticates the exact local Owner approval
// and recovery intent persisted before any restore staging side effect.
type OwnerRestoreRecoverySignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

// OwnerRestoreResultSignature authenticates the terminal staged-restore
// evidence. It uses a different domain so intent and result signatures cannot
// be replayed across lifecycle phases.
type OwnerRestoreResultSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

func SignOwnerRestoreRecovery(
	workspaceRoot string,
	canonicalRecovery []byte,
) (OwnerRestoreRecoverySignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(
		workspaceRoot,
		canonicalRecovery,
		ownerRestoreRecoveryDomain,
		"restore recovery anchor",
	)
	if err != nil {
		return OwnerRestoreRecoverySignature{}, err
	}
	return OwnerRestoreRecoverySignature{OwnerRef: ownerRef, KeyID: keyID, Value: value}, nil
}

func VerifyOwnerRestoreRecovery(
	workspaceRoot string,
	canonicalRecovery []byte,
	signature OwnerRestoreRecoverySignature,
) error {
	return verifyOwnerRestore(
		workspaceRoot,
		canonicalRecovery,
		ownerRestoreRecoveryDomain,
		signature.OwnerRef,
		signature.KeyID,
		signature.Value,
		"restore recovery anchor",
	)
}

func SignOwnerRestoreResult(
	workspaceRoot string,
	canonicalResult []byte,
) (OwnerRestoreResultSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(
		workspaceRoot,
		canonicalResult,
		ownerRestoreResultDomain,
		"restore result",
	)
	if err != nil {
		return OwnerRestoreResultSignature{}, err
	}
	return OwnerRestoreResultSignature{OwnerRef: ownerRef, KeyID: keyID, Value: value}, nil
}

func VerifyOwnerRestoreResult(
	workspaceRoot string,
	canonicalResult []byte,
	signature OwnerRestoreResultSignature,
) error {
	return verifyOwnerRestore(
		workspaceRoot,
		canonicalResult,
		ownerRestoreResultDomain,
		signature.OwnerRef,
		signature.KeyID,
		signature.Value,
		"restore result",
	)
}

func signOwnerRestore(
	workspaceRoot string,
	canonical []byte,
	domain string,
	label string,
) (value, ownerRef, keyID string, err error) {
	if len(canonical) == 0 {
		return "", "", "", errors.New("localevidence: owner " + label + " is empty")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return "", "", "", err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return "", "", "", err
	}
	if owner.OwnerRef != key.OwnerRef || owner.KeyID != key.KeyID {
		return "", "", "", errors.New("localevidence: " + label + " signer differs from established owner")
	}
	return base64.RawStdEncoding.EncodeToString(
		ed25519.Sign(key.private, ownerRestoreDigest(domain, canonical)),
	), owner.OwnerRef, owner.KeyID, nil
}

func verifyOwnerRestore(
	workspaceRoot string,
	canonical []byte,
	domain string,
	signatureOwnerRef string,
	signatureKeyID string,
	signatureValue string,
	label string,
) error {
	if len(canonical) == 0 || signatureOwnerRef == "" || signatureKeyID == "" {
		return errors.New("localevidence: owner " + label + " signature is incomplete")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return err
	}
	if signatureOwnerRef != owner.OwnerRef ||
		signatureKeyID != owner.KeyID ||
		key.OwnerRef != owner.OwnerRef ||
		key.KeyID != owner.KeyID {
		return errors.New("localevidence: owner " + label + " signature is not bound to established custody")
	}
	value, err := base64.RawStdEncoding.Strict().DecodeString(signatureValue)
	if err != nil ||
		len(value) != ed25519.SignatureSize ||
		!ed25519.Verify(key.Public(), ownerRestoreDigest(domain, canonical), value) {
		return errors.New("localevidence: owner " + label + " signature does not verify")
	}
	return nil
}

func ownerRestoreDigest(domain string, canonical []byte) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte(domain))
	_, _ = digest.Write(canonical)
	return digest.Sum(nil)
}
