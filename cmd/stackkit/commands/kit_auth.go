package commands

import (
	"net/http"
	"os"

	"github.com/kombifyio/stackkits/internal/auth"
)

// attachKitClientAuth sets the right auth header on req based on env state:
//
//   - SERVICE_AUTH_SECRET set → mints HS256 service-auth JWT via
//     internal/auth.SignServiceToken, sets X-Kombify-Service-Auth header.
//     Compatible with the admin's requireServiceKeyOrAdmin path.
//   - else legacyToken set → Authorization: Bearer <token>. Only works
//     when admin runs with ALLOW_LEGACY_SERVICE_KEYS=true.
//   - else no auth header. Admin will 401.
//
// Both kit.go (raw http.Request) and internal/kitio.AdminClient call into
// internal/auth.SignServiceToken for the actual signing — single source
// of truth for the wire-format that admin's tryServiceAuth verifies.
func attachKitClientAuth(req *http.Request, legacyToken string) error {
	if secret := os.Getenv("SERVICE_AUTH_SECRET"); secret != "" {
		token, err := auth.SignServiceToken("stackkits", "administration", secret, auth.DefaultTokenTTL)
		if err != nil {
			return err
		}
		req.Header.Set(auth.HeaderServiceAuth, token)
		return nil
	}
	if legacyToken != "" {
		req.Header.Set("Authorization", "Bearer "+legacyToken)
	}
	return nil
}
