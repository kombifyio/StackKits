package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/netmove"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// newNetworkCommand groups the "home network move" operations of a standalone
// server: observe, re-bind after the move, pre-stage WiFi, and keep a watcher.
func newNetworkCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "network",
		Short: "Move this server to another network: status, re-bind, WiFi and watcher",
		Long: `Operations for carrying this server from one network to another.

Before the move, stage the destination WiFi (stackkit network wifi add). After
the move the server gets a new address from the destination DHCP; the network
watcher (stackkit network watch enable, installed by stackkit apply) notices
that the recorded site address is stale and re-binds the stack by itself.`,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	command.AddCommand(
		newNetworkStatusCommand(),
		newNetworkRebindCommand(),
		newNetworkWifiCommand(),
		newNetworkWatchCommand(),
	)
	return command
}

type networkStatusReport struct {
	Verdict          string              `json:"verdict"`
	CurrentAddress   string              `json:"currentAddress,omitempty"`
	Interface        string              `json:"interface,omitempty"`
	Gateway          string              `json:"gateway,omitempty"`
	Wireless         bool                `json:"wireless"`
	SSID             string              `json:"ssid,omitempty"`
	CustodyAddress   string              `json:"custodyAddress,omitempty"`
	InventoryAddress string              `json:"inventoryAddress,omitempty"`
	ApplyPending     bool                `json:"applyPending,omitempty"`
	FleetMember      bool                `json:"fleetMember,omitempty"`
	Watch            netmove.WatchStatus `json:"watch"`
}

func collectNetworkStatus(ctx context.Context, wd string) networkStatusReport {
	observed := netmove.Observe(ctx, netmove.DefaultSys())
	custody, _ := localevidence.BasementLANDNSResolverAddress(wd)
	inventory := inventorySiteAddress(wd)
	state := netmove.LoadState(wd)
	report := networkStatusReport{
		Verdict:          netmove.Verdict(observed.Address, custody, inventory),
		CurrentAddress:   observed.Address,
		Interface:        observed.Interface,
		Gateway:          observed.Gateway,
		Wireless:         observed.Wireless,
		SSID:             observed.SSID,
		CustodyAddress:   custody,
		InventoryAddress: inventory,
		ApplyPending:     state.ApplyPending,
		FleetMember:      architectureV2WorkspaceIsMember(wd),
		Watch:            netmove.DefaultWatchInstaller().Status(ctx),
	}
	if report.Verdict != netmove.VerdictMoved && state.ApplyPending && observed.Address != "" {
		report.Verdict = netmove.VerdictMoved
	}
	return report
}

// inventorySiteAddress returns the site address recorded for this host's node
// in .stackkit/inventory.yaml, or "" when none is recorded.
func inventorySiteAddress(wd string) string {
	rawSpec, _, handled, err := classifyArchitectureV2ExecutionSpec(wd, specFile)
	if err != nil || !handled {
		return ""
	}
	data, _, err := locateArchitectureV2Inventory(wd, "")
	if err != nil || len(data) == 0 {
		return ""
	}
	nodeRef, _, err := localInventoryNode(wd, rawSpec, architectureV2ExecutionCLIOptions{})
	if err != nil {
		return ""
	}
	var document map[string]any
	if yaml.Unmarshal(data, &document) != nil {
		return ""
	}
	nodes, _ := document["nodes"].(map[string]any)
	node, _ := nodes[nodeRef].(map[string]any)
	address, _ := node["siteAddress"].(string)
	return strings.TrimSpace(address)
}

func newNetworkStatusCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "status",
		Short: "Show whether this server moved to another network",
		Long: `Compare the address this server has now with the address recorded in the
LAN resolver custody and in .stackkit/inventory.yaml. The verdict is "in-place"
when they match, "moved" when a recorded address is stale, and "unknown" when
there is nothing to compare. The command always exits 0.`,
		Example: `  stackkit network status
  stackkit network status --json`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := collectNetworkStatus(cmd.Context(), getWorkDir())
			if asJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), report)
			}
			printInfo("Network verdict: %s", report.Verdict)
			printInfo("  Current address: %s", orDash(report.CurrentAddress))
			printInfo("  Interface: %s (gateway %s)", orDash(report.Interface), orDash(report.Gateway))
			if report.Wireless {
				printInfo("  WiFi: %s", orDash(report.SSID))
			}
			printInfo("  Recorded in LAN resolver custody: %s", orDash(report.CustodyAddress))
			printInfo("  Recorded in inventory: %s", orDash(report.InventoryAddress))
			if report.Verdict == netmove.VerdictMoved {
				printWarning("This server moved. Run: stackkit network rebind")
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Emit the observation as JSON")
	return command
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

type networkRebindOptions struct {
	yes     bool
	noApply bool
	auto    bool
	asJSON  bool
}

type networkWifiFacts struct {
	Interface string `json:"interface"`
	SSID      string `json:"ssid"`
}

type networkRebindResult struct {
	Moved           bool             `json:"moved"`
	PreviousAddress string           `json:"previousAddress,omitempty"`
	CurrentAddress  string           `json:"currentAddress,omitempty"`
	Applied         bool             `json:"applied"`
	CustodyReissued bool             `json:"custodyReissued"`
	Wifi            networkWifiFacts `json:"wifi"`
	Skipped         string           `json:"skipped,omitempty"`
}

func newNetworkRebindCommand() *cobra.Command {
	options := &networkRebindOptions{}
	command := &cobra.Command{
		Use:   "rebind",
		Short: "Re-bind the stack to this server's new network address",
		Long: `After the server moved to another network, re-issue the signed LAN resolver
record for the new address and regenerate and apply the stack so listeners bind
to it. Nothing happens when the server did not move.

Owner identity, secrets, certificate authorities and data are not touched; the
previous resolver record is kept under .stackkit/custody/backups. LAN clients
and the router's DNS must point at the new resolver address afterwards.`,
		Example: `  stackkit network rebind --yes
  stackkit network rebind --yes --json
  stackkit network rebind --no-apply`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := runNetworkRebind(cmd, options)
			if err != nil {
				if options.asJSON {
					return writeMachineCommandFailure(cmd, err)
				}
				return err
			}
			if options.asJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), result)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&options.yes, "yes", false, "Re-bind without asking for confirmation")
	command.Flags().BoolVar(&options.noApply, "no-apply", false, "Re-issue the resolver record only; do not regenerate and apply")
	command.Flags().BoolVar(&options.auto, "auto", false, "Unattended run for the network watcher: quiet when nothing moved, one run at a time, backs off after a failure")
	command.Flags().BoolVar(&options.asJSON, "json", false, "Emit the result as JSON")
	return command
}

