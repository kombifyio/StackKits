package fleetlifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

type PhaseRequest struct {
	OperationID string
	Attempt     uint64
	Phase       string
	Authority   MutationAuthority
	Checkpoint  CheckpointAuthority
	Recovery    RecoveryAuthority
	Completed   []PhaseRecord
}

type RecoveryRequest struct {
	OperationID string
	Attempt     uint64
	FailedPhase string
	LastError   string
	Authority   MutationAuthority
	Checkpoint  CheckpointAuthority
	Recovery    RecoveryAuthority
	Completed   []PhaseRecord
}

// Executor owns the finite local phase implementations. It receives no
// provider allocation, multi-server orchestration, or credential-custody
// authority from the lifecycle service.
type Executor interface {
	Prepare(context.Context, Mutation) (Preparation, error)
	ExecutePhase(context.Context, PhaseRequest) (ArtifactEvidence, error)
	Recover(context.Context, RecoveryRequest) (ArtifactEvidence, error)
}

type ExecuteInput struct {
	OperationID     string
	Operation       Operation
	CurrentPlan     resolvedplan.ResolvedPlan
	TargetPlan      resolvedplan.ResolvedPlan
	SourceMemberRef string
	TargetMemberRef string
	OwnerApproved   bool
}

type Service struct {
	Store    Store
	Executor Executor
	Now      func() time.Time
}

// Execute starts and completes one mutation through the exact compiler phase
// sequence. On any post-Begin failure the durable state is moved to
// recovery-required before the error is returned.
func (service Service) Execute(
	ctx context.Context,
	input ExecuteInput,
) (State, error) {
	if service.Executor == nil {
		return State{}, errors.New("fleet lifecycle executor is required")
	}
	mutation, err := BindMutation(
		input.CurrentPlan, input.TargetPlan, input.Operation,
		input.SourceMemberRef, input.TargetMemberRef,
	)
	if err != nil {
		return State{}, err
	}
	state, err := service.Store.BeginPrepared(
		mutation, input.OperationID, input.OwnerApproved, service.now(),
		func() (Preparation, error) {
			return service.Executor.Prepare(ctx, mutation)
		},
	)
	if err != nil {
		return State{}, err
	}
	return service.run(ctx, mutation, input.OperationID, state)
}

// Resume re-enters only the exact recovery-required operation and continues
// at its first phase without immutable evidence.
func (service Service) Resume(
	ctx context.Context,
	input ExecuteInput,
) (State, error) {
	if service.Executor == nil {
		return State{}, errors.New("fleet lifecycle executor is required")
	}
	mutation, err := BindMutation(
		input.CurrentPlan, input.TargetPlan, input.Operation,
		input.SourceMemberRef, input.TargetMemberRef,
	)
	if err != nil {
		return State{}, err
	}
	state, err := service.Store.Resume(
		mutation, input.OperationID, input.OwnerApproved, service.now(),
	)
	if err != nil {
		return State{}, err
	}
	return service.run(ctx, mutation, input.OperationID, state)
}

// Recover invokes the bounded recovery adapter and closes the exact operation
// without pretending that unfinished forward phases succeeded.
func (service Service) Recover(
	ctx context.Context,
	input ExecuteInput,
) (State, error) {
	if service.Executor == nil {
		return State{}, errors.New("fleet lifecycle executor is required")
	}
	mutation, err := BindMutation(
		input.CurrentPlan, input.TargetPlan, input.Operation,
		input.SourceMemberRef, input.TargetMemberRef,
	)
	if err != nil {
		return State{}, err
	}
	contract, err := Project(input.CurrentPlan)
	if err != nil {
		return State{}, err
	}
	state, err := service.Store.Load(contract)
	if err != nil {
		return State{}, err
	}
	record, err := exactRecord(state, mutation, input.OperationID)
	if err != nil {
		return State{}, err
	}
	if record.Status != StatusRecoveryRequired {
		return State{}, errors.New("fleet mutation is not recovery-required")
	}
	artifact, err := service.Executor.Recover(ctx, RecoveryRequest{
		OperationID: record.ID, Attempt: record.Attempt,
		FailedPhase: record.CurrentPhase, LastError: record.LastError,
		Authority: record.Authority, Checkpoint: record.Checkpoint,
		Recovery:  record.Recovery,
		Completed: append([]PhaseRecord(nil), record.Phases...),
	})
	if err != nil {
		return state, fmt.Errorf("recover fleet mutation %s: %w", record.ID, err)
	}
	recovered, err := service.Store.MarkRecovered(
		mutation, record.ID, input.OwnerApproved, artifact, service.now(),
	)
	if err != nil {
		return state, fmt.Errorf("commit fleet mutation recovery evidence: %w", err)
	}
	return recovered, nil
}

func (service Service) run(
	ctx context.Context,
	mutation Mutation,
	operationID string,
	state State,
) (State, error) {
	for {
		record, err := exactRecord(state, mutation, operationID)
		if err != nil {
			return state, err
		}
		if record.Status == StatusSucceeded {
			return state, nil
		}
		if record.Status != StatusRunning {
			return state, fmt.Errorf(
				"fleet mutation %s is %s and cannot execute forward",
				record.ID, record.Status,
			)
		}
		request := PhaseRequest{
			OperationID: record.ID, Attempt: record.Attempt,
			Phase: record.CurrentPhase, Authority: record.Authority,
			Checkpoint: record.Checkpoint, Recovery: record.Recovery,
			Completed: append([]PhaseRecord(nil), record.Phases...),
		}
		artifact, executeErr := service.Executor.ExecutePhase(ctx, request)
		if executeErr != nil {
			recoveryState, recoveryErr := service.Store.RequireRecovery(
				mutation, record.ID,
				fmt.Sprintf("phase %s: %v", record.CurrentPhase, executeErr),
				service.now(),
			)
			if recoveryErr != nil {
				return state, errors.Join(executeErr, recoveryErr)
			}
			return recoveryState, fmt.Errorf(
				"execute fleet mutation %s phase %s: %w",
				record.ID, record.CurrentPhase, executeErr,
			)
		}
		next, persistErr := service.Store.RecordPhase(
			mutation, record.ID, artifact, service.now(),
		)
		if persistErr != nil {
			recoveryState, recoveryErr := service.Store.RequireRecovery(
				mutation, record.ID,
				fmt.Sprintf("persist phase %s evidence: %v", record.CurrentPhase, persistErr),
				service.now(),
			)
			if recoveryErr != nil {
				return state, errors.Join(persistErr, recoveryErr)
			}
			return recoveryState, fmt.Errorf(
				"commit fleet mutation %s phase %s: %w",
				record.ID, record.CurrentPhase, persistErr,
			)
		}
		state = next
	}
}

func exactRecord(
	state State,
	mutation Mutation,
	operationID string,
) (MutationRecord, error) {
	if len(state.Operations) == 0 || state.CurrentOperation != operationID {
		return MutationRecord{}, errors.New("exact current fleet mutation is required")
	}
	record := state.Operations[len(state.Operations)-1]
	if record.ID != operationID ||
		!reflectMutationAuthority(record.Authority, mutation.Authority) {
		return MutationRecord{}, errors.New("fleet mutation differs from persisted authority")
	}
	return record, nil
}

func (service Service) now() time.Time {
	if service.Now == nil {
		return time.Now().UTC()
	}
	return service.Now().UTC()
}
