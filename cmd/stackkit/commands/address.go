package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/addressplan"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

type addressCommandOptions struct {
	prefix           string
	zone             string
	outputPath       string
	expectedSpecHash string
}

func newAddressCommand() *cobra.Command {
	command := &cobra.Command{
		Use:         "address",
		Short:       "Plan and bind secret-free public service addresses",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	command.AddCommand(newAddressPlanCommand(), newAddressBindCommand())
	return command
}

func newAddressPlanCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "plan",
		Short:         "Emit the account-free address registration plan",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			canonical, err := validateAddressStackSpec(getWorkDir(), specFile)
			if err != nil {
				return err
			}
			plan, err := addressplan.FromCanonicalStackSpec(canonical)
			if err != nil {
				return err
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetEscapeHTML(false)
			return encoder.Encode(plan)
		},
	}
}

func newAddressBindCommand() *cobra.Command {
	options := &addressCommandOptions{}
	command := &cobra.Command{
		Use:           "bind",
		Short:         "Bind an allocated prefix or install zone into canonical public routes",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(options.outputPath) == "" {
				return fmt.Errorf("--output is required")
			}
			if (strings.TrimSpace(options.prefix) == "") == (strings.TrimSpace(options.zone) == "") {
				return fmt.Errorf("exactly one of --prefix or --zone is required")
			}
			canonical, err := validateAddressStackSpec(getWorkDir(), specFile)
			if err != nil {
				return err
			}
			bind := addressplan.BindPrefix
			allocated := options.prefix
			if strings.TrimSpace(options.zone) != "" {
				bind, allocated = addressplan.BindZone, options.zone
			}
			candidate, err := bind(canonical, allocated)
			if err != nil {
				return err
			}
			service, err := newArchitectureV2CLIService(getWorkDir(), "", os.Getenv(architectureAuthorityRootEnv))
			if err != nil {
				return err
			}
			_, err = stackspecintent.Persist(stackspecintent.Request{
				WorkspaceRoot:    getWorkDir(),
				SpecPath:         resolvePathFromWorkDir(getWorkDir(), options.outputPath),
				Candidate:        candidate,
				ExpectedSpecHash: options.expectedSpecHash,
				BuildVersion:     version,
				Authority:        service,
			})
			if err != nil {
				return fmt.Errorf("persist bound StackSpec intent: %w", err)
			}
			return nil
		},
	}
	command.Flags().StringVar(&options.prefix, "prefix", "", "Allocated DNS-safe subdomain prefix: hosts <prefix>-<service>.<domain>")
	command.Flags().StringVar(&options.zone, "zone", "", "Allocated install zone label: the stack is served from <zone>.<domain> with hosts <service>.<zone>.<domain>")
	command.Flags().StringVarP(&options.outputPath, "output", "o", "", "Write the validated bound StackSpec to this path")
	command.Flags().StringVar(&options.expectedSpecHash, "expected-spec-hash", "", "Native v2 only: exact current CUE-normalized spec hash required for replacement")
	return command
}

func validateAddressStackSpec(wd, requestedPath string) ([]byte, error) {
	path := resolvePathFromWorkDir(wd, requestedPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read StackSpec %s: %w", path, err)
	}
	service, err := newArchitectureV2CLIService(wd, "", os.Getenv(architectureAuthorityRootEnv))
	if err != nil {
		return nil, err
	}
	validation, err := service.ValidateStackSpec(raw)
	if err != nil {
		return nil, err
	}
	return validation.CanonicalStackSpec, nil
}
