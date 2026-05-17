package commands

// registry.go implements `stackkit registry` subcommands that manage the
// embedded OSS-safe registry snapshot under internal/registry/data/.
//
//   - `snapshot`       fetches the live registry from the Admin API
//                      (kombify-internal) and writes it to disk. Used
//                      by release pipelines to bake fresh data into the
//                      private CLI build before syncing to OSS.
//
//   - `bake-from-cue`  produces a snapshot purely from the local CUE
//                      module tree. Used as the OSS bootstrap path and
//                      for dev machines that have no Admin API access.
//
//   - `info`           prints a human-readable summary of the currently
//                      embedded snapshot so operators can see what the
//                      CLI would serve in offline mode.
//
// The OSS contract: a pure checkout of the public kombifyio/stackKits
// repo must build and run the CLI with no Admin API access. `bake-from-
// cue` is the escape hatch that keeps that promise -- it never touches
// Postgres or any kombify-internal endpoint.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	skcue "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/registry"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/spf13/cobra"
)

var (
	registrySnapshotEndpoint string
	registrySnapshotToken    string
	registrySnapshotOutput   string

	registryBakeModulesDir string
	registryBakeOutput     string

	registryInfoJSON bool
)

var registryCmd = &cobra.Command{
	Use:   "registry",
	Short: "Manage the embedded StackKits registry snapshot",
	Long: `Manage the OSS-safe registry snapshot baked into the CLI binary.

The snapshot lives at internal/registry/data/registry_snapshot.json and is
used at runtime when STACKKIT_ADMIN_ENDPOINT is not set. Refresh it from
the Admin API for internal builds (snapshot) or from the local CUE tree
for pure-OSS builds (bake-from-cue).`,
}

var registrySnapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Fetch the registry from the Admin API and write it to disk",
	Long: `Fetch a complete registry snapshot from the kombify-internal Admin API
and write it to internal/registry/data/registry_snapshot.json (or --output).

Requires --endpoint and a Bearer token via --token or $STACKKIT_ADMIN_TOKEN.
This command is intended for the release pipeline -- the snapshot is then
baked into the goreleaser build and synced to the public OSS repo as frozen
data.`,
	RunE: runRegistrySnapshot,
}

var registryBakeCmd = &cobra.Command{
	Use:   "bake-from-cue",
	Short: "Produce a registry snapshot from the local CUE module tree",
	Long: `Walk modules/<slug>/module.cue, compute each module's contract_hash, and
write the resulting Snapshot to internal/registry/data/registry_snapshot.json
(or --output).

This command never talks to the Admin API -- it is the OSS bootstrap path
and works on a pure checkout of the public kombifyio/stackKits repo.`,
	RunE: runRegistryBakeFromCUE,
}

var registryInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show a summary of the embedded registry snapshot",
	RunE:  runRegistryInfo,
}

func init() {
	defaultOut := defaultSnapshotPath()

	registrySnapshotCmd.Flags().StringVar(&registrySnapshotEndpoint, "endpoint", "", "Admin API base URL (required)")
	registrySnapshotCmd.Flags().StringVar(&registrySnapshotToken, "token", "", "Bearer token. Defaults to $STACKKIT_ADMIN_TOKEN.")
	registrySnapshotCmd.Flags().StringVar(&registrySnapshotOutput, "output", defaultOut, "Output path for the snapshot JSON")

	registryBakeCmd.Flags().StringVar(&registryBakeModulesDir, "modules-dir", "modules", "Directory containing module/<slug>/module.cue")
	registryBakeCmd.Flags().StringVar(&registryBakeOutput, "output", defaultOut, "Output path for the snapshot JSON")

	registryInfoCmd.Flags().BoolVar(&registryInfoJSON, "json", false, "Print the full embedded snapshot as JSON")

	registryCmd.AddCommand(registrySnapshotCmd)
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

func runRegistrySnapshot(cmd *cobra.Command, _ []string) error {
	if registrySnapshotEndpoint == "" {
		return fmt.Errorf("--endpoint is required")
	}
	token := registrySnapshotToken
	if token == "" {
		token = os.Getenv(registry.EnvToken)
	}

	client := registry.NewRemoteClient(registrySnapshotEndpoint, token)
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()

	snap, err := client.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("fetch snapshot: %w", err)
	}

	// Normalise metadata so the file is reproducible even if the server
	// omits some envelope fields.
	snap.SchemaVersion = registry.SnapshotVersion
	snap.Source = registry.SourceAdminAPI
	if snap.GeneratedAt.IsZero() {
		snap.GeneratedAt = time.Now().UTC()
	}
	snap.AdminEndpoint = registrySnapshotEndpoint
	sortSnapshot(&snap)

	if err := writeSnapshot(registrySnapshotOutput, snap); err != nil {
		return err
	}

	printSuccess("Wrote registry snapshot to %s", registrySnapshotOutput)
	printInfo("source=admin-api tools=%d services=%d modules=%d stackkits=%d",
		len(snap.Tools), len(snap.Services), len(snap.Modules), len(snap.StackKits))
	return nil
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
		GeneratedAt:   time.Now().UTC(),
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
	fmt.Printf("  tools          : %d\n", len(snap.Tools))
	fmt.Printf("  services       : %d\n", len(snap.Services))
	fmt.Printf("  modules        : %d\n", len(snap.Modules))
	fmt.Printf("  stackkits      : %d\n", len(snap.StackKits))

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
		mods := snap.StackKits[i].Modules
		sort.Slice(mods, func(a, b int) bool { return mods[a].Slug < mods[b].Slug })
	}
}