func runNetworkRebind(cmd *cobra.Command, options *networkRebindOptions) (result networkRebindResult, retErr error) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	wd := getWorkDir()
	now := time.Now()

	if options.auto {
		release, ok, err := netmove.AcquireLock(wd, now)
		if err != nil {
			return result, err
		}
		if !ok {
			result.Skipped = "another network rebind is running"
			return result, nil
		}
		defer release()
		if state := netmove.LoadState(wd); state.ShouldBackOff(now) {
			result.Skipped = "the last automatic rebind failed less than " + netmove.RetryBackoff.String() + " ago"
			return result, nil
		}
	}

	report := collectNetworkStatus(ctx, wd)
	result.CurrentAddress = report.CurrentAddress
	result.Wifi = networkWifiFacts{Interface: report.Interface, SSID: report.SSID}
	result.PreviousAddress = firstNonEmpty(report.CustodyAddress, report.InventoryAddress)
	if report.FleetMember {
		// A member's address belongs to the Foundation Node's shared Inventory.
		result.Skipped = "this server is a Fleet member; the Home owner re-binds the shared Inventory"
		if !options.auto {
			printInfo("%s", result.Skipped)
		}
		return result, nil
	}
	if report.Verdict != netmove.VerdictMoved {
		if !options.auto {
			printSuccess("Network unchanged (%s): nothing to re-bind.", orDash(report.CurrentAddress))
		}
		return result, nil
	}
	result.Moved = true

	if !options.yes && !options.auto {
		if !confirmNetworkRebind(result.PreviousAddress, result.CurrentAddress) {
			return result, errors.New("network rebind needs confirmation; pass --yes to proceed")
		}
	}

	defer func() {
		// Record the outcome so a failed run backs off and is retried even
		// though the recorded addresses may already match.
		state := netmove.LoadState(wd)
		state.LastAttempt, state.LastFailed, state.LastAddress = now, retErr != nil, result.CurrentAddress
		state.LastError = ""
		if retErr != nil {
			state.LastError = retErr.Error()
		}
		if retErr == nil && result.Applied {
			state.ApplyPending = false
		}
		_ = netmove.SaveState(wd, state)
	}()

	printInfo("This server moved: %s -> %s. Re-binding the stack.", orDash(result.PreviousAddress), result.CurrentAddress)
	if report.CustodyAddress != "" && report.CustodyAddress != report.CurrentAddress {
		address, err := netip.ParseAddr(report.CurrentAddress)
		if err != nil {
			return result, fmt.Errorf("network rebind: current address: %w", err)
		}
		if err := withLifecycleMutation(wd, "network rebind", func() error {
			_, _, rebindErr := localevidence.RebindBasementLANAddress(wd, address)
			return rebindErr
		}); err != nil {
			return result, fmt.Errorf("network rebind: re-issue the LAN resolver record: %w", err)
		}
		result.CustodyReissued = true
		printSuccess("Re-issued the signed LAN resolver record for %s.", report.CurrentAddress)
	}
	state := netmove.LoadState(wd)
	state.ApplyPending = true
	_ = netmove.SaveState(wd, state)

	if options.noApply {
		printInfo("Skipped regenerate and apply (--no-apply). Run `stackkit apply` to finish the re-bind.")
	} else {
		// The resolved plan on disk still describes the old network; regenerate
		// it exactly as `stackkit generate` does, then apply without a prompt.
		if err := runGenerate(cmd, nil); err != nil {
			return result, fmt.Errorf("network rebind: regenerate on the new address: %w", err)
		}
		applyAutoApprove = true
		if err := runApply(cmd, nil); err != nil {
			return result, fmt.Errorf("network rebind: apply on the new address: %w", err)
		}
		result.Applied = true
		printSuccess("Applied the stack on %s.", report.CurrentAddress)
	}
	if result.CustodyReissued {
		printInfo("LAN clients must resolve this server's names at %s. Point the router's DNS (or each client's DNS) at %s.", report.CurrentAddress, report.CurrentAddress)
	}
	return result, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func confirmNetworkRebind(previous, current string) bool {
	if info, err := os.Stdin.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	fmt.Fprintf(os.Stderr, "Re-bind the stack from %s to %s and apply it? [y/N] ", orDash(previous), current)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}
