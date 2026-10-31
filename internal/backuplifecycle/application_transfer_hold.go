package backuplifecycle

import (
	"bytes"
	"errors"
	wire "github.com/kombifyio/stackkits/contracts/advancedcapability"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"strings"
)

var ErrApplicationTransferHoldNotRecorded = errors.New("authenticated snapshot completed before transfer hold was recorded")

var ErrApplicationTransferHeld = errors.New("selected application transfer is held awaiting authenticated destination outcome")

// OpenTransferJournal opens only the existing backup operation records. Runtime
// operations remain unavailable until the ordinary Creator.Create supplies one.
func (Creator) OpenTransferJournal(workspace string) (*Service, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	absolute := root.Name()
	if err = root.Close(); err != nil {
		return nil, err
	}
	return &Service{workspaceRoot: absolute}, nil
}

// RecordApplicationTransferOutcome updates the existing signed snapshot journal;
// success never restarts source containers or removes their retained data.
// The caller holds the existing snapshot mutation lease and authenticates the
// destination outcome before passing its canonical digest/state here.
func (s *Service) RecordApplicationTransferOutcome(operation, transferID, outcomeDigest, state string) error {
	if !validOperationID(operation) || !validDigest(transferID) || !validDigest(outcomeDigest) || (state != "activated" && state != "stopped") {
		return errors.New("invalid application transfer outcome")
	}
	path := operationDirectory + "/" + operationKey(operation) + ".json"
	op, err := s.loadOperation(path)
	if err != nil {
		return err
	}
	if op.State != "pending" || op.Quiescence == nil || op.Quiescence.TransferHoldID != transferID {
		return errors.New("source transfer hold differs from authenticated outcome")
	}
	if op.Quiescence.TransferOutcomeDigest == outcomeDigest && op.Quiescence.TransferOutcomeState == state {
		return nil
	}
	if op.Quiescence.TransferOutcomeDigest != "" && op.Quiescence.TransferOutcomeDigest != outcomeDigest {
		return errors.New("transfer hold already has a different destination outcome")
	}
	if state == "stopped" && (op.Quiescence.TransferRetirementCandidateHash != "" || op.Quiescence.TransferRetiredSpecHash != "") {
		return errors.New("source retirement already planned; stopped-target outcome cannot resume retired intent")
	}
	op.Quiescence.TransferOutcomeDigest = outcomeDigest
	op.Quiescence.TransferOutcomeState = state
	return s.writeSnapshotOperation(path, op)
}

func ReadApplicationTransferHold(workspace, operation, transferID string) ([]string, error) {
	return readApplicationTransferHold(workspace, operation, transferID, false)
}

// ReadApplicationTransferRetirementHold alone admits terminal replay by finish.
// Export, resume and receipt minting still require an actively held source.
func ReadApplicationTransferRetirementHold(workspace, operation, transferID string) ([]string, error) {
	return readApplicationTransferHold(workspace, operation, transferID, true)
}

func readApplicationTransferHold(workspace, operation, transferID string, allowRetired bool) ([]string, error) {
	if !validOperationID(operation) || !validDigest(transferID) {
		return nil, errors.New("invalid transfer hold selection")
	}
	s := &Service{workspaceRoot: workspace}
	op, err := s.loadOperation(operationDirectory + "/" + operationKey(operation) + ".json")
	if err != nil {
		return nil, err
	}
	if op.State == "pending" && op.Quiescence != nil && op.Quiescence.Phase == "snapshot-created" && op.Quiescence.TransferHoldID == "" {
		return nil, ErrApplicationTransferHoldNotRecorded
	}
	terminal := allowRetired && op.State == "transferred" && op.Quiescence != nil && op.Quiescence.Phase == "transfer-retired" && validDigest(op.Quiescence.TransferRetiredSpecHash)
	if (!terminal && op.State != "pending") || op.Quiescence == nil || op.Quiescence.TransferHoldID != transferID || (!terminal && op.Quiescence.Phase != "snapshot-created") {
		return nil, errors.New("authenticated selected transfer hold is unavailable")
	}
	if len(op.Quiescence.TransferWorkloads) > 0 {
		return append([]string(nil), op.Quiescence.TransferWorkloads...), nil
	}
	return append([]string(nil), op.Input.Workloads...), nil
}

