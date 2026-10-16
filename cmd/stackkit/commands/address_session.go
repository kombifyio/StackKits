package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

func newAddressSessionZoneCommand() *cobra.Command {
	var zone string
	var abort bool
	var check bool
	var migrate bool
	command := &cobra.Command{
		Use: "session-zone", Short: "Resume an installed Cloud session pair in an allocated private zone, preserving PocketID",
		Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) (returnErr error) {
			defer func() { returnErr = localevidence.CloudSessionRoutingDiagnostic(returnErr) }()
			if strings.TrimSpace(zone) == "" {
				return fmt.Errorf("--zone is required; allocate and verify both service routes through your existing address provider first")
			}
			if abort && check {
				return fmt.Errorf("--abort and --check are mutually exclusive")
			}
			workspace := getWorkDir()
			authority, err := newArchitectureV2CLIService(workspace, "", os.Getenv(architectureAuthorityRootEnv))
			if err != nil {
				return localevidence.CloudSessionRoutingDiagnostic(err)
			}
			absolute := resolvePathFromWorkDir(workspace, specFile)
			relative, err := filepath.Rel(workspace, absolute)
			if err != nil {
				return err
			}
			var state localevidence.CloudSessionRouting
			err = stackspecintent.WithCloudSessionRouteMigrator(workspace, zone, version, authority, func(intent stackspecintent.CloudSessionRouteMigrator) error {
				service, err := localowner.NewService(workspace)
				if err != nil {
					return err
				}
				if check {
					state.After, err = service.CheckCloudSessionRoutes(cmd.Context(), filepath.ToSlash(relative), intent, nativehost.NewCloudSessionMigrationRuntime(workspace, migrate))
					state.Phase = "preflight-ready"
					return err
				}
				if abort {
					state, err = service.AbortCloudSessionRoutes(cmd.Context(), filepath.ToSlash(relative), intent, nativehost.NewCloudSessionRuntime(workspace))
				} else {
					state, err = service.TransitionCloudSessionRoutes(cmd.Context(), filepath.ToSlash(relative), intent, nativehost.NewCloudSessionMigrationRuntime(workspace, migrate))
				}
				return err
			})
			if err != nil {
				return err
			}
			if state.Phase == "no-active-transition" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"status": state.Phase})
			}
			topology := state.After
			if state.Phase == "aborted" {
				topology = state.Before
			}
			result := map[string]any{"status": state.Phase, "pocketIdOrigin": "https://" + topology.PocketIDHost, "tinyAuthOrigin": "https://" + topology.TinyAuthHost, "hubURL": "https://" + topology.HubHost, "sourceComposeDigest": state.SourceDigest, "overlayDigest": state.OverlayDigest, "transitionDigest": localevidence.SessionRoutingValueDigest(state)}
			if state.Migration != nil {
				result["componentMigration"] = state.Migration
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		},
	}
	command.Flags().StringVar(&zone, "zone", "", "Already allocated private kombify.me zone for Auth and Hub; the existing PocketID origin is retained")
	command.Flags().BoolVar(&abort, "abort", false, "Restore only the signed original session routes and callback after an interrupted transition")
	command.Flags().BoolVar(&check, "check", false, "Inspect existing custody, component pin and source/runtime closure without writing transition state or changing the callback")
	command.Flags().BoolVar(&migrate, "migrate-tinyauth", false, "Migrate the qualified legacy TinyAuth component to the CUE-pinned image with a verified cold original-data snapshot; no whole-kit upgrade")
	return command
}
