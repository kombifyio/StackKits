// Package kittemplates renders the per-kit OpenTofu/Terramate template trees
// from the single canonical source under base/templates/.
//
// base/templates/ is the source of truth. The committed per-kit trees
// (basement-kit/templates/, cloud-kit/templates/) are generated artifacts:
// edit base/templates/, then run `go generate ./...` (or
// `go run ./cmd/gen-kit-templates`). The freshness test in this package fails
// if a per-kit tree drifts from what the canonical source would produce, so the
// duplication can never be edited out of sync.
//
// The canonical files carry three literal sentinels — substituted per kit — and
// are otherwise byte-identical to every kit. Sentinels are plain literals (not
// Go template directives) so the 46 runtime `{{ ... }}` directives and HCL
// `${ ... }` interpolations in the templates pass through untouched and are
// rendered later by internal/template at `stackkit generate` time.
package kittemplates

//go:generate go run github.com/kombifyio/stackkits/cmd/gen-kit-templates

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CanonicalDir is the repo-relative path to the canonical template source.
const CanonicalDir = "base/templates"

// Kit identifies a derived product kit. Slug is also the output directory name
// and matches the kit's stackkit.yaml metadata.name.
type Kit struct{ Slug string }

// Kits is the ordered set of kits derived from the canonical templates.
var Kits = []Kit{{Slug: "basement-kit"}, {Slug: "cloud-kit"}}

// Sentinel tokens substituted in the canonical source.
const (
	sentinelSlug         = "__KIT_SLUG__"          // -> "basement-kit"
	sentinelDisplay      = "__KIT_DISPLAY__"       // -> "Basement Kit"
	sentinelDisplayUpper = "__KIT_DISPLAY_UPPER__" // -> "BASEMENT KIT"
)

// boxBorder is the vertical bar used by the ASCII architecture-summary boxes in
// the templates. Banner lines containing a substituted kit name are re-padded so
// the box border stays aligned regardless of the kit name's length.
const boxBorder = '║'

// skipFiles are canonical files that document the source itself and must not be
// materialized into the per-kit trees.
var skipFiles = map[string]bool{"README.md": true}

// DisplayName returns the human kit name for a slug, e.g. "basement-kit" ->
// "Basement Kit".
func DisplayName(slug string) string {
	base := strings.TrimSuffix(slug, "-kit")
	if base == "" {
		base = slug
	}
	if base != "" {
		base = strings.ToUpper(base[:1]) + base[1:]
	}
	return base + " Kit"
}

// DisplayUpper returns the all-caps banner form, e.g. "basement-kit" ->
// "BASEMENT KIT".
func DisplayUpper(slug string) string { return strings.ToUpper(DisplayName(slug)) }

// RenderBytes applies the kit substitutions to one canonical file's content and
// re-aligns any box-bordered banner line whose length changed as a result.
func RenderBytes(slug string, content []byte) []byte {
	lines := strings.Split(string(content), "\n")
	width := dominantBoxWidth(lines)
	for i, line := range lines {
		changed := strings.Contains(line, sentinelSlug) ||
			strings.Contains(line, sentinelDisplay) ||
			strings.Contains(line, sentinelDisplayUpper)
		line = strings.ReplaceAll(line, sentinelDisplayUpper, DisplayUpper(slug))
		line = strings.ReplaceAll(line, sentinelDisplay, DisplayName(slug))
		line = strings.ReplaceAll(line, sentinelSlug, slug)
		if changed && width > 0 {
			if aligned, ok := realignBoxLine(line, width); ok {
				line = aligned
			}
		}
		lines[i] = line
	}
	return []byte(strings.Join(lines, "\n"))
}

// RenderKit returns the materialized file set for one kit as a map of
// canonical-relative (slash-separated) path -> content.
func RenderKit(canonicalDir, slug string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(canonicalDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(canonicalDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skipFiles[rel] {
			return nil
		}
		content, err := os.ReadFile(path) // #nosec G122 -- path comes from WalkDir over the repo-controlled canonical template dir (build-time codegen via `go generate`), not untrusted input; no attacker-controlled symlinks.
		if err != nil {
			return err
		}
		out[rel] = RenderBytes(slug, content)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Generate (re)writes every kit's templates/ tree under repoRoot from the
// canonical source. Each kit's templates/ directory is removed and recreated so
// stale files cannot survive a regeneration.
func Generate(repoRoot string) error {
	canonical := filepath.Join(repoRoot, filepath.FromSlash(CanonicalDir))
	for _, kit := range Kits {
		files, err := RenderKit(canonical, kit.Slug)
		if err != nil {
			return fmt.Errorf("render %s: %w", kit.Slug, err)
		}
		dst := filepath.Join(repoRoot, kit.Slug, "templates")
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("clean %s: %w", dst, err)
		}
		for rel, content := range files {
			outPath := filepath.Join(dst, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(outPath, content, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// FindRepoRoot walks up from start to locate the directory containing go.mod.
func FindRepoRoot(start string) (string, error) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", start)
		}
		dir = parent
	}
}

// dominantBoxWidth returns the most common inner rune width among bordered box
// lines that carry neither a sentinel nor a Terraform interpolation (${...}), or
// 0 when the content has no such box. Sentinel-bearing banner lines are excluded
// because their post-substitution width is exactly what we re-pad to width.
func dominantBoxWidth(lines []string) int {
	counts := map[int]int{}
	for _, line := range lines {
		if strings.Contains(line, "${") {
			continue
		}
		if strings.Contains(line, sentinelSlug) ||
			strings.Contains(line, sentinelDisplay) ||
			strings.Contains(line, sentinelDisplayUpper) {
			continue
		}
		_, inner, trailing, ok := splitBoxLine(line)
		if !ok || strings.TrimSpace(trailing) != "" {
			continue
		}
		counts[len([]rune(inner))]++
	}
	best, bestN := 0, 0
	for w, n := range counts {
		if n > bestN || (n == bestN && w > best) {
			best, bestN = w, n
		}
	}
	return best
}

// realignBoxLine re-pads the trailing spaces inside a bordered box line so its
// inner width equals width, preserving the leading content. ok is false when the
// line is not a box line or its content already exceeds width.
func realignBoxLine(line string, width int) (string, bool) {
	indent, inner, trailing, ok := splitBoxLine(line)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimRight(inner, " ")
	pad := width - len([]rune(trimmed))
	if pad < 0 {
		return "", false
	}
	return indent + string(boxBorder) + trimmed + strings.Repeat(" ", pad) + string(boxBorder) + trailing, true
}

// splitBoxLine splits a `<indent>║<inner>║<trailing>` line into its parts. ok is
// false when the line has no surrounding border (only whitespace may precede the
// first border).
func splitBoxLine(line string) (indent, inner, trailing string, ok bool) {
	r := []rune(line)
	first := -1
	for i, c := range r {
		if c == boxBorder {
			first = i
			break
		}
		if c != ' ' && c != '\t' {
			return "", "", "", false
		}
	}
	if first < 0 {
		return "", "", "", false
	}
	last := -1
	for i := len(r) - 1; i > first; i-- {
		if r[i] == boxBorder {
			last = i
			break
		}
	}
	if last <= first {
		return "", "", "", false
	}
	return string(r[:first]), string(r[first+1 : last]), string(r[last+1:]), true
}
