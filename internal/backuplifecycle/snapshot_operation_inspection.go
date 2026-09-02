package backuplifecycle

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// SnapshotOperationInspection is a read-only, owner-authenticated view of a
// snapshot operation journal. It deliberately contains no runtime handle and
// cannot resume, settle, or mutate the operation.
type SnapshotOperationInspection struct {
	Found                   bool
	OperationID             string
	State                   string
	OwnerRef                string
	AuthorityRef            string
	Lineage                 AuthorityLineage
	PolicyArtifactDigest    string
	QuiescencePhase         string
	QuiescenceAuthenticated bool
	SnapshotAnchorID        string
	SnapshotAnchorVerified  bool
}

// InspectSnapshotOperation reads and authenticates one operation journal. A
// missing journal is returned as Found=false; malformed, tampered, or
// internally inconsistent journals fail closed. Found=false only proves that
// no durable journal is currently present; it does not prove that no earlier
// runtime effect occurred. The inspection is safe to use while the repository
// runtime is unavailable.
func InspectSnapshotOperation(workspaceRoot, operationID string) (SnapshotOperationInspection, error) {
	if !validOperationID(operationID) {
		return SnapshotOperationInspection{}, errors.New("backuplifecycle: snapshot operation ID is invalid")
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return SnapshotOperationInspection{}, fmt.Errorf("backuplifecycle: open snapshot operation workspace: %w", err)
	}
	absolute := root.Name()
	if err := root.Close(); err != nil {
		return SnapshotOperationInspection{}, fmt.Errorf("backuplifecycle: close snapshot operation workspace: %w", err)
	}

	service := &Service{workspaceRoot: absolute}
	operationPath := operationDirectory + "/" + operationKey(operationID) + ".json"
	operation, err := service.loadOperation(operationPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SnapshotOperationInspection{OperationID: operationID}, nil
		}
		return SnapshotOperationInspection{}, fmt.Errorf("backuplifecycle: inspect snapshot operation: %w", err)
	}
	if operation.Input.OperationID != operationID ||
		operation.Input.OwnerRef == "" || operation.Input.AuthorityRef == "" ||
		!validDigest(operation.Input.PolicyArtifactDigest) {
		return SnapshotOperationInspection{}, errors.New("backuplifecycle: snapshot operation input is incomplete or mismatched")
	}
	if err := validateLineage(operation.Input.Lineage); err != nil {
		return SnapshotOperationInspection{}, err
	}
	if err := validatePolicy(operation.Input.Policy); err != nil {
		return SnapshotOperationInspection{}, err
	}

	inspection := SnapshotOperationInspection{
		Found:                true,
		OperationID:          operationID,
		State:                operation.State,
		OwnerRef:             operation.Input.OwnerRef,
		AuthorityRef:         operation.Input.AuthorityRef,
		Lineage:              operation.Input.Lineage,
		PolicyArtifactDigest: operation.Input.PolicyArtifactDigest,
	}
	if operation.Quiescence != nil {
		inspection.QuiescencePhase = operation.Quiescence.Phase
		inspection.QuiescenceAuthenticated = true
	}
	switch operation.State {
	case "pending":
		if operation.Anchor != nil {
			return SnapshotOperationInspection{}, errors.New("backuplifecycle: pending snapshot operation contains a completed anchor")
		}
	case "completed":
		if operation.Anchor == nil || operation.Anchor.OperationID != operationID {
			return SnapshotOperationInspection{}, errors.New("backuplifecycle: completed snapshot operation lacks its exact anchor")
		}
		if !anchorMatchesOperation(*operation.Anchor, operation.Input) {
			return SnapshotOperationInspection{}, errors.New("backuplifecycle: completed snapshot anchor differs from its exact operation input")
		}
		if err := VerifySnapshotAnchor(absolute, *operation.Anchor); err != nil {
			return SnapshotOperationInspection{}, err
		}
		stored, err := service.loadStoredSnapshotAnchor(operation.Anchor.ID)
		if err != nil {
			return SnapshotOperationInspection{}, err
		}
		if !reflect.DeepEqual(stored, *operation.Anchor) {
			return SnapshotOperationInspection{}, errors.New("backuplifecycle: completed snapshot operation differs from its content-addressed anchor")
		}
		inspection.SnapshotAnchorID = operation.Anchor.ID
		inspection.SnapshotAnchorVerified = true
	default:
		return SnapshotOperationInspection{}, errors.New("backuplifecycle: snapshot operation state is malformed")
	}
	return inspection, nil
}

// SafeForAuthorityReplacement reports whether no writer or unresolved
// snapshot mutation remains in the inspected journal. A missing journal is
// safe for replacement decisions because no durable journal is present; it is
// not proof that no transient runtime effect occurred before it disappeared.
func (inspection SnapshotOperationInspection) SafeForAuthorityReplacement() bool {
	if !inspection.Found {
		return true
	}
	if inspection.QuiescenceAuthenticated && inspection.QuiescencePhase == "restored" {
		return true
	}
	return inspection.State == "completed" && inspection.SnapshotAnchorVerified &&
		(!inspection.QuiescenceAuthenticated || inspection.QuiescencePhase == "restored")
}
