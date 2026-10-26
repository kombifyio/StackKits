package runtimeprofile

import (
	"fmt"
	"path"
	"regexp"
)

// OfflineBundleRef identifies immutable installation input. This reference
// never replaces release attestation or verification of the complete bundle.
type OfflineBundleRef struct {
	Path           string `json:"path" yaml:"path"`
	ManifestSHA256 string `json:"manifestSha256" yaml:"manifestSha256"`
}

var (
	offlineBundlePathPattern = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
	offlineBundleHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Validate checks the portable Linux mount reference, without reading files or
// implying trust. Restricting characters keeps Compose interpolation literal.
func (r OfflineBundleRef) Validate() error {
	if !offlineBundlePathPattern.MatchString(r.Path) || r.Path == "/" || path.Clean(r.Path) != r.Path {
		return fmt.Errorf("offlineBundle.path must be a clean absolute local directory with only letters, digits, slash, dot, underscore or hyphen")
	}
	if !offlineBundleHashPattern.MatchString(r.ManifestSHA256) {
		return fmt.Errorf("offlineBundle.manifestSha256 must be 64 lowercase hexadecimal characters")
	}
	return nil
}
