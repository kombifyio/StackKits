package fleetlifecycle

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	StateAPIVersion         = "stackkit.fleet-mutation-state/v1"
	PhaseEvidenceAPIVersion = "stackkit.fleet-mutation-evidence/v1"

	stateRoot    = ".stackkit/lifecycle/fleet"
	statePath    = stateRoot + "/state.json"
	evidenceRoot = stateRoot + "/evidence"

	StatusRunning          = "running"
	StatusRecoveryRequired = "recovery-required"
	StatusSucceeded        = "succeeded"
	StatusRecovered        = "recovered"
)

var (
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	operationIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{7,127}$`)
)

type ArtifactEvidence struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

type CheckpointAuthority struct {
	Required bool   `json:"required"`
	Ref      string `json:"ref,omitempty"`
	Digest   string `json:"digest,omitempty"`
}

type RecoveryAuthority struct {
	Required bool   `json:"required"`
	Ref      string `json:"ref"`
}

// PhaseEvidence is an immutable, Owner-signed result for one compiler-owned
// fleet phase. The full document is stored under its content digest.
type PhaseEvidence struct {
	APIVersion             string                                        `json:"apiVersion"`
	OperationID            string                                        `json:"operationId"`
	Sequence               uint64                                        `json:"sequence"`
	Phase                  string                                        `json:"phase"`
	Status                 string                                        `json:"status"`
	Authority              MutationAuthority                             `json:"authority"`
	Checkpoint             CheckpointAuthority                           `json:"checkpoint"`
	Recovery               RecoveryAuthority                             `json:"recovery"`
	Artifact               ArtifactEvidence                              `json:"artifact"`
	PreviousEvidenceDigest string                                        `json:"previousEvidenceDigest,omitempty"`
	RecordedAt             time.Time                                     `json:"recordedAt"`
	Signature              localevidence.OwnerLifecycleMutationSignature `json:"signature"`
}

type PhaseRecord struct {
	Sequence uint64 `json:"sequence"`
	Phase    string `json:"phase"`
	Status   string `json:"status"`
	Ref      string `json:"ref"`
	Digest   string `json:"digest"`
}

type MutationRecord struct {
	ID               string              `json:"id"`
	Authority        MutationAuthority   `json:"authority"`
	OwnerRef         string              `json:"ownerRef"`
	Status           string              `json:"status"`
	CurrentPhase     string              `json:"currentPhase"`
	Attempt          uint64              `json:"attempt"`
	Checkpoint       CheckpointAuthority `json:"checkpoint"`
	Recovery         RecoveryAuthority   `json:"recovery"`
	Phases           []PhaseRecord       `json:"phases"`
	RecoveryEvidence *PhaseRecord        `json:"recoveryEvidence,omitempty"`
	LastError        string              `json:"lastError,omitempty"`
	StartedAt        time.Time           `json:"startedAt"`
	UpdatedAt        time.Time           `json:"updatedAt"`
	CompletedAt      time.Time           `json:"completedAt,omitempty"`
	PreviousDigest   string              `json:"previousDigest,omitempty"`
	Digest           string              `json:"digest"`
}

// State is the one local fleet lifecycle projection consumed unchanged by the
// CLI, MCP status tool, and State Console.
type State struct {
	APIVersion       string                                        `json:"apiVersion"`
	Kind             string                                        `json:"kind"`
	WorkspaceHash    string                                        `json:"workspaceHash"`
	StackID          string                                        `json:"stackId"`
	FleetRef         string                                        `json:"fleetRef,omitempty"`
	OwnerRef         string                                        `json:"ownerRef,omitempty"`
	CurrentPlanHash  string                                        `json:"currentPlanHash,omitempty"`
	CurrentOperation string                                        `json:"currentOperation,omitempty"`
	Operations       []MutationRecord                              `json:"operations"`
	UpdatedAt        time.Time                                     `json:"updatedAt,omitempty"`
	StateDigest      string                                        `json:"stateDigest,omitempty"`
	Signature        localevidence.OwnerLifecycleMutationSignature `json:"signature,omitempty"`
}

type Preparation struct {
	Checkpoint  ArtifactEvidence
	RecoveryRef string
}

type Store struct {
	Workspace string
}

// NewOperationID returns a collision-resistant, readable local identity.
func NewOperationID(operation Operation) (string, error) {
	if _, ok := expectedOperationContracts()[operation]; !ok {
		return "", fmt.Errorf("unsupported fleet operation %q", operation)
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("create fleet mutation identity: %w", err)
	}
	return string(operation) + "-" + hex.EncodeToString(nonce), nil
}

// Load verifies the complete signed state and every referenced immutable phase
// document. A missing state is represented as an empty projection.
func (store Store) Load(contract Contract) (State, error) {
	if contract.APIVersion != APIVersion || contract.Kind != Kind ||
		strings.TrimSpace(contract.StackID) == "" {
		return State{}, errors.New("fleet lifecycle contract is incomplete")
	}
	root, err := confinedfs.Open(filepath.Clean(store.Workspace))
	if err != nil {
		return State{}, fmt.Errorf("open fleet lifecycle workspace: %w", err)
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return State{}, fmt.Errorf("begin fleet lifecycle read: %w", err)
	}
	defer transaction.Close()
	state, exists, err := loadState(store.Workspace, transaction)
	if err != nil {
		return State{}, err
	}
	if !exists {
		return newState(store.Workspace, contract), nil
	}
	if state.StackID != contract.StackID || state.FleetRef != contract.FleetRef {
		return State{}, errors.New("persisted fleet lifecycle state belongs to a different Stack or Fleet")
	}
	return state, nil
}

// LoadForPlan projects and verifies fleet state from the same canonical plan
// used by the runtime status surface.
func (store Store) LoadForPlan(plan resolvedplan.ResolvedPlan) (Contract, State, error) {
	contract, err := Project(plan)
	if err != nil {
		return Contract{}, State{}, err
	}
	state, err := store.Load(contract)
	if err != nil {
		return Contract{}, State{}, err
	}
	return contract, state, nil
}

// BeginPrepared holds the fleet state lock while the caller establishes the
// exact checkpoint and recovery authority, then persists the first signed
// record before any mutation phase can run.
func (store Store) BeginPrepared(
	mutation Mutation,
	operationID string,
	ownerApproved bool,
	now time.Time,
	prepare func() (Preparation, error),
) (State, error) {
	if err := validateMutation(mutation); err != nil {
		return State{}, err
	}
	if !operationIDPattern.MatchString(operationID) {
		return State{}, errors.New("fleet mutation operation ID is invalid")
	}
	if !ownerApproved {
		return State{}, errors.New("local Owner approval is required for fleet mutation")
	}
	if prepare == nil {
		return State{}, errors.New("fleet mutation preparation callback is required")
	}
	return store.mutate(mutation, func(
		state *State,
		transaction *confinedfs.Transaction,
		view confinedfs.View,
	) error {
		_ = transaction
		_ = view
		for _, record := range state.Operations {
			if record.ID == operationID {
				return errors.New("fleet mutation operation ID was already used")
			}
		}
		if current := currentMutation(*state); current != nil &&
			(current.Status == StatusRunning || current.Status == StatusRecoveryRequired) {
			return fmt.Errorf("fleet mutation %s requires completion or recovery", current.ID)
		}
		if state.CurrentPlanHash != "" &&
			state.CurrentPlanHash != mutation.Authority.CurrentPlanHash {
			return errors.New("fleet mutation current plan differs from durable fleet state")
		}
		owner, err := localevidence.LoadOwnerCustody(store.Workspace)
		if err != nil {
			return fmt.Errorf("load fleet mutation Owner custody: %w", err)
		}
		preparation, err := prepare()
		if err != nil {
			return err
		}
		checkpoint := CheckpointAuthority{
			Required: mutation.Contract.CheckpointRequired,
			Ref:      strings.TrimSpace(preparation.Checkpoint.Ref),
			Digest:   strings.TrimSpace(preparation.Checkpoint.Digest),
		}
		recovery := RecoveryAuthority{
			Required: mutation.Contract.RecoveryRequired,
			Ref:      strings.TrimSpace(preparation.RecoveryRef),
		}
		if err := validatePreparedAuthority(checkpoint, recovery); err != nil {
			return err
		}
		timestamp := normalizedMutationTime(now)
		previous := ""
		if len(state.Operations) > 0 {
			previous = state.Operations[len(state.Operations)-1].Digest
		}
		record := MutationRecord{
			ID: operationID, Authority: mutation.Authority,
			OwnerRef: owner.OwnerRef, Status: StatusRunning,
			CurrentPhase: mutation.Contract.Phases[0], Attempt: 1,
			Checkpoint: checkpoint, Recovery: recovery, Phases: []PhaseRecord{},
			StartedAt: timestamp, UpdatedAt: timestamp,
			PreviousDigest: previous,
		}
		record.Digest = mutationRecordDigest(record)
		state.OwnerRef = owner.OwnerRef
		state.CurrentPlanHash = mutation.Authority.CurrentPlanHash
		state.CurrentOperation = operationID
		state.Operations = append(state.Operations, record)
		state.UpdatedAt = timestamp
		return nil
	})
}

// RecordPhase commits one successful phase as immutable Owner evidence. The
// next phase is always selected from the compiler contract, never by a caller.
func (store Store) RecordPhase(
	mutation Mutation,
	operationID string,
	artifact ArtifactEvidence,
	now time.Time,
) (State, error) {
	if err := validateMutation(mutation); err != nil {
		return State{}, err
	}
	if err := validateArtifact(artifact); err != nil {
		return State{}, err
	}
	return store.mutate(mutation, func(
		state *State,
		transaction *confinedfs.Transaction,
		view confinedfs.View,
	) error {
		record, err := currentExactMutation(state, mutation, operationID)
		if err != nil {
			return err
		}
		if record.Status != StatusRunning {
			return errors.New("only a running fleet mutation may record phase evidence")
		}
		next := len(record.Phases)
		if next >= len(mutation.Contract.Phases) ||
			record.CurrentPhase != mutation.Contract.Phases[next] {
			return errors.New("fleet mutation phase differs from compiler contract")
		}
		phase := mutation.Contract.Phases[next]
		evidence, evidenceRef, evidenceDigest, err := persistPhaseEvidence(
			store.Workspace, transaction, view, *record, phase,
			"succeeded", artifact, normalizedMutationTime(now),
		)
		if err != nil {
			return err
		}
		record.Phases = append(record.Phases, PhaseRecord{
			Sequence: evidence.Sequence, Phase: phase, Status: evidence.Status,
			Ref: evidenceRef, Digest: evidenceDigest,
		})
		record.UpdatedAt = evidence.RecordedAt
		if len(record.Phases) == len(mutation.Contract.Phases) {
			record.Status = StatusSucceeded
			record.CompletedAt = evidence.RecordedAt
			state.CurrentPlanHash = mutation.Authority.TargetPlanHash
		} else {
			record.CurrentPhase = mutation.Contract.Phases[len(record.Phases)]
		}
		refreshMutationRecord(state, record)
		return nil
	})
}

func (store Store) RequireRecovery(
	mutation Mutation,
	operationID, diagnostic string,
	now time.Time,
) (State, error) {
	if strings.TrimSpace(diagnostic) == "" {
		return State{}, errors.New("fleet mutation recovery requires a diagnostic")
	}
	return store.mutate(mutation, func(
		state *State,
		_ *confinedfs.Transaction,
		_ confinedfs.View,
	) error {
		record, err := currentExactMutation(state, mutation, operationID)
		if err != nil {
			return err
		}
		if record.Status != StatusRunning {
			return errors.New("only a running fleet mutation may require recovery")
		}
		record.Status = StatusRecoveryRequired
		record.LastError = strings.TrimSpace(diagnostic)
		record.UpdatedAt = normalizedMutationTime(now)
		refreshMutationRecord(state, record)
		return nil
	})
}

func (store Store) Resume(
	mutation Mutation,
	operationID string,
	ownerApproved bool,
	now time.Time,
) (State, error) {
	if !ownerApproved {
		return State{}, errors.New("local Owner approval is required to resume fleet mutation")
	}
	return store.mutate(mutation, func(
		state *State,
		_ *confinedfs.Transaction,
		_ confinedfs.View,
	) error {
		record, err := currentExactMutation(state, mutation, operationID)
		if err != nil {
			return err
		}
		if record.Status != StatusRecoveryRequired {
			return errors.New("only a recovery-required fleet mutation may resume")
		}
		record.Status = StatusRunning
		record.Attempt++
		record.LastError = ""
		record.UpdatedAt = normalizedMutationTime(now)
		refreshMutationRecord(state, record)
		return nil
	})
}

func (store Store) MarkRecovered(
	mutation Mutation,
	operationID string,
	ownerApproved bool,
	artifact ArtifactEvidence,
	now time.Time,
) (State, error) {
	if !ownerApproved {
		return State{}, errors.New("local Owner approval is required to recover fleet mutation")
	}
	if err := validateArtifact(artifact); err != nil {
		return State{}, err
	}
	return store.mutate(mutation, func(
		state *State,
		transaction *confinedfs.Transaction,
		view confinedfs.View,
	) error {
		record, err := currentExactMutation(state, mutation, operationID)
		if err != nil {
			return err
		}
		if record.Status != StatusRecoveryRequired || record.RecoveryEvidence != nil {
			return errors.New("only one recovery-required fleet mutation may be recovered")
		}
		evidence, evidenceRef, evidenceDigest, err := persistPhaseEvidence(
			store.Workspace, transaction, view, *record, "recovery",
			"recovered", artifact, normalizedMutationTime(now),
		)
		if err != nil {
			return err
		}
		record.RecoveryEvidence = &PhaseRecord{
			Sequence: evidence.Sequence, Phase: evidence.Phase,
			Status: evidence.Status, Ref: evidenceRef, Digest: evidenceDigest,
		}
		record.Status = StatusRecovered
		record.CurrentPhase = "recovery"
		record.CompletedAt = evidence.RecordedAt
		record.UpdatedAt = evidence.RecordedAt
		state.CurrentPlanHash = mutation.Authority.CurrentPlanHash
		refreshMutationRecord(state, record)
		return nil
	})
}

func (store Store) mutate(
	mutation Mutation,
	change func(*State, *confinedfs.Transaction, confinedfs.View) error,
) (State, error) {
	if err := validateMutation(mutation); err != nil {
		return State{}, err
	}
	root, err := confinedfs.Open(filepath.Clean(store.Workspace))
	if err != nil {
		return State{}, fmt.Errorf("open fleet lifecycle workspace: %w", err)
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return State{}, fmt.Errorf("begin fleet lifecycle transaction: %w", err)
	}
	defer transaction.Close()
	if err := transaction.MkdirAll(stateRoot, 0o700); err != nil {
		return State{}, fmt.Errorf("create fleet lifecycle state root: %w", err)
	}
	if err := transaction.MkdirAll(evidenceRoot, 0o700); err != nil {
		return State{}, fmt.Errorf("create fleet lifecycle evidence root: %w", err)
	}
	for _, relative := range []string{stateRoot, evidenceRoot} {
		if err := backupcustody.ProtectPrivatePath(
			filepath.Join(store.Workspace, filepath.FromSlash(relative)), true,
		); err != nil {
			return State{}, err
		}
	}
	lock, err := transaction.TryAcquireOutputLock(stateRoot)
	if err != nil {
		return State{}, fmt.Errorf("acquire fleet lifecycle state lock: %w", err)
	}
	defer lock.Release()
	state, exists, err := loadState(store.Workspace, transaction)
	if err != nil {
		return State{}, err
	}
	contract := Contract{
		APIVersion: APIVersion, Kind: Kind,
		StackID: mutation.Authority.StackID, FleetRef: mutation.Authority.FleetRef,
	}
	if !exists {
		state = newState(store.Workspace, contract)
	} else if state.StackID != contract.StackID || state.FleetRef != contract.FleetRef {
		return State{}, errors.New("persisted fleet lifecycle state belongs to a different Stack or Fleet")
	}
	view, err := root.View(".")
	if err != nil {
		return State{}, err
	}
	if err := change(&state, transaction, view); err != nil {
		return State{}, err
	}
	if err := persistState(store.Workspace, transaction, view, &state); err != nil {
		return State{}, err
	}
	return state, nil
}

func newState(workspace string, contract Contract) State {
	return State{
		APIVersion: StateAPIVersion, Kind: "FleetMutationState",
		WorkspaceHash: workspaceDigest(workspace),
		StackID:       contract.StackID, FleetRef: contract.FleetRef,
		Operations: []MutationRecord{},
	}
}

func persistState(
	workspace string,
	transaction *confinedfs.Transaction,
	view confinedfs.View,
	state *State,
) error {
	if state == nil {
		return errors.New("fleet lifecycle state is required")
	}
	state.StateDigest = stateDigest(*state)
	state.Signature = localevidence.OwnerLifecycleMutationSignature{}
	signing, err := stateSigningBytes(*state)
	if err != nil {
		return err
	}
	state.Signature, err = localevidence.SignOwnerLifecycleMutation(workspace, signing)
	if err != nil {
		return err
	}
	raw, err := resolvedplan.CanonicalJSON(*state)
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(statePath, raw)
	if err != nil || !result.Installed || !result.FileSynced {
		if err == nil {
			err = errors.New("atomic fleet lifecycle state write was not installed and synced")
		}
		return fmt.Errorf("persist fleet lifecycle state: %w", err)
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(statePath)), false,
	); err != nil {
		return err
	}
	loaded, exists, err := loadState(workspace, transaction)
	if err != nil || !exists || loaded.StateDigest != state.StateDigest {
		if err != nil {
			return err
		}
		return errors.New("re-read fleet lifecycle state differs after commit")
	}
	return nil
}

func loadState(
	workspace string,
	transaction *confinedfs.Transaction,
) (State, bool, error) {
	exists, info, err := transaction.Exists(statePath)
	if err != nil || !exists {
		return State{}, false, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 8<<20 {
		return State{}, false, errors.New("fleet lifecycle state file is invalid")
	}
	if err := backupcustody.RequirePrivatePath(
		filepath.Join(workspace, filepath.FromSlash(statePath)), false,
	); err != nil {
		return State{}, false, fmt.Errorf("verify private fleet lifecycle state: %w", err)
	}
	raw, _, err := transaction.ReadStable(statePath)
	if err != nil {
		return State{}, false, err
	}
	var state State
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, false, fmt.Errorf("decode fleet lifecycle state: %w", err)
	}
	if err := validateState(workspace, transaction, state); err != nil {
		return State{}, false, err
	}
	canonical, err := resolvedplan.CanonicalJSON(state)
	if err != nil || !bytes.Equal(raw, canonical) {
		return State{}, false, errors.New("fleet lifecycle state is not canonical")
	}
	return state, true, nil
}

func validateState(
	workspace string,
	transaction *confinedfs.Transaction,
	state State,
) error {
	if state.APIVersion != StateAPIVersion || state.Kind != "FleetMutationState" ||
		state.WorkspaceHash != workspaceDigest(workspace) ||
		strings.TrimSpace(state.StackID) == "" || len(state.Operations) > 256 {
		return errors.New("fleet lifecycle state is incomplete")
	}
	if len(state.Operations) == 0 {
		return errors.New("persisted fleet lifecycle state contains no operations")
	}
	if state.OwnerRef == "" || state.Signature.OwnerRef != state.OwnerRef ||
		state.StateDigest != stateDigest(state) {
		return errors.New("fleet lifecycle state digest or Owner identity is invalid")
	}
	signing, err := stateSigningBytes(state)
	if err != nil {
		return err
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(
		workspace, signing, state.Signature,
	); err != nil {
		return fmt.Errorf("verify fleet lifecycle state Owner signature: %w", err)
	}
	previous := ""
	for index := range state.Operations {
		record := state.Operations[index]
		if err := validateMutationRecord(record, state); err != nil {
			return fmt.Errorf("fleet mutation record %d: %w", index, err)
		}
		if record.PreviousDigest != previous ||
			record.Digest != mutationRecordDigest(record) {
			return fmt.Errorf("fleet mutation record %d digest chain is invalid", index)
		}
		if err := validateOperationEvidence(workspace, transaction, record); err != nil {
			return fmt.Errorf("fleet mutation record %d evidence: %w", index, err)
		}
		previous = record.Digest
	}
	current := state.Operations[len(state.Operations)-1]
	if state.CurrentOperation != current.ID {
		return errors.New("fleet lifecycle current operation differs from history")
	}
	switch current.Status {
	case StatusSucceeded:
		if state.CurrentPlanHash != current.Authority.TargetPlanHash {
			return errors.New("successful fleet lifecycle state does not advance to target plan")
		}
	case StatusRecovered, StatusRunning, StatusRecoveryRequired:
		if state.CurrentPlanHash != current.Authority.CurrentPlanHash {
			return errors.New("incomplete fleet lifecycle state lost current plan authority")
		}
	}
	return nil
}

func validateMutationRecord(record MutationRecord, state State) error {
	if !operationIDPattern.MatchString(record.ID) ||
		record.Authority.StackID != state.StackID ||
		record.Authority.FleetRef != state.FleetRef ||
		record.OwnerRef != state.OwnerRef ||
		record.Attempt == 0 || record.StartedAt.IsZero() ||
		record.UpdatedAt.IsZero() || record.CurrentPhase == "" ||
		!validMutationStatus(record.Status) ||
		!digestPattern.MatchString(record.Authority.CurrentPlanHash) ||
		!digestPattern.MatchString(record.Authority.TargetPlanHash) ||
		!digestPattern.MatchString(record.Authority.ContractDigest) {
		return errors.New("record is incomplete")
	}
	if err := validatePreparedAuthority(record.Checkpoint, record.Recovery); err != nil {
		return err
	}
	for index, phase := range record.Phases {
		if phase.Sequence != uint64(index+1) || phase.Phase == "" ||
			phase.Status != "succeeded" || !digestPattern.MatchString(phase.Digest) ||
			strings.TrimSpace(phase.Ref) == "" {
			return errors.New("phase history is invalid")
		}
	}
	if record.RecoveryEvidence != nil &&
		(record.RecoveryEvidence.Sequence != uint64(len(record.Phases)+1) ||
			record.RecoveryEvidence.Phase != "recovery" ||
			record.RecoveryEvidence.Status != "recovered" ||
			!digestPattern.MatchString(record.RecoveryEvidence.Digest)) {
		return errors.New("recovery evidence is invalid")
	}
	switch record.Status {
	case StatusRunning:
		if record.LastError != "" || !record.CompletedAt.IsZero() {
			return errors.New("running record has terminal fields")
		}
	case StatusRecoveryRequired:
		if strings.TrimSpace(record.LastError) == "" || !record.CompletedAt.IsZero() {
			return errors.New("recovery-required record lacks a diagnostic")
		}
	case StatusSucceeded:
		if record.CompletedAt.IsZero() || record.LastError != "" ||
			record.RecoveryEvidence != nil {
			return errors.New("successful record has invalid terminal fields")
		}
	case StatusRecovered:
		if record.CompletedAt.IsZero() || record.RecoveryEvidence == nil ||
			strings.TrimSpace(record.LastError) == "" {
			return errors.New("recovered record lacks recovery closure")
		}
	}
	return nil
}

func validateOperationEvidence(
	workspace string,
	transaction *confinedfs.Transaction,
	record MutationRecord,
) error {
	previous := ""
	records := append([]PhaseRecord(nil), record.Phases...)
	if record.RecoveryEvidence != nil {
		records = append(records, *record.RecoveryEvidence)
	}
	for _, pointer := range records {
		exists, info, err := transaction.Exists(pointer.Ref)
		if err != nil || !exists {
			if err != nil {
				return err
			}
			return errors.New("immutable phase evidence is missing")
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
			return errors.New("immutable phase evidence file is invalid")
		}
		if err := backupcustody.RequirePrivatePath(
			filepath.Join(workspace, filepath.FromSlash(pointer.Ref)), false,
		); err != nil {
			return err
		}
		raw, _, err := transaction.ReadStable(pointer.Ref)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		if digest != pointer.Digest {
			return errors.New("immutable phase evidence digest differs from state")
		}
		var evidence PhaseEvidence
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&evidence); err != nil {
			return err
		}
		if evidence.APIVersion != PhaseEvidenceAPIVersion ||
			evidence.OperationID != record.ID ||
			evidence.Sequence != pointer.Sequence ||
			evidence.Phase != pointer.Phase ||
			evidence.Status != pointer.Status ||
			!reflectMutationAuthority(evidence.Authority, record.Authority) ||
			evidence.Checkpoint != record.Checkpoint ||
			evidence.Recovery != record.Recovery ||
			evidence.PreviousEvidenceDigest != previous ||
			evidence.Signature.OwnerRef != record.OwnerRef {
			return errors.New("immutable phase evidence differs from operation authority")
		}
		signing, err := phaseSigningBytes(evidence)
		if err != nil {
			return err
		}
		if err := localevidence.VerifyOwnerLifecycleMutation(
			workspace, signing, evidence.Signature,
		); err != nil {
			return err
		}
		canonical, err := resolvedplan.CanonicalJSON(evidence)
		if err != nil || !bytes.Equal(raw, canonical) {
			return errors.New("immutable phase evidence is not canonical")
		}
		previous = digest
	}
	return nil
}

func persistPhaseEvidence(
	workspace string,
	transaction *confinedfs.Transaction,
	view confinedfs.View,
	record MutationRecord,
	phase, status string,
	artifact ArtifactEvidence,
	recordedAt time.Time,
) (PhaseEvidence, string, string, error) {
	operationRoot := evidenceRoot + "/" + record.ID
	if err := transaction.MkdirAll(operationRoot, 0o700); err != nil {
		return PhaseEvidence{}, "", "", err
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(operationRoot)), true,
	); err != nil {
		return PhaseEvidence{}, "", "", err
	}
	sequence := uint64(len(record.Phases) + 1)
	previous := ""
	if len(record.Phases) > 0 {
		previous = record.Phases[len(record.Phases)-1].Digest
	}
	evidence := PhaseEvidence{
		APIVersion:  PhaseEvidenceAPIVersion,
		OperationID: record.ID, Sequence: sequence, Phase: phase, Status: status,
		Authority: record.Authority, Checkpoint: record.Checkpoint,
		Recovery: record.Recovery, Artifact: artifact,
		PreviousEvidenceDigest: previous, RecordedAt: recordedAt,
	}
	signing, err := phaseSigningBytes(evidence)
	if err != nil {
		return PhaseEvidence{}, "", "", err
	}
	evidence.Signature, err = localevidence.SignOwnerLifecycleMutation(workspace, signing)
	if err != nil {
		return PhaseEvidence{}, "", "", err
	}
	raw, err := resolvedplan.CanonicalJSON(evidence)
	if err != nil {
		return PhaseEvidence{}, "", "", err
	}
	sum := sha256.Sum256(raw)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	ref := fmt.Sprintf(
		"%s/%020d-%s.json",
		operationRoot, sequence, strings.TrimPrefix(digest, "sha256:"),
	)
	result, err := view.WriteAtomic0600NoReplace(ref, raw)
	if err != nil || !result.Installed || !result.FileSynced {
		if err == nil {
			err = errors.New("immutable phase evidence was not installed and synced")
		}
		return PhaseEvidence{}, "", "", fmt.Errorf("persist fleet phase evidence: %w", err)
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(ref)), false,
	); err != nil {
		return PhaseEvidence{}, "", "", err
	}
	return evidence, ref, digest, nil
}

func validateMutation(mutation Mutation) error {
	if mutation.Contract.Name != mutation.Authority.Operation ||
		!mutation.Contract.Mutation || !mutation.Contract.TargetPlanRequired ||
		!mutation.Contract.OwnerApproval.Required ||
		!mutation.Contract.RecoveryRequired ||
		mutation.Contract.Evidence.Schema != PhaseEvidenceAPIVersion ||
		!mutation.Contract.Evidence.Immutable ||
		!mutation.Contract.Evidence.LocalCustody ||
		len(mutation.Contract.Phases) < 3 ||
		strings.TrimSpace(mutation.Authority.StackID) == "" ||
		!digestPattern.MatchString(mutation.Authority.CurrentPlanHash) ||
		!digestPattern.MatchString(mutation.Authority.TargetPlanHash) ||
		!digestPattern.MatchString(mutation.Authority.CurrentSpecHash) ||
		!digestPattern.MatchString(mutation.Authority.TargetSpecHash) ||
		!digestPattern.MatchString(mutation.Authority.CurrentInventoryHash) ||
		!digestPattern.MatchString(mutation.Authority.TargetInventoryHash) {
		return errors.New("fleet mutation authority is incomplete")
	}
	digest, err := canonicalContractDigest(mutation.Contract)
	if err != nil || digest != mutation.Authority.ContractDigest {
		return errors.New("fleet mutation contract digest is invalid")
	}
	return nil
}

func validatePreparedAuthority(
	checkpoint CheckpointAuthority,
	recovery RecoveryAuthority,
) error {
	if checkpoint.Required {
		if err := validateArtifact(ArtifactEvidence{
			Ref: checkpoint.Ref, Digest: checkpoint.Digest,
		}); err != nil {
			return fmt.Errorf("fleet mutation checkpoint: %w", err)
		}
	} else if checkpoint.Ref != "" || checkpoint.Digest != "" {
		return errors.New("fleet mutation checkpoint exists when the contract forbids it")
	}
	if !recovery.Required || strings.TrimSpace(recovery.Ref) == "" ||
		len(recovery.Ref) > 1024 || strings.ContainsAny(recovery.Ref, "\x00\r\n") {
		return errors.New("fleet mutation recovery authority is invalid")
	}
	return nil
}

func validateArtifact(artifact ArtifactEvidence) error {
	artifact.Ref = strings.TrimSpace(artifact.Ref)
	artifact.Digest = strings.TrimSpace(artifact.Digest)
	if artifact.Ref == "" || len(artifact.Ref) > 1024 ||
		strings.ContainsAny(artifact.Ref, "\x00\r\n") ||
		!digestPattern.MatchString(artifact.Digest) {
		return errors.New("fleet mutation artifact evidence is invalid")
	}
	return nil
}

func currentExactMutation(
	state *State,
	mutation Mutation,
	operationID string,
) (*MutationRecord, error) {
	if state == nil || len(state.Operations) == 0 ||
		state.CurrentOperation != operationID {
		return nil, errors.New("exact current fleet mutation is required")
	}
	record := &state.Operations[len(state.Operations)-1]
	if record.ID != operationID ||
		!reflectMutationAuthority(record.Authority, mutation.Authority) {
		return nil, errors.New("fleet mutation differs from persisted authority")
	}
	return record, nil
}

func reflectMutationAuthority(left, right MutationAuthority) bool {
	return left == right
}

func refreshMutationRecord(state *State, record *MutationRecord) {
	index := len(state.Operations) - 1
	record.Digest = mutationRecordDigest(*record)
	state.Operations[index] = *record
	state.UpdatedAt = record.UpdatedAt
}

func currentMutation(state State) *MutationRecord {
	if len(state.Operations) == 0 {
		return nil
	}
	return &state.Operations[len(state.Operations)-1]
}

func mutationRecordDigest(record MutationRecord) string {
	record.Digest = ""
	raw, _ := resolvedplan.CanonicalJSON(record)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func stateDigest(state State) string {
	state.StateDigest = ""
	state.Signature = localevidence.OwnerLifecycleMutationSignature{}
	raw, _ := resolvedplan.CanonicalJSON(state)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func stateSigningBytes(state State) ([]byte, error) {
	state.Signature = localevidence.OwnerLifecycleMutationSignature{}
	return resolvedplan.CanonicalJSON(state)
}

func phaseSigningBytes(evidence PhaseEvidence) ([]byte, error) {
	evidence.Signature = localevidence.OwnerLifecycleMutationSignature{}
	return resolvedplan.CanonicalJSON(evidence)
}

func workspaceDigest(workspace string) string {
	absolute, _ := filepath.Abs(filepath.Clean(workspace))
	sum := sha256.Sum256([]byte(absolute))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizedMutationTime(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

func validMutationStatus(status string) bool {
	return status == StatusRunning || status == StatusRecoveryRequired ||
		status == StatusSucceeded || status == StatusRecovered
}
