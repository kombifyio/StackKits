package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/kombifyio/stackkits/internal/workloadremoval"
	"github.com/spf13/cobra"
)

func useCaseRemovalCandidate(service *architecturev2.Service, raw []byte, workspace, inventoryPath, slug string) (architecturev2.StackSpecValidation, []string, error) {
	inventory, err := readArchitectureV2Inventory(workspace, inventoryPath)
	if err != nil {
		return architecturev2.StackSpecValidation{}, nil, err
	}
	current, err := service.Resolve(architecturev2.ResolveInput{StackSpec: raw, Inventory: inventory})
	if err != nil {
		return architecturev2.StackSpecValidation{}, nil, err
	}
	plan, err := service.VerifyCanonicalPlan(current.CanonicalPlan)
	if err != nil {
		return architecturev2.StackSpecValidation{}, nil, err
	}
	selected, err := service.SelectedUseCaseWorkloads(plan, slug)
	if err != nil {
		return architecturev2.StackSpecValidation{}, nil, err
	}
	selected, err = useCaseRemovalOrder(plan, selected)
	if err != nil {
		return architecturev2.StackSpecValidation{}, nil, err
	}
	candidate, err := service.MaterializeWorkloadRemovals(raw, selected)
	if err != nil {
		return candidate, nil, err
	}
	// Intermediate selections are never persisted or executed. The complete
	// remaining graph must validate before any member is removed.
	if err := validateUseCaseRemovalGraph(service, workspace, inventoryPath, candidate); err != nil {
		return candidate, nil, err
	}
	return candidate, selected, nil
}

// Removal order is derived by the same embedded authority as membership.
func useCaseRemovalOrder(plan generationartifact.VerifiedPlan, selected []string) ([]string, error) {
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return nil, err
	}
	return service.OrderWorkloadRemovals(plan, selected)
}

func runUseCaseGroupRemoval(cmd *cobra.Command, slug string) error {
	workspace := adoptConventionalInstallerWorkspace(getWorkDir())
	if removeCandidateOutput != "" {
		if removeDeleteData {
			return errors.New("--candidate-output retains data and cannot combine with --delete-data")
		}
		return authorRetainedIntent(cmd, intentChangeOptions{output: removeCandidateOutput, expectedHash: removeExpectedSpecHash, json: removeJSON}, func(service *architecturev2.Service, raw []byte) (architecturev2.StackSpecValidation, error) {
			candidate, _, err := useCaseRemovalCandidate(service, raw, workspace, removeV2ExecutionOptions.inventoryPath, slug)
			return candidate, err
		}, "", nil)
	}
	if commandRequestsMachineOutput(cmd) && !removeAutoApprove {
		return errors.New("use-case runtime removal requires --auto-approve for machine output")
	}
	specPath, _, _, err := config.NewLoader(workspace).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return err
	}
	current, err := service.ValidateStackSpec(raw)
	if err != nil {
		return err
	}
	var spec struct {
		Install struct {
			Mode string `json:"mode"`
		} `json:"install"`
		Nodes []json.RawMessage `json:"nodes"`
		Sites []json.RawMessage `json:"sites"`
	}
	if err := json.Unmarshal(current.CanonicalStackSpec, &spec); err != nil {
		return err
	}
	if spec.Install.Mode == "advanced" || len(spec.Nodes) != 1 || len(spec.Sites) != 1 {
		return errors.New("managed use-case removal requires --candidate-output and existing Advanced change-set dispatch")
	}
	candidate, selected, err := useCaseRemovalCandidate(service, current.CanonicalStackSpec, workspace, removeV2ExecutionOptions.inventoryPath, slug)
	if err != nil {
		return err
	}
	if !removeAutoApprove {
		fmt.Fprintf(cmd.OutOrStdout(), "Remove use case %s (selected workloads %v)? Type yes: ", slug, selected)
		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if confirm != removeConfirmationValue {
			return nil
		}
	}
	options := removeV2ExecutionOptions
	options.context, options.workloadRef, options.workloadRefs = cmd.Context(), selected[0], selected
	options.dataDisposition = workloadremoval.DataDispositionRetain
	if removeDeleteData {
		options.dataDisposition = workloadremoval.DataDispositionDelete
	}
	var terminal []workloadremoval.Evidence
	options.removalEvidenceSink = func(evidence workloadremoval.Evidence) error { terminal = append(terminal, evidence); return nil }
	options.removalSink = nil
	handled, err := newArchitectureV2ExecutionGate().preflight(workspace, specFile, architectureV2Remove, options)
	if err != nil {
		return err
	}
	if !handled || len(terminal) != len(selected) {
		return errors.New("use-case removal has no complete terminal evidence; desired intent was retained")
	}
	for index, evidence := range terminal {
		if evidence.Authority.WorkloadRef != selected[index] {
			return errors.New("use-case terminal removal evidence differs from its exact selected workload; desired intent was retained")
		}
	}
	result, err := stackspecintent.Persist(stackspecintent.Request{WorkspaceRoot: workspace, SpecPath: specPath, Candidate: candidate.CanonicalStackSpec, ExpectedSpecHash: current.SpecHash, BuildVersion: version, Authority: service})
	if err != nil {
		return fmt.Errorf("selected runtime workloads were removed and terminal evidence retained; desired-intent CAS is pending: %w", err)
	}
	if removeJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"apiVersion": "stackkit.use-case-removal/v1", "useCaseRef": slug, "removedWorkloads": terminal, "specHash": result.SpecHash, "dataDisposition": options.dataDisposition, "nextStep": "Run stackkit generate, then stackkit apply before another runtime lifecycle operation; retained data is preserved."})
	}
	printSuccess("Removed use case %s (%d selected workloads); data disposition: %s", slug, len(selected), options.dataDisposition)
	printInfo("Run stackkit generate, then stackkit apply before another runtime lifecycle operation. Retained data is preserved.")
	return nil
}
