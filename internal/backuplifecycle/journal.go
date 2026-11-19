package backuplifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

type operationInput struct {
	Workloads            []string         `json:"workloads,omitempty"`
	OwnerRef             string           `json:"ownerRef"`
	AuthorityRef         string           `json:"authorityRef"`
	Lineage              AuthorityLineage `json:"lineage"`
	PolicyArtifactDigest string           `json:"policyArtifactDigest"`
	OperationID          string           `json:"operationId"`
	Policy               Policy           `json:"policy"`
	ProtectRecovery      bool             `json:"protectRecovery,omitempty"`
}

type snapshotOperation struct {
	Failure          string                                         `json:"failure,omitempty"`
	AppGroupClosures []string                                       `json:"appGroupClosures,omitempty"`
	APIVersion       string                                         `json:"apiVersion"`
	State            string                                         `json:"state"`
	Input            operationInput                                 `json:"input"`
	Quiescence       *SnapshotQuiescence                            `json:"quiescence,omitempty"`
	Anchor           *SnapshotAnchor                                `json:"anchor,omitempty"`
	Signature        *localevidence.OwnerLifecycleMutationSignature `json:"signature,omitempty"`
}

func (s *Service) loadOperation(relative string) (snapshotOperation, error) {
	var operation snapshotOperation
	if err := s.readJSON(relative, &operation); err != nil {
		return snapshotOperation{}, err
	}
	if operation.APIVersion != snapshotOperationAPI {
		return snapshotOperation{}, errors.New("backuplifecycle: snapshot operation has an unsupported API version")
	}
	if operation.Quiescence != nil {
		if err := validateSnapshotQuiescence(*operation.Quiescence); err != nil {
			return snapshotOperation{}, err
		}
	}
	if operation.State == "failed" && (operation.Failure != "SNAPSHOT_FAILED_AFTER_SETTLEMENT" || operation.Anchor != nil || operation.Quiescence == nil || operation.Quiescence.ControlVersion != 2 || validateRestoredSnapshotQuiescence(*operation.Quiescence) != nil) {
		return snapshotOperation{}, errors.New("negative snapshot completion invalid")
	}
	if operation.State != "failed" && operation.Failure != "" {
		return snapshotOperation{}, errors.New("snapshot failure marker differs from phase")
	}
	if err := validateSnapshotGroupClosures(operation); err != nil {
		return snapshotOperation{}, err
	}
	if err := verifySnapshotOperation(s.workspaceRoot, operation); err != nil {
		return snapshotOperation{}, err
	}
	return operation, nil
}

func (s *Service) loadRestoreOperation(relative string) (restoreOperation, error) {
	var operation restoreOperation
	if err := s.readJSON(relative, &operation); err != nil {
		return restoreOperation{}, err
	}
	if operation.APIVersion != restoreOperationAPI {
		return restoreOperation{}, errors.New("backuplifecycle: restore operation has an unsupported API version")
	}
	return operation, nil
}

