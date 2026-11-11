package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

func newSetupWorkbenchHostReleaseCommand() *cobra.Command {
	var tokenFile string
	var outputJSON bool
	command := &cobra.Command{
		Use: "workbench-host-release", Short: "Import private Desktop release access for the selected Workbench host",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Long: `Import an owner-custodied GitHub credential with contents:read access to
the private Workbench Desktop repository (GitHub repository id 1347582990)
before the first Workbench host apply.
Read it from a private --token-file, or use --token-file - for standard input.
The credential is encrypted in existing local owner custody, never written to
the StackSpec or command result. Repeating this explicit command rotates it.
Neither secrets materialization nor an Advanced change set invents this slot.
Connect enrollment and installed-provider authentication are separate setup.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			canonical, err := loadCanonicalLocalSecretSpec()
			if err != nil {
				return machineAwareCommandError(cmd, err)
			}
			var spec struct {
				Workloads map[string]struct {
					Alternative string            `json:"alternative"`
					SecretRefs  map[string]string `json:"secretRefs"`
				} `json:"workloads"`
			}
			if err := json.Unmarshal(canonical, &spec); err != nil {
				return machineAwareCommandError(cmd, fmt.Errorf("decode canonical StackSpec secret authority: %w", err))
			}
			workload := spec.Workloads["dev-workbench"]
			ref := workload.SecretRefs["release-token"]
			if workload.Alternative != "workbench-host" || !strings.HasPrefix(ref, "secret://") {
				return errors.New("select dev-workbench with alternative workbench-host and its local release-token reference first")
			}
			input := cmd.InOrStdin()
			if tokenFile == "-" {
				// A terminal would echo the credential; standard input must be a pipe or a redirected file.
				if file, ok := input.(*os.File); ok {
					if info, err := file.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
						return errors.New("--token-file - reads a pipe or redirected file, never a terminal")
					}
				}
			} else {
				// Lstat first: opening a FIFO or device would block or read something else.
				if info, err := os.Lstat(tokenFile); err != nil || !info.Mode().IsRegular() {
					return errors.New("release credential must be a private regular file (mode 0600 or stricter)")
				}
				file, err := os.Open(tokenFile) //nolint:gosec // Explicit owner-selected custody file, never an argument value.
				if err != nil {
					return errors.New("cannot open private release credential file")
				}
				defer func() { _ = file.Close() }()
				info, err := file.Stat()
				if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
					return errors.New("release credential must be a private regular file (mode 0600 or stricter)")
				}
				input = file
			}
			raw, err := io.ReadAll(io.LimitReader(input, 8194))
			if err != nil {
				return errors.New("cannot read release credential")
			}
			defer clear(raw)
			token := bytes.TrimSpace(raw)
			if len(raw) > 8193 || len(token) == 0 || len(token) > 8192 {
				return errors.New("release credential must contain 1 to 8192 printable non-space ASCII bytes")
			}
			for _, b := range token {
				if b < 0x21 || b > 0x7e {
					return errors.New("release credential must contain only printable non-space ASCII bytes")
				}
			}
			if err := localevidence.StoreEncryptedLocalIssuedSecret(getWorkDir(), ref, token); err != nil {
				return machineAwareCommandError(cmd, fmt.Errorf("custody release credential: %w", err))
			}
			result := struct {
				WorkloadRef string   `json:"workloadRef"`
				Preparation string   `json:"preparation"`
				NextSteps   []string `json:"nextSteps"`
			}{"dev-workbench", "release-token-custodied", []string{"stackkit generate", "stackkit apply"}}
			if outputJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), result)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Workbench release access is in encrypted local owner custody. Generate and apply, then explicitly enroll Connect and configure the installed host provider.")
			return err
		},
	}
	command.Flags().StringVar(&tokenFile, "token-file", "", "Private credential file, or - for standard input")
	command.Flags().BoolVar(&outputJSON, "json", false, "Emit the secret-free setup result")
	_ = command.MarkFlagRequired("token-file")
	return command
}
