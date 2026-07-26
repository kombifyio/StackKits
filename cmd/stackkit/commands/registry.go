package commands

// registry.go implements public `stackkit registry` subcommands that inspect
// and reproduce the embedded OSS-safe snapshot under internal/registry/data/.
//
//   - `bake-from-cue`  produces a snapshot purely from the local CUE
//                      module tree. Used as the OSS bootstrap path and
//                      for offline development.
//
//   - `info`           prints a human-readable summary of the currently
//                      embedded snapshot so operators can see what the
//                      CLI would serve in offline mode.
//
// The OSS contract: a pure checkout of the public kombifyio/stackKits
// repo must build and run the CLI without an account or private service.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	skcue "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/registry"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/spf13/cobra"
)

var (
	registryBakeModulesDir string
	registryBakeOutput     string

	registryInfoJSON bool
)

var registryCmd = &cobra.Command{
	Use:   "registry",
	Short: "Manage the embedded StackKits registry snapshot",
	Long: `Manage the OSS-safe registry snapshot baked into the CLI binary.

The snapshot lives at internal/registry/data/registry_snapshot.json. Inspect
the embedded data or reproduce it deterministically from the local CUE tree.`,
}

var registryBakeCmd = &cobra.Command{
	Use:   "bake-from-cue",
	Short: "Produce a registry snapshot from the local CUE module tree",
	Long: `Walk modules/<slug>/module.cue, compute each module's contract_hash, and
write the resulting Snapshot to internal/registry/data/registry_snapshot.json
(or --output).

This command is the offline OSS bootstrap path and works on a pure checkout
of the public kombifyio/stackKits repo.`,
	RunE: runRegistryBakeFromCUE,
}

var registryInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show a summary of the embedded registry snapshot",
	RunE:  runRegistryInfo,
}

func init() {
	defaultOut := defaultSnapshotPath()

	registryBakeCmd.Flags().StringVar(&registryBakeModulesDir, "modules-dir", "modules", "Directory containing module/<slug>/module.cue")
	registryBakeCmd.Flags().StringVar(&registryBakeOutput, "output", defaultOut, "Output path for the snapshot JSON")

	registryInfoCmd.Flags().BoolVar(&registryInfoJSON, "json", false, "Print the full embedded snapshot as JSON")

	registryCmd.AddCommand(registryBakeCmd)
	registryCmd.AddCommand(registryInfoCmd)
	rootCmd.AddCommand(registryCmd)
}

// defaultSnapshotPath resolves to internal/registry/data/registry_snapshot.json
// relative to the repo root when running from a checkout, or to the
// current directory otherwise. The path is only read by the two writer
// commands; the EmbeddedClient always uses the compiled-in bytes.
func defaultSnapshotPath() string {
	return filepath.Join("internal", "registry", "data", "registry_snapshot.json")
}

func runRegistryBakeFromCUE(_ *cobra.Command, _ []string) error {
	reader := skcue.NewModuleReader()
	contracts, err := reader.ReadAllModules(registryBakeModulesDir)
	if err != nil {
		return fmt.Errorf("read modules: %w", err)
	}

	modules := make([]registry.Module, 0, len(contracts))
	for _, c := range contracts {
		slug := c.Metadata.Name
		if slug == "" {
			continue
		}
		hash, err := skcue.ContractHash(moduleContractToCanonicalMap(c))
		if err != nil {
			return fmt.Errorf("hash %s: %w", slug, err)
		}
		modules = append(modules, registry.Module{
			Slug:              slug,
			DisplayName:       c.Metadata.DisplayName,
			Version:           c.Metadata.Version,
			Layer:             c.Metadata.Layer,
			Description:       c.Metadata.Description,
			ContractHash:      hash,
			Core:              c.Metadata.Core,
			SupportedContexts: []string{"local", "cloud", "pi"},
		})
	}

	snap := registry.Snapshot{
		SchemaVersion: registry.SnapshotVersion,
		Source:        "cue",
		GeneratedAt:   reproducibleNow(),
		Modules:       modules,
		Services:      registryServicesFromCatalog(servicecatalog.FromCUE(serviceCatalogEntriesFromContracts(contracts))),
		// Tools and StackKits are authoritative only in the DB; OSS
		// bakes are module-centric. Admin API baking fills the rest.
		Tools:     []registry.Tool{},
		StackKits: []registry.StackKit{},
	}
	sortSnapshot(&snap)

	if err := writeSnapshot(registryBakeOutput, snap); err != nil {
		return err
	}

	printSuccess("Wrote registry snapshot to %s", registryBakeOutput)
	printInfo("source=cue services=%d modules=%d (tools/stackkits empty in CUE-only bake)", len(snap.Services), len(snap.Modules))
	return nil
}

