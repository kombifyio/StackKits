package commands

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// compat emit-os-matrix renders the committed OS-compatibility matrix data
// (docs/data/os-compat/latest.json, ingested from os-matrix workflow runs)
// into the two generated consumer surfaces:
//   - docs/OS_COMPATIBILITY.md      (public mirror doc, DO NOT EDIT)
//   - website/public/os-compat.json (stackkit.cc feed with version history)
//
// The committed JSON is the single in-repo source of truth; both outputs are
// pure functions of it, parity-gated in CI (os-compat-docs-parity). The
// emitter is the third redaction line of defense after RedactForPublish and
// validate-os-matrix.mjs: it refuses inputs carrying private fields.

var (
	emitOSMatrixInput      string
	emitOSMatrixArchiveDir string
	emitOSMatrixDocsOut    string
	emitOSMatrixWebsiteOut string
)

var compatEmitOSMatrixCmd = &cobra.Command{
	Use:   "emit-os-matrix",
	Short: "Render the OS compatibility matrix doc and website feed from committed matrix data",
	RunE:  runCompatEmitOSMatrix,
}

func init() {
	compatEmitOSMatrixCmd.Flags().StringVar(&emitOSMatrixInput, "input", "docs/data/os-compat/latest.json", "committed public matrix JSON (redacted form)")
	compatEmitOSMatrixCmd.Flags().StringVar(&emitOSMatrixArchiveDir, "archive-dir", "docs/data/os-compat", "directory holding per-release v*.json archives for the history feed")
	compatEmitOSMatrixCmd.Flags().StringVar(&emitOSMatrixDocsOut, "docs-out", "docs/OS_COMPATIBILITY.md", "generated markdown output")
	compatEmitOSMatrixCmd.Flags().StringVar(&emitOSMatrixWebsiteOut, "website-out", "website/public/os-compat.json", "generated website feed output")
	compatCmd.AddCommand(compatEmitOSMatrixCmd)
}

// osMatrixDoc mirrors the public matrix JSON (schemas/os-compat-matrix.schema.json).
type osMatrixDoc struct {
	SchemaVersion   int              `json:"schemaVersion"`
	Kit             string           `json:"kit,omitempty"`
	StackKitVersion string           `json:"stackkitVersion,omitempty"`
	RunID           string           `json:"runId"`
	Results         []osMatrixDocRow `json:"results"`
	GeneratedAt     string           `json:"generatedAt"`
}

type osMatrixDocRow struct {
	OS          osMatrixDocOS    `json:"os"`
	RunID       string           `json:"runId,omitempty"`
	Lane        string           `json:"lane,omitempty"`
	Overall     string           `json:"overall"`
	Stages      []osMatrixDocStg `json:"stages"`
	GeneratedAt string           `json:"generatedAt,omitempty"`
}

type osMatrixDocOS struct {
	ID           string `json:"id"`
	DistroFamily string `json:"distroFamily,omitempty"`
	Version      string `json:"version,omitempty"`
	Arch         string `json:"arch,omitempty"`
	PackageMgr   string `json:"packageMgr,omitempty"`
	InitSystem   string `json:"initSystem,omitempty"`
	VirtType     string `json:"virtType,omitempty"`
	VirtTier     string `json:"virtTier,omitempty"`
	Kernel       string `json:"kernel,omitempty"`
}

type osMatrixDocStg struct {
	Stage         string   `json:"stage"`
	Status        string   `json:"status"`
	Preconditions []string `json:"preconditions,omitempty"`
	Limitations   []string `json:"limitations,omitempty"`
	Detail        string   `json:"detail,omitempty"`
}

var osMatrixPrivateKeys = regexp.MustCompile(`"(osReleaseRaw|evidencePath|mdnsHost)"\s*:`)
var osMatrixRFC1918 = regexp.MustCompile(`(^|[^0-9.])(10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}|192\.168\.[0-9]{1,3}\.[0-9]{1,3}|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3})([^0-9.]|$)`)

// docStageColumns is the fixed column order of the markdown table. serverprep
// and binary-boot (TechStack lane stages) are not part of the StackKit doc.
var docStageColumns = []string{"cpu-baseline", "install", "prepare", "init", "generate", "apply", "service-up"}

