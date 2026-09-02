package upgradelifecycle

import (
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
)

func (custody MaterializedRuntimeCustody) validateActive() error {
	if custody.active == nil || !custody.active.Load() || custody.context == nil || custody.transaction == nil {
		return errors.New("executor state: runtime custody is outside its callback lifetime")
	}
	if err := custody.context.Err(); err != nil {
		return err
	}
	return custody.transaction.VerifyPathIdentity()
}

// ReadArtifact reads exact retained bytes by their checkpoint artifact identity.
// It rechecks the CAS digest and is valid only inside WithRuntimeCustody.
func (custody MaterializedRuntimeCustody) ReadArtifact(id string) ([]byte, error) {
	if err := custody.validateActive(); err != nil {
		return nil, err
	}
	blob, ok := custody.blobs[id]
	if !ok {
		return nil, errors.New("executor state: artifact is outside materialized runtime custody")
	}
	return readExecutorStateRecoveryBlob(custody.transaction, blob)
}

// BindRestore verifies the real owner-signed staging result against this exact
// checkpoint, then projects the historical runtime into the private file view.
// The upgrade controller still owns target admission and the mutation journal.
func (custody MaterializedRuntimeCustody) BindRestore(result backuplifecycle.RestoreResult) (restoreactivation.Authority, error) {
	if err := custody.validateActive(); err != nil {
		return restoreactivation.Authority{}, err
	}
	if err := backuplifecycle.VerifyRestoreResult(custody.workspace, result); err != nil {
		return restoreactivation.Authority{}, fmt.Errorf("executor state: verify staged recovery: %w", err)
	}
	if result.OwnerRef != custody.ownerRef || result.AuthorityRef != custody.anchor.AuthorityRef ||
		result.AuthorizationLineage != custody.lineage || result.SnapshotLineage != custody.anchor.Lineage ||
		result.SnapshotAnchorID != custody.anchor.ID ||
		result.Request.RepositoryID != custody.anchor.Repository.RepositoryID ||
		result.Request.SnapshotReceipt != custody.anchor.Snapshot {
		return restoreactivation.Authority{}, errors.New("executor state: staged recovery differs from the signed checkpoint")
	}
	authority, err := restoreactivation.BindRuntimeRecoveryGraph(custody.graph, result)
	if err != nil {
		return restoreactivation.Authority{}, err
	}
	if authority.ComposePath, err = custody.Path(authority.ComposePath); err != nil {
		return restoreactivation.Authority{}, err
	}
	for index := range authority.ComposeRuntimes {
		runtime := &authority.ComposeRuntimes[index]
		if runtime.Path, err = custody.Path(runtime.Path); err != nil {
			return restoreactivation.Authority{}, err
		}
		if runtime.EnvironmentPath != "" {
			if runtime.EnvironmentPath, err = custody.Path(runtime.EnvironmentPath); err != nil {
				return restoreactivation.Authority{}, err
			}
		}
	}
	return authority, nil
}
