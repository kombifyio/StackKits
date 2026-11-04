package api

import (
	"net/http"

	"github.com/kombifyio/stackkits/internal/identity"
)

func (s *Server) registerHomeIdentityVerifierRoutes() {
	s.mux.Handle("POST "+identity.HomeVerifierPath, identity.HomeVerifierHandler(s.config.BaseDir))
	s.mux.Handle("POST "+identity.HomeEnrollmentPath, identity.HomeEnrollmentHandler(s.config.BaseDir))
	s.mux.Handle("POST "+identity.HomeDeviceRenewalPath, identity.HomeDeviceRenewalHandler(s.config.BaseDir))
	recovery := identity.RecoveryDeviceHandler(s.config.BaseDir, identity.LocalRecoveryPasskeyIssuer(s.config.BaseDir))
	for _, path := range []string{identity.RecoveryRequestPath, identity.RecoveryCompletePath, identity.RecoveryCancelPath} {
		s.mux.Handle("POST "+path, recovery)
	}
	s.mux.Handle("GET "+identity.RecoveryDeviceNoticesPath, recovery)
	s.mux.Handle("GET "+identity.RecoveryNoticesPath, identity.RecoveryNoticesHandler(s.config.BaseDir))
}

// isDeviceProofAuthenticatedRoute marks device credential renewal and the
// Owner device recovery routes, whose DPoP proof by an enrolled device key
// replaces the node API key: a device holds only its own key and never the
// API key.
func isDeviceProofAuthenticatedRoute(r *http.Request) bool {
	switch {
	case r.Method == http.MethodGet:
		return r.URL.Path == identity.RecoveryDeviceNoticesPath
	case r.Method != http.MethodPost:
		return false
	}
	switch r.URL.Path {
	case identity.HomeDeviceRenewalPath, identity.RecoveryRequestPath, identity.RecoveryCompletePath, identity.RecoveryCancelPath:
		return true
	}
	return false
}