// CompleteApplicationTransferRetirement is terminal only after the canonical
// intent writer retired the selected workloads while retaining their data.
// Its distinct state is never a successful/restored full-node backup anchor.
func (s *Service) CompleteApplicationTransferRetirement(operation, transferID, retiredSpecHash string) error {
	if !validDigest(retiredSpecHash) {
		return errors.New("transfer retirement requires canonical intent CAS hash")
	}
	path := operationDirectory + "/" + operationKey(operation) + ".json"
	op, err := s.loadOperation(path)
	if err != nil {
		return err
	}
	if err = validateApplicationTransferRetirementAuthorization(op); err != nil {
		return err
	}
	if op.State == "transferred" && op.Quiescence != nil && op.Quiescence.Phase == "transfer-retired" && op.Quiescence.TransferHoldID == transferID && op.Quiescence.TransferRetiredSpecHash == retiredSpecHash {
		return nil
	}
	if op.State != "pending" || op.Quiescence == nil || op.Quiescence.TransferHoldID != transferID || op.Quiescence.TransferOutcomeState != "activated" || !validDigest(op.Quiescence.TransferOutcomeDigest) || op.Quiescence.TransferRetirementCandidateHash != retiredSpecHash {
		return errors.New("transfer source cannot retire without authenticated activated destination and exact planned intent CAS")
	}
	op.State = "transferred"
	op.Quiescence.Phase = "transfer-retired"
	op.Quiescence.TransferRetiredSpecHash = retiredSpecHash
	return s.writeSnapshotOperation(path, op)
}

func (s *Service) PrepareApplicationTransferRetirement(operation, transferID, candidateHash string, requireCapability ...bool) error {
	if len(requireCapability) > 1 {
		return errors.New("ambiguous transfer retirement authorization requirement")
	}
	if !validDigest(candidateHash) {
		return errors.New("invalid transfer retirement candidate")
	}
	path := operationDirectory + "/" + operationKey(operation) + ".json"
	op, err := s.loadOperation(path)
	if err != nil {
		return err
	}
	if op.State != "pending" || op.Quiescence == nil || op.Quiescence.TransferHoldID != transferID || (op.Quiescence.TransferOutcomeState != "activated" && !(strings.HasPrefix(operation, "transfer-abort.") && op.Quiescence.TransferOutcomeState == "stopped")) {
		return errors.New("retirement preparation requires activated destination or exact pre-import target abort")
	}
	if op.Quiescence.TransferRetirementCandidateHash != "" && op.Quiescence.TransferRetirementCandidateHash != candidateHash {
		return errors.New("transfer retirement candidate changed")
	}
	op.Quiescence.TransferRetirementCandidateHash = candidateHash
	if len(requireCapability) == 1 && requireCapability[0] {
		op.Quiescence.TransferRetirementRequiresCapability = true
	}
	return s.writeSnapshotOperation(path, op)
}

func ReadApplicationTransferRetirementCandidate(workspace, operation, transferID string) (string, error) {
	s := &Service{workspaceRoot: workspace}
	op, err := s.loadOperation(operationDirectory + "/" + operationKey(operation) + ".json")
	if err != nil {
		return "", err
	}
	if op.Quiescence == nil || op.Quiescence.TransferHoldID != transferID {
		return "", errors.New("transfer retirement hold unavailable")
	}
	return op.Quiescence.TransferRetirementCandidateHash, nil
}

