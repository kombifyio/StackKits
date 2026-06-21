package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

const generatedArtifactsSchemaVersion = 1

var (
	registryArtifactsManifest string
	registryArtifactsKind     string
	registryArtifactsState    string
)

type generatedArtifactsManifest struct {
	SchemaVersion int                 `json:"schema_version"`
	Artifacts     []generatedArtifact `json:"artifacts"`
}

type generatedArtifact struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	Generator string `json:"generator,omitempty"`
	Note      string `json:"note,omitempty"`
}

var registryArtifactsCmd = &cobra.Command{
	Use:   "artifacts",
	Short: "Print registry/generated artifact pathspecs from the ownership manifest",
	Long: `Print repository pathspecs from the generated-artifact ownership manifest.

The generate-cue workflow and CI parity gate use this command so bot-owned
generated files are explicit. Frozen artifacts remain in their existing paths
but are not staged or diffed as generated output until their manifest state
changes.`,
	RunE: runRegistryArtifacts,
}

func init() {
	registryArtifactsCmd.Flags().StringVar(&registryArtifactsManifest, "manifest", defaultGeneratedArtifactsManifestPath(), "Generated artifact ownership manifest")
	registryArtifactsCmd.Flags().StringVar(&registryArtifactsKind, "kind", "all", "Artifact kind filter (all, cue, snapshot)")
	registryArtifactsCmd.Flags().StringVar(&registryArtifactsState, "state", "generated", "Artifact state filter (all, generated, frozen)")
	registryCmd.AddCommand(registryArtifactsCmd)
}

func defaultGeneratedArtifactsManifestPath() string {
	return filepath.Join("internal", "registry", "generated_artifacts.json")
}

func runRegistryArtifacts(cmd *cobra.Command, _ []string) error {
	manifest, err := loadGeneratedArtifactsManifest(registryArtifactsManifest)
	if err != nil {
		return err
	}

	paths, err := filterGeneratedArtifactPaths(manifest, registryArtifactsKind, registryArtifactsState)
	if err != nil {
		return err
	}
	for _, path := range paths {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), path)
	}
	return nil
}

func loadGeneratedArtifactsManifest(path string) (generatedArtifactsManifest, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-controlled CLI input
	if err != nil {
		return generatedArtifactsManifest{}, fmt.Errorf("read generated artifact manifest %s: %w", path, err)
	}

	var manifest generatedArtifactsManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return generatedArtifactsManifest{}, fmt.Errorf("decode generated artifact manifest %s: %w", path, err)
	}
	if manifest.SchemaVersion != generatedArtifactsSchemaVersion {
		return generatedArtifactsManifest{}, fmt.Errorf("generated artifact manifest schema version mismatch: got %d, expected %d",
			manifest.SchemaVersion, generatedArtifactsSchemaVersion)
	}
	for _, artifact := range manifest.Artifacts {
		if strings.TrimSpace(artifact.Path) == "" {
			return generatedArtifactsManifest{}, fmt.Errorf("generated artifact manifest contains an artifact with empty path")
		}
		if filepath.IsAbs(artifact.Path) {
			return generatedArtifactsManifest{}, fmt.Errorf("generated artifact path %q must stay repo-relative", artifact.Path)
		}
		for _, part := range strings.Split(filepath.ToSlash(artifact.Path), "/") {
			if part == ".." {
				return generatedArtifactsManifest{}, fmt.Errorf("generated artifact path %q must stay repo-relative", artifact.Path)
			}
		}
	}
	return manifest, nil
}

func filterGeneratedArtifactPaths(manifest generatedArtifactsManifest, kind, state string) ([]string, error) {
	kind = strings.TrimSpace(strings.ToLower(kind))
	state = strings.TrimSpace(strings.ToLower(state))
	if kind == "" {
		kind = "all"
	}
	if state == "" {
		state = "all"
	}
	if kind != "all" && kind != "cue" && kind != "snapshot" {
		return nil, fmt.Errorf("invalid artifact kind %q (use all, cue, or snapshot)", kind)
	}
	if state != "all" && state != "generated" && state != "frozen" {
		return nil, fmt.Errorf("invalid artifact state %q (use all, generated, or frozen)", state)
	}

	paths := make([]string, 0, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		artifactKind := strings.ToLower(strings.TrimSpace(artifact.Kind))
		artifactState := strings.ToLower(strings.TrimSpace(artifact.State))
		if artifactKind == "" || artifactState == "" {
			return nil, fmt.Errorf("generated artifact %q must declare kind and state", artifact.Path)
		}
		if kind != "all" && artifactKind != kind {
			continue
		}
		if state != "all" && artifactState != state {
			continue
		}
		paths = append(paths, filepath.ToSlash(artifact.Path))
	}
	sort.Strings(paths)
	return paths, nil
}
