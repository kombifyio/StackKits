package localowner

import (
	"context"
	"os"
)

// KombifyLinkClientID is the public PKCE client a Techstack-connected node
// registers so a household member can link their kombify account to their
// Pocket ID user. It matches Techstack's household link client id.
const KombifyLinkClientID = "stackkit-kombify-link"

// kombifyLinkCallbackURLs are the exact portal callbacks of the production and
// preview channels (kombify Cloud /household/homelab-link/callback).
var kombifyLinkCallbackURLs = []string{
	"https://kombify.io/household/homelab-link/callback",
	"https://preview.kombify.io/household/homelab-link/callback",
}

// techstackEnrollmentPaths are the Techstack agent enrollment files; one of
// them exists only on a node the owner connected to Techstack.
var techstackEnrollmentPaths = []string{
	"/etc/techstack/agent-enrollment.json",
	"/var/lib/techstack/guard/agent-enrollment.json",
}

func techstackConnected() bool {
	for _, path := range techstackEnrollmentPaths {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// ensureKombifyLinkClient registers the link client idempotently on a
// Techstack-connected node. A standalone node stays account-free: no client.
func (s *Service) ensureKombifyLinkClient(ctx context.Context) error {
	if !techstackConnected() {
		return nil
	}
	_, err := s.EnsureApplicationOIDCClient(ctx, ApplicationOIDCClientRequest{
		Public: true, ClientID: KombifyLinkClientID, Name: "kombify account link",
		RequiredPrivilege: "user", CallbackURLs: kombifyLinkCallbackURLs,
	})
	return err
}
