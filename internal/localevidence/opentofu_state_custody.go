package localevidence

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"strings"
)

const openTofuStateKeyDomain = "stackkit.owner-custodied-opentofu-state-key/v1\x00"

// WithOpenTofuStateKey derives the encryption key of one portable OpenTofu
// root from established Owner custody. It never creates or rotates custody.
// The key is scoped to the workspace-relative root rather than the absolute
// checkout path, so a recovered workspace can move without changing its key.
// The supplied key is valid only for the callback and is cleared afterwards.
func WithOpenTofuStateKey(workspaceRoot, portableRoot string, use func([]byte) error) error {
	if use == nil {
		return errors.New("localevidence: OpenTofu state key requires a consumer")
	}
	clean := path.Clean(strings.TrimSpace(portableRoot))
	if clean != portableRoot || clean == "." || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "../") {
		return errors.New("localevidence: OpenTofu state key requires a portable root identity")
	}
	custody, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return fmt.Errorf("localevidence: load Owner custody for OpenTofu state: %w", err)
	}
	ownerKey, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return fmt.Errorf("localevidence: load Owner key for OpenTofu state: %w", err)
	}
	if ownerKey.OwnerRef != custody.OwnerRef || ownerKey.KeyID != custody.KeyID {
		return errors.New("localevidence: OpenTofu state custody is not bound to the established Owner")
	}

	seed := ownerKey.private.Seed()
	defer clear(seed)
	mac := hmac.New(sha256.New, seed)
	_, _ = mac.Write([]byte(openTofuStateKeyDomain))
	_, _ = mac.Write([]byte(custody.OwnerRef))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(clean))
	key := mac.Sum(nil)
	defer clear(key)
	return use(key)
}
