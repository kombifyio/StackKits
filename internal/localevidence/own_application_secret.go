package localevidence

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// OwnApplicationSecretCommitment binds private input without publishing an
// offline password verifier. It reads existing installation authority only;
// dry review neither creates a key nor writes secret custody.
func OwnApplicationSecretCommitment(workspace, application string, canonical []byte) (string, error) {
	owner, e := LoadOwnerCustody(workspace)
	if e != nil {
		return "", e
	}
	key, e := LoadOwnerKey(workspace)
	if e != nil {
		return "", e
	}
	if key.OwnerRef != owner.OwnerRef || key.KeyID != owner.KeyID {
		return "", errors.New("own application review authority mismatch")
	}
	seed := key.private.Seed()
	defer clear(seed)
	mac := hmac.New(sha256.New, seed)
	mac.Write([]byte("stackkit.own-application-secret-review/v1\x00"))
	identity, _ := json.Marshal(struct {
		Owner, Key, Application string
		Binding                 LocalBinding
	}{owner.OwnerRef, owner.KeyID, application, owner.Binding})
	mac.Write(identity)
	mac.Write([]byte{0})
	mac.Write(canonical)
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil)), nil
}
