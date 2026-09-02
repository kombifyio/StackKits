package localbackupschedule

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/clibinding"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const AuthorizationPath = ".stackkit/backups/schedule/authorization.json"
const AuthorizationArchiveDirectory = ".stackkit/backups/schedule/authorization-archive"
const authorizationAPI = "stackkit.local-backup-schedule-authorization/v1"

// AuthorizationBinding is a projection of already verified local Apply and
// backup authority. A trigger cannot supply or widen any of these fields.
type AuthorizationBinding struct {
	OwnerRef      string                           `json:"ownerRef"`
	AuthorityRef  string                           `json:"authorityRef"`
	Lineage       backuplifecycle.AuthorityLineage `json:"lineage"`
	PolicyDigest  string                           `json:"policyDigest"`
	WorkspaceRoot string                           `json:"workspaceRoot"`
	SpecPath      string                           `json:"specPath"`
	ProcessUID    string                           `json:"processUid"`
	CLI           clibinding.Identity              `json:"cli"`
	Schedule      localbackuppolicy.Schedule       `json:"schedule"`
	UnitName      string                           `json:"unitName"`
	UnitDigest    string                           `json:"unitDigest"`
}

// ScheduledExecution records an attempt through the ordinary backup lifecycle.
// A pending attempt is resumed with the same operation ID, even after its UTC
// slot has passed. Only a verified SnapshotAnchor can complete it.
type ScheduledExecution struct {
	Slot             time.Time `json:"slot"`
	OperationID      string    `json:"operationId"`
	State            string    `json:"state"`
	SnapshotAnchorID string    `json:"snapshotAnchorId,omitempty"`
	// ApprovedAt retains the original slot grant when a later explicit
	// CLI/unit reapproval resumes this same runtime operation.
	ApprovedAt time.Time `json:"approvedAt,omitzero"`
}

