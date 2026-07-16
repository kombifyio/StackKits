package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/kitio"
	"github.com/kombifyio/stackkits/internal/productkits"
	"github.com/kombifyio/stackkits/internal/registry"
	"github.com/spf13/cobra"
)

var registryVerifyParityStrict bool

var registryVerifyParityCmd = &cobra.Command{
	Use:   "verify-parity",
	Short: "Verify kit definition hashes in the snapshot match local stackkit.yaml",
	Long: `Compares kit_definition_hash values from the committed registry snapshot
against locally computed canonical hashes of each kit's stackkit.yaml.

Only kits that carry at least one spec_profile with a kit_definition_hash
are checked. Kits without profiles are skipped.

Exit codes:
  0 — all hashes match (or no profiles to check)
  1 — hash drift detected`,
	RunE: runRegistryVerifyParity,
}

func init() {
	registryVerifyParityCmd.Flags().BoolVar(&registryVerifyParityStrict, "strict", false, "Exit non-zero on any drift (default: report only)")
	registryCmd.AddCommand(registryVerifyParityCmd)
}

func runRegistryVerifyParity(_ *cobra.Command, _ []string) error {
	raw, err := os.ReadFile(defaultSnapshotPath())
	if err != nil {
		return fmt.Errorf("read snapshot: %w", err)
	}
	var snap registry.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}

	checked := 0
	drifted := 0
	seenProducts := make(map[string]bool, len(productkits.Slugs()))

	for _, kit := range snap.StackKits {
		if !productkits.IsActive(kit.Slug) {
			drifted++
			printError("non-product stackkit slug in registry snapshot: %s", kit.Slug)
			continue
		}
		if seenProducts[kit.Slug] {
			drifted++
			printError("duplicate product stackkit slug in registry snapshot: %s", kit.Slug)
			continue
		}
		seenProducts[kit.Slug] = true
		kitDir := kit.Slug
		yamlPath := filepath.Join(kitDir, "stackkit.yaml")
		if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
			continue
		}

		for _, profile := range kit.SpecProfiles {
			if profile.KitDefinitionHash == "" {
				continue
			}

			yamlBytes, err := os.ReadFile(yamlPath) // #nosec G304
			if err != nil {
				return fmt.Errorf("read %s: %w", yamlPath, err)
			}
			localDef, err := kitio.Import(yamlBytes)
			if err != nil {
				return fmt.Errorf("import %s: %w", yamlPath, err)
			}
			localHash, err := kitio.CanonicalHash(localDef)
			if err != nil {
				return fmt.Errorf("hash %s: %w", yamlPath, err)
			}

			checked++
			if localHash == profile.KitDefinitionHash {
				printSuccess("kit=%s profile=%s kit_definition_hash OK (%s)", kit.Slug, profile.Slug, localHash[:12])
			} else {
				drifted++
				printError("kit=%s profile=%s kit_definition_hash DRIFT\n  snapshot: %s\n  local:    %s",
					kit.Slug, profile.Slug, profile.KitDefinitionHash, localHash)
			}
		}
	}
	if snap.Source == registry.SourceAdminAPI {
		for _, slug := range productkits.Slugs() {
			if !seenProducts[slug] {
				drifted++
				printError("canonical product missing from admin registry snapshot: %s", slug)
			}
		}
	}

	if checked == 0 {
		printInfo("no spec profiles with kit_definition_hash found in snapshot — nothing to verify")
	}

	printInfo("checked=%d drifted=%d", checked, drifted)
	if drifted > 0 && registryVerifyParityStrict {
		return fmt.Errorf("%d kit definition hash(es) drifted from snapshot", drifted)
	}
	return nil
}
