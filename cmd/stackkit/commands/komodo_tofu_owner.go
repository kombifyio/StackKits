package commands

import (
	"errors"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
	"github.com/spf13/cobra"
)

func init() {
	command := &cobra.Command{Use: "komodo-tofu-owner", Hidden: true, Args: cobra.NoArgs, SilenceErrors: true, SilenceUsage: true,
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			code, err := opentofu.RunKomodoTofuOwner(cmd.Context())
			if err != nil {
				return err
			}
			if code != 0 {
				return &exitCodeError{code: code, err: errors.New("Komodo OpenTofu process exit")}
			}
			return nil
		}}
	rootCmd.AddCommand(command)
}
