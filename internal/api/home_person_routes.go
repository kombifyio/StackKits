package api

import (
	"log/slog"
	"net/http"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localowner"
)

// registerHomePersonRoutes mounts the routes a person's device client calls
// at the Authority (homelab identity target §6.2): the device and notice
// routes, the confirmation of an away enrollment and the public enrollment
// bootstrap and its page. They authenticate the device credential and DPoP
// proof (or carry no secret) instead of the node API key. Their proofs are
// admitted only on this server's identity origins: the Hub origin of the
// identity runtime custody and the configured Owner step-up origin.
func (s *Server) registerHomePersonRoutes() {
	if origin := ownerStepUpOriginOrEmpty(s.config.OwnerStepUpOrigin); origin != "" {
		if err := identity.ConfigureHomeIdentityOrigins(s.config.BaseDir, origin); err != nil {
			slog.Error("identity origin rejected; device proofs are admitted only on the Hub origin", "setting", "STACKKIT_OWNER_STEP_UP_ORIGIN")
		}
	}
	identity.RegisterHomeIdentityRoutes(s.mux, s.config.BaseDir)
	s.mux.HandleFunc("GET "+homeEnrollStartPath, s.serveHomeEnrollStart)
}

// isHomePersonRoute marks the person routes, away enrollment (authenticated
// by the person's passkey sign-in and the key's proof) and the public
// enrollment bootstrap. Renewal is marked by isDeviceProofAuthenticatedRoute.
func isHomePersonRoute(r *http.Request) bool {
	return identity.IsHomeIdentityDeviceRoute(r) || isHomeEnrollStartRoute(r)
}

// keepHomeTrustCurrent runs on every server with Owner custody, the Authority
// and a Cloud Kit alike, so the Owner-signed Home trust behind every passkey
// verification never lapses while the server runs.
func (s *Server) keepHomeTrustCurrent() {
	if s.config.BaseDir == "" {
		return
	}
	if _, err := localowner.NewService(s.config.BaseDir); err != nil {
		return
	}
	go identity.KeepHomePublicTrustCurrent(s.ctx, s.config.BaseDir)
}
