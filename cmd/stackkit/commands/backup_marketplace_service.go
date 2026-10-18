package commands

import (
	"context"
	"errors"
	"fmt"
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/spf13/cobra"
)

func runMarketplaceBackupSelection(cmd *cobra.Command, service string) error {
	component, _ := cmd.Flags().GetString("marketplace-component")
	volume, _ := cmd.Flags().GetString("marketplace-volume")
	dataClass, _ := cmd.Flags().GetString("marketplace-data-class")
	if component == "" || volume == "" {
		return errors.New("Store selection requires exact --marketplace-component and --marketplace-volume")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), backupQuickOperationTimeout)
	defer cancel()
	authority, err := inspectNativeV2BackupAuthorityForRequest(ctx, getWorkDir(), specFile)
	if err != nil {
		return err
	}
	document, err := nativeBackupMarketplaceDocument(ctx, authority)
	if err != nil {
		return err
	}
	selected, err := nativehost.InspectMarketplaceStoreSelection(ctx, authority.WorkspaceRoot, document, service, component, volume, dataClass)
	if err != nil {
		return err
	}
	if backupOutputJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), selected)
	}
	return fmt.Errorf("Store selection returns structured generation metadata; rerun with --json and explicitly select it in backupPolicy.marketplaceServices before generate/apply")
}
func nativeMarketplaceServiceActivationRuntime(ctx context.Context, base restoreactivation.Runtime, a restoreactivation.Authority, held nativeV2BackupAuthority, service string) (restoreactivation.Runtime, error) {
	document, err := nativeBackupMarketplaceDocument(ctx, held)
	if err != nil {
		return nil, err
	}
	for _, source := range held.Policy.SourceProjection().MarketplaceServices {
		if source.ServiceUUID == service {
			observe := func(ctx context.Context) (backupexec.QuiesceContainer, error) {
				current, err := inspectNativeV2BackupAuthorityForRequest(ctx, held.WorkspaceRoot, specFile)
				if err != nil {
					return backupexec.QuiesceContainer{}, err
				}
				if !sameNativeV2BackupAuthority(held, current) {
					return backupexec.QuiesceContainer{}, errors.New("Store current Plan/Apply changed")
				}
				return nativehost.ObserveMarketplaceStore(ctx, held.WorkspaceRoot, document, source)
			}
			driver, err := nativehost.NewMarketplaceStoreControlDriver(held.WorkspaceRoot, document, source, a.OperationID)
			if err != nil {
				return nil, err
			}
			return restoreactivation.WithMarketplaceService(base, a, source, observe, driver)
		}
	}
	return nil, errors.New("Store service is outside the signed source policy")
}
