package runtimeapply

import (
	"errors"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// operationCoverage tracks the exact authority consumed by the sorted child
// set. It is local to one validation and never mutates the supplied operation.
type operationCoverage struct {
	targets map[string]runtimeexecutor.RuntimeTarget
	stepIDs map[string]struct{}
	digests map[string]struct{}
	runtime map[string]struct{}
	health  map[HealthExpectation]struct{}
}

func validateOperationBinding(parent runtimeexecutor.ExecutionRequest, operation Operation) error {
	if operation.APIVersion != APIVersion || operation.OperationID != parent.RequestDigest ||
		operation.ParentRequestDigest != parent.RequestDigest || operation.PlanHash != parent.PlanHash ||
		operation.ManifestHash != parent.ManifestHash || operation.GenerationReceiptHash != parent.GenerationReceiptHash ||
		operation.RequirementsHash != parent.RequirementsHash || operation.EvidenceBundleHash != parent.EvidenceBundleHash ||
		operation.Executor != parent.Executor {
		return errors.New("runtime Apply operation does not bind the exact parent request")
	}
	return nil
}

func validateOperationSteps(parent runtimeexecutor.ExecutionRequest, steps []Step) error {
	coverage := operationCoverage{
		targets: make(map[string]runtimeexecutor.RuntimeTarget, len(parent.RuntimeTargets)),
		stepIDs: map[string]struct{}{}, digests: map[string]struct{}{},
		runtime: map[string]struct{}{}, health: map[HealthExpectation]struct{}{},
	}
	for _, target := range parent.RuntimeTargets {
		coverage.targets[target.RequirementID] = target
	}
	for _, step := range steps {
		if err := coverage.validateStep(parent, step); err != nil {
			return err
		}
	}
	return coverage.validateComplete(parent)
}

func (coverage *operationCoverage) validateStep(parent runtimeexecutor.ExecutionRequest, step Step) error {
	if step.ID != step.RequestDigest || len(step.Runtime) == 0 {
		return errors.New("runtime Apply step ID must equal its exact child request digest")
	}
	if _, duplicate := coverage.stepIDs[step.ID]; duplicate {
		return errors.New("runtime Apply operation contains a duplicate step")
	}
	coverage.stepIDs[step.ID] = struct{}{}
	if _, duplicate := coverage.digests[step.RequestDigest]; duplicate {
		return errors.New("runtime Apply operation contains a duplicate child request digest")
	}
	coverage.digests[step.RequestDigest] = struct{}{}
	if err := validateStepAuthority(parent, step); err != nil {
		return err
	}
	if step.Executor.ID == "" || step.Executor.Version == "" || !digestPattern.MatchString(step.Executor.Digest) {
		return errors.New("runtime Apply step has invalid executor identity")
	}
	if err := coverage.validateRuntime(step.Runtime); err != nil {
		return err
	}
	return coverage.validateHealth(step.Health)
}

func validateStepAuthority(parent runtimeexecutor.ExecutionRequest, step Step) error {
	if !digestPattern.MatchString(step.RequestDigest) || !digestPattern.MatchString(step.ArtifactSetHash) ||
		step.PlanHash != parent.PlanHash || step.ManifestHash != parent.ManifestHash ||
		step.GenerationReceiptHash != parent.GenerationReceiptHash || step.RequirementsHash != parent.RequirementsHash ||
		step.EvidenceBundleHash != parent.EvidenceBundleHash ||
		(step.Compensation != CompensationNone && step.Compensation != CompensationExplicit) {
		return errors.New("runtime Apply step has invalid or foreign authority")
	}
	return nil
}

func (coverage *operationCoverage) validateRuntime(expectations []RuntimeExpectation) error {
	for index, runtime := range expectations {
		if index > 0 && expectations[index-1].RequirementID >= runtime.RequirementID {
			return errors.New("runtime Apply step runtime expectations must be sorted and unique")
		}
		target, exists := coverage.targets[runtime.RequirementID]
		if !exists || runtime.InstanceRef != target.InstanceRef {
			return errors.New("runtime Apply step does not bind an exact parent runtime target")
		}
		if _, duplicate := coverage.runtime[runtime.RequirementID]; duplicate {
			return errors.New("runtime Apply operation contains duplicate runtime authority")
		}
		coverage.runtime[runtime.RequirementID] = struct{}{}
	}
	return nil
}

func (coverage *operationCoverage) validateHealth(expectations []HealthExpectation) error {
	for _, item := range expectations {
		if item.RequirementID == "" || item.TargetRef == "" {
			return errors.New("runtime Apply step has invalid health expectation")
		}
		if _, duplicate := coverage.health[item]; duplicate {
			return errors.New("runtime Apply operation contains duplicate health authority")
		}
		coverage.health[item] = struct{}{}
	}
	return nil
}

func (coverage *operationCoverage) validateComplete(parent runtimeexecutor.ExecutionRequest) error {
	if len(coverage.runtime) != len(parent.RuntimeTargets) {
		return errors.New("runtime Apply operation does not cover the complete runtime target set")
	}
	if len(coverage.health) != len(parent.HealthTargets) {
		return errors.New("runtime Apply operation does not cover the complete health target set")
	}
	for _, health := range parent.HealthTargets {
		if _, exists := coverage.health[HealthExpectation{RequirementID: health.RequirementID, TargetRef: health.TargetRef}]; !exists {
			return errors.New("runtime Apply operation contains foreign health authority")
		}
	}
	return nil
}

func verifyStepResultAuthority(step Step, result runtimeexecutor.ExecutionResult) error {
	if result.Executor != step.Executor || result.PlanHash != step.PlanHash || result.ManifestHash != step.ManifestHash ||
		result.GenerationReceiptHash != step.GenerationReceiptHash || result.RequirementsHash != step.RequirementsHash ||
		result.EvidenceBundleHash != step.EvidenceBundleHash || result.ArtifactSetHash != step.ArtifactSetHash ||
		result.RequestDigest != step.RequestDigest {
		return errors.New("runtime Apply step result has foreign authority")
	}
	return nil
}

func verifyStepRuntime(step Step, result runtimeexecutor.ExecutionResult) error {
	if len(result.Runtime) != len(step.Runtime) {
		return errors.New("runtime Apply step result does not prove the exact runtime target")
	}
	for index, expected := range step.Runtime {
		actual := result.Runtime[index]
		if actual.RequirementID != expected.RequirementID || actual.InstanceRef != expected.InstanceRef || actual.Status != runtimeexecutor.RuntimeStatusApplied {
			return errors.New("runtime Apply step result contains foreign runtime evidence")
		}
	}
	return nil
}

func verifyStepHealth(step Step, result runtimeexecutor.ExecutionResult) error {
	if len(result.Health) != len(step.Health) {
		return errors.New("runtime Apply step result does not prove the exact health set")
	}
	for index, expected := range step.Health {
		actual := result.Health[index]
		if actual.RequirementID != expected.RequirementID || actual.TargetRef != expected.TargetRef || actual.Status != runtimeexecutor.HealthStatusHealthy {
			return errors.New("runtime Apply step result contains foreign health evidence")
		}
	}
	return nil
}
