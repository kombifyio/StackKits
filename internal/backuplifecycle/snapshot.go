package backuplifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

type RunInput struct {
	Workloads       []string         `json:"workloads,omitempty"`
	OwnerRef        string           `json:"ownerRef"`
	AuthorityRef    string           `json:"authorityRef"`
	Lineage         AuthorityLineage `json:"lineage"`
	PolicyArtifact  []byte           `json:"-"`
	OperationID     string           `json:"operationId"`
	ProtectRecovery bool             `json:"protectRecovery,omitempty"`
}

type SnapshotAnchor struct {
	Workloads            []string                                   `json:"workloads,omitempty"`
	APIVersion           string                                     `json:"apiVersion"`
	ID                   string                                     `json:"id"`
	OwnerRef             string                                     `json:"ownerRef"`
	AuthorityRef         string                                     `json:"authorityRef"`
	Lineage              AuthorityLineage                           `json:"lineage"`
	PolicyArtifactDigest string                                     `json:"policyArtifactDigest"`
	OperationID          string                                     `json:"operationId"`
	Policy               Policy                                     `json:"policy"`
	Repository           RepositoryReceipt                          `json:"repository"`
	Snapshot             RepositorySnapshotReceipt                  `json:"snapshot"`
	Quiescence           *SnapshotQuiescence                        `json:"quiescence,omitempty"`
	Signature            localevidence.OwnerSnapshotAnchorSignature `json:"signature"`
	ProtectRecovery      bool                                       `json:"protectRecovery,omitempty"`
}

func (s *Service) Run(ctx context.Context, input RunInput) (SnapshotAnchor, error) {
	stage := RunStageRuntime
	anchor, err := s.run(ctx, input, &stage)
	return anchor, WithRunStage(stage, err)
}

