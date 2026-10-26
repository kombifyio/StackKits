package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
	"github.com/spf13/cobra"
)

func init() {
	var project, digest string
	action := &cobra.Command{Use: "runtipi-workload-action", Hidden: true, Args: cobra.NoArgs,
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			return opentofu.RunRuntipiWorkloadAction(cmd.Context(), project, digest)
		}}
	action.Flags().StringVar(&project, "project", "", "Exact held Runtipi workload project")
	action.Flags().StringVar(&digest, "request-digest", "", "Signed parent request digest")
	rootCmd.AddCommand(action)
	command := &cobra.Command{Use: "runtipi-runtime", Hidden: true, Args: cobra.NoArgs}
	command.AddCommand(newRuntipiTerminalQualificationCommand())
	for _, operation := range []string{"apply", "observe"} {
		var deploymentFile string
		child := &cobra.Command{Use: operation, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				d, err := readRuntipiAPIDeployment(deploymentFile)
				if err != nil {
					return err
				}
				operations, err := opentofu.NewRuntipiWorkloadOperations(opentofu.Runtime{WorkspaceRoot: getWorkDir()})
				if err != nil {
					return err
				}
				var result any
				if operation == "apply" {
					result, err = operations.ApplyWorkload(cmd.Context(), d)
				} else {
					result, err = operations.ObserveWorkload(cmd.Context(), d)
				}
				if err != nil {
					return err
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}}
		child.Flags().StringVar(&deploymentFile, "deployment-file", "", "Private exact generated Files deployment JSON")
		_ = child.MarkFlagRequired("deployment-file")
		command.AddCommand(child)
	}
	rootCmd.AddCommand(command)
}

func readRuntipiAPIDeployment(path string) (nativehost.SelectedPaaSWorkloadDeployment, error) {
	var d nativehost.SelectedPaaSWorkloadDeployment
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4<<20 {
		return d, errors.New("Runtipi API bridge requires a private bounded generated deployment file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return d, err
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&d) != nil || decoder.Decode(new(any)) != io.EOF {
		return d, errors.New("Runtipi deployment request is invalid")
	}
	return d, nil
}
