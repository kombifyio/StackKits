// Package referenceid contains the closed grammars shared by otherwise
// independent provider-free wire packages.
package referenceid

import (
	"regexp"
	"unicode"
	"unicode/utf8"
)

var (
	localExecutionChannelPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	externalExecutionChannelPattern = regexp.MustCompile(`^execution-channel://sha256/[a-f0-9]{64}$`)
)

// ValidExecutionChannel reports whether value is either a bounded local
// construction identity or the exact hash-bound external channel reference
// used by StackKits CUE. It never accepts an endpoint or free-form URI.
func ValidExecutionChannel(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return localExecutionChannelPattern.MatchString(value) ||
		externalExecutionChannelPattern.MatchString(value)
}