func (s *Service) run(ctx context.Context, input RunInput, stage *string) (SnapshotAnchor, error) {
	if err := s.ready(ctx); err != nil {
		return SnapshotAnchor{}, err
	}
	if err := RequireSettledRuntimeControls(s.workspaceRoot, input.OperationID); err != nil {
		return SnapshotAnchor{}, err
	}
	if !validOperationID(input.OperationID) {
		return SnapshotAnchor{}, errors.New("backuplifecycle: operation ID must match [A-Za-z0-9][A-Za-z0-9._-]{0,127}")
	}
	*stage = RunStageConfiguration
	configuration, err := s.boundConfiguration(StatusInput{
		OwnerRef: input.OwnerRef, AuthorityRef: input.AuthorityRef,
		Lineage: input.Lineage, PolicyArtifact: input.PolicyArtifact,
	})
	if err != nil {
		return SnapshotAnchor{}, err
	}
	*stage = RunStageOperationJournal
	boundInput := operationInput{
		OwnerRef: configuration.OwnerRef, AuthorityRef: configuration.AuthorityRef,
		Lineage: configuration.Lineage, PolicyArtifactDigest: configuration.PolicyArtifactDigest,
		OperationID: input.OperationID, Policy: clonePolicy(configuration.Policy), ProtectRecovery: input.ProtectRecovery, Workloads: append([]string(nil), input.Workloads...),
	}
	operationPath := operationDirectory + "/" + operationKey(input.OperationID) + ".json"
	pending := false
	var pendingOperation snapshotOperation
	if operation, loadErr := s.loadOperation(operationPath); loadErr == nil {
		if !operationInputsEqual(operation.Input, boundInput) {
			return SnapshotAnchor{}, errors.New("backuplifecycle: operation intent differs from requested authority lineage, policy, or operation")
		}
		switch {
		case operation.State == "completed" && operation.Anchor != nil:
			if err := VerifySnapshotAnchor(s.workspaceRoot, *operation.Anchor); err != nil {
				return SnapshotAnchor{}, err
			}
			if !anchorMatchesOperation(*operation.Anchor, operation) {
				return SnapshotAnchor{}, errors.New("backuplifecycle: completed operation anchor differs from its exact input")
			}
			stored, err := s.loadStoredSnapshotAnchor(operation.Anchor.ID)
			if err != nil {
				return SnapshotAnchor{}, err
			}
			if !reflect.DeepEqual(stored, *operation.Anchor) {
				return SnapshotAnchor{}, errors.New("backuplifecycle: completed operation journal differs from its content-addressed snapshot anchor")
			}
			*stage = RunStageAppGroups
			if err := s.closeCompletedSnapshotGroups(ctx, operationPath, operation); err != nil {
				return SnapshotAnchor{}, err
			}
			return stored, nil
		case operation.State == "failed" && operation.Anchor == nil:
			*stage = RunStageAppGroups
			if err := s.closeFailedSnapshotGroups(ctx, operationPath, operation); err != nil {
				return SnapshotAnchor{}, err
			}
			return SnapshotAnchor{}, ErrAppGroupFailed
		case operation.State == "pending" && operation.Anchor == nil:
			pending, pendingOperation = true, operation
		default:
			return SnapshotAnchor{}, errors.New("backuplifecycle: snapshot operation journal is malformed")
		}
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return SnapshotAnchor{}, loadErr
	}
	*stage = RunStageRetention
	if err := s.admitSnapshotRetention(ctx, configuration); err != nil {
		return SnapshotAnchor{}, err
	}

	*stage = RunStageRepository
	request, err := snapshotRequest(configuration, input.OperationID, input.Workloads...)
	if err != nil {
		return SnapshotAnchor{}, err
	}
	request.ProtectRecovery = input.ProtectRecovery
	quiescedRuntime, supportsQuiescence := s.runtime.(SnapshotQuiesceRuntime)
	persistQuiescence := func(quiescence SnapshotQuiescence) error {
		return s.persistSnapshotQuiescence(operationPath, boundInput, quiescence)
	}
	if pending && pendingOperation.Quiescence != nil && !supportsQuiescence {
		return SnapshotAnchor{}, errors.New("backuplifecycle: journaled snapshot quiescence requires a quiescence-capable runtime")
	}
	if pending && supportsQuiescence {
		if pendingOperation.Quiescence == nil {
			status, statusErr := s.repositoryStatus(ctx, configuration)
			if statusErr != nil {
				return SnapshotAnchor{}, statusErr
			}
			if !status.Ready || status.Consistency != ConsistencyCrashConsistent {
				return SnapshotAnchor{}, errors.New("backuplifecycle: repository is not ready for a crash-consistent snapshot")
			}
		}
		quiescence := SnapshotQuiescence{}
		if pendingOperation.Quiescence != nil {
			quiescence = cloneSnapshotQuiescence(*pendingOperation.Quiescence)
		}
		*stage = RunStageSnapshot
		snapshot, quiesceErr := quiescedRuntime.CreateSnapshotWithQuiescence(ctx, request, quiescence, persistQuiescence)
		if quiesceErr != nil {
			return SnapshotAnchor{}, errors.Join(fmt.Errorf("backuplifecycle: create repository snapshot: %w", quiesceErr), s.settleFailedSnapshot(ctx, operationPath, boundInput))
		}
		*stage = RunStageSnapshotReceipt
		if err := validateSnapshotReceipt(snapshot, request); err != nil {
			return SnapshotAnchor{}, err
		}
		*stage = RunStageComplete
		return s.completeSnapshot(ctx, operationPath, boundInput, configuration.Repository, snapshot, true)
	}
	if pending {
		*stage = RunStageSnapshot
		snapshot, found, lookupErr := s.runtime.LookupSnapshot(ctx, request)
		if lookupErr != nil {
			return SnapshotAnchor{}, fmt.Errorf("backuplifecycle: lookup pending repository snapshot: %w", lookupErr)
		}
		if found {
			*stage = RunStageSnapshotReceipt
			if err := validateSnapshotReceipt(snapshot, request); err != nil {
				return SnapshotAnchor{}, err
			}
			*stage = RunStageComplete
			return s.completeSnapshot(ctx, operationPath, boundInput, configuration.Repository, snapshot, false)
		}
	}

	*stage = RunStageRepository
	status, err := s.repositoryStatus(ctx, configuration)
	if err != nil {
		return SnapshotAnchor{}, err
	}
	if !status.Ready || status.Consistency != ConsistencyCrashConsistent {
		return SnapshotAnchor{}, errors.New("backuplifecycle: repository is not ready for a crash-consistent snapshot")
	}
	*stage = RunStageOperationJournal
	if !pending {
		if err := s.writeSnapshotOperation(operationPath, snapshotOperation{
			APIVersion: snapshotOperationAPI, State: "pending", Input: cloneOperationInput(boundInput),
		}); err != nil {
			return SnapshotAnchor{}, err
		}
	}
	*stage = RunStageSnapshot
	var snapshot RepositorySnapshotReceipt
	if supportsQuiescence {
		snapshot, err = quiescedRuntime.CreateSnapshotWithQuiescence(ctx, request, SnapshotQuiescence{}, persistQuiescence)
	} else {
		snapshot, err = s.runtime.CreateSnapshot(ctx, request)
	}
	if err != nil {
		return SnapshotAnchor{}, errors.Join(fmt.Errorf("backuplifecycle: create repository snapshot: %w", err), s.settleFailedSnapshot(ctx, operationPath, boundInput))
	}
	*stage = RunStageSnapshotReceipt
	if err := validateSnapshotReceipt(snapshot, request); err != nil {
		return SnapshotAnchor{}, err
	}
	*stage = RunStageComplete
	return s.completeSnapshot(ctx, operationPath, boundInput, configuration.Repository, snapshot, supportsQuiescence)
}

