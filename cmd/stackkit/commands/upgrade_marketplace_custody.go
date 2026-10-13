package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
)

// The command owner binds an image change to the same signed upgrade child as
// host admission. Historical artifacts describe the prior installation only.
func publicMarketplaceUpgradeAuthority(workspace string) nativehost.MarketplaceUpgradeAuthority {
	return func(ctx context.Context, target architecturev2renderer.MarketplaceCoolifyDocument) error {
		join, err := currentLifecycleMutationJoin("apply")
		if err != nil {
			return err
		}
		snapshotID, err := lifecyclemutation.InspectUpgradeTargetApplySnapshot(workspace, join)
		if err != nil {
			return err
		}
		store := upgradelifecycle.ExecutorStateStore{}
		snapshot, err := store.Load(workspace, snapshotID)
		if err != nil {
			return err
		}
		owner, err := localevidence.LoadOwnerCustody(workspace)
		if err != nil {
			return err
		}
		if snapshot.OperationID != join.OperationID || snapshot.OwnerRef != owner.OwnerRef {
			return errors.New("Marketplace checkpoint differs from the admitted upgrade or node owner")
		}
		return store.WithRuntimeCustody(ctx, workspace, snapshotID, func(_ context.Context, custody upgradelifecycle.MaterializedRuntimeCustody) error {
			raw, err := custody.ReadArtifact("applied-runtime-custody")
			if err != nil {
				return err
			}
			defer clear(raw)
			historical, err := architecturev2.ParseHistoricalAppliedRuntimeCustody(raw)
			if err != nil || historical.Binding() != snapshot.Lineage.Binding || historical.ManifestHash() != snapshot.Lineage.ManifestHash {
				return errors.New("Marketplace prior Apply differs from its signed checkpoint lineage")
			}
			prior, err := nativeBackupMarketplaceInstallationDocument(historical.Request(), nativehost.LocalTargetBinding{
				SiteRef: owner.Binding.SiteRef, NodeRef: owner.Binding.NodeRef, ExecutionChannelRef: owner.Binding.ChannelRef,
			})
			if err != nil {
				return err
			}
			if err := marketplaceUpgradePreservesIdentity(prior, target); err != nil {
				return err
			}
			root, err := confinedfs.Open(workspace)
			if err != nil {
				return err
			}
			defer root.Close()
			transaction, err := root.BeginTransaction()
			if err != nil {
				return err
			}
			defer transaction.Close()
			for _, name := range localbackuppolicy.MarketplaceRuntimeFiles() {
				file := path.Join(localbackuppolicy.MarketplaceRuntimeDirectory, name)
				retained, err := custody.Path(file)
				if err != nil {
					return fmt.Errorf("Marketplace checkpoint has no private %s: %w", name, err)
				}
				old, oldInfo, oldErr := transaction.ReadStableBounded(retained, 1<<20)
				current, info, readErr := transaction.ReadStableBounded(file, 1<<20)
				equal := oldErr == nil && readErr == nil && oldInfo != nil && info != nil &&
					info.Mode().Perm()&0077 == 0 && oldInfo.Mode().Perm()&0077 == 0 && bytes.Equal(old, current)
				clear(old)
				clear(current)
				if !equal {
					return fmt.Errorf("Marketplace private %s differs from the checkpoint; recover before changing images", name)
				}
			}
			return transaction.VerifyPathIdentity()
		})
	}
}

func marketplaceUpgradePreservesIdentity(prior, target architecturev2renderer.MarketplaceCoolifyDocument) error {
	// Parsing both recipes retains the qualified Coolify 4.4, PostgreSQL 15
	// and Redis 7 bounds; a checkpoint cannot admit a major data migration.
	if err := architecturev2renderer.ValidateMarketplaceCoolifyDocument(prior); err != nil {
		return err
	}
	if err := architecturev2renderer.ValidateMarketplaceCoolifyDocument(target); err != nil {
		return err
	}
	if prior.KitRef != target.KitRef || prior.SiteRef != target.SiteRef || prior.NodeRef != target.NodeRef ||
		prior.DomainBase != target.DomainBase || prior.SubdomainPrefix != target.SubdomainPrefix || !reflect.DeepEqual(prior.Route, target.Route) {
		return errors.New("Marketplace upgrade must preserve its Kit, node, identity and addresses; use an explicit manager transition for ownership changes")
	}
	return nil
}
