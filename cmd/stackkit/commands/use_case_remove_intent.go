package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

type removedUseCaseIntent struct {
	request stackspecintent.Request
}

func (intent *removedUseCaseIntent) persist() error {
	_, err := stackspecintent.Persist(intent.request)
	return err
}

func validateUseCaseRemovalGraph(service *architecturev2.Service, workspace, inventoryPath string, candidate architecturev2.StackSpecValidation) error {
	inventory, err := readArchitectureV2Inventory(workspace, inventoryPath)
	if err != nil {
		return err
	}
	if _, err := service.Resolve(architecturev2.ResolveInput{StackSpec: candidate.CanonicalStackSpec, Inventory: inventory}); err != nil {
		return fmt.Errorf("remaining workload and Foundation dependency graph refuses removal: %w", err)
	}
	return nil
}

func authorRemovalCandidate(cmd *cobra.Command, slug, path string) error {
	return authorRetainedIntent(cmd, intentChangeOptions{output: path, expectedHash: removeExpectedSpecHash, json: removeJSON}, func(service *architecturev2.Service, raw []byte) (architecturev2.StackSpecValidation, error) {
		candidate, err := service.MaterializeUseCaseRemoval(raw, slug)
		if err != nil {
			return candidate, err
		}
		if err := validateUseCaseRemovalGraph(service, getWorkDir(), removeV2ExecutionOptions.inventoryPath, candidate); err != nil {
			return candidate, err
		}
		return candidate, nil
	}, "", nil)
}

func prepareRemovedUseCaseIntent(workspace, slug, inventoryPath string) (*removedUseCaseIntent, error) {
	// Core removal retains its documented reinstall source. Advanced commits
	// its candidate only through the signed change-set transaction.
	if slug == "basement-core" || slug == "cloud-core" {
		return nil, nil
	}
	specPath, _, _, err := config.NewLoader(workspace).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return nil, err
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return nil, err
	}
	current, err := service.ValidateStackSpec(raw)
	if err != nil {
		return nil, err
	}
	var spec struct {
		APIVersion string `json:"apiVersion"`
		Install    struct {
			Mode string `json:"mode"`
		} `json:"install"`
		Nodes []json.RawMessage `json:"nodes"`
		Sites []json.RawMessage `json:"sites"`
	}
	if err := json.Unmarshal(current.CanonicalStackSpec, &spec); err != nil {
		return nil, err
	}
	if spec.Install.Mode == "advanced" || spec.APIVersion != "stackkit/v2alpha2" {
		return nil, nil
	}
	if len(spec.Nodes) != 1 || len(spec.Sites) != 1 {
		return nil, fmt.Errorf("multi-node or multi-site workload removal requires --candidate-output and existing Advanced change-set dispatch")
	}
	candidate, err := service.MaterializeUseCaseRemoval(current.CanonicalStackSpec, slug)
	if err != nil {
		return nil, err
	}
	if err := validateUseCaseRemovalGraph(service, workspace, inventoryPath, candidate); err != nil {
		return nil, err
	}
	return &removedUseCaseIntent{request: stackspecintent.Request{WorkspaceRoot: workspace, SpecPath: specPath, Candidate: candidate.CanonicalStackSpec, ExpectedSpecHash: current.SpecHash, BuildVersion: version, Authority: service}}, nil
}
