package resolvedplan

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var semanticVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

type semanticVersion struct {
	major, minor, patch int
	prerelease          string
}

func versionAtLeast(actual, minimum string) (bool, error) {
	actualVersion, err := parseSemanticVersion(actual)
	if err != nil {
		return false, fmt.Errorf("actual version %q: %w", actual, err)
	}
	minimumVersion, err := parseSemanticVersion(minimum)
	if err != nil {
		return false, fmt.Errorf("minimum version %q: %w", minimum, err)
	}
	for _, pair := range [][2]int{{actualVersion.major, minimumVersion.major}, {actualVersion.minor, minimumVersion.minor}, {actualVersion.patch, minimumVersion.patch}} {
		if pair[0] != pair[1] {
			return pair[0] > pair[1], nil
		}
	}
	return comparePrerelease(actualVersion.prerelease, minimumVersion.prerelease) >= 0, nil
}

// VersionAtLeast applies the compiler's semantic-version ordering for
// downstream compatibility gates. Keeping this comparison here prevents
// generation/apply adapters from drifting onto a second version algorithm.
func VersionAtLeast(actual, minimum string) (bool, error) {
	return versionAtLeast(actual, minimum)
}

func parseSemanticVersion(value string) (semanticVersion, error) {
	matches := semanticVersionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if matches == nil {
		return semanticVersion{}, fmt.Errorf("not semantic version")
	}
	parts := make([]int, 3)
	for i := range parts {
		parsed, err := strconv.Atoi(matches[i+1])
		if err != nil {
			return semanticVersion{}, err
		}
		parts[i] = parsed
	}
	return semanticVersion{major: parts[0], minor: parts[1], patch: parts[2], prerelease: matches[4]}, nil
}

func comparePrerelease(actual, minimum string) int {
	if actual == minimum {
		return 0
	}
	if actual == "" {
		return 1
	}
	if minimum == "" {
		return -1
	}
	actualParts := strings.Split(actual, ".")
	minimumParts := strings.Split(minimum, ".")
	length := len(actualParts)
	if len(minimumParts) < length {
		length = len(minimumParts)
	}
	for i := 0; i < length; i++ {
		if actualParts[i] == minimumParts[i] {
			continue
		}
		actualNumber, actualErr := strconv.Atoi(actualParts[i])
		minimumNumber, minimumErr := strconv.Atoi(minimumParts[i])
		switch {
		case actualErr == nil && minimumErr == nil:
			if actualNumber < minimumNumber {
				return -1
			}
			return 1
		case actualErr == nil:
			return -1
		case minimumErr == nil:
			return 1
		case actualParts[i] < minimumParts[i]:
			return -1
		default:
			return 1
		}
	}
	if len(actualParts) < len(minimumParts) {
		return -1
	}
	return 1
}
