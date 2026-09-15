package commands

import (
	"encoding/json"
	"fmt"

	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/spf13/cobra"
)

type hostEnvironmentReport struct {
	Environment        string `json:"environment"`
	PublicIP           string `json:"publicIP,omitempty"`
	PrivateIP          string `json:"privateIP,omitempty"`
	IsNAT              bool   `json:"isNAT,omitempty"`
	HasPublicInterface bool   `json:"hasPublicInterface,omitempty"`
	Description        string `json:"description"`
	PublicServer       bool   `json:"publicServer"`
}

func newHostEnvironmentCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "environment",
		Short: "Observe whether this host looks like a home network or a public server",
		Long: `Detect the network environment of this machine.

This is observation, not kit selection. Installers and init use it to ask
before authoring local home addresses on a VPS, or Cloud Kit on a LAN host.
STACKKIT_NETWORK_ENV=home|vps|cloud|unknown overrides live detection.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			detected := netenv.Detect(cmd.Context())
			report := hostEnvironmentReport{
				Environment:        string(detected.Environment),
				PublicIP:           detected.PublicIP,
				PrivateIP:          detected.PrivateIP,
				IsNAT:              detected.IsNAT,
				HasPublicInterface: detected.HasPublicInterface,
				Description:        netenv.FormatEnvironment(detected.Environment),
				PublicServer:       netenv.IsPublicServer(detected.Environment),
			}
			if asJSON {
				encoded, err := json.Marshal(report)
				if err != nil {
					return fmt.Errorf("encode host environment: %w", err)
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", encoded)
				return err
			}
			printInfo("Network: %s", report.Description)
			if report.PublicIP != "" {
				printInfo("  Public IP: %s", report.PublicIP)
			}
			if report.PrivateIP != "" {
				printInfo("  Private IP: %s", report.PrivateIP)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the observation as JSON")
	return cmd
}
