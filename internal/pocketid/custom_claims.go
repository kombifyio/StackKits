package pocketid

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// CustomClaim is the per-user key/value DTO in pinned Pocket ID v2.16.0:
// https://github.com/pocket-id/pocket-id/blob/v2.16.0/backend/internal/dto/custom_claim_dto.go
// User.CustomClaims contains this user's own claims, without inherited groups.
// Pocket ID emits custom claims only when the client requests the profile
// scope, and user claims take precedence over group claims with the same key:
// https://github.com/pocket-id/pocket-id/blob/v2.16.0/backend/internal/oidc/claims_service.go
// https://github.com/pocket-id/pocket-id/blob/v2.16.0/backend/internal/service/custom_claim_service.go
type CustomClaim struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// UpdateUserCustomClaims replaces the complete per-user claim set. Callers
// must merge any intended change with GetUser.CustomClaims first; Pocket ID
// deletes omitted keys. No group claim or user administrator flag is changed.
// https://github.com/pocket-id/pocket-id/blob/v2.16.0/backend/internal/service/custom_claim_service.go
func (c *Client) UpdateUserCustomClaims(ctx context.Context, subject string, claims []CustomClaim) error {
	subject = strings.TrimSpace(subject)
	if subject == "" || strings.ContainsAny(subject, "/?#") {
		return errors.New("pocketid: custom claims require an exact user subject")
	}
	if claims == nil {
		return errors.New("pocketid: custom claims require an explicit complete claim set")
	}
	seen := map[string]bool{}
	for _, claim := range claims {
		if claim.Key == "" || claim.Value == "" || seen[claim.Key] {
			return errors.New("pocketid: custom claim keys and values must be nonempty and unique")
		}
		seen[claim.Key] = true
	}
	return c.do(ctx, http.MethodPut, "/api/custom-claims/user/"+subject, claims, nil)
}
