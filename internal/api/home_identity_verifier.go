package api

import "github.com/kombifyio/stackkits/internal/identity"

func (s *Server) registerHomeIdentityVerifierRoutes() {
	s.mux.Handle("POST "+identity.HomeVerifierPath, identity.HomeVerifierHandler(s.config.BaseDir))
	s.mux.Handle("POST "+identity.HomeEnrollmentPath, identity.HomeEnrollmentHandler())
}