func (s *Service) completeSnapshot(
	ctx context.Context,
	operationPath string,
	input operationInput,
	repository RepositoryReceipt,
	snapshot RepositorySnapshotReceipt,
	requireRestoredQuiescence bool,
) (SnapshotAnchor, error) {
	operation, err := s.loadOperation(operationPath)
	if err != nil {
		return SnapshotAnchor{}, fmt.Errorf("backuplifecycle: authenticate snapshot operation before completion: %w", err)
	}
	if operation.State != "pending" || operation.Anchor != nil || !operationInputsEqual(operation.Input, input) {
		return SnapshotAnchor{}, errors.New("backuplifecycle: snapshot operation changed before completion")
	}
	var quiescence *SnapshotQuiescence
	if operation.Quiescence != nil {
		if err := validateRestoredSnapshotQuiescence(*operation.Quiescence); err != nil {
			return SnapshotAnchor{}, err
		}
		cloned := cloneSnapshotQuiescence(*operation.Quiescence)
		quiescence = &cloned
	} else if requireRestoredQuiescence {
		return SnapshotAnchor{}, errors.New("backuplifecycle: quiescence-capable snapshot lacks authenticated restored evidence")
	}
	anchor := SnapshotAnchor{
		APIVersion: snapshotAnchorAPIVersion,
		OwnerRef:   input.OwnerRef, AuthorityRef: input.AuthorityRef,
		Lineage: input.Lineage, PolicyArtifactDigest: input.PolicyArtifactDigest,
		OperationID: input.OperationID, Policy: clonePolicy(input.Policy), ProtectRecovery: input.ProtectRecovery, Workloads: append([]string(nil), input.Workloads...),
		Quiescence: quiescence,
		Repository: repository, Snapshot: snapshot,
	}
	unsigned, err := canonicalAnchor(anchor)
	if err != nil {
		return SnapshotAnchor{}, err
	}
	anchor.ID = digestBytes(unsigned)
	signingBytes, err := canonicalAnchor(anchor)
	if err != nil {
		return SnapshotAnchor{}, err
	}
	anchor.Signature, err = localevidence.SignOwnerSnapshotAnchor(s.workspaceRoot, signingBytes)
	if err != nil {
		return SnapshotAnchor{}, err
	}
	if err := VerifySnapshotAnchor(s.workspaceRoot, anchor); err != nil {
		return SnapshotAnchor{}, err
	}
	anchorPath := anchorDirectory + "/" + strings.TrimPrefix(anchor.ID, "sha256:") + ".json"
	if stored, loadErr := s.loadStoredSnapshotAnchor(anchor.ID); loadErr == nil {
		if !reflect.DeepEqual(stored, anchor) {
			return SnapshotAnchor{}, errors.New("backuplifecycle: content-addressed snapshot anchor differs from completed snapshot")
		}
	} else if errors.Is(loadErr, os.ErrNotExist) {
		if err := s.writeJSON(anchorPath, anchor); err != nil {
			return SnapshotAnchor{}, err
		}
		stored, err := s.loadStoredSnapshotAnchor(anchor.ID)
		if err != nil {
			return SnapshotAnchor{}, err
		}
		if !reflect.DeepEqual(stored, anchor) {
			return SnapshotAnchor{}, errors.New("backuplifecycle: persisted snapshot anchor differs from completed snapshot")
		}
	} else {
		return SnapshotAnchor{}, loadErr
	}
	if err := s.writeSnapshotOperation(operationPath, snapshotOperation{
		APIVersion: snapshotOperationAPI, State: "completed",
		Input: cloneOperationInput(input), Quiescence: quiescence, Anchor: &anchor,
	}); err != nil {
		return SnapshotAnchor{}, err
	}
	completed, err := s.loadOperation(operationPath)
	if err != nil {
		return SnapshotAnchor{}, err
	}
	if err = s.closeCompletedSnapshotGroups(ctx, operationPath, completed); err != nil {
		return SnapshotAnchor{}, err
	}
	return anchor, nil
}

