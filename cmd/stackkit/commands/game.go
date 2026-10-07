package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"text/tabwriter"
	"time"

	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/spf13/cobra"
)

// The game commands are the scoped owner operations on the game servers of
// the installed Game platform (ADR-0043, ADR-0048): they reach the Panel
// through the applied workload with keys derived from owner custody, so
// neither the owner nor an agent calling them through MCP ever handles a
// Panel key.

func newGameCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "game",
		Short: "Operate the owner's game servers on the installed Game workload",
		Long:  "List game servers and run the routine owner operations (power, allow list) through the Panel (Calagopus, Pelican or Pterodactyl) of the applied Game workload. Creating a server is `stackkit setup game`.",
	}
	list := &cobra.Command{
		Use: "list", Short: "List the owner's game servers with state and port", Args: cobra.NoArgs,
		Example: "  stackkit game list --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var servers []appsetup.GameServerSummary
			err := withGamePanel(cmd, 2*time.Minute, func(ctx context.Context, panel appsetup.GamePanel) error {
				var err error
				servers, err = panel.ListGameServers(ctx)
				return err
			})
			if err != nil {
				return err
			}
			if servers == nil {
				servers = []appsetup.GameServerSummary{}
			}
			if asJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"servers": servers})
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(writer, "SERVER\tNAME\tSTATE\tPORT")
			for _, server := range servers {
				fmt.Fprintf(writer, "%s\t%s\t%s\t%d\n", server.Identifier, server.Name, server.State, server.Port)
			}
			return writer.Flush()
		},
	}
	var signal string
	var ownerApproved bool
	power := &cobra.Command{
		Use: "power <server>", Short: "Start, stop or restart one game server and wait for the result", Args: cobra.ExactArgs(1),
		Example: "  stackkit game power 92c66407 --signal restart --owner-approve",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !ownerApproved {
				return errors.New("game power requires --owner-approve")
			}
			var state string
			err := withGamePanel(cmd, 10*time.Minute, func(ctx context.Context, panel appsetup.GamePanel) error {
				var err error
				state, err = panel.GameServerPower(ctx, args[0], signal)
				return err
			})
			if err != nil {
				return err
			}
			if asJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"server": args[0], "signal": signal, "state": state})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Game server %s is %s\n", args[0], state)
			return err
		},
	}
	power.Flags().StringVar(&signal, "signal", "", "Power signal: start, stop or restart")
	_ = power.MarkFlagRequired("signal")
	power.Flags().BoolVar(&ownerApproved, "owner-approve", false, "Authorize the power change with the established local Owner custody")
	var player string
	var allowApproved bool
	allow := &cobra.Command{
		Use: "allow <server>", Short: "Admit one player on a StackKits allow-list game server", Args: cobra.ExactArgs(1),
		Example: "  stackkit game allow 92c66407 --player Notch --owner-approve",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !allowApproved {
				return errors.New("game allow requires --owner-approve")
			}
			err := withGamePanel(cmd, 2*time.Minute, func(ctx context.Context, panel appsetup.GamePanel) error {
				return panel.AllowGamePlayer(ctx, args[0], player)
			})
			if err != nil {
				return err
			}
			if asJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"server": args[0], "player": player, "admitted": true})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s is on the allow list of game server %s\n", player, args[0])
			return err
		},
	}
	allow.Flags().StringVar(&player, "player", "", "Exact game account name (Minecraft account name or Xbox gamertag)")
	_ = allow.MarkFlagRequired("player")
	allow.Flags().BoolVar(&allowApproved, "owner-approve", false, "Authorize the allow-list change with the established local Owner custody")
	for _, sub := range []*cobra.Command{list, power, allow} {
		sub.Flags().BoolVar(&asJSON, "json", false, "Emit stackkit.command-result/v1 JSON")
		cmd.AddCommand(sub)
	}
	return cmd
}

