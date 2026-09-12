package architecturev2

// ValidateFederationRemoteActionEnvelope uses this service's immutable CUE
// authority, including its closed remote-action shape.
func (s *Service) ValidateFederationRemoteActionEnvelope(raw []byte) error {
	if s == nil || s.validator == nil {
		return resolveError(ErrAuthorityLoad, "remote action validator unavailable", nil)
	}
	return s.validator.ValidateFederationRemoteActionEnvelope(raw)
}
