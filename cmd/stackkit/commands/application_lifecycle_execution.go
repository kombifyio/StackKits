package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

type architectureV2ApplicationLifecycleRun struct {
	Contract    applicationlifecycle.Contract
	OperationID string
}

func beginArchitectureV2ApplicationLifecycles(
	workspace string,
	plan generationartifact.VerifiedPlan,
	stage string,
	operationRef string,
	workloadRef string,
	now time.Time,
) ([]architectureV2ApplicationLifecycleRun, error) {
	return beginArchitectureV2ApplicationLifecyclesWithID(
		workspace, plan, stage, operationRef, workloadRef, "", now,
	)
}

func beginArchitectureV2ApplicationLifecyclesWithID(
	workspace string,
	plan generationartifact.VerifiedPlan,
	stage string,
	operationRef string,
	workloadRef string,
	operationID string,
	now time.Time,
) ([]architectureV2ApplicationLifecycleRun, error) {
	resolved, err := resolvedplan.DecodeCanonicalPlan(plan.Canonical())
	if err != nil {
		return nil, fmt.Errorf("decode verified ResolvedPlan application lifecycles: %w", err)
	}
	contracts, err := applicationlifecycle.ContractsFromResolvedPlan(resolved)
	if err != nil {
		return nil, err
	}
	store := applicationlifecycle.Store{Workspace: workspace}
	runs := make([]architectureV2ApplicationLifecycleRun, 0, len(contracts))
	for _, contract := range contracts {
		if workloadRef != "" && contract.WorkloadRef != workloadRef {
			continue
		}
		if !applicationLifecycleStageSupportedByDelivery(contract, stage) {
			if workloadRef != "" {
				return nil, fmt.Errorf(
					"application workload %q adapter %q does not support the %s lifecycle",
					contract.WorkloadRef,
					contract.Delivery.AdapterRef,
					stage,
				)
			}
			continue
		}
		runOperationID := strings.TrimSpace(operationID)
		if runOperationID == "" {
			runOperationID, err = applicationlifecycle.NewOperationID(operationRef)
			if err != nil {
				return nil, failStartedApplicationLifecycles(
					store, runs, "application lifecycle operation identity could not be created", now, err,
				)
			}
		}
		if _, err := store.Begin(contract, applicationlifecycle.BeginRequest{
			ID: runOperationID, Stage: stage, OperationRef: operationRef, Now: now,
		}); err != nil {
			return nil, failStartedApplicationLifecycles(
				store, runs, "application lifecycle could not start before runtime mutation", now, err,
			)
		}
		runs = append(runs, architectureV2ApplicationLifecycleRun{
			Contract: contract, OperationID: runOperationID,
		})
	}
	return runs, nil
}

func applicationLifecycleStageSupportedByDelivery(
	contract applicationlifecycle.Contract,
	stage string,
) bool {
	switch stage {
	case "backup", "restore":
		switch contract.Delivery.Kind {
		case "stackkit":
			return true
		case "application-adapter", "selected-paas":
			return contract.Delivery.Capabilities != nil &&
				contract.Delivery.Capabilities.BackupRestore
		default:
			return false
		}
	default:
		return true
	}
}

func succeedArchitectureV2ApplicationLifecycles(
	workspace string,
	runs []architectureV2ApplicationLifecycleRun,
	evidence []applicationlifecycle.Evidence,
	now time.Time,
) error {
	store := applicationlifecycle.Store{Workspace: workspace}
	var transitionErr error
	for _, run := range runs {
		if _, err := store.Transition(run.Contract, applicationlifecycle.TransitionRequest{
			ID: run.OperationID, Status: applicationlifecycle.StatusSucceeded,
			Evidence: append([]applicationlifecycle.Evidence(nil), evidence...), Now: now,
		}); err != nil {
			transitionErr = errors.Join(transitionErr, fmt.Errorf(
				"complete application lifecycle for workload %q: %w", run.Contract.WorkloadRef, err,
			))
		}
	}
	return transitionErr
}

func failArchitectureV2ApplicationLifecycles(
	workspace string,
	runs []architectureV2ApplicationLifecycleRun,
	diagnostic string,
	now time.Time,
	cause error,
) error {
	store := applicationlifecycle.Store{Workspace: workspace}
	return failStartedApplicationLifecycles(store, runs, diagnostic, now, cause)
}