// CompleteApplicationTransferAbortRetirement settles the existing target hold
// only after canonical selected intent retirement. No target runtime is resumed.
func (s *Service) CompleteApplicationTransferAbortRetirement(operation, transferID, retiredSpecHash string) error {
	if !strings.HasPrefix(operation, "transfer-abort.") || !validDigest(retiredSpecHash) {
		return errors.New("invalid target abort retirement")
	}
	path := operationDirectory + "/" + operationKey(operation) + ".json"
	op, err := s.loadOperation(path)
	if err != nil {
		return err
	}
	if err = validateApplicationTransferRetirementAuthorization(op); err != nil {
		return err
	}
	if op.State == "transferred" && op.Quiescence != nil && op.Quiescence.Phase == "transfer-aborted" && op.Quiescence.TransferHoldID == transferID && op.Quiescence.TransferRetiredSpecHash == retiredSpecHash {
		return nil
	}
	if op.State != "pending" || op.Quiescence == nil || op.Quiescence.TransferHoldID != transferID || op.Quiescence.TransferOutcomeState != "stopped" || !validDigest(op.Quiescence.TransferOutcomeDigest) || op.Quiescence.TransferRetirementCandidateHash != retiredSpecHash {
		return errors.New("target abort retirement lacks exact stopped hold and planned CAS")
	}
	op.State = "transferred"
	op.Quiescence.Phase = "transfer-aborted"
	op.Quiescence.TransferRetiredSpecHash = retiredSpecHash
	return s.writeSnapshotOperation(path, op)
}

// RecordApplicationTransferRetirementAuthorization stores the already verified
// secret-free capability in the same Owner-signed journal before intent CAS.
// The caller must hold the existing snapshot lease and verify issuer trust,
// signature, trusted time, Owner, stack and reconstructed scope first.
func (s *Service) RecordApplicationTransferRetirementAuthorization(operation, transferID string, raw []byte) error {
	record := operationDirectory + "/" + operationKey(operation) + ".json"
	op, err := s.loadOperation(record)
	if err != nil {
		return err
	}
	if op.State != "pending" || op.Quiescence == nil || op.Quiescence.TransferHoldID != transferID || !op.Quiescence.TransferRetirementRequiresCapability {
		return errors.New("transfer authorization requires its pending exact capability-gated retirement")
	}
	prior := op.Quiescence.TransferRetirementCapability
	op.Quiescence.TransferRetirementCapability = append([]byte(nil), raw...)
	if err = validateApplicationTransferRetirementAuthorization(op); err != nil {
		return err
	}
	if bytes.Equal(prior, raw) {
		return nil
	}
	return s.writeSnapshotOperation(record, op)
}

func validateApplicationTransferRetirementAuthorization(op snapshotOperation) error {
	if op.Quiescence == nil {
		return errors.New("transfer retirement hold is unavailable")
	}
	q := op.Quiescence
	if !q.TransferRetirementRequiresCapability {
		if len(q.TransferRetirementCapability) != 0 {
			return errors.New("unexpected transfer retirement capability")
		}
		return nil
	}
	capability, err := wire.DecodeCapability(q.TransferRetirementCapability)
	if err != nil {
		return errors.New("verified transfer retirement capability record is required")
	}
	scope := capability.TransferRetirement
	role := "source"
	state := "activated"
	if strings.HasPrefix(op.Input.OperationID, "transfer-abort.") {
		role = "destination"
		state = "stopped"
	}
	if scope == nil || scope.OperationID != op.Input.OperationID || scope.TransferID != q.TransferHoldID || scope.OutcomeDigest != q.TransferOutcomeDigest || q.TransferOutcomeState != state || scope.Role != role || scope.BaselineSpecHash != op.Input.Lineage.Binding.SpecHash || scope.CandidateSpecHash != q.TransferRetirementCandidateHash || capability.OwnerRef != op.Input.OwnerRef {
		return errors.New("recorded capability differs from its held transfer, peer outcome or exact canonical intent transition")
	}
	return nil
}

func validateApplicationTransferRetirementRecordShape(q SnapshotQuiescence) error {
	if len(q.TransferRetirementCapability) == 0 {
		if q.TransferRetirementRequiresCapability && (q.Phase == "transfer-retired" || q.Phase == "transfer-aborted") {
			return errors.New("terminal Advanced retirement lacks its verified capability record")
		}
		return nil
	}
	capability, err := wire.DecodeCapability(q.TransferRetirementCapability)
	if err != nil {
		return err
	}
	scope := capability.TransferRetirement
	if !q.TransferRetirementRequiresCapability || scope == nil || scope.TransferID != q.TransferHoldID || scope.OutcomeDigest != q.TransferOutcomeDigest || scope.CandidateSpecHash != q.TransferRetirementCandidateHash {
		return errors.New("transfer retirement capability record differs from signed snapshot control")
	}
	return nil
}
