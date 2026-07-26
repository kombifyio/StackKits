package releaseindex

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var semverPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

type parsedVersion struct {
	original   string
	major      int
	minor      int
	patch      int
	prerelease []string
}

func parseVersion(value string) (parsedVersion, error) {
	match := semverPattern.FindStringSubmatch(value)
	if match == nil {
		return parsedVersion{}, fmt.Errorf("%q is not SemVer", value)
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	var prerelease []string
	if match[4] != "" {
		prerelease = strings.Split(match[4], ".")
		for _, identifier := range prerelease {
			if len(identifier) > 1 && identifier[0] == '0' && identifier[0] >= '0' && identifier[0] <= '9' {
				allNumeric := true
				for _, character := range identifier {
					if character < '0' || character > '9' {
						allNumeric = false
						break
					}
				}
				if allNumeric {
					return parsedVersion{}, fmt.Errorf("%q has a numeric prerelease identifier with a leading zero", value)
				}
			}
		}
	}
	return parsedVersion{original: value, major: major, minor: minor, patch: patch, prerelease: prerelease}, nil
}

func (version parsedVersion) channel() Channel {
	if len(version.prerelease) == 0 {
		return ChannelStable
	}
	switch version.prerelease[0] {
	case "beta":
		return ChannelBeta
	case "edge":
		return ChannelEdge
	default:
		return ""
	}
}

func compareVersions(left, right parsedVersion) int {
	for _, pair := range [][2]int{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) > 0 {
		return 1
	}
	if len(right.prerelease) == 0 && len(left.prerelease) > 0 {
		return -1
	}
	for position := 0; position < len(left.prerelease) && position < len(right.prerelease); position++ {
		leftNumber, leftNumeric := numericIdentifier(left.prerelease[position])
		rightNumber, rightNumeric := numericIdentifier(right.prerelease[position])
		switch {
		case leftNumeric && rightNumeric && leftNumber != rightNumber:
			if leftNumber < rightNumber {
				return -1
			}
			return 1
		case leftNumeric != rightNumeric:
			if leftNumeric {
				return -1
			}
			return 1
		case left.prerelease[position] < right.prerelease[position]:
			return -1
		case left.prerelease[position] > right.prerelease[position]:
			return 1
		}
	}
	switch {
	case len(left.prerelease) < len(right.prerelease):
		return -1
	case len(left.prerelease) > len(right.prerelease):
		return 1
	default:
		return 0
	}
}

func numericIdentifier(value string) (int, bool) {
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	number, err := strconv.Atoi(value)
	return number, err == nil
}
