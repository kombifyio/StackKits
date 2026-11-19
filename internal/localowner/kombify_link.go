package localowner

import (
	"context"
	"os"
)

// KombifyLinkClientID is the public PKCE client a Homelab-connected node
// registers so a household member can link their kombify account to their
// Pocket ID user. It matches Homelab's household link client id.
const KombifyLinkClientID = "stackkit-kombify-link"

// kombifyLinkCallbackURLs are the exact portal callbacks of the production and
// preview channels (kombify Cloud /household/homelab-link/callback).
var kombifyLinkCallbackURLs = []string{
	"https://kombify.io/household/homelab-link/callback",
	"https://preview.kombify.io/household/homelab-link/callback",
}

// homelabEnrollmentPaths are the Homelab agent enrollment files; one of
// them exists only on a node the owner connected to Homelab.
var homelabEnrollmentPaths = []string{
	"/etc/techstack/agent-enrollment.json",
	"/var/lib/techstack/guard/agent-enrollment.json",
}

func homelabConnected() bool {
	for _, path := range homelabEnrollmentPaths {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// ensureKombifyLinkClient registers the link client idempotently on a
// Homelab-connected node. A standalone node stays account-free: no client.
func (s *Service) ensureKombifyLinkClient(ctx context.Context) error {
	if !homelabConnected() {
		return nil
	}
	_, err := s.EnsureApplicationOIDCClient(ctx, ApplicationOIDCClientRequest{
		Public: true, ClientID: KombifyLinkClientID, Name: "kombify account link",
		RequiredPrivilege: "user", CallbackURLs: kombifyLinkCallbackURLs,
	})
	return err
}
