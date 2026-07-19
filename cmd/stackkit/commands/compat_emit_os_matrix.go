package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// compat emit-os-matrix renders the committed OS-only public projection into
// docs and the website feed. Host/runtime diagnostics are separate artifacts
// and are rejected here even when they have otherwise been redacted.
var (
	emitOSMatrixInput      string
	emitOSMatrixArchiveDir string
	emitOSMatrixDocsOut    string
	emitOSMatrixWebsiteOut string
)

var compatEmitOSMatrixCmd = &cobra.Command{
	Use:   "emit-os-matrix",
	Short: "Render the public OS-only compatibility projection",
	RunE:  runCompatEmitOSMatrix,
}

func init() {
	compatEmitOSMatrixCmd.Flags().StringVar(&emitOSMatrixInput, "input", "docs/data/os-compat/latest.json", "committed public OS-only projection")
	compatEmitOSMatrixCmd.Flags().StringVar(&emitOSMatrixArchiveDir, "archive-dir", "docs/data/os-compat", "directory holding per-release v*.json archives")
	compatEmitOSMatrixCmd.Flags().StringVar(&emitOSMatrixDocsOut, "docs-out", "docs/OS_COMPATIBILITY.md", "generated markdown output")
	compatEmitOSMatrixCmd.Flags().StringVar(&emitOSMatrixWebsiteOut, "website-out", "website/public/os-compat.json", "generated website feed output")
	compatCmd.AddCommand(compatEmitOSMatrixCmd)
}

type osMatrixDoc struct {
	SchemaVersion    int              `json:"schemaVersion"`
	StackKitsVersion string           `json:"stackkitsVersion"`
	Results          []osMatrixDocRow `json:"results"`
	GeneratedAt      string           `json:"generatedAt"`
}

type osMatrixDocRow struct {
	OS          osMatrixDocOS `json:"os"`
	Grade       string        `json:"grade"`
	ReasonCodes []string      `json:"reasonCodes"`
}

type osMatrixDocOS struct {
	Family       string `json:"family"`
	Distribution string `json:"distribution"`
	Version      string `json:"version"`
}

var (
	osMatrixForbiddenKeys = regexp.MustCompile(`"(runId|lane|stages|stage|overall|target|arch|architecture|kernel|packageMgr|initSystem|virtType|virtTier|virtualization|runtime|engine|osReleaseRaw|evidencePath|mdnsHost|host|hostname|provider|device|resourceId|lease|cleanupState)"\s*:`)
	osMatrixRFC1918       = regexp.MustCompile(`(^|[^0-9.])(10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}|192\.168\.[0-9]{1,3}\.[0-9]{1,3}|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3})([^0-9.]|$)`)
	osMatrixInfraText     = regexp.MustCompile(`(?i)\b(docker|container|wsl2?|proxmox|pico\s*kvm|kvm|hypervisor|bare[ -]?metal|virtual(?:ization| machine)|ionos|centron)\b`)
	osMatrixSlug          = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
	osMatrixVersion       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	osMatrixRelease       = regexp.MustCompile(`^(unreleased|v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?)$`)
)

func runCompatEmitOSMatrix(cmd *cobra.Command, args []string) error {
	raw, err := os.ReadFile(emitOSMatrixInput)
	if err != nil {
		return fmt.Errorf("read matrix input: %w", err)
	}
	matrix, err := decodeOSMatrix(raw)
	if err != nil {
		return err
	}
	sort.Slice(matrix.Results, func(i, j int) bool { return osMatrixRowKey(matrix.Results[i]) < osMatrixRowKey(matrix.Results[j]) })

	sourceHash := sha256.Sum256(raw)
	if err := writeGenerated(emitOSMatrixDocsOut, []byte(renderOSMatrixMarkdown(matrix, fmt.Sprintf("%x", sourceHash)))); err != nil {
		return err
	}
	feed, err := renderOSMatrixFeed(matrix, emitOSMatrixArchiveDir)
	if err != nil {
		return err
	}
	if err := writeGenerated(emitOSMatrixWebsiteOut, feed); err != nil {
		return err
	}
	fmt.Printf("emitted %s and %s from %s (%d OS rows)\n", emitOSMatrixDocsOut, emitOSMatrixWebsiteOut, emitOSMatrixInput, len(matrix.Results))
	return nil
}

