package commands

import (
	"errors"
	"path"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
)

// Read the exact retained plan from verified checkpoint custody. Generated
// workspace artifacts may have changed during a failed recovery attempt.
func materializeRecoveredUpgradeInventory(workspace string, snapshot upgradelifecycle.ExecutorStateSnapshot) (string, func(), error) {
	for _, artifact := range snapshot.Artifacts {
		if artifact.ID != "resolved-plan" {
			continue
		}
		data, err := (upgradelifecycle.ExecutorStateStore{}).ReadArtifact(workspace, snapshot.ID, artifact.ID)
		if err != nil {
			return "", nil, err
		}
		if exactBeta4CheckpointDigest(data) != artifact.SHA256 {
			return "", nil, errors.New("recovered canonical plan differs from signed checkpoint bytes")
		}
		inspection := generationartifact.PlanInspection{Binding: snapshot.Lineage.Binding, OutputRoot: path.Dir(path.Dir(artifact.Path))}
		if _, err := generationartifact.ParseHistoricalPlan(data, inspection); err != nil {
			return "", nil, err
		}
		inventory, cleanup, err := materializePlanInventory(data)
		if err != nil {
			return "", nil, err
		}
		if inventory == "" {
			cleanup()
			return "", nil, errors.New("signed recovered plan has no sealed Inventory")
		}
		return inventory, cleanup, nil
	}
	return "", func() {}, nil
}
