package platformdeploy

import (
	"fmt"
	"strings"
)

// GenerateKomodoStackResource creates the bounded PoC artifact for Komodo
// Resource Sync. It intentionally does not make Komodo the default adapter.
func GenerateKomodoStackResource(manifest AppManifest) ([]byte, error) {
	if strings.TrimSpace(manifest.Name) == "" {
		return nil, fmt.Errorf("komodo stack resource requires app name")
	}
	if strings.TrimSpace(manifest.ComposeYAML) == "" {
		return nil, fmt.Errorf("komodo stack resource requires compose yaml")
	}

	var sb strings.Builder
	sb.WriteString("type: Stack\n")
	fmt.Fprintf(&sb, "name: %s\n", manifest.Name)
	sb.WriteString("tags:\n")
	sb.WriteString("  - stackkit\n")
	sb.WriteString("  - komodo-spike\n")
	sb.WriteString("config:\n")
	sb.WriteString("  file_contents: |\n")
	for _, line := range strings.Split(strings.TrimRight(manifest.ComposeYAML, "\n"), "\n") {
		sb.WriteString("    ")
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return []byte(sb.String()), nil
}
