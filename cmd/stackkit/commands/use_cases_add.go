package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

func init() {
	useCasesCmd.AddCommand(newUseCasesAddCommand())
}

func newUseCasesAddCommand() *cobra.Command {
	var expectedHash string
	cmd := &cobra.Command{
		Use: "add <slug>", Short: "Add a catalog use case to an existing standalone installation",
		Long: `Add a use case using the installed kit's CUE-owned alternative and module
profile defaults. Preserve existing workloads, routes, placement and storage.
Persist the candidate by the current spec hash and materialize only the selected
use case's local secret slots. Existing custody is reused. No services are
deployed; generate and apply the updated intent through the normal lifecycle.
Native stackkit/v2alpha2 and a single standalone node/site are required.`,
		Example: `  stackkit use-cases add mail
  stackkit use-cases add mail --expected-spec-hash sha256:<current-hash>`,
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUseCasesAdd(cmd, args[0], expectedHash)
		},
	}
	cmd.Flags().StringVar(&expectedHash, "expected-spec-hash", "", "Require this canonical current spec hash; omitted uses the hash read before authoring")
	return cmd
}

func runUseCasesAdd(cmd *cobra.Command, slug, expectedHash string) error {
	wd := getWorkDir()
	specPath, displayPath, _, err := config.NewLoader(wd).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("read existing StackSpec: %w", err)
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return err
	}
	current, err := service.ValidateStackSpec(raw)
	if err != nil {
		return err
	}
	expectedHash = strings.TrimSpace(expectedHash)
	if expectedHash != "" && expectedHash != current.SpecHash {
		return fmt.Errorf("expected spec hash does not match current intent; current spec hash: %s", current.SpecHash)
	}
	if expectedHash == "" {
		expectedHash = current.SpecHash
	}
	candidate, err := service.MaterializeUseCaseAddition(current.CanonicalStackSpec, slug)
	if err != nil {
		return fmt.Errorf("author use case addition: %w", err)
	}
	// Restrict the existing materializer to the requested workload. It must
	// never populate missing slots belonging to unrelated installed workloads.
	var document struct {
		Workloads map[string]json.RawMessage `json:"workloads"`
	}
	if err := json.Unmarshal(candidate.CanonicalStackSpec, &document); err != nil {
		return err
	}
	slug = strings.TrimSpace(slug)
	selected, err := json.Marshal(map[string]any{"workloads": map[string]json.RawMessage{slug: document.Workloads[slug]}})
	if err != nil {
		return err
	}
	if _, err := localevidence.LoadOwnerCustody(wd); err != nil {
		return fmt.Errorf("existing local owner custody is required before adding a use case: %w", err)
	}
	var slots struct {
		SecretRefs map[string]string `json:"secretRefs"`
	}
	if err := json.Unmarshal(document.Workloads[slug], &slots); err != nil {
		return err
	}
	for _, ref := range slots.SecretRefs {
		if !strings.HasPrefix(strings.TrimSpace(ref), "secret://") {
			continue
		}
		material, err := localevidence.ResolveLocalSecretMaterial(wd, strings.TrimSpace(ref))
		clear(material)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("existing selected use case custody is invalid: %w", err)
		}
	}
	result, err := stackspecintent.Persist(stackspecintent.Request{
		WorkspaceRoot: wd, SpecPath: specPath, Candidate: candidate.CanonicalStackSpec,
		ExpectedSpecHash: expectedHash, BuildVersion: version, Authority: service,
	})
	if err != nil {
		return fmt.Errorf("persist use case addition: %w", err)
	}
	if _, err := materializeArchitectureV2LocalSecrets(wd, selected); err != nil {
		return fmt.Errorf("spec intent is persisted at %s, but secret materialization failed; retry use-cases add %s to complete custody: %w", result.SpecHash, slug, err)
	}
	if result.Outcome == stackspecintent.OutcomeAlreadyApplied {
		printSuccess("Use case %s is already selected; local secret custody is ready", slug)
	} else {
		printSuccess("Added use case %s to %s; local secret custody is ready", slug, displayPath)
	}
	printInfo("Spec hash: %s", result.SpecHash)
	printInfo("Next: stackkit generate, then stackkit apply")
	return nil
}