func (s *Service) readJSON(relative string, target any) error {
	root, err := confinedfs.Open(s.workspaceRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	file, err := view.Open(relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		return err
	}
	defer func() { _ = file.Close() }()
	absolute := filepath.Join(root.Name(), filepath.FromSlash(relative))
	if err := backupcustody.RequirePrivatePath(filepath.Dir(absolute), true); err != nil {
		return fmt.Errorf("backuplifecycle: private custody check for %s: %w", filepath.Dir(relative), err)
	}
	if err := backupcustody.RequirePrivatePath(absolute, false); err != nil {
		return fmt.Errorf("backuplifecycle: private custody check for %s: %w", relative, err)
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("backuplifecycle: decode %s: %w", relative, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return fmt.Errorf("backuplifecycle: decode %s: trailing content: %w", relative, err)
	}
	return nil
}

func (s *Service) writeJSON(relative string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("backuplifecycle: encode %s: %w", relative, err)
	}
	root, err := confinedfs.Open(s.workspaceRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	parent := filepath.ToSlash(filepath.Dir(relative))
	if err := transaction.MkdirAll(parent, 0o700); err != nil {
		_ = transaction.Close()
		return err
	}
	if err := transaction.Close(); err != nil {
		return err
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(relative, encoded)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("backuplifecycle: lifecycle record was not atomically installed and synced")
	}
	absoluteParent := filepath.Join(root.Name(), filepath.FromSlash(parent))
	if err := backupcustody.ProtectPrivatePath(absoluteParent, true); err != nil {
		return fmt.Errorf("backuplifecycle: protect %s: %w", parent, err)
	}
	if err := backupcustody.ProtectPrivatePath(filepath.Join(root.Name(), filepath.FromSlash(relative)), false); err != nil {
		return fmt.Errorf("backuplifecycle: protect %s: %w", relative, err)
	}
	return nil
}

func (s *Service) writeSnapshotOperation(relative string, operation snapshotOperation) error {
	signed, err := signSnapshotOperation(s.workspaceRoot, operation)
	if err != nil {
		return err
	}
	return s.writeJSON(relative, signed)
}

func signSnapshotOperation(workspaceRoot string, operation snapshotOperation) (snapshotOperation, error) {
	operation.Signature = nil
	canonical, err := canonicalSnapshotOperation(operation)
	if err != nil {
		return snapshotOperation{}, err
	}
	signature, err := localevidence.SignOwnerLifecycleMutation(workspaceRoot, canonical)
	if err != nil {
		return snapshotOperation{}, fmt.Errorf("backuplifecycle: sign snapshot operation: %w", err)
	}
	operation.Signature = &signature
	return operation, nil
}

func verifySnapshotOperation(workspaceRoot string, operation snapshotOperation) error {
	if operation.Signature == nil {
		if operation.Quiescence != nil {
			return errors.New("backuplifecycle: snapshot operation with quiescence is not owner-authenticated")
		}
		return nil
	}
	if operation.Signature.OwnerRef == "" || operation.Signature.OwnerRef != operation.Input.OwnerRef {
		return errors.New("backuplifecycle: snapshot operation Owner signature differs from its input owner")
	}
	canonical, err := canonicalSnapshotOperation(operation)
	if err != nil {
		return err
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(workspaceRoot, canonical, *operation.Signature); err != nil {
		return fmt.Errorf("backuplifecycle: verify snapshot operation Owner signature: %w", err)
	}
	return nil
}

func canonicalSnapshotOperation(operation snapshotOperation) ([]byte, error) {
	operation.Signature = nil
	encoded, err := resolvedplan.CanonicalJSON(operation)
	if err != nil {
		return nil, fmt.Errorf("backuplifecycle: encode canonical snapshot operation: %w", err)
	}
	return encoded, nil
}

// ValidateSnapshotOperationID preserves the public snapshot/recovery identity
// domain. Native group UUIDs and restore activation IDs have separate owners.
func ValidateSnapshotOperationID(value string) bool { return validOperationID(value) }
func validOperationID(value string) bool {
	if len(value) == 0 || len(value) > 128 || !isOperationAlphaNumeric(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isOperationAlphaNumeric(value[index]) &&
			value[index] != '.' && value[index] != '_' && value[index] != '-' {
			return false
		}
	}
	return true
}

func validPortableValue(value string) bool {
	return validOperationID(value)
}

func isOperationAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func operationInputsEqual(left, right operationInput) bool {
	return left.OwnerRef == right.OwnerRef &&
		left.AuthorityRef == right.AuthorityRef &&
		lineagesEqual(left.Lineage, right.Lineage) &&
		left.PolicyArtifactDigest == right.PolicyArtifactDigest &&
		left.OperationID == right.OperationID && left.ProtectRecovery == right.ProtectRecovery && reflect.DeepEqual(left.Workloads, right.Workloads) &&
		policiesEqual(left.Policy, right.Policy)
}

func cloneOperationInput(input operationInput) operationInput {
	input.Policy = clonePolicy(input.Policy)
	input.Workloads = append([]string(nil), input.Workloads...)
	return input
}

func operationKey(operationID string) string {
	sum := sha256.Sum256([]byte(operationID))
	return hex.EncodeToString(sum[:])
}