func (s *Service) loadStoredSnapshotAnchor(anchorID string) (SnapshotAnchor, error) {
	if !validDigest(anchorID) {
		return SnapshotAnchor{}, errors.New("backuplifecycle: snapshot anchor identity is invalid")
	}
	anchorPath := anchorDirectory + "/" + strings.TrimPrefix(anchorID, "sha256:") + ".json"
	var anchor SnapshotAnchor
	if err := s.readJSON(anchorPath, &anchor); err != nil {
		return SnapshotAnchor{}, err
	}
	if anchor.ID != anchorID {
		return SnapshotAnchor{}, errors.New("backuplifecycle: stored snapshot anchor identity differs from its content address")
	}
	if err := VerifySnapshotAnchor(s.workspaceRoot, anchor); err != nil {
		return SnapshotAnchor{}, err
	}
	return anchor, nil
}

func VerifySnapshotAnchor(workspaceRoot string, anchor SnapshotAnchor) error {
	if anchor.APIVersion != snapshotAnchorAPIVersion ||
		anchor.ID == "" || anchor.OwnerRef == "" || anchor.AuthorityRef == "" ||
		anchor.OperationID == "" || !validDigest(anchor.PolicyArtifactDigest) {
		return errors.New("backuplifecycle: snapshot anchor is incomplete")
	}
	if err := validateLineage(anchor.Lineage); err != nil {
		return err
	}
	if err := validatePolicy(anchor.Policy); err != nil {
		return err
	}
	if anchor.Quiescence != nil {
		if err := validateRestoredSnapshotQuiescence(*anchor.Quiescence); err != nil {
			return err
		}
	}
	configuration := RepositoryConfiguration{
		OwnerRef: anchor.OwnerRef, AuthorityRef: anchor.AuthorityRef,
		Lineage: anchor.Lineage, PolicyArtifactDigest: anchor.PolicyArtifactDigest,
		Policy: clonePolicy(anchor.Policy),
	}
	configuration.OperationID = configurationOperationID(configuration)
	if err := validateRepositoryReceipt(anchor.Repository, configuration); err != nil {
		return err
	}
	request, err := snapshotRequestFromAnchor(anchor)
	if err != nil {
		return err
	}
	if err := validateSnapshotReceipt(anchor.Snapshot, request); err != nil {
		return err
	}
	if anchor.Quiescence != nil && anchor.Quiescence.CaptureStartedAt != nil &&
		anchor.Quiescence.CaptureStartedAt.After(anchor.Snapshot.CreatedAt) {
		return errors.New("backuplifecycle: snapshot capture starts after its completion")
	}
	unsigned := anchor
	unsigned.ID = ""
	unsigned.Signature = localevidence.OwnerSnapshotAnchorSignature{}
	canonicalUnsigned, err := json.Marshal(unsigned)
	if err != nil {
		return fmt.Errorf("backuplifecycle: encode snapshot anchor identity: %w", err)
	}
	if anchor.ID != digestBytes(canonicalUnsigned) {
		return errors.New("backuplifecycle: snapshot anchor content identity does not verify")
	}
	signingBytes, err := canonicalAnchor(anchor)
	if err != nil {
		return err
	}
	return localevidence.VerifyOwnerSnapshotAnchor(workspaceRoot, signingBytes, anchor.Signature)
}

