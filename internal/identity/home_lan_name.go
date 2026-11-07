package identity

import (
	"time"

	"github.com/kombifyio/stackkits/internal/lancert"
)

// homeEnrollmentLANName is the Authority's LAN name (lan.<domain>) while the
// node holds a publicly trusted certificate that covers it, and empty
// otherwise: a local-only homelab has none, and a public one before its first
// issuance keeps the away flow (homelab identity target §5).
func homeEnrollmentLANName(root string) string {
	authority, err := homeAuthority(root)
	if err != nil {
		return ""
	}
	address, err := authority.IdentityRuntimeAddress()
	if err != nil || address.SubdomainPrefix != "" {
		return ""
	}
	name := lancert.LANName(address.Domain)
	if !lancert.Covers(root, name, time.Now()) {
		return ""
	}
	return name
}
