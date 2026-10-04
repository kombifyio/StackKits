package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/netmove"
	"github.com/spf13/cobra"
)

// networkWatchEnv opts a host out of the automatic watcher that apply enables.
const networkWatchEnv = "STACKKIT_NETWORK_WATCH"

func newNetworkWatchCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "watch",
		Short: "Re-bind automatically after the server moved (systemd timer)",
		Long: `Install a systemd timer that runs "stackkit network rebind --yes --auto" every
minute and shortly after boot. It is silent while the network is unchanged,
runs one rebind at a time and waits five minutes after a failed attempt.
stackkit apply enables it on systemd hosts; set STACKKIT_NETWORK_WATCH=off or
run "stackkit network watch disable" to opt out.`,
	}
	command.AddCommand(newNetworkWatchEnableCommand(), newNetworkWatchDisableCommand(), newNetworkWatchStatusCommand())
	return command
}

func networkWatchConfig(wd string) (netmove.WatchConfig, error) {
	binary, err := os.Executable()
	if err != nil {
		return netmove.WatchConfig{}, fmt.Errorf("resolve the stackkit executable: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(binary); resolveErr == nil {
		binary = resolved
	}
	return netmove.WatchConfig{Binary: binary, Workspace: wd, Spec: specFile, Home: os.Getenv("HOME"), Path: os.Getenv("PATH")}, nil
}

func newNetworkWatchEnableCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "enable",
		Short:         "Install and start the network watcher timer",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			wd := getWorkDir()
			cfg, err := networkWatchConfig(wd)
			if err != nil {
				return err
			}
			if err := netmove.SetWatchOptOut(wd, false); err != nil {
				return err
			}
			changed, err := netmove.DefaultWatchInstaller().Enable(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			if changed {
				printSuccess("Network watcher enabled (%s).", netmove.WatchTimer)
			} else {
				printInfo("Network watcher already enabled.")
			}
			return nil
		},
	}
}

func newNetworkWatchDisableCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "disable",
		Short:         "Stop and remove the network watcher timer",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := netmove.SetWatchOptOut(getWorkDir(), true); err != nil {
				return err
			}
			removed, err := netmove.DefaultWatchInstaller().Disable(cmd.Context())
			if err != nil {
				return err
			}
			if removed {
				printSuccess("Network watcher disabled. stackkit apply will not enable it again.")
			} else {
				printInfo("Network watcher was not installed. stackkit apply will not enable it.")
			}
			return nil
		},
	}
}

func newNetworkWatchStatusCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:           "status",
		Short:         "Show whether the network watcher timer is installed and running",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status := netmove.DefaultWatchInstaller().Status(cmd.Context())
			if asJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), status)
			}
			printInfo("Network watcher: installed=%t enabled=%t active=%t (%s)", status.Installed, status.Enabled, status.Active, status.Unit)
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Emit the status as JSON")
	return command
}

// runAutomaticNetworkWatch enables the watcher after a converged Apply so a
// server carried to another network recovers by itself. Like the other
// post-Apply conveniences it never fails Apply and prints at most one line.
func runAutomaticNetworkWatch(ctx context.Context, wd string) {
	if strings.TrimSpace(lifecycleJoinOperation) != "" {
		return
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(networkWatchEnv))) {
	case "off", "0", "false", "no":
		return
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 || netmove.WatchOptedOut(wd) {
		return
	}
	installer := netmove.DefaultWatchInstaller()
	if !installer.WatchSupported() || architectureV2WorkspaceIsMember(wd) {
		return
	}
	cfg, err := networkWatchConfig(wd)
	if err != nil || strings.HasSuffix(cfg.Binary, ".test") {
		return // a test binary must never become a system service
	}
	if ctx == nil {
		ctx = context.Background()
	}
	changed, err := installer.Enable(ctx, cfg)
	if err != nil {
		printWarning("network watcher was not enabled: %v; run `stackkit network watch enable`", err)
		return
	}
	if changed {
		printInfo("Network watcher enabled: this server re-binds itself after moving to another network (opt out: %s=off).", networkWatchEnv)
	}
}
