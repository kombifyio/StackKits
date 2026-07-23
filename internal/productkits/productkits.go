// Package productkits owns the active product allowlist used by CLI execution,
// discovery, and registry projections.
//
// The public OSS distribution contains Basement, Cloud, and Modern Homelab.
// Keeping that taxonomy in one source prevents the private and exported CLIs
// from disagreeing about which release artifacts they can initialize.
package productkits

import (
	"fmt"
	"strings"
)

const (
	Basement = "basement-kit"
	Cloud    = "cloud-kit"
	Modern   = "modern-homelab"
)

var active = []string{Basement, Cloud, Modern}

// Slugs returns a copy of the active distribution's product taxonomy.
func Slugs() []string {
	return append([]string(nil), active...)
}

// IsActive reports whether name is a canonical installable product slug in
// the current source distribution.
func IsActive(name string) bool {
	name = strings.TrimSpace(name)
	for _, slug := range active {
		if name == slug {
			return true
		}
	}
	return false
}

// Validate keeps runtime consumers fail-closed to the active distribution.
// Legacy aliases must be normalized only at an explicit compatibility seam.
func Validate(name string) error {
	name = strings.TrimSpace(name)
	if IsActive(name) {
		return nil
	}
	return fmt.Errorf(
		"unsupported stackkit product %q; allowed products are %s",
		name,
		strings.Join(active, ", "),
	)
}