func snapshotRequestFromAnchor(anchor SnapshotAnchor) (RepositorySnapshotRequest, error) {
	sources, err := anchor.Policy.ApplicationSnapshotSources(anchor.Workloads)
	if err != nil {
		return RepositorySnapshotRequest{}, err
	}
	return RepositorySnapshotRequest{
		OwnerRef: anchor.OwnerRef, AuthorityRef: anchor.AuthorityRef,
		Lineage: anchor.Lineage, PolicyArtifactDigest: anchor.PolicyArtifactDigest,
		RepositoryID:    anchor.Repository.RepositoryID,
		OperationID:     anchor.OperationID,
		ProtectRecovery: anchor.ProtectRecovery, Workloads: append([]string(nil), anchor.Workloads...),
		Source:      anchor.Policy.Source.ContainerPath,
		Excludes:    append([]string(nil), anchor.Policy.Source.ExcludePaths...),
		Consistency: ConsistencyCrashConsistent,
		Sources:     sources,
	}, nil
}

func anchorMatchesOperation(anchor SnapshotAnchor, operation snapshotOperation) bool {
	input := operation.Input
	return anchor.OwnerRef == input.OwnerRef &&
		anchor.AuthorityRef == input.AuthorityRef &&
		lineagesEqual(anchor.Lineage, input.Lineage) &&
		anchor.PolicyArtifactDigest == input.PolicyArtifactDigest &&
		anchor.OperationID == input.OperationID && anchor.ProtectRecovery == input.ProtectRecovery && reflect.DeepEqual(anchor.Workloads, input.Workloads) &&
		policiesEqual(anchor.Policy, input.Policy) &&
		snapshotQuiescencePointersEqual(anchor.Quiescence, operation.Quiescence)
}

func snapshotRequest(configuration Configuration, operationID string, workloads ...string) (RepositorySnapshotRequest, error) {
	sources, err := configuration.Policy.ApplicationSnapshotSources(workloads)
	if err != nil {
		return RepositorySnapshotRequest{}, err
	}
	return RepositorySnapshotRequest{
		OwnerRef: configuration.OwnerRef, AuthorityRef: configuration.AuthorityRef,
		Lineage: configuration.Lineage, PolicyArtifactDigest: configuration.PolicyArtifactDigest,
		RepositoryID: configuration.Repository.RepositoryID, OperationID: operationID,
		Source:      configuration.Policy.Source.ContainerPath,
		Excludes:    append([]string(nil), configuration.Policy.Source.ExcludePaths...),
		Consistency: ConsistencyCrashConsistent,
		Sources:     sources, Workloads: append([]string(nil), workloads...),
	}, nil
}

func canonicalAnchor(anchor SnapshotAnchor) ([]byte, error) {
	anchor.Signature = localevidence.OwnerSnapshotAnchorSignature{}
	encoded, err := json.Marshal(anchor)
	if err != nil {
		return nil, fmt.Errorf("backuplifecycle: encode canonical snapshot anchor: %w", err)
	}
	return encoded, nil
}
