package opentofu

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrLegacyPaaSDetected is returned fail-closed when a workspace holds retired
// Layer-2 PaaS workload roots (Komodo, Dokploy, Runtipi, Coolify-core) or
// legacy Full-Core Coolify 4.1.2 services, preventing dual active copies
// (ADR-0054 §2, CONSTITUTION.md rule 12).
var ErrLegacyPaaSDetected = errors.New("retired legacy PaaS workload or Full-Core Coolify 4.1.2 detected; refusing dual active copies")

// LegacyPaaSFinding describes one retired PaaS root or Full-Core Coolify 4.1.2
// runtime artifact detected in a workspace.
type LegacyPaaSFinding struct {
	RelativePath string `json:"relativePath"`
	Kind         string `json:"kind"` // "legacy-paas-root" or "legacy-full-core-coolify-4.1"
	Execution    string `json:"execution"`
	Remediation  string `json:"remediation"`
}

// DetectLegacyPaaSWorkloads scans canonical OpenTofu roots and core Compose
// files for retired Layer-2 PaaS executions or legacy Full-Core Coolify 4.1.2.
func DetectLegacyPaaSWorkloads(workspace string) ([]LegacyPaaSFinding, error) {
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	var findings []LegacyPaaSFinding

	roots, err := DiscoverCanonicalRoots(absWorkspace)
	if err != nil {
		return nil, err
	}
	for _, root := range roots {
		rootDir := filepath.Join(absWorkspace, filepath.FromSlash(root.RelativeRoot))
		marker, err := ReadRootMarker(rootDir)
		if err == nil && IsPlatformProviderExecution(marker.Execution) {
			findings = append(findings, LegacyPaaSFinding{
				RelativePath: root.RelativeRoot,
				Kind:         "legacy-paas-root",
				Execution:    marker.Execution,
				Remediation:  "Follow docs/runbooks/paas-to-opentofu-restore-cutover.md (write-fenced Kopia backup -> fresh Stage-1 install -> verified restore).",
			})
		}
		// Check Core roots for legacy Full-Core Coolify 4.1.2 references.
		if root.Order == 0 {
			composePath := filepath.Join(filepath.Dir(rootDir), ComposeFile)
			if raw, readErr := os.ReadFile(composePath); readErr == nil {
				text := string(raw)
				if strings.Contains(text, "coollabsio/coolify:4.1.") || strings.Contains(text, "ghcr.io/coollabsio/coolify:4.1.") {
					findings = append(findings, LegacyPaaSFinding{
						RelativePath: filepath.ToSlash(filepath.Join(".stackkit", "runtime", root.Name, ComposeFile)),
						Kind:         "legacy-full-core-coolify-4.1",
						Execution:    "coolify-core-4.1",
						Remediation:  "Follow docs/runbooks/paas-to-opentofu-restore-cutover.md to export user projects and fresh-install optional modules/coolify 4.7.5+.",
					})
				}
			}
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		return findings[i].RelativePath < findings[j].RelativePath
	})
	return findings, nil
}

// FormatLegacyPaaSFindings returns a compact human/operator summary for errors.
func FormatLegacyPaaSFindings(findings []LegacyPaaSFinding) string {
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("%s (%s/%s)", f.RelativePath, f.Kind, f.Execution))
	}
	return strings.Join(parts, ", ") + "; see docs/runbooks/paas-to-opentofu-restore-cutover.md"
}