func decodeOSMatrix(raw []byte) (osMatrixDoc, error) {
	if err := checkOSMatrixPublishable(raw); err != nil {
		return osMatrixDoc{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var matrix osMatrixDoc
	if err := decoder.Decode(&matrix); err != nil {
		return osMatrixDoc{}, fmt.Errorf("parse OS compatibility projection: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return osMatrixDoc{}, fmt.Errorf("parse OS compatibility projection: trailing JSON data")
	}
	if matrix.SchemaVersion != 2 || !osMatrixRelease.MatchString(matrix.StackKitsVersion) || len(matrix.Results) == 0 {
		return osMatrixDoc{}, fmt.Errorf("matrix input must be public schemaVersion 2 with version, generation time, and at least one result")
	}
	if _, err := time.Parse(time.RFC3339, matrix.GeneratedAt); err != nil {
		return osMatrixDoc{}, fmt.Errorf("matrix generatedAt must be RFC3339: %w", err)
	}
	identities := make(map[string]struct{}, len(matrix.Results))
	for i, row := range matrix.Results {
		if !osMatrixSlug.MatchString(row.OS.Family) || !osMatrixSlug.MatchString(row.OS.Distribution) || !osMatrixVersion.MatchString(row.OS.Version) {
			return osMatrixDoc{}, fmt.Errorf("matrix result %d has an incomplete OS identity", i)
		}
		identity := osMatrixRowKey(row)
		if _, exists := identities[identity]; exists {
			return osMatrixDoc{}, fmt.Errorf("matrix result %d duplicates OS identity %s", i, identity)
		}
		identities[identity] = struct{}{}
		switch row.Grade {
		case "unverified":
		default:
			return osMatrixDoc{}, fmt.Errorf("matrix result %d must remain unverified until the receipt projector exists", i)
		}
		if len(row.ReasonCodes) == 0 {
			return osMatrixDoc{}, fmt.Errorf("matrix result %d requires a closed reason code", i)
		}
		reasons := make(map[string]struct{}, len(row.ReasonCodes))
		for _, code := range row.ReasonCodes {
			if code != "current-candidate-receipt-pending" && code != "os-policy-not-yet-admitted" {
				return osMatrixDoc{}, fmt.Errorf("matrix result %d has unknown public reason code %q", i, code)
			}
			if _, exists := reasons[code]; exists {
				return osMatrixDoc{}, fmt.Errorf("matrix result %d duplicates public reason code %q", i, code)
			}
			reasons[code] = struct{}{}
		}
	}
	return matrix, nil
}

func checkOSMatrixPublishable(raw []byte) error {
	if match := osMatrixForbiddenKeys.Find(raw); match != nil {
		return fmt.Errorf("matrix input carries forbidden diagnostic/infrastructure field %s", string(match))
	}
	if osMatrixRFC1918.Match(raw) {
		return fmt.Errorf("matrix input contains RFC1918 addresses")
	}
	if osMatrixInfraText.Match(raw) {
		return fmt.Errorf("matrix input contains infrastructure/runtime terminology")
	}
	return nil
}

func gradeGlyph(grade string) string {
	switch grade {
	case "supported":
		return "✅"
	case "preview":
		return "🟡"
	case "unsupported":
		return "❌"
	case "unverified":
		return "⚪"
	default:
		return "—"
	}
}

func osMatrixRowKey(row osMatrixDocRow) string {
	return strings.Join([]string{row.OS.Family, row.OS.Distribution, row.OS.Version}, "/")
}

func osMatrixReason(code string) string {
	switch code {
	case "current-candidate-receipt-pending":
		return "Current candidate HostConformanceReceipt pending"
	case "os-policy-not-yet-admitted":
		return "OS policy not yet admitted"
	default:
		return code
	}
}

func renderOSMatrixMarkdown(matrix osMatrixDoc, sourceHash string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- Code generated by 'stackkit compat emit-os-matrix'. DO NOT EDIT. source_hash: %s -->\n\n", sourceHash)
	b.WriteString("# OS Compatibility\n\n")
	b.WriteString("This is the public OS-only StackKits support projection. Future positive rows will\n")
	b.WriteString("be derived from versioned support policy and current `HostConformanceReceipt` evidence; host,\n")
	b.WriteString("runtime, provider, device, and virtualization diagnostics are not support dimensions.\n\n")
	fmt.Fprintf(&b, "- StackKits version: `%s`\n- Generated: %s\n\n", matrix.StackKitsVersion, matrix.GeneratedAt)
	b.WriteString("Positive grades remain disabled until the controlled receipt projector exists.\n\n")
	b.WriteString("| OS family | Distribution | Version | Grade | Reason |\n")
	b.WriteString("| --- | --- | --- | :-: | --- |\n")
	for _, row := range matrix.Results {
		reasons := make([]string, 0, len(row.ReasonCodes))
		for _, code := range row.ReasonCodes {
			reasons = append(reasons, osMatrixReason(code))
		}
		fmt.Fprintf(&b, "| %s | %s | `%s` | **%s %s** | %s |\n", row.OS.Family, row.OS.Distribution, row.OS.Version, gradeGlyph(row.Grade), row.Grade, strings.Join(reasons, "; "))
	}
	b.WriteString("\n## Authority boundary\n\n")
	b.WriteString("Missing evidence is `unverified`, never an inferred pass and never a pre-beta release gate.\n")
	b.WriteString("Positive grades will require a future projector that validates complete CUE receipts,\n")
	b.WriteString("candidate identity, content hashes, observation windows, and protected producer provenance.\n")
	b.WriteString("Development diagnostics may exercise containers or controlled hosts, but cannot update this\n")
	b.WriteString("public support surface. Provider allocation, execution, leases, and cleanup belong to TechStack.\n\n")
	b.WriteString("Run `stackkit compat` for non-destructive host diagnostics. A clean diagnostic report does\n")
	b.WriteString("not create an OS support claim.\n")
	return b.String()
}

type osMatrixHistoryEntry struct {
	Version     string            `json:"version"`
	GeneratedAt string            `json:"generatedAt"`
	Grades      map[string]string `json:"grades"`
}

func renderOSMatrixFeed(latest osMatrixDoc, archiveDir string) ([]byte, error) {
	history := []osMatrixHistoryEntry{}
	entries, err := os.ReadDir(archiveDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read archive dir: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "v") || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(archiveDir, name))
		if err != nil {
			return nil, fmt.Errorf("read archive %s: %w", name, err)
		}
		matrix, err := decodeOSMatrix(raw)
		if err != nil {
			return nil, fmt.Errorf("archive %s: %w", name, err)
		}
		grades := map[string]string{}
		for _, row := range matrix.Results {
			grades[osMatrixRowKey(row)] = row.Grade
		}
		history = append(history, osMatrixHistoryEntry{Version: matrix.StackKitsVersion, GeneratedAt: matrix.GeneratedAt, Grades: grades})
	}
	sort.Slice(history, func(i, j int) bool { return history[i].Version > history[j].Version })
	feed := struct {
		SchemaVersion int                    `json:"schemaVersion"`
		Latest        osMatrixDoc            `json:"latest"`
		History       []osMatrixHistoryEntry `json:"history"`
	}{SchemaVersion: 2, Latest: latest, History: history}
	out, err := json.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func writeGenerated(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}
