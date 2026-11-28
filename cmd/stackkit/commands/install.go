package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/installer"
	"github.com/spf13/cobra"
)

func newInstallCommand() *cobra.Command {
	var opts installer.Options
	var outputJSON bool

	cmd := &cobra.Command{
		Use:   "install [basement-kit|cloud-kit|modern-homelab]",
		Short: "Install and onboard a StackKit with 3-axis setup (Go installer replacing shell scripts)",
		Long: `Unified Go installer replacing base-install.sh, cloud-install.sh, and
install-host-bootstrap.sh (Blueprint v5.1 §6.2, ADR-0044, ADR-0055).

Exposes the three user-facing onboarding axes:
  1. Workloads & Kit: basement-kit | cloud-kit | modern-homelab
  2. Address Zone:    custom-domain | kombify-me | local-only (lab.home)
  3. Setup Style:     bootstrapped (default) | clean (Clean Rollout)

Performs zero local docker build and supports 7 languages (en, de, es, fr, hi, ar, zh-Hans).`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && strings.TrimSpace(opts.Kit) == "" {
				opts.Kit = strings.TrimSpace(args[0])
			}
			plan, err := installer.BuildPlan(opts)
			if err != nil {
				return machineAwareCommandError(cmd, err)
			}
			if !opts.PlanOnly && !opts.CLIOnly {
				if err := os.MkdirAll(plan.WorkspaceDir, 0o755); err != nil {
					return machineAwareCommandError(cmd, fmt.Errorf("create workspace directory: %w", err))
				}
			}
			if outputJSON || opts.PlanOnly {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(plan)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s (%s)\n", installer.Msg(plan.Language, "title"), plan.Kit)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", installer.Msg(plan.Language, "plan_ready"))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Workspace: %s | Zone: %s (%s) | Setup: %s\n",
				plan.WorkspaceDir, plan.AddressZone, plan.Domain, plan.SetupStyle)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", installer.Msg(plan.Language, "next_apply"))
			return nil
		},
	}

	cmd.Flags().StringVar(&opts.Kit, "kit", "", "Kit to install: basement-kit, cloud-kit, or modern-homelab")
	cmd.Flags().StringVar(&opts.AddressZone, "address-zone", "", "Address zone: custom-domain, kombify-me, or local-only")
	cmd.Flags().StringVar(&opts.Domain, "domain", "", "Zone domain (e.g. example.com, demo.kombify.me, or lab.home)")
	cmd.Flags().StringVar(&opts.SetupStyle, "setup-style", "bootstrapped", "Setup style: bootstrapped (default) or clean")
	cmd.Flags().StringVar(&opts.WorkspaceDir, "workspace", "my-homelab", "Workspace directory to prepare")
	cmd.Flags().StringVar(&opts.OwnerEmail, "owner-email", "", "Local owner email address")
	cmd.Flags().StringVar(&opts.Language, "lang", "", "Installer language: en, de, es, fr, hi, ar, zh-Hans")
	cmd.Flags().BoolVar(&opts.CLIOnly, "cli-only", false, "Install/verify CLI only without initializing a kit workspace")
	cmd.Flags().BoolVar(&opts.PrepareHost, "prepare-host", false, "Prepare host prerequisites (Docker/Compose) using interactive sudo")
	cmd.Flags().BoolVar(&opts.InstallSystemd, "install-systemd", false, "Install ADR-0051 systemd watchdog and host-exec units")
	cmd.Flags().BoolVar(&opts.NonInteractive, "non-interactive", false, "Fail closed instead of prompting interactively")
	cmd.Flags().BoolVar(&opts.PlanOnly, "plan-only", false, "Validate inputs and emit the install plan without mutating the host")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Emit machine-readable install plan as JSON")

	return cmd
}

func init() {
	rootCmd.AddCommand(newInstallCommand())
}
