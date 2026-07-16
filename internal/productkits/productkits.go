// Package productkits owns the active product allowlist used by CLI execution,
// discovery, and registry projections.
//
// The public OSS distribution contains Basement and Cloud. The private source
// distribution registers Modern Home Lab from private_modern.go; the public
// exporter deliberately removes that file. This keeps one runtime guard while
// preserving the narrower public product surface.
package productkits

import (
	"fmt"
	"strings"
)

const (
	Basement = "basement-kit"
	Cloud    = "cloud-kit"
)

var active = []string{Basement, Cloud}

// Slugs returns a copy of the active distribution's product taxonomy.
func Slugs() []string {
	return append([]string(nil), active...)
}

// IsActive reports whether name is a canonical installable product slug in
// the current private or public source distribution.
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

func registerPrivate(slug string) {
	if !IsActive(slug) {
		active = append(active, slug)
	}
}
