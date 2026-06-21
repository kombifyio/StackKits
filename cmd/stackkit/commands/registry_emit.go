package commands

// registry_emit.go implements `stackkit registry emit-cue`: it renders the
// committed base/generated CUE artifacts from the registry snapshot
// (truth-consolidation plan 2026-06-10, WS-1).
//
// Replaces the legacy kombify-admin/scripts/generate-cue.ts path: instead of
// seeding a throwaway Postgres from a stale Prisma seed, ONE Admin snapshot
// fetch feeds both the runtime catalog (registry_snapshot.json) and the
// generated CUE files.
//
// Emission sources:
//   - #ToolCatalog: snapshot.Tools (kombify-DB sk_tool via Admin API).
//   - #ServiceCatalog: CUE module contracts (modules/<slug>/module.cue) — the
//     same source bake-from-cue uses. Service identity stays CUE-owned until
//     sk_service exists (registry types.go, Services doc comment).
//   - Tool categories: NOT emitted anymore. The curated taxonomy lives in
//     hand-written base/tool_categorization.cue (#ToolCategory); the emitted
//     #CatalogTool.category references it, so `cue vet` enforces membership
//     (resolves the TODO recorded in tool_categorization.cue).
//
// Determinism: output depends only on the snapshot file and the module tree
// (header carries the snapshot's generated_at/content_hash, never time.Now),
// so the CI parity gate can regenerate and `git diff --exit-code`.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	skcue "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/registry"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/spf13/cobra"

	"encoding/json"
	"os"
	"path/filepath"
)

var (
	registryEmitInput      string
	registryEmitModulesDir string
	registryEmitOutputDir  string
)

var registryEmitCmd = &cobra.Command{
	Use:   "emit-cue",
	Short: "Render base/generated CUE artifacts from the registry snapshot",
	Long: `Render base/generated/tool_catalog.cue from the registry snapshot plus the
local CUE module tree.

Reads the snapshot JSON (default: internal/registry/data/registry_snapshot.json,
refresh it first via 'registry snapshot' or 'registry bake-from-cue'), renders
deterministically, and writes the artifact. The header records the snapshot's
content_hash and generated_at so drift between snapshot and artifact is
detectable; CI regenerates from the committed snapshot and diffs.`,
	RunE: runRegistryEmitCUE,
}

func init() {
	registryEmitCmd.Flags().StringVar(&registryEmitInput, "input", defaultSnapshotPath(), "Snapshot JSON to render from")
	registryEmitCmd.Flags().StringVar(&registryEmitModulesDir, "modules-dir", "modules", "Directory containing modules/<slug>/module.cue (service catalog source)")
	registryEmitCmd.Flags().StringVar(&registryEmitOutputDir, "output-dir", filepath.Join("base", "generated"), "Directory for the rendered CUE files")
	registryCmd.AddCommand(registryEmitCmd)
}

