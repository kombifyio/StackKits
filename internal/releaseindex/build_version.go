package releaseindex

import (
	"fmt"
	"strings"
)

// ExactTagForBuildVersion converts the release build value embedded by
// GoReleaser into the one exact GitHub tag that publishes it.
func ExactTagForBuildVersion(buildVersion string) (string, error) {
	normalized := normalizeReleaseBuildVersion(buildVersion)
	if normalized == "" {
		return "", fmt.Errorf(
			"build version %q is not an exact stable, beta, or edge StackKits release",
			buildVersion,
		)
	}
	return "v" + normalized, nil
}

func normalizeReleaseBuildVersion(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "v") {
		value = value[1:]
	}
	if value == "" || strings.Contains(value, "+") {
		return ""
	}
	parsed, err := parseVersion(value)
	if err != nil || parsed.channel() == "" {
		return ""
	}
	return value
}