func init() { rootCmd.AddCommand(newGameCommand()) }

// withGamePanel resolves the applied Game workload and calls use with its
// Panel bound to the custody-derived keys.
func withGamePanel(cmd *cobra.Command, timeout time.Duration, use func(ctx context.Context, panel appsetup.GamePanel) error) error {
	ctx, cancel := context.WithTimeout(commandContext(cmd), timeout)
	defer cancel()
	workspace := getWorkDir()
	authority, err := inspectNativeV2AppliedAuthority(ctx, workspace, specFile)
	if err != nil {
		return err
	}
	if !planSelectsWorkload(authority, "game") {
		return errors.New("the applied StackKit does not include the Game workload")
	}
	deployment, err := nativeAppliedWorkloadDeployment(authority, "game")
	if err != nil {
		return err
	}
	keys, err := gameCustodyKeys(workspace, deployment)
	if err != nil {
		return err
	}
	return nativehost.WithStandaloneComposeHTTP(ctx, workspace, deployment, func(client *http.Client, baseURL string) error {
		panel, err := keys.panel(client, baseURL)
		if err != nil {
			return err
		}
		return use(ctx, panel)
	})
}

// gamePanelKeys are the Panel keys of one applied Game platform.
type gamePanelKeys struct {
	platform, setupKey, clientKey string
}

func (k gamePanelKeys) panel(client *http.Client, baseURL string) (appsetup.GamePanel, error) {
	return appsetup.NewGamePanel(k.platform, client, baseURL, k.setupKey, k.clientKey)
}

// gamePlatformModules maps each admitted Game platform module to its
// platform and the key format its bootstrap minted from owner custody
// (ADR-0043, ADR-0048).
var gamePlatformModules = map[string]struct {
	platform, setupPrefix, clientPrefix string
	// split keys are an 11-character identifier plus a 32-character token;
	// otherwise the whole custody material follows the prefix.
	split bool
}{
	"stackkits-calagopus-runtime":   {platform: appsetup.GamePlatformCalagopus, setupPrefix: "c7sp_", clientPrefix: "c7sp_"},
	"stackkits-pelican-runtime":     {platform: appsetup.GamePlatformPelican, setupPrefix: "papp_", clientPrefix: "pacc_", split: true},
	"stackkits-pterodactyl-runtime": {platform: appsetup.GamePlatformPterodactyl, setupPrefix: "ptla_", clientPrefix: "ptlc_", split: true},
}

// gameCustodyKeys derives the Panel's setup and client keys from owner
// custody exactly as the platform's bootstrap minted them.
func gameCustodyKeys(workspace string, deployment nativehost.SelectedPaaSWorkloadDeployment) (gamePanelKeys, error) {
	module, ok := gamePlatformModules[deployment.ModuleRef]
	if !ok {
		return gamePanelKeys{}, fmt.Errorf("the Game workload module %q is not an admitted game platform", deployment.ModuleRef)
	}
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(deployment.Bundle)
	if err != nil {
		return gamePanelKeys{}, err
	}
	derive := func(slot, prefix string) (string, error) {
		material, err := localevidence.ResolveLocalSecretMaterial(workspace, bundle.SecretRefs[slot])
		if err != nil {
			return "", fmt.Errorf("resolve the owner-custodied %s: %w", slot, err)
		}
		defer clear(material)
		if len(material) < 43 {
			return "", fmt.Errorf("custody material for %s is too short", slot)
		}
		if module.split {
			return prefix + string(material[:11]) + string(material[11:43]), nil
		}
		return prefix + string(material), nil
	}
	keys := gamePanelKeys{platform: module.platform}
	if keys.setupKey, err = derive("application-api-key", module.setupPrefix); err != nil {
		return gamePanelKeys{}, err
	}
	if keys.clientKey, err = derive("client-api-key", module.clientPrefix); err != nil {
		return gamePanelKeys{}, err
	}
	return keys, nil
}