func failStartedApplicationLifecycles(
	store applicationlifecycle.Store,
	runs []architectureV2ApplicationLifecycleRun,
	diagnostic string,
	now time.Time,
	cause error,
) error {
	result := cause
	for _, run := range runs {
		if _, err := store.Transition(run.Contract, applicationlifecycle.TransitionRequest{
			ID: run.OperationID, Status: applicationlifecycle.StatusFailed,
			LastError: diagnostic, Now: now,
		}); err != nil {
			result = errors.Join(result, fmt.Errorf(
				"record failed application lifecycle for workload %q: %w", run.Contract.WorkloadRef, err,
			))
		}
	}
	return result
}

func requireArchitectureV2ApplicationLifecycleRecovery(
	workspace string,
	runs []architectureV2ApplicationLifecycleRun,
	diagnostic string,
	recoveryRef string,
	now time.Time,
	cause error,
) error {
	store := applicationlifecycle.Store{Workspace: workspace}
	result := cause
	for _, run := range runs {
		if _, err := store.Transition(run.Contract, applicationlifecycle.TransitionRequest{
			ID: run.OperationID, Status: applicationlifecycle.StatusRecoveryRequired,
			LastError: diagnostic, RecoveryRef: recoveryRef, Now: now,
		}); err != nil {
			result = errors.Join(result, fmt.Errorf(
				"record recovery-required lifecycle for workload %q: %w", run.Contract.WorkloadRef, err,
			))
		}
	}
	return result
}

func recoverArchitectureV2ApplicationLifecycles(
	workspace string,
	runs []architectureV2ApplicationLifecycleRun,
	diagnostic string,
	recoveryRef string,
	evidence []applicationlifecycle.Evidence,
	now time.Time,
	cause error,
) error {
	store := applicationlifecycle.Store{Workspace: workspace}
	result := cause
	for _, run := range runs {
		if _, err := store.Transition(run.Contract, applicationlifecycle.TransitionRequest{
			ID: run.OperationID, Status: applicationlifecycle.StatusRecoveryRequired,
			LastError: diagnostic, RecoveryRef: recoveryRef, Now: now,
		}); err != nil {
			result = errors.Join(result, fmt.Errorf(
				"record recovery-required lifecycle for workload %q: %w", run.Contract.WorkloadRef, err,
			))
			continue
		}
		if _, err := store.Transition(run.Contract, applicationlifecycle.TransitionRequest{
			ID: run.OperationID, Status: applicationlifecycle.StatusRecovered,
			Evidence: append([]applicationlifecycle.Evidence(nil), evidence...), Now: now,
		}); err != nil {
			result = errors.Join(result, fmt.Errorf(
				"record recovered lifecycle for workload %q: %w", run.Contract.WorkloadRef, err,
			))
		}
	}
	return result
}

func completeExistingRecoveredApplicationLifecycles(
	workspace string,
	plan generationartifact.VerifiedPlan,
	operationID string,
	evidence []applicationlifecycle.Evidence,
	now time.Time,
) error {
	resolved, err := resolvedplan.DecodeCanonicalPlan(plan.Canonical())
	if err != nil {
		return fmt.Errorf("decode verified recovery ResolvedPlan: %w", err)
	}
	contracts, err := applicationlifecycle.ContractsFromResolvedPlan(resolved)
	if err != nil {
		return err
	}
	store := applicationlifecycle.Store{Workspace: workspace}
	var result error
	for _, contract := range contracts {
		state, loadErr := store.Load(contract)
		if loadErr != nil {
			result = errors.Join(result, loadErr)
			continue
		}
		found := false
		for _, operation := range state.Operations {
			if operation.ID == operationID {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		if _, transitionErr := store.Transition(contract, applicationlifecycle.TransitionRequest{
			ID: operationID, Status: applicationlifecycle.StatusRecovered,
			Evidence: append([]applicationlifecycle.Evidence(nil), evidence...), Now: now,
		}); transitionErr != nil {
			result = errors.Join(result, fmt.Errorf(
				"complete recovered application lifecycle for workload %q: %w",
				contract.WorkloadRef, transitionErr,
			))
		}
	}
	return result
}

func architectureV2WorkspaceEvidenceRef(workspace, path string) (string, error) {
	workspaceAbsolute, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve application lifecycle workspace: %w", err)
	}
	pathAbsolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve application lifecycle evidence path: %w", err)
	}
	relative, err := filepath.Rel(filepath.Clean(workspaceAbsolute), filepath.Clean(pathAbsolute))
	if err != nil {
		return "", fmt.Errorf("derive application lifecycle evidence path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("application lifecycle evidence must remain inside owner workspace custody")
	}
	return filepath.ToSlash(relative), nil
}

func architectureV2ApplicationLifecycleDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}
