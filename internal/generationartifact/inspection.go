package generationartifact

import (
	"bytes"
	"sort"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	PlanInspectionAPIVersion       = "stackkit.plan-inspection/v1"
	PlanInspectionKind             = "PlanInspection"
	InfrastructureDiffNotAvailable = "not-available"
)

// PlanInspection is a read-only projection of one generation-verified
// Architecture v2 plan and its exact generated artifact closure. It is not an
// infrastructure diff and never claims that an executor was invoked.
type PlanInspection struct {
	APIVersion         string                  `json:"apiVersion"`
	Kind               string                  `json:"kind"`
	VerifiedPhase      ExecutionPhase          `json:"verifiedPhase"`
	Binding            PlanBinding             `json:"binding"`
	Renderer           RendererIdentity        `json:"renderer"`
	OutputRoot         string                  `json:"outputRoot"`
	Readiness          PlanInspectionReadiness `json:"readiness"`
	Manifest           PlanInspectionManifest  `json:"manifest"`
	InfrastructureDiff string                  `json:"infrastructureDiff"`
	ExecutorInvoked    bool                    `json:"executorInvoked"`
}

type PlanInspectionReadiness struct {
	Generation PlanInspectionPhase `json:"generation"`
	Apply      PlanInspectionPhase `json:"apply"`
}

type PlanInspectionPhase struct {
	Status   string                  `json:"status"`
	Blockers []PlanInspectionBlocker `json:"blockers"`
}

type PlanInspectionBlocker struct {
	Code string   `json:"code"`
	Refs []string `json:"refs"`
}

type PlanInspectionManifest struct {
	Hash      string             `json:"hash"`
	Artifacts []RenderedArtifact `json:"artifacts"`
}

// InspectExecution first performs the complete generation execution gate and
// only then creates a defensive in-memory projection. Apply verification is a
// separate mutating authorization concern and is deliberately not accepted.
func InspectExecution(input ExecutionGateInput) (PlanInspection, error) {
	if input.Phase != ExecutionPhaseGeneration {
		return PlanInspection{}, fail(ErrInvalidContract, "inspection.phase", "plan inspection requires the generation execution phase")
	}
	if err := VerifyExecution(input); err != nil {
		return PlanInspection{}, err
	}

	generation, err := inspectReadinessPhase(input.Plan, ExecutionPhaseGeneration)
	if err != nil {
		return PlanInspection{}, err
	}
	apply, err := inspectReadinessPhase(input.Plan, ExecutionPhaseApply)
	if err != nil {
		return PlanInspection{}, err
	}
	manifestHash, err := input.Manifest.Hash()
	if err != nil {
		return PlanInspection{}, err
	}
	artifacts := append([]RenderedArtifact(nil), input.Manifest.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool {
		left, _ := resolvedplan.CanonicalJSON(artifacts[i])
		right, _ := resolvedplan.CanonicalJSON(artifacts[j])
		return bytes.Compare(left, right) < 0
	})

	binding := input.Plan.Binding()
	return PlanInspection{
		APIVersion:         PlanInspectionAPIVersion,
		Kind:               PlanInspectionKind,
		VerifiedPhase:      ExecutionPhaseGeneration,
		Binding:            binding,
		Renderer:           binding.Renderer,
		OutputRoot:         input.Plan.OutputRoot(),
		Readiness:          PlanInspectionReadiness{Generation: generation, Apply: apply},
		Manifest:           PlanInspectionManifest{Hash: manifestHash, Artifacts: artifacts},
		InfrastructureDiff: InfrastructureDiffNotAvailable,
		ExecutorInvoked:    false,
	}, nil
}

func inspectReadinessPhase(plan VerifiedPlan, phase ExecutionPhase) (PlanInspectionPhase, error) {
	decision, exists := plan.readiness[phase]
	if !exists {
		return PlanInspectionPhase{}, fail(ErrInvalidPlan, "resolvedPlan.executionReadiness", "phase %q is not governed", phase)
	}
	if decision.status != "ready" && decision.status != "blocked" {
		return PlanInspectionPhase{}, fail(ErrInvalidPlan, "resolvedPlan.executionReadiness."+string(phase)+".status", "must be ready or blocked")
	}
	if decision.status == "ready" && len(decision.blockers) != 0 {
		return PlanInspectionPhase{}, fail(ErrInvalidPlan, "resolvedPlan.executionReadiness."+string(phase)+".blockers", "ready phase cannot retain blockers")
	}
	if decision.status == "blocked" && len(decision.blockers) == 0 {
		return PlanInspectionPhase{}, fail(ErrInvalidPlan, "resolvedPlan.executionReadiness."+string(phase)+".blockers", "blocked phase requires at least one blocker")
	}

	blockers := make([]PlanInspectionBlocker, len(decision.blockers))
	for index, blocker := range decision.blockers {
		blockers[index] = PlanInspectionBlocker{Code: blocker.Code, Refs: append([]string(nil), blocker.Refs...)}
	}
	return PlanInspectionPhase{Status: decision.status, Blockers: blockers}, nil
}

// MarshalCanonical returns stable bytes without adding timestamps, host facts,
// or any other environment-dependent value.
func (inspection PlanInspection) MarshalCanonical() ([]byte, error) {
	return resolvedplan.CanonicalJSON(inspection)
}
