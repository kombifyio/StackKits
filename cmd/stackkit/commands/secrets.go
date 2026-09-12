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
	secretsCmd.AddCommand(newSecretsRevealCommand())
	rootCmd.AddCommand(secretsCmd)
}

func loadCanonicalLocalSecretSpec() ([]byte, error) {
	wd := getWorkDir()
	loader := config.NewLoader(wd)
	specPath, _, _, err := loader.ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("read StackSpec for secret custody: %w", err)
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return nil, fmt.Errorf("load embedded Architecture v2 secret authority: %w", err)
	}
	validation, err := service.ValidateStackSpec(raw)
	if err != nil {
		return nil, err
	}
	return validation.CanonicalStackSpec, nil
}

func runSecretsMaterialize(_ *cobra.Command, _ []string) error {
	canonical, err := loadCanonicalLocalSecretSpec()
	if err != nil {
		return err
	}
	count, err := materializeArchitectureV2LocalSecrets(getWorkDir(), canonical)
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

// Reveal is an explicit local-owner read. It does not accept arbitrary secret
// references, create custody, or send secret values to deploy observability.
func newSecretsRevealCommand() *cobra.Command {
	var workload, slot string
	cmd := &cobra.Command{
		Use: "reveal", Short: "Print one selected workload secret from local owner custody",
		Long:        "Print the existing value of one secret slot declared by a selected workload in the current CUE-valid StackSpec. Output contains secret material; use it only in a private terminal or an intentional pipe. No value is written to deploy logs or receipts.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			canonical, err := loadCanonicalLocalSecretSpec()
			if err != nil {
				return err
			}
			var spec struct {
				Workloads map[string]struct {
					SecretRefs map[string]string `json:"secretRefs"`
				} `json:"workloads"`
			}
			if err := json.Unmarshal(canonical, &spec); err != nil {
				return fmt.Errorf("decode canonical workload secret selection: %w", err)
			}
			ref := spec.Workloads[workload].SecretRefs[slot]
			if ref == "" || !strings.HasPrefix(ref, "secret://") {
				return fmt.Errorf("selected workload does not declare that local secret slot")
			}
			material, err := localevidence.ResolveLocalSecretMaterial(getWorkDir(), ref)
			if err != nil {
				return fmt.Errorf("read owner-bound workload secret: %w", err)
			}
			defer clear(material)
			if _, err := cmd.OutOrStdout().Write(material); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout())
			return err
		},
	}
	cmd.Flags().StringVar(&workload, "workload", "", "Selected workload ID")
	cmd.Flags().StringVar(&slot, "slot", "", "Declared workload secret slot")
	_ = cmd.MarkFlagRequired("workload")
	_ = cmd.MarkFlagRequired("slot")
	return cmd
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
