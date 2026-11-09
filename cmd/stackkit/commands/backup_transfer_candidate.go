package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/spf13/cobra"
)

func configureApplicationTransferCandidateCommand() {
	candidate := &cobra.Command{Use: "transfer-candidate", Short: "Author the receiving candidate of one native application transfer from the source configuration", Args: machineAwareNoArgs, RunE: runApplicationTransferCandidate}
	candidate.Flags().String("portable-configuration", "", "Source portable application configuration document")
	candidate.Flags().String("output", "", "New receiving candidate StackSpec path")
	backupCmd.AddCommand(candidate)
}

func runApplicationTransferCandidate(cmd *cobra.Command, _ []string) error {
	configurationPath, _ := cmd.Flags().GetString("portable-configuration")
	outputPath, _ := cmd.Flags().GetString("output")
	if configurationPath == "" || outputPath == "" {
		return errors.New("transfer candidate requires --portable-configuration and --output")
	}
	workspace := getWorkDir()
	raw, err := readAdvancedRegular(resolvePathFromWorkDir(workspace, configurationPath), maxAdvancedCandidateBytes, "source portable application configuration")
	if err != nil {
		return err
	}
	var document applicationTransferConfigurationDocument
	if err = json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("decode source portable application configuration: %w", err)
	}
	if document.APIVersion != "stackkit.application-transfer-configuration/v1" || len(document.Configuration) == 0 {
		return errors.New("source portable application configuration is incomplete")
	}
	digest, err := backuplifecycle.ApplicationTransferConfigurationDigest(document.Configuration)
	if err != nil {
		return err
	}
	if digest != document.PortableConfigurationDigest {
		return errors.New("source portable application configuration differs from its digest")
	}
	workloads := make([]string, 0, len(document.Configuration))
	for workload := range document.Configuration {
		workloads = append(workloads, workload)
	}
	slices.Sort(workloads)
	if !slices.Equal(workloads, document.Workloads) {
		return errors.New("source portable application configuration names another selection")
	}
	applications := make(map[string]architecturev2.PortableApplicationConfiguration, len(document.Configuration))
	for workload, configuration := range document.Configuration {
		applications[workload] = architecturev2.PortableApplicationConfiguration{Alternative: configuration.AlternativeRef, Settings: configuration.Settings, Profiles: portableModuleProfiles(configuration.Profiles)}
	}
	specPath, _, _, err := config.NewLoader(workspace).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return err
	}
	baselineRaw, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	author, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return err
	}
	current, err := author.ValidateStackSpec(baselineRaw)
	if err != nil {
		return err
	}
	output, err := filepath.Abs(resolvePathFromWorkDir(workspace, outputPath))
	if err != nil {
		return err
	}
	active, err := filepath.Abs(specPath)
	if err != nil {
		return err
	}
	if output == active {
		return errors.New("the receiving candidate must not replace the active baseline StackSpec")
	}
	candidate, err := author.MaterializeApplicationTransferAddition(current.CanonicalStackSpec, applications)
	if err != nil {
		return err
	}
	added, err := addedTransferWorkloads(current.CanonicalStackSpec, candidate.CanonicalStackSpec)
	if err != nil {
		return err
	}
	if !slices.Equal(added, workloads) {
		return errors.New("the receiving candidate must add exactly the source applications; the destination already selects one of them")
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create new receiving candidate: %w", err)
	}
	if _, err = file.Write(append(candidate.CanonicalStackSpec, '\n')); err != nil {
		return errors.Join(err, file.Close())
	}
	if err = file.Close(); err != nil {
		return err
	}
	result := map[string]any{"apiVersion": "stackkit.intent-change/v1", "previousSpecHash": current.SpecHash, "specHash": candidate.SpecHash, "path": output, "outcome": "candidate", "runtimeApplied": false,
		"previousCanonicalSpec": json.RawMessage(current.CanonicalStackSpec), "portableConfigurationDigest": digest}
	return writeCommandResult(cmd, cmd.CommandPath(), result)
}

// portableModuleProfiles names the selected module profiles of one portable
// configuration; compiler-derived hashes and bindings stay on their node.
func portableModuleProfiles(profiles map[string]any) architecturev2.ModuleProfileOverride {
	selected := func(field string) string {
		value, _ := profiles[field].(string)
		return value
	}
	return architecturev2.ModuleProfileOverride{ComputeProfile: selected("computeProfile"), StorageProfile: selected("storageProfile"), AcceleratorProfile: selected("acceleratorProfile")}
}

// addedTransferWorkloads names the workloads a candidate adds to its previous intent.
func addedTransferWorkloads(previous, candidate []byte) ([]string, error) {
	var before, after struct {
		Workloads map[string]json.RawMessage `json:"workloads"`
	}
	if json.Unmarshal(previous, &before) != nil || json.Unmarshal(candidate, &after) != nil {
		return nil, errors.New("the receiving candidate lacks its previous and new intent")
	}
	added := []string{}
	for workload, previousIntent := range before.Workloads {
		retained, exists := after.Workloads[workload]
		if !exists || !bytes.Equal(retained, previousIntent) {
			// A receiving candidate adds applications; it never edits or removes
			// a retained one.
			return nil, errors.New("the receiving candidate must only add applications")
		}
	}
	for workload := range after.Workloads {
		if _, ok := before.Workloads[workload]; !ok {
			added = append(added, workload)
		}
	}
	slices.Sort(added)
	return added, nil
}
