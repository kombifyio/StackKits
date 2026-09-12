package commands

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/spf13/cobra"
)

func init() {
	link := &cobra.Command{Use: "link", Short: "Bind and stop the local portion of an external Federation fabric"}
	var file, fabric string
	bind := &cobra.Command{Use: "bind", Short: "Adopt external WireGuard handles into local Owner custody", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		defer input.Close()
		decoder := json.NewDecoder(io.LimitReader(input, 64<<10))
		decoder.DisallowUnknownFields()
		var custody runtimeexecutorlocal.WireGuardFabricCustody
		if err := decoder.Decode(&custody); err != nil {
			return err
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return errors.New("one external fabric custody document required")
		}
		if err := withLifecycleMutation(getWorkDir(), "federation link bind", func() error { return runtimeexecutorlocal.BindWireGuardFabric(getWorkDir(), custody) }); err != nil {
			return err
		}
		return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"fabricRef": custody.FabricRef, "interface": runtimeexecutorlocal.WireGuardFabricInterface(custody.FabricRef), "bound": true})
	}}
	bind.Flags().StringVar(&file, "file", "", "External fabric custody JSON; contains no private keys")
	stop := &cobra.Command{Use: "stop", Short: "Stop the adopted interface without deleting the external fabric", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := withLifecycleMutation(getWorkDir(), "federation link stop", func() error {
			return runtimeexecutorlocal.NewOSFederationLinkOperations(getWorkDir()).StopInterSiteLink(cmd.Context(), fabric)
		}); err != nil {
			return err
		}
		return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"fabricRef": fabric, "active": false})
	}}
	stop.Flags().StringVar(&fabric, "fabric-ref", "", "Exact adopted opaque fabric reference")
	link.AddCommand(bind, stop)
	federationCmd.AddCommand(link)
}