type Authorization struct {
	APIVersion string                                  `json:"apiVersion"`
	Binding    AuthorizationBinding                    `json:"binding"`
	State      string                                  `json:"state"`
	ApprovedAt time.Time                               `json:"approvedAt"`
	ChangedAt  time.Time                               `json:"changedAt"`
	Execution  *ScheduledExecution                     `json:"execution,omitempty"`
	Signature  localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// PrepareAuthorization persists approval before a trigger is installed. A
// prepared authorization cannot execute; activation follows trigger readback.
// The caller holds the shared lifecycle mutation lock throughout installation.
func PrepareAuthorization(workspace string, binding AuthorizationBinding, ownerApproved bool) (Authorization, error) {
	if !ownerApproved {
		return Authorization{}, errors.New("backup schedule requires explicit local Owner approval")
	}
	if err := validateAuthorizationBinding(workspace, binding); err != nil {
		return Authorization{}, err
	}
	existing, err := LoadAuthorization(workspace)
	if errors.Is(err, os.ErrNotExist) {
		now := time.Now().UTC()
		record := Authorization{APIVersion: authorizationAPI, Binding: binding, State: "prepared", ApprovedAt: now, ChangedAt: now}
		return persistAuthorization(workspace, record)
	}
	if err != nil {
		return Authorization{}, err
	}

	sameRuntime := sameRuntimeAuthority(existing.Binding, binding)
	preserveExecution, err := scheduledExecutionForReapproval(workspace, existing, sameRuntime)
	if err != nil {
		return Authorization{}, err
	}
	now := time.Now().UTC()
	record := existing
	record.Binding = binding
	record.State = "prepared"
	record.ChangedAt = now
	record.ApprovedAt = now
	if preserveExecution {
		execution := *existing.Execution
		if execution.ApprovedAt.IsZero() {
			execution.ApprovedAt = existing.ApprovedAt
		}
		record.Execution = &execution
	} else {
		record.Execution = nil
	}
	if err := archiveAuthorization(workspace, existing); err != nil {
		return Authorization{}, err
	}
	return persistAuthorization(workspace, record)
}

func sameRuntimeAuthority(left, right AuthorizationBinding) bool {
	return left.OwnerRef == right.OwnerRef &&
		left.AuthorityRef == right.AuthorityRef &&
		left.PolicyDigest == right.PolicyDigest &&
		reflect.DeepEqual(left.Lineage, right.Lineage)
}

func scheduledExecutionForReapproval(workspace string, existing Authorization, sameRuntime bool) (bool, error) {
	if existing.Execution == nil {
		return false, nil
	}
	inspection, err := backuplifecycle.InspectSnapshotOperation(workspace, existing.Execution.OperationID)
	if err != nil {
		return false, fmt.Errorf("backup schedule reapproval is recovery-required for scheduled operation %q: %w", existing.Execution.OperationID, err)
	}
	if inspection.Found && !snapshotOperationMatchesBinding(inspection, existing.Binding) {
		return false, fmt.Errorf("backup schedule reapproval is recovery-required for scheduled operation %q: snapshot authority differs from its schedule binding", existing.Execution.OperationID)
	}
	if existing.Execution.State == "pending" {
		if sameRuntime {
			return true, nil
		}
		if inspection.SafeForAuthorityReplacement() {
			return false, nil
		}
		return false, fmt.Errorf("backup schedule reapproval is recovery-required for scheduled operation %q: snapshot quiescence is unresolved", existing.Execution.OperationID)
	}
	if existing.Execution.State != "completed" {
		return false, fmt.Errorf("backup schedule reapproval is recovery-required for scheduled operation %q: execution state is malformed", existing.Execution.OperationID)
	}
	if !inspection.Found {
		// A missing journal cannot support a historical success claim. The old
		// authorization is archived, and the new approval starts a fresh slot.
		return false, nil
	}
	if !inspection.SnapshotAnchorVerified {
		if !sameRuntime && inspection.SafeForAuthorityReplacement() {
			return false, nil
		}
		return false, fmt.Errorf("backup schedule reapproval is recovery-required for scheduled operation %q: completed snapshot anchor is not verified", existing.Execution.OperationID)
	}
	if existing.Execution.SnapshotAnchorID != inspection.SnapshotAnchorID {
		return false, fmt.Errorf("backup schedule completed anchor differs from operation %q", existing.Execution.OperationID)
	}
	if sameRuntime {
		return true, nil
	}
	if inspection.SafeForAuthorityReplacement() {
		return false, nil
	}
	return false, fmt.Errorf("backup schedule reapproval is recovery-required for scheduled operation %q: snapshot quiescence is unresolved", existing.Execution.OperationID)
}

func snapshotOperationMatchesBinding(inspection backuplifecycle.SnapshotOperationInspection, binding AuthorizationBinding) bool {
	return inspection.OwnerRef == binding.OwnerRef &&
		inspection.AuthorityRef == binding.AuthorityRef &&
		inspection.PolicyArtifactDigest == binding.PolicyDigest &&
		reflect.DeepEqual(inspection.Lineage, binding.Lineage)
}

func ActivateAuthorization(workspace string, binding AuthorizationBinding) (Authorization, error) {
	record, err := LoadAuthorization(workspace)
	if err != nil {
		return Authorization{}, err
	}
	if !reflect.DeepEqual(record.Binding, binding) || record.State == "disabled" {
		return Authorization{}, errors.New("backup schedule activation differs from the approved binding")
	}
	record.State, record.ChangedAt = "enabled", time.Now().UTC()
	return persistAuthorization(workspace, record)
}

// DisableAuthorization revokes local dispatch before any timer is disabled.
// It needs current Owner custody, but never needs the old runtime to be alive.
func DisableAuthorization(workspace string, ownerApproved bool) (Authorization, error) {
	if !ownerApproved {
		return Authorization{}, errors.New("backup schedule disable requires explicit local Owner approval")
	}
	record, err := LoadAuthorization(workspace)
	if err != nil {
		return Authorization{}, err
	}
	record.State, record.ChangedAt = "disabled", time.Now().UTC()
	return persistAuthorization(workspace, record)
}

// RequireAuthorization fails closed when a schedule is stale, disabled or the
// CLI bytes changed. The binding supplied here must come from current local
// authority, never directly from the persisted record.
func RequireAuthorization(workspace string, current AuthorizationBinding) (Authorization, error) {
	record, err := LoadAuthorization(workspace)
	if err != nil {
		return Authorization{}, err
	}
	now := time.Now().UTC()
	if record.State != "enabled" || !reflect.DeepEqual(record.Binding, current) || now.Before(record.ApprovedAt) || now.Before(record.ChangedAt) {
		return Authorization{}, errors.New("backup schedule is disabled or differs from current local authority; reapprove its binding")
	}
	if err := clibinding.VerifyIdentity(current.CLI); err != nil {
		return Authorization{}, err
	}
	return record, nil
}

// BeginScheduledAttempt durably names the governed UTC slot before any snapshot.
// Callers cannot choose a slot or operation ID. A pending attempt is resumed
// even when a later timer fires; noOp means no new snapshot is due.
func BeginScheduledAttempt(workspace string, current AuthorizationBinding) (record Authorization, noOp bool, returnErr error) {
	record, err := RequireAuthorization(workspace, current)
	if err != nil {
		return Authorization{}, false, err
	}
	if record.Execution != nil && record.Execution.State == "pending" {
		return record, false, nil
	}
	slot, err := LatestSlot(current.Schedule, time.Now().UTC())
	if err != nil {
		return Authorization{}, false, err
	}
	if slot.Before(record.ApprovedAt) || (record.Execution != nil && !slot.After(record.Execution.Slot)) {
		return record, true, nil
	}
	digest := sha256.Sum256([]byte(current.PolicyDigest + "\x00" + current.Lineage.ApplyResultHash + "\x00" + record.ApprovedAt.Format(time.RFC3339Nano)))
	operationID := "scheduled-" + hex.EncodeToString(digest[:8]) + "-" + slot.Format("20060102T150405Z")
	record.Execution = &ScheduledExecution{Slot: slot.UTC(), OperationID: operationID, State: "pending", ApprovedAt: record.ApprovedAt}
	record.ChangedAt = time.Now().UTC()
	record, err = persistAuthorization(workspace, record)
	return record, false, err
}

func CompleteScheduledAttempt(workspace string, current AuthorizationBinding, anchor backuplifecycle.SnapshotAnchor) (Authorization, error) {
	record, err := RequireAuthorization(workspace, current)
	if err != nil {
		return Authorization{}, err
	}
	if record.Execution == nil || record.Execution.State != "pending" ||
		record.Execution.OperationID != anchor.OperationID || anchor.OwnerRef != current.OwnerRef ||
		anchor.AuthorityRef != current.AuthorityRef || anchor.PolicyArtifactDigest != current.PolicyDigest || !reflect.DeepEqual(anchor.Lineage, current.Lineage) {
		return Authorization{}, errors.New("scheduled backup result differs from its pending authority")
	}
	if err := backuplifecycle.VerifySnapshotAnchor(workspace, anchor); err != nil {
		return Authorization{}, err
	}
	record.Execution.State, record.Execution.SnapshotAnchorID = "completed", anchor.ID
	record.ChangedAt = time.Now().UTC()
	return persistAuthorization(workspace, record)
}

func LoadAuthorization(workspace string) (Authorization, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return Authorization{}, err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return Authorization{}, err
	}
	file, err := view.Open(AuthorizationPath)
	if err != nil {
		return Authorization{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Authorization{}, err
	}
	if info.Size() > 256*1024 {
		return Authorization{}, errors.New("backup schedule authorization exceeds its bounded size")
	}
	absolute := filepath.Join(workspace, filepath.FromSlash(AuthorizationPath))
	if err := backupcustody.RequirePrivatePath(filepath.Dir(absolute), true); err != nil {
		return Authorization{}, err
	}
	if err := backupcustody.RequirePrivatePath(absolute, false); err != nil {
		return Authorization{}, err
	}
	var record Authorization
	decoder := json.NewDecoder(io.LimitReader(file, 256*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Authorization{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Authorization{}, errors.New("backup schedule authorization has trailing data")
	}
	if err := validateAuthorization(workspace, record); err != nil {
		return Authorization{}, err
	}
	payload, err := authorizationPayload(record)
	if err != nil {
		return Authorization{}, err
	}
	if err := localevidence.VerifyOwnerPolicyState(workspace, payload, record.Signature); err != nil {
		return Authorization{}, err
	}
	return record, nil
}

func validateAuthorizationBinding(workspace string, binding AuthorizationBinding) error {
	absolute, err := filepath.Abs(workspace)
	if err != nil || binding.WorkspaceRoot != absolute || !filepath.IsAbs(binding.SpecPath) ||
		binding.OwnerRef == "" || binding.AuthorityRef == "" || binding.PolicyDigest == "" || binding.ProcessUID == "" ||
		binding.Lineage.Binding.PlanHash == "" || binding.Lineage.ApplyReceiptHash == "" || binding.Lineage.OwnerBindingDigest == "" ||
		binding.UnitName == "" || binding.UnitDigest == "" {
		return errors.New("backup schedule authorization binding is incomplete")
	}
	if err := binding.Schedule.Validate(); err != nil {
		return err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return err
	}
	if owner.OwnerRef != binding.OwnerRef {
		return errors.New("backup schedule Owner differs from local custody")
	}
	return nil
}

func validateAuthorization(workspace string, record Authorization) error {
	if record.APIVersion != authorizationAPI || record.ApprovedAt.IsZero() || record.ChangedAt.Before(record.ApprovedAt) ||
		(record.State != "prepared" && record.State != "enabled" && record.State != "disabled") {
		return errors.New("backup schedule authorization is malformed")
	}
	if record.Execution != nil {
		execution := record.Execution
		approvedAt := execution.ApprovedAt
		if approvedAt.IsZero() {
			approvedAt = record.ApprovedAt
		}
		if execution.Slot.IsZero() || execution.Slot.Before(approvedAt) || approvedAt.After(record.ApprovedAt) || execution.Slot.After(record.ChangedAt) || execution.OperationID == "" ||
			(execution.State != "pending" && execution.State != "completed") ||
			(execution.State == "pending" && execution.SnapshotAnchorID != "") ||
			(execution.State == "completed" && execution.SnapshotAnchorID == "") {
			return errors.New("backup schedule execution evidence is malformed")
		}
	}
	return validateAuthorizationBinding(workspace, record.Binding)
}

func persistAuthorization(workspace string, record Authorization) (Authorization, error) {
	if err := validateAuthorization(workspace, record); err != nil {
		return Authorization{}, err
	}
	payload, err := authorizationPayload(record)
	if err != nil {
		return Authorization{}, err
	}
	record.Signature, err = localevidence.SignOwnerPolicyState(workspace, payload)
	if err != nil {
		return Authorization{}, err
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return Authorization{}, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return Authorization{}, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return Authorization{}, err
	}
	parent := filepath.ToSlash(filepath.Dir(AuthorizationPath))
	if err := transaction.MkdirAll(parent, 0o700); err != nil {
		transaction.Close()
		return Authorization{}, err
	}
	if err := transaction.Close(); err != nil {
		return Authorization{}, err
	}
	view, err := root.View(".")
	if err != nil {
		return Authorization{}, err
	}
	result, err := view.WriteAtomic0600(AuthorizationPath, encoded)
	if err != nil {
		return Authorization{}, err
	}
	if !result.Installed || !result.FileSynced {
		return Authorization{}, fmt.Errorf("backup schedule authorization was not durably installed")
	}
	absolute := filepath.Join(workspace, filepath.FromSlash(AuthorizationPath))
	if err := backupcustody.ProtectPrivatePath(filepath.Dir(absolute), true); err != nil {
		return Authorization{}, err
	}
	if err := backupcustody.ProtectPrivatePath(absolute, false); err != nil {
		return Authorization{}, err
	}
	return record, nil
}

func archiveAuthorization(workspace string, record Authorization) error {
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode previous backup schedule authorization: %w", err)
	}
	digest := sha256.Sum256(encoded)
	relative := filepath.ToSlash(filepath.Join(AuthorizationArchiveDirectory, hex.EncodeToString(digest[:])+".json"))
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	if err := transaction.MkdirAll(AuthorizationArchiveDirectory, 0o700); err != nil {
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
	if existing, readErr := readAuthorizationArchive(view, relative); readErr == nil {
		if bytes.Equal(existing, encoded) {
			return nil
		}
		return errors.New("previous backup schedule authorization archive has a conflicting content address")
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	result, err := view.WriteAtomic0600NoReplace(relative, encoded)
	if err != nil {
		if existing, readErr := readAuthorizationArchive(view, relative); readErr == nil && bytes.Equal(existing, encoded) {
			return nil
		}
		return fmt.Errorf("archive previous backup schedule authorization: %w", err)
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("previous backup schedule authorization was not durably archived")
	}
	absolute := filepath.Join(root.Name(), filepath.FromSlash(relative))
	if err := backupcustody.ProtectPrivatePath(filepath.Dir(absolute), true); err != nil {
		return err
	}
	if err := backupcustody.ProtectPrivatePath(absolute, false); err != nil {
		return err
	}
	return nil
}

func readAuthorizationArchive(view confinedfs.View, relative string) ([]byte, error) {
	file, err := view.Open(relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 256*1024 {
		return nil, errors.New("backup schedule authorization archive exceeds its bounded size")
	}
	return io.ReadAll(io.LimitReader(file, 256*1024))
}

func authorizationPayload(record Authorization) ([]byte, error) {
	record.Signature = localevidence.OwnerPolicyStateSignature{}
	raw, err := json.Marshal(record)
	return bytes.TrimSpace(raw), err
}