func runCompatEmitOSMatrix(cmd *cobra.Command, args []string) error {
	raw, err := os.ReadFile(emitOSMatrixInput)
	if err != nil {
		return fmt.Errorf("read matrix input: %w", err)
	}
	if err := checkOSMatrixPublishable(raw); err != nil {
		return err
	}
	var matrix osMatrixDoc
	if err := json.Unmarshal(raw, &matrix); err != nil {
		return fmt.Errorf("parse matrix input: %w", err)
	}
	if matrix.SchemaVersion != 1 || len(matrix.Results) == 0 {
		return fmt.Errorf("matrix input must be schemaVersion 1 with at least one result")
	}
	sort.Slice(matrix.Results, func(i, j int) bool { return matrix.Results[i].OS.ID < matrix.Results[j].OS.ID })

	sourceHash := sha256.Sum256(raw)
	markdown := renderOSMatrixMarkdown(matrix, fmt.Sprintf("%x", sourceHash))
	if err := writeGenerated(emitOSMatrixDocsOut, []byte(markdown)); err != nil {
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

// checkOSMatrixPublishable refuses inputs that still carry private producer
// data — committed matrix data must already be the redacted public form.
func checkOSMatrixPublishable(raw []byte) error {
	if m := osMatrixPrivateKeys.Find(raw); m != nil {
		return fmt.Errorf("matrix input carries private field %s — only RedactForPublish output may be committed", string(m))
	}
	if osMatrixRFC1918.Match(raw) {
		return fmt.Errorf("matrix input contains RFC1918 addresses — redaction leak, refusing to emit")
	}
	if regexp.MustCompile(`"target"\s*:\s*\{\s*"`).Match(raw) {
		return fmt.Errorf("matrix input carries a populated target object — redaction leak, refusing to emit")
	}
	return nil
}

func gradeGlyph(status string) string {
	switch status {
	case "supported":
		return "✅"
	case "partial":
		return "🟡"
	case "unsupported":
		return "❌"
	case "":
		return "—"
	default:
		return status
	}
}

func renderOSMatrixMarkdown(matrix osMatrixDoc, sourceHash string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- Code generated by 'stackkit compat emit-os-matrix'. DO NOT EDIT. source_hash: %s -->\n\n", sourceHash)
	b.WriteString("# OS Compatibility\n\n")
	kit := matrix.Kit
	if kit == "" {
		kit = "basement-kit"
	}
	fmt.Fprintf(&b, "Automated rollout compatibility of **%s** across Linux distributions,\n", kit)
	b.WriteString("graded per stage by the OS-compatibility matrix lanes (containers for\nbreadth, full VM rollouts for depth, occasional bare-metal spot checks).\n\n")
	version := matrix.StackKitVersion
	if version == "" {
		version = "unreleased"
	}
	fmt.Fprintf(&b, "- StackKit version: `%s`\n- Run: `%s`\n- Generated: %s\n\n", version, matrix.RunID, matrix.GeneratedAt)
	b.WriteString("Legend: ✅ supported · 🟡 partial (works with the noted preconditions or\nreduced coverage) · ❌ unsupported · — stage not run for this lane.\n\n")

	b.WriteString("| OS | Lane | " + strings.Join(docStageColumns, " | ") + " | Overall |\n")
	b.WriteString("| --- | --- |" + strings.Repeat(" :-: |", len(docStageColumns)) + " :-: |\n")

	type footnote struct {
		index int
		text  string
	}
	var footnotes []footnote
	noteIndex := map[string]int{}
	noteRef := func(texts []string) string {
		var refs []string
		for _, t := range texts {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			idx, ok := noteIndex[t]
			if !ok {
				idx = len(noteIndex) + 1
				noteIndex[t] = idx
				footnotes = append(footnotes, footnote{index: idx, text: t})
			}
			refs = append(refs, fmt.Sprintf("[^%d]", idx))
		}
		return strings.Join(refs, "")
	}

	for _, row := range matrix.Results {
		stagesByName := map[string]osMatrixDocStg{}
		for _, st := range row.Stages {
			stagesByName[st.Stage] = st
		}
		lane := row.Lane
		if lane == "" {
			lane = "vm"
		}
		cells := []string{fmt.Sprintf("`%s`", row.OS.ID), lane}
		for _, col := range docStageColumns {
			st, ok := stagesByName[col]
			if !ok {
				cells = append(cells, "—")
				continue
			}
			cells = append(cells, gradeGlyph(st.Status)+noteRef(append(append([]string{}, st.Preconditions...), st.Limitations...)))
		}
		cells = append(cells, "**"+gradeGlyph(row.Overall)+"**")
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	b.WriteString("\n")
	for _, fn := range footnotes {
		fmt.Fprintf(&b, "[^%d]: %s\n", fn.index, fn.text)
	}
	if len(footnotes) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("Provider/virtualization compatibility (KVM vs LXC vs OpenVZ, kernel\nfeatures, known VPS providers) is a separate axis — see\n[vps-compatibility.md](vps-compatibility.md) and `stackkit compat`.\n")
	return b.String()
}

// osMatrixHistoryEntry summarizes one archived per-release matrix for the feed.
type osMatrixHistoryEntry struct {
	Version     string            `json:"version"`
	RunID       string            `json:"runId"`
	GeneratedAt string            `json:"generatedAt"`
	Overall     map[string]string `json:"overall"` // os.id -> grade
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
		if err := checkOSMatrixPublishable(raw); err != nil {
			return nil, fmt.Errorf("archive %s: %w", name, err)
		}
		var m osMatrixDoc
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("parse archive %s: %w", name, err)
		}
		overall := map[string]string{}
		for _, r := range m.Results {
			overall[r.OS.ID] = r.Overall
		}
		version := strings.TrimSuffix(name, ".json")
		if m.StackKitVersion != "" {
			version = m.StackKitVersion
		}
		history = append(history, osMatrixHistoryEntry{
			Version:     version,
			RunID:       m.RunID,
			GeneratedAt: m.GeneratedAt,
			Overall:     overall,
		})
	}
	sort.Slice(history, func(i, j int) bool { return history[i].Version > history[j].Version })

	feed := struct {
		SchemaVersion int                    `json:"schemaVersion"`
		Latest        osMatrixDoc            `json:"latest"`
		History       []osMatrixHistoryEntry `json:"history"`
	}{SchemaVersion: 1, Latest: latest, History: history}
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
