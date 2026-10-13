package upgradelifecycle

import (
	"errors"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// ReadArtifact returns immutable retained bytes after re-verifying the complete
// owner-signed checkpoint. It performs no materialization or execution and can
// be used from a recovery callback already holding the store's mutation lock.
func (store ExecutorStateStore) ReadArtifact(workspace, snapshotID, artifactID string) ([]byte, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer transaction.Close()
	snapshot, err := store.loadWithTransaction(workspace, transaction, snapshotID)
	if err != nil {
		return nil, err
	}
	for _, artifact := range snapshot.Artifacts {
		if artifact.ID == artifactID {
			return readExecutorStateRecoveryBlob(transaction, artifact)
		}
	}
	return nil, errors.New("executor state: requested artifact is outside the signed checkpoint")
}