func runRegistryEmitCUE(_ *cobra.Command, _ []string) error {
	raw, err := os.ReadFile(registryEmitInput) // #nosec G304 -- operator-controlled CLI input
	if err != nil {
		return fmt.Errorf("read snapshot %s: %w", registryEmitInput, err)
	}
	var snap registry.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return fmt.Errorf("decode snapshot %s: %w", registryEmitInput, err)
	}
	if snap.SchemaVersion != registry.SnapshotVersion {
		return fmt.Errorf("snapshot schema version mismatch: got %d, expected %d -- refresh with `stackkit registry snapshot` or `registry bake-from-cue`",
			snap.SchemaVersion, registry.SnapshotVersion)
	}

	reader := skcue.NewModuleReader()
	contracts, err := reader.ReadAllModules(registryEmitModulesDir)
	if err != nil {
		return fmt.Errorf("read modules: %w", err)
	}
	services := registryServicesFromCatalog(servicecatalog.FromCUE(serviceCatalogEntriesFromContracts(contracts)))

	content := renderToolCatalogCUE(snap, services)
	outPath := filepath.Join(registryEmitOutputDir, "tool_catalog.cue")
	if err := os.MkdirAll(registryEmitOutputDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", registryEmitOutputDir, err)
	}
	if err := os.WriteFile(outPath, content, 0o644); err != nil { // #nosec G306 -- generated source artifact
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	printSuccess("Wrote %s", outPath)
	printInfo("tools=%d services=%d snapshot_source=%s content_hash=%s",
		len(snap.Tools), len(services), snap.Source, orNone(snap.ContentHash))
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// cueStr renders a CUE string literal. Go's quoting rules are a compatible
// subset of CUE string escapes (\" \\ \n \uXXXX).
func cueStr(s string) string {
	return strconv.Quote(s)
}

// renderToolCatalogCUE is pure and deterministic: same snapshot + services in,
// byte-identical artifact out. Golden-tested in registry_emit_test.go.
func renderToolCatalogCUE(snap registry.Snapshot, services []registry.Service) []byte {
	var b strings.Builder

	hash := snap.ContentHash
	if hash == "" {
		hash = "(none — CUE-baked snapshot)"
	}
	fmt.Fprintf(&b, `// Code generated by 'stackkit registry emit-cue'. DO NOT EDIT.
//
// Source of truth (ADR-0010): kombify-DB sk_* exported through the Admin
// registry snapshot. Refresh chain:
//   stackkit registry snapshot --endpoint $STACKKIT_ADMIN_ENDPOINT   (or bake-from-cue)
//   stackkit registry emit-cue
//
// Snapshot: source=%s generated_at=%s
// Snapshot content_hash: %s
// Services: rendered from CUE module contracts (modules/<slug>/module.cue),
// the same source 'registry bake-from-cue' uses.
// Tool categories: see base/tool_categorization.cue (#ToolCategory) — the
// curated taxonomy is hand-written there and enforced here via 'cue vet'.

package base
`, snap.Source, snap.GeneratedAt.UTC().Format("2006-01-02T15:04:05Z"), hash)

	b.WriteString(`
// =============================================================================
// TOOL CATALOG DEFINITIONS
// =============================================================================

#CatalogTool: {
	name:         string
	displayName:  string
	description?: string
	layer?:       string
	category:     #ToolCategory
	status?:      string
	homepage?:    string
	logoUrl?:     string
	imageUrl?:    string
	tags?: [...string]
}

#IdentityPolicy: "none" | "forwardauth" | "oidc" | "provider" | "self-auth"

#OwnerProvisioningPolicy: "none" | "required"

#SetupPolicy: "manual" | "on_demand" | "automatic"

#CatalogService: {
	key:                     string
	displayName:             string
	description?:            string
	toolName:                string
	moduleSlug:              string
	role?:                   string
	defaultTool?:            string
	alternatives?: [...string]
	localSlug:     string
	publicSlug:    string
	legacyAliases: [...string]
	identityPolicy:          #IdentityPolicy
	ownerProvisioningPolicy: #OwnerProvisioningPolicy
	icon?:              string
	logoUrl?:           string
	badge?:             string
	layer?:             string
	section?:           string
	order:              int
	enableVar?:         string
	guideUrl?:          string
	setupPolicy?:       #SetupPolicy
	setupActionLabel?:  string
	delivery?: {
		managedBy: string
	}
	bootstrapProvider?: string
	default:            bool | *false
}
`)

	renderCatalogTools(&b, snap.Tools)
	renderCatalogServices(&b, services)

	return []byte(b.String())
}

// writeField emits one optional CUE field line; empty values are omitted so
// the artifact matches Go omitempty semantics.
func writeField(b *strings.Builder, label, value string) {
	if value != "" {
		fmt.Fprintf(b, "%s %s\n", label, cueStr(value))
	}
}

func renderCatalogTools(b *strings.Builder, tools []registry.Tool) {
	b.WriteString(`
// =============================================================================
// CATALOG TOOLS (kombify-DB sk_tool via Admin snapshot)
// =============================================================================

#ToolCatalog: {
`)
	sorted := append([]registry.Tool(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })
	for _, t := range sorted {
		fmt.Fprintf(b, "\t%s: {\n", cueStr(t.Slug))
		fmt.Fprintf(b, "\t\tname:        %s\n", cueStr(t.Slug))
		fmt.Fprintf(b, "\t\tdisplayName: %s\n", cueStr(t.DisplayName))
		writeField(b, "\t\tdescription:", t.Description)
		writeField(b, "\t\tlayer:      ", t.Layer)
		fmt.Fprintf(b, "\t\tcategory:    %s\n", cueStr(t.Category))
		writeField(b, "\t\tstatus:     ", t.Status)
		writeField(b, "\t\thomepage:   ", t.Homepage)
		writeField(b, "\t\tlogoUrl:    ", t.LogoURL)
		writeField(b, "\t\timageUrl:   ", t.ImageURL)
		if len(t.Tags) > 0 {
			fmt.Fprintf(b, "\t\ttags: [%s]\n", cueStrList(t.Tags))
		}
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n")
}

func renderCatalogServices(b *strings.Builder, services []registry.Service) {
	b.WriteString(`
// =============================================================================
// SERVICE CATALOG (CUE module contracts)
// =============================================================================

#ServiceCatalog: {
`)
	sorted := append([]registry.Service(nil), services...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	for _, s := range sorted {
		fmt.Fprintf(b, "\t%s: {\n", cueStr(s.Key))
		fmt.Fprintf(b, "\t\tkey:                     %s\n", cueStr(s.Key))
		fmt.Fprintf(b, "\t\tdisplayName:             %s\n", cueStr(s.DisplayName))
		writeField(b, "\t\tdescription:            ", s.Description)
		fmt.Fprintf(b, "\t\ttoolName:                %s\n", cueStr(s.ToolName))
		fmt.Fprintf(b, "\t\tmoduleSlug:              %s\n", cueStr(s.ModuleSlug))
		writeField(b, "\t\trole:                   ", s.Role)
		writeField(b, "\t\tdefaultTool:            ", s.DefaultTool)
		if len(s.Alternatives) > 0 {
			fmt.Fprintf(b, "\t\talternatives: [%s]\n", cueStrList(s.Alternatives))
		}
		fmt.Fprintf(b, "\t\tlocalSlug:               %s\n", cueStr(s.LocalSlug))
		fmt.Fprintf(b, "\t\tpublicSlug:              %s\n", cueStr(s.PublicSlug))
		fmt.Fprintf(b, "\t\tlegacyAliases: [%s]\n", cueStrList(s.LegacyAliases))
		fmt.Fprintf(b, "\t\tidentityPolicy:          %s\n", cueStr(s.IdentityPolicy))
		fmt.Fprintf(b, "\t\townerProvisioningPolicy: %s\n", cueStr(s.OwnerProvisioningPolicy))
		writeField(b, "\t\ticon:            ", s.Icon)
		writeField(b, "\t\tlogoUrl:         ", s.LogoURL)
		writeField(b, "\t\tbadge:           ", s.Badge)
		writeField(b, "\t\tlayer:           ", s.Layer)
		writeField(b, "\t\tsection:         ", s.Section)
		fmt.Fprintf(b, "\t\torder:            %d\n", s.Order)
		writeField(b, "\t\tenableVar:       ", s.EnableVar)
		writeField(b, "\t\tguideUrl:        ", s.GuideURL)
		writeField(b, "\t\tsetupPolicy:     ", s.SetupPolicy)
		writeField(b, "\t\tsetupActionLabel:", s.SetupActionLabel)
		if s.Delivery.ManagedBy != "" {
			fmt.Fprintf(b, "\t\tdelivery: {managedBy: %s}\n", cueStr(s.Delivery.ManagedBy))
		}
		writeField(b, "\t\tbootstrapProvider:", s.BootstrapProvider)
		fmt.Fprintf(b, "\t\tdefault:          %t\n", s.Default)
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n")
}

func cueStrList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, cueStr(v))
	}
	return strings.Join(quoted, ", ")
}
