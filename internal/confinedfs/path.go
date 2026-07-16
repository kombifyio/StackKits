package confinedfs

import (
	"fmt"
	"path"
	"strings"
)

func validatePortablePath(value string, allowDot bool) (string, error) {
	if value == "." && allowDot {
		return value, nil
	}
	if value == "" || strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 ||
		strings.Contains(value, `\`) || strings.ContainsAny(value, `<>:"|?*`) {
		return "", fmt.Errorf("must be a non-empty portable slash-separated relative path")
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || (len(value) >= 2 && value[1] == ':') {
		return "", fmt.Errorf("absolute, drive-relative, and UNC paths are forbidden")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return "", fmt.Errorf("path must be canonical and remain beneath its root")
	}
	for _, segment := range strings.Split(clean, "/") {
		if strings.TrimRight(segment, ". ") != segment || windowsReservedSegment(segment) {
			return "", fmt.Errorf("path segment %q is not portable to Windows", segment)
		}
	}
	return clean, nil
}

func windowsReservedSegment(segment string) bool {
	base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$", "CONIN$", "CONOUT$",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}
