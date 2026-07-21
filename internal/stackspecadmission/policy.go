// Package stackspecadmission owns the release-policy boundary between the
// bounded StackSpec v1 compatibility minor and canonical Architecture v2.
package stackspecadmission

import (
	"strconv"
	"strings"
)

// RejectOperationalV1 reports whether an operational reader must reject raw
// StackSpec v1. Only an explicit, parseable v0.6 build identity retains the
// one-minor compatibility window. Older, newer, development, empty, and
// malformed identities fail closed so build metadata cannot accidentally
// reopen a removed executor.
func RejectOperationalV1(buildVersion string) bool {
	normalized := strings.TrimPrefix(strings.TrimSpace(buildVersion), "v")
	if normalized == "" || normalized == "dev" {
		return true
	}
	core, valid := strictSemVerCore(normalized)
	if !valid {
		return true
	}
	parts := strings.Split(core, ".")
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil {
		return true
	}
	return major != 0 || minor != 6
}

func strictSemVerCore(version string) (string, bool) {
	if strings.Count(version, "+") > 1 {
		return "", false
	}
	withoutBuild := version
	if index := strings.IndexByte(withoutBuild, '+'); index >= 0 {
		if !validSemVerIdentifiers(withoutBuild[index+1:], false) {
			return "", false
		}
		withoutBuild = withoutBuild[:index]
	}
	core := withoutBuild
	if index := strings.IndexByte(withoutBuild, '-'); index >= 0 {
		if !validSemVerIdentifiers(withoutBuild[index+1:], true) {
			return "", false
		}
		core = withoutBuild[:index]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return "", false
	}
	for _, part := range parts {
		if !validNumericIdentifier(part) {
			return "", false
		}
	}
	return core, true
}

func validSemVerIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" || (rejectNumericLeadingZero && len(identifier) > 1 && identifier[0] == '0' && allDigits(identifier)) {
			return false
		}
		for _, character := range identifier {
			if !((character >= '0' && character <= '9') || (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || character == '-') {
				return false
			}
		}
	}
	return true
}

func validNumericIdentifier(value string) bool {
	return value != "" && !(len(value) > 1 && value[0] == '0') && allDigits(value)
}

func allDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value != ""
}