// reproducibleNow returns a deterministic timestamp when SOURCE_DATE_EPOCH is
// set (the reproducible-builds convention), so that repeated CUE bakes of the
// same source tree are byte-identical. The public-export pipeline pins this to
// the source commit time so the curated export and the pushed mirror carry the
// exact same snapshot and the parity audit holds. Falls back to the wall clock
// when the variable is absent or unparseable.
func reproducibleNow() time.Time {
	if epoch := strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH")); epoch != "" {
		if secs, err := strconv.ParseInt(epoch, 10, 64); err == nil {
			return time.Unix(secs, 0).UTC()
		}
	}
	return time.Now().UTC()
}

func runRegistryInfo(_ *cobra.Command, _ []string) error {
	snap, err := registry.EmbeddedSnapshot()
	if err != nil {
		return err
	}
	if registryInfoJSON {
		out, err := json.MarshalIndent(snap, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	fmt.Printf("Embedded registry snapshot\n")
	fmt.Printf("  schema_version : %d\n", snap.SchemaVersion)
	fmt.Printf("  source         : %s\n", snap.Source)
	fmt.Printf("  generated_at   : %s\n", snap.GeneratedAt.Format(time.RFC3339))
	if snap.AdminEndpoint != "" {
		fmt.Printf("  admin_endpoint : %s\n", snap.AdminEndpoint)
	}
	if snap.ContentHash != "" {
		fmt.Printf("  content_hash   : %s\n", snap.ContentHash)
	}
	fmt.Printf("  tools          : %d\n", len(snap.Tools))
	fmt.Printf("  services       : %d\n", len(snap.Services))
	fmt.Printf("  modules        : %d\n", len(snap.Modules))
	fmt.Printf("  stackkits      : %d\n", len(snap.StackKits))
	fmt.Printf("  service_groups : %d\n", len(snap.ServiceGroups))
	fmt.Printf("  tool_defaults  : %d\n", len(snap.ToolDefaultConfigs))

	if len(snap.Modules) > 0 {
		fmt.Println()
		fmt.Println("Modules:")
		for _, m := range snap.Modules {
			fmt.Printf("  %-22s %-10s %s\n", m.Slug, m.Version, shortHash(m.ContractHash))
		}
	}
	if len(snap.StackKits) > 0 {
		fmt.Println()
		fmt.Println("StackKits:")
		for _, sk := range snap.StackKits {
			fmt.Printf("  %-22s (%d modules)\n", sk.Slug, len(sk.Modules))
		}
	}
	return nil
}

// writeSnapshot serializes snap to a pretty-printed JSON file at path.
// The file uses the same 2-space indent as hand-written fixtures so
// diffs stay small.
func writeSnapshot(path string, snap registry.Snapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	// Trailing newline for POSIX friendliness + git hygiene.
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { // #nosec G304 G703 -- path is constructed by the registry-cache layer from operator-controlled CLI inputs.
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func serviceCatalogEntriesFromContracts(contracts []skcue.ModuleContract) []skcue.CatalogEntry {
	var entries []skcue.CatalogEntry
	for _, contract := range contracts {
		for _, svc := range contract.Services {
			if svc.SubdomainKey == "" {
				continue
			}
			displayName := svc.DisplayName
			if displayName == "" {
				displayName = contract.Metadata.DisplayName
			}
			description := svc.Description
			if description == "" {
				description = contract.Metadata.Description
			}
			if description == "" {
				description = svc.OutputDesc
			}
			entries = append(entries, skcue.CatalogEntry{
				Key:         svc.SubdomainKey,
				Nested:      svc.SubdomainNested,
				Flat:        svc.SubdomainFlat,
				ToolName:    svc.Name,
				ModuleSlug:  contract.Metadata.Name,
				DisplayName: displayName,
				Description: description,
				Icon:        svc.DashboardIcon,
				Badge:       svc.DashboardBadge,
				Section:     svc.DashboardSection,
				Order:       svc.DashboardOrder,
				EnableVar:   svc.DashboardEnableVar,
			})
		}
	}
	return entries
}

func registryServicesFromCatalog(catalog []servicecatalog.Service) []registry.Service {
	services := make([]registry.Service, 0, len(catalog))
	for _, svc := range catalog {
		services = append(services, registry.Service{
			Key:                     svc.Key,
			DisplayName:             svc.DisplayName,
			Description:             svc.Description,
			ToolName:                svc.ToolName,
			ModuleSlug:              svc.ModuleSlug,
			LocalSlug:               svc.LocalSlug,
			PublicSlug:              svc.PublicSlug,
			LegacyAliases:           append([]string(nil), svc.LegacyAliases...),
			IdentityPolicy:          svc.IdentityPolicy,
			OwnerProvisioningPolicy: svc.OwnerProvisioningPolicy,
			Icon:                    svc.Icon,
			LogoURL:                 svc.LogoURL,
			Badge:                   svc.Badge,
			Layer:                   svc.Layer,
			Section:                 svc.Section,
			Order:                   svc.Order,
			EnableVar:               svc.EnableVar,
			Default:                 svc.Default,
			GuideURL:                svc.GuideURL,
			SetupPolicy:             svc.SetupPolicy,
			SetupActionLabel:        svc.SetupActionLabel,
		})
	}
	return services
}

// sortSnapshot deterministically orders all slices so two snapshots
// that encode the same data hash identically.
func sortSnapshot(snap *registry.Snapshot) {
	sort.Slice(snap.Tools, func(i, j int) bool { return snap.Tools[i].Slug < snap.Tools[j].Slug })
	sort.Slice(snap.Services, func(i, j int) bool { return snap.Services[i].Key < snap.Services[j].Key })
	sort.Slice(snap.Modules, func(i, j int) bool { return snap.Modules[i].Slug < snap.Modules[j].Slug })
	sort.Slice(snap.StackKits, func(i, j int) bool { return snap.StackKits[i].Slug < snap.StackKits[j].Slug })
	for i := range snap.StackKits {
		kit := &snap.StackKits[i]
		sort.Slice(kit.Modules, func(a, b int) bool { return kit.Modules[a].Slug < kit.Modules[b].Slug })
		sort.Slice(kit.ServiceSelections, func(a, b int) bool {
			return kit.ServiceSelections[a].ServiceGroupSlug < kit.ServiceSelections[b].ServiceGroupSlug
		})
		sort.Slice(kit.SpecProfiles, func(a, b int) bool {
			return kit.SpecProfiles[a].Slug < kit.SpecProfiles[b].Slug
		})
		sort.Slice(kit.ToolConfigs, func(a, b int) bool {
			if kit.ToolConfigs[a].ServiceGroupSlug != kit.ToolConfigs[b].ServiceGroupSlug {
				return kit.ToolConfigs[a].ServiceGroupSlug < kit.ToolConfigs[b].ServiceGroupSlug
			}
			return kit.ToolConfigs[a].ModuleSlug < kit.ToolConfigs[b].ModuleSlug
		})
	}
	sort.Slice(snap.ServiceGroups, func(i, j int) bool {
		return snap.ServiceGroups[i].Slug < snap.ServiceGroups[j].Slug
	})
	sort.Slice(snap.ToolDefaultConfigs, func(i, j int) bool {
		return snap.ToolDefaultConfigs[i].ToolSlug < snap.ToolDefaultConfigs[j].ToolSlug
	})
}
