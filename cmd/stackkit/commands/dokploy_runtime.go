package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
	"github.com/spf13/cobra"
)

func init() {
	var localEndpoint string
	command := &cobra.Command{Use: "dokploy-runtime", Short: "Inspect the optional Dokploy OpenTofu provider integration", Args: cobra.NoArgs}
	bootstrap := &cobra.Command{Use: "bind-bootstrap", Short: "Authenticate and encrypt the existing node-owned Dokploy bootstrap identity", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := nativehost.BindDokployProviderBootstrap(cmd.Context(), getWorkDir(), localEndpoint)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}}
	bootstrap.Flags().StringVar(&localEndpoint, "local-endpoint", "", "Literal loopback URL for the existing Dokploy API when bootstrap stores its public UI URL")
	var project, composeID string
	prepare := &cobra.Command{Use: "prepare", Short: "Prepare an exact existing service's import-bound root without applying it", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := nativehost.PrepareDokployProviderWorkload(cmd.Context(), getWorkDir(), project, composeID)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}}
	prepare.Flags().StringVar(&project, "project", "", "Exact generated node workload project")
	prepare.Flags().StringVar(&composeID, "compose-id", "", "Held Dokploy composeId; never a display-name lookup")
	_ = prepare.MarkFlagRequired("project")
	_ = prepare.MarkFlagRequired("compose-id")
	command.AddCommand(bootstrap, prepare)
	for _, action := range []string{"recipe", "adopt", "apply", "observe"} {
		var deploymentFile, heldID string
		workload := &cobra.Command{Use: action, Hidden: true, Args: cobra.NoArgs,
			Short: "Experimental opt-in Vault provider " + action,
			RunE: func(cmd *cobra.Command, _ []string) error {
				d, err := readDokployDeployment(deploymentFile)
				if err != nil {
					return err
				}
				if d.GenerationTarget != "opentofu" {
					return errors.New("the local Dokploy command admits Standard Mode opentofu only; Advanced requires Techstack dispatch")
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Minute)
				defer cancel()
				operations, err := opentofu.NewDokployWorkloadOperations(opentofu.Runtime{WorkspaceRoot: getWorkDir()})
				if err != nil {
					return err
				}
				var result any
				switch action {
				case "recipe":
					result, err = operations.PrepareAdoptionRecipe(ctx, d)
				case "adopt":
					result, err = operations.AdoptWorkload(ctx, d, heldID)
				case "apply":
					result, err = operations.ApplyWorkload(ctx, d)
				case "observe":
					result, err = operations.ObserveWorkload(ctx, d)
				}
				if err != nil {
					return err
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}}
		workload.Flags().StringVar(&deploymentFile, "deployment-file", "", "Private generated SelectedPaaSWorkloadDeployment JSON for this exact Vault node")
		_ = workload.MarkFlagRequired("deployment-file")
		if action == "adopt" {
			workload.Flags().StringVar(&heldID, "compose-id", "", "Explicit existing Dokploy composeId to bind, never a display name")
			_ = workload.MarkFlagRequired("compose-id")
		}
		command.AddCommand(workload)
	}
	rootCmd.AddCommand(command)
}

func readDokployDeployment(path string) (nativehost.SelectedPaaSWorkloadDeployment, error) {
	var d nativehost.SelectedPaaSWorkloadDeployment
	file, err := os.Open(path)
	if err != nil {
		return d, errors.New("Dokploy requires the exact generated deployment file")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return d, errors.New("Dokploy deployment file exceeds its bounded schema")
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&d) != nil {
		return d, errors.New("Dokploy deployment file is invalid")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return d, errors.New("Dokploy deployment file contains trailing input")
	}
	return d, nil
}
