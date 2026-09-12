package resolvedplan

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ValidateFederationRemoteActionEnvelope applies the closed catalog-owned wire
// shape. Signatures, present-time TTL and replay decisions remain runtime checks.
func (v *CUEContractValidator) ValidateFederationRemoteActionEnvelope(raw []byte) error {
	if len(raw) == 0 || len(raw) > 64<<10 || !json.Valid(raw) {
		return fmt.Errorf("invalid bounded Federation action JSON")
	}
	return v.validateExpression("federation-remote-action", "foundation.#FederationRemoteActionEnvelopeV1 & ("+string(bytes.TrimSpace(raw))+")")
}
