package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage owner-bound local secret custody",
	Annotations: map[string]string{
		noDeployObservabilityAnnotation: "true",
	},
}

var secretsMaterializeCmd = &cobra.Command{
	Use:   "materialize",
	Short: "Establish custody for secret references in the current StackSpec",
	Args:  cobra.NoArgs,
	Long: `Establish or reuse owner-bound local custody for every secret:// reference
in the current canonical StackSpec.

Run this explicit, idempotent step after adding a workload to an existing
standalone workspace and before generate/apply. It never prints secret
references or material and never replaces invalid or foreign custody.`,
	Example: "  stackkit secrets materialize\n  stackkit generate\n  stackkit apply",
	RunE:    runSecretsMaterialize,
}

func init() {
	secretsCmd.AddCommand(secretsMaterializeCmd)
	rootCmd.AddCommand(secretsCmd)
}

func runSecretsMaterialize(_ *cobra.Command, _ []string) error {
	wd := getWorkDir()
	loader := config.NewLoader(wd)
	specPath, _, _, err := loader.ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("read StackSpec for secret custody: %w", err)
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return fmt.Errorf("load embedded Architecture v2 secret authority: %w", err)
	}
	validation, err := service.ValidateStackSpec(raw)
	if err != nil {
		return err
	}
	count, err := materializeArchitectureV2LocalSecrets(wd, validation.CanonicalStackSpec)
	if err != nil {
		return fmt.Errorf("establish workload secret custody: %w", err)
	}
	if count == 0 {
		printInfo("The current StackSpec declares no local secret references")
		return nil
	}
	printSuccess("Owner-bound workload secret custody is ready for %d reference(s)", count)
	return nil
}

func materializeArchitectureV2LocalSecrets(workspaceRoot string, canonicalStackSpec []byte) (int, error) {
	var spec struct {
		Workloads map[string]struct {
			SecretRefs map[string]string `json:"secretRefs"`
		} `json:"workloads"`
	}
	if err := json.Unmarshal(canonicalStackSpec, &spec); err != nil {
		return 0, fmt.Errorf("decode canonical StackSpec secret authority: %w", err)
	}
	refs := map[string]struct{}{}
	for _, workload := range spec.Workloads {
		for _, ref := range workload.SecretRefs {
			if strings.HasPrefix(strings.TrimSpace(ref), "secret://") {
				refs[strings.TrimSpace(ref)] = struct{}{}
			}
		}
	}
	ordered := make([]string, 0, len(refs))
	for ref := range refs {
		ordered = append(ordered, ref)
	}
	sort.Strings(ordered)
	for _, ref := range ordered {
		if err := localevidence.MaterializeLocalSecret(workspaceRoot, ref); err != nil {
			return 0, err
		}
	}
	return len(ordered), nil
}
