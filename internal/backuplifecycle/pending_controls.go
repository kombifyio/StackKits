package backuplifecycle

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// RequireSettledRuntimeControls reads existing authenticated snapshot journals.
// The shared lifecycle gate calls it before Plan/generation/ordinary mutation;
// a new operation ID cannot evade an older pending manager dispatch. Only the
// exact original snapshot may enter its observation/recovery path.
func RequireSettledRuntimeControls(workspace, resumeOperation string) error {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	entries, err := tx.Walk(operationDirectory)
	if closeErr := tx.Close(); closeErr != nil {
		return closeErr
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	service := &Service{workspaceRoot: workspace}
	for _, entry := range entries {
		if entry.Info.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Path, ".json") {
			return errors.New("backuplifecycle: unexpected snapshot journal entry")
		}
		op, err := service.loadOperation(entry.Path)
		if err != nil {
			return err
		}
		if op.Quiescence == nil || op.Quiescence.ControlVersion != 1 || op.Quiescence.Phase == "restored" {
			continue
		}
		pending := false
		for _, control := range op.Quiescence.Controls {
			if control.Stop.State != "" || control.Start.State != "" {
				pending = true
			}
		}
		if pending && op.Input.OperationID != resumeOperation {
			return fmt.Errorf("backuplifecycle: original snapshot %s retains runtime control/recovery obligations; new mutation is denied", op.Input.OperationID)
		}
	}
	return nil
}
