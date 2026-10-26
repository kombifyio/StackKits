package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// StandaloneComposeWorkloadVerifyObservation is the Verify readback of one
// applied standalone Compose workload: the project Apply started and waited
// for, observed again through the same operations owner.
type StandaloneComposeWorkloadVerifyObservation struct {
	WorkloadRef    string
	ModuleRef      string
	ProjectRef     string
	InstanceRef    string
	ArtifactDigest string
	// Status is ready, or degraded when a component the bundle admits as
	// degraded is down; a blocking component that is missing, stopped or
	// unhealthy fails the verification instead.
	Status     string
	Components []SelectedPaaSComponentObservation
	Route      SelectedPaaSRouteObservation
}

type standaloneComposeReadinessObserver interface {
	observeReadyWorkload(context.Context, SelectedPaaSWorkloadDeployment) (SelectedPaaSWorkloadObservation, error)
}

// AppliedStandaloneComposeWorkloads selects every standalone Compose workload
// the sealed applied request placed on the local binding and rebuilds the
// deployment its executor applied. Targets of other hosts are not this host's
// to observe.
func AppliedStandaloneComposeWorkloads(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding) ([]SelectedPaaSWorkloadDeployment, error) {
	return appliedSelectedPaaSWorkloads(request, binding, standaloneComposeAdapterRef, standaloneComposeModuleRef)
}

func appliedSelectedPaaSWorkloads(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, adapterRef, adapterModuleRef string) ([]SelectedPaaSWorkloadDeployment, error) {
	if len(request.RuntimeTargets) == 0 {
		return nil, nil
	}
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("validate applied runtime custody: %w", err)
	}
	var deployments []SelectedPaaSWorkloadDeployment
	for _, target := range request.RuntimeTargets {
		if target.RuntimeAdapter == nil || target.RuntimeAdapter.ID != adapterRef ||
			target.RuntimeAdapter.ModuleRef != adapterModuleRef ||
			!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
			target.ExecutionChannelRef != binding.ExecutionChannelRef {
			continue
		}
		deployment, err := selectedPaaSDeploymentFromApplied(target, request.Artifacts, adapterRef, adapterModuleRef)
		if err != nil {
			return nil, fmt.Errorf("workload %q: %w", target.WorkloadRef, err)
		}
		deployments = append(deployments, deployment)
	}
	sort.Slice(deployments, func(i, j int) bool { return deployments[i].WorkloadRef < deployments[j].WorkloadRef })
	return deployments, nil
}

// VerifyAppliedStandaloneComposeWorkloads observes every applied standalone
// Compose workload of the local binding through the operations owner Apply
// used and applies the product-owned observation contract. It returns the
// observations of the ready workloads and one error naming every workload,
// project and component that is not ready.
func VerifyAppliedStandaloneComposeWorkloads(
	ctx context.Context,
	request runtimeexecutor.ExecutionRequest,
	binding LocalTargetBinding,
	operations SelectedPaaSWorkloadOperations,
) ([]StandaloneComposeWorkloadVerifyObservation, error) {
	if ctx == nil || operations == nil {
		return nil, errors.New("standalone Compose workload verification requires a context and operations owner")
	}
	ctx, cancel := context.WithTimeout(ctx, standaloneComposeVerifyReadinessBudget)
	defer cancel()
	deployments, err := AppliedStandaloneComposeWorkloads(request, binding)
	if err != nil {
		return nil, err
	}
	return verifySelectedPaaSDeployments(ctx, deployments, operations)
}

// VerifyAppliedCoolifyWorkloads observes every applied Coolify API workload on
// this node through the same Coolify owner Apply used. These workloads own no
// OpenTofu root; this observation is their runtime verification.
func VerifyAppliedCoolifyWorkloads(
	ctx context.Context,
	request runtimeexecutor.ExecutionRequest,
	binding LocalTargetBinding,
	operations SelectedPaaSWorkloadOperations,
) ([]StandaloneComposeWorkloadVerifyObservation, error) {
	if ctx == nil || operations == nil {
		return nil, errors.New("Coolify workload verification requires a context and operations owner")
	}
	ctx, cancel := context.WithTimeout(ctx, standaloneComposeVerifyReadinessBudget)
	defer cancel()
	deployments, err := appliedSelectedPaaSWorkloads(request, binding, "coolify", "stackkits-coolify-runtime")
	if err != nil || len(deployments) == 0 {
		return nil, err
	}
	return verifySelectedPaaSDeployments(ctx, deployments, operations)
}

func verifySelectedPaaSDeployments(ctx context.Context, deployments []SelectedPaaSWorkloadDeployment, operations SelectedPaaSWorkloadOperations) ([]StandaloneComposeWorkloadVerifyObservation, error) {
	validate := ValidateSelectedPaaSWorkloadObservation
	if validator, ok := operations.(SelectedPaaSWorkloadObservationValidator); ok {
		validate = validator.ValidateWorkloadObservation
	}
	observations := make([]StandaloneComposeWorkloadVerifyObservation, 0, len(deployments))
	var failures []error
	for _, deployment := range deployments {
		projectRef := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
		observe := operations.ObserveWorkload
		if readiness, ok := operations.(standaloneComposeReadinessObserver); ok {
			observe = readiness.observeReadyWorkload
		}
		observation, err := observe(ctx, deployment)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return observations, fmt.Errorf("workload %q readiness interrupted: %w", deployment.WorkloadRef, err)
		}
		if ctx.Err() != nil {
			return observations, fmt.Errorf("standalone Compose verification interrupted: %w", ctx.Err())
		}
		if err == nil {
			err = validate(deployment, observation)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("workload %q (Compose project %s) is not ready: %w", deployment.WorkloadRef, projectRef, err))
			continue
		}
		status := "ready"
		if observation.Status != "running" {
			status = observation.Status
		}
		components := append([]SelectedPaaSComponentObservation(nil), observation.Components...)
		sort.Slice(components, func(i, j int) bool { return components[i].ID < components[j].ID })
		observations = append(observations, StandaloneComposeWorkloadVerifyObservation{
			WorkloadRef: deployment.WorkloadRef, ModuleRef: deployment.ModuleRef, ProjectRef: projectRef,
			InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest,
			Status: status, Components: components, Route: observation.Route,
		})
	}
	return observations, joinStandaloneComposeVerifyFailures(failures)
}

// joinStandaloneComposeVerifyFailures keeps a typed drift difference visible
// to the caller only when every failure is drift; a workload that failed
// observation for any other reason must not be masked as reportable drift.
func joinStandaloneComposeVerifyFailures(failures []error) error {
	allDrift := true
	for _, failure := range failures {
		var drift *StandaloneComposeDriftError
		if !errors.As(failure, &drift) {
			allDrift = false
		}
	}
	if allDrift {
		return errors.Join(failures...)
	}
	flattened := make([]error, len(failures))
	for i, failure := range failures {
		flattened[i] = errors.New(failure.Error())
	}
	return errors.Join(flattened...)
}

// standaloneComposeDeploymentFromApplied rebuilds the deployment of one
// applied standalone Compose target from the sealed artifacts: the workload
// bundle the target owns and the adapter and agent artifacts its binding
// references. Removal and Verify share it so both observe the exact project
// Apply created.
func standaloneComposeDeploymentFromApplied(target runtimeexecutor.RuntimeTarget, artifacts []runtimeexecutor.Artifact) (SelectedPaaSWorkloadDeployment, error) {
	return selectedPaaSDeploymentFromApplied(target, artifacts, standaloneComposeAdapterRef, standaloneComposeModuleRef)
}

// selectedPaaSDeploymentFromApplied reconstructs one applied workload bound to
// the exact named adapter from the sealed Apply request.
func selectedPaaSDeploymentFromApplied(target runtimeexecutor.RuntimeTarget, artifacts []runtimeexecutor.Artifact, adapterRef, adapterModuleRef string) (SelectedPaaSWorkloadDeployment, error) {
	if target.RuntimeAdapter == nil || target.RuntimeAdapter.ID != adapterRef ||
		target.RuntimeAdapter.ModuleRef != adapterModuleRef {
		return SelectedPaaSWorkloadDeployment{}, fmt.Errorf("applied workload is not bound to the %s adapter", adapterRef)
	}
	if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 {
		return SelectedPaaSWorkloadDeployment{}, fmt.Errorf("applied %s workload requires one exact Site and node", adapterRef)
	}
	if len(target.ArtifactRefs) != 1 {
		return SelectedPaaSWorkloadDeployment{}, fmt.Errorf("applied %s workload requires exactly one workload artifact", adapterRef)
	}
	adapterRefs := map[string]struct{}{}
	for _, ref := range target.RuntimeAdapter.ArtifactRefs {
		adapterRefs[ref] = struct{}{}
	}
	for _, agent := range target.RuntimeAdapter.Agents {
		for _, ref := range agent.ArtifactRefs {
			adapterRefs[ref] = struct{}{}
		}
	}
	var artifactContent []byte
	var artifactID, artifactDigest string
	var adapterArtifacts []runtimeexecutor.Artifact
	for _, artifact := range artifacts {
		if artifact.ID == target.ArtifactRefs[0] {
			artifactContent = append([]byte(nil), artifact.Content...)
			artifactID, artifactDigest = artifact.ID, artifact.Digest
			continue
		}
		if _, adapter := adapterRefs[artifact.ID]; adapter {
			adapterArtifacts = append(adapterArtifacts, artifact)
		}
	}
	if len(artifactContent) == 0 || artifactDigest == "" {
		return SelectedPaaSWorkloadDeployment{}, fmt.Errorf("applied %s workload artifact is absent", adapterRef)
	}
	sum := sha256.Sum256(artifactContent)
	if artifactDigest != "sha256:"+hex.EncodeToString(sum[:]) {
		return SelectedPaaSWorkloadDeployment{}, fmt.Errorf("applied %s workload artifact digest does not match its content", adapterRef)
	}
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(artifactContent)
	if err != nil {
		return SelectedPaaSWorkloadDeployment{}, fmt.Errorf("validate applied %s workload bundle: %w", adapterRef, err)
	}
	if bundle.WorkloadRef != target.WorkloadRef || bundle.ModuleRef != target.ModuleRef ||
		bundle.InstanceRef != target.InstanceRef || bundle.SiteRef != target.SiteRefs[0] ||
		bundle.NodeRef != target.NodeRefs[0] {
		return SelectedPaaSWorkloadDeployment{}, fmt.Errorf("applied %s workload bundle differs from the sealed target", adapterRef)
	}
	return SelectedPaaSWorkloadDeployment{
		WorkloadRef: bundle.WorkloadRef, ModuleRef: bundle.ModuleRef, UnitRef: target.UnitRef,
		Release: bundle.Release, SiteRef: bundle.SiteRef, NodeRef: bundle.NodeRef,
		InstanceRef: bundle.InstanceRef, ExecutionChannelRef: target.ExecutionChannelRef,
		ArtifactRef: artifactID, ArtifactDigest: artifactDigest, Bundle: artifactContent,
		Route: bundle.Route, RuntimeAdapter: *target.RuntimeAdapter, AdapterArtifacts: adapterArtifacts,
	}, nil
}

// standaloneComposeMissingComponents names the authorized components the
// Compose status readback does not carry, so a removed or never-created
// container is reported by its component instead of as a count mismatch.
func standaloneComposeMissingComponents(
	components []architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	statuses map[string]standaloneComposePS,
) string {
	var missing []string
	for _, component := range components {
		if _, exists := statuses[component.ID]; !exists {
			missing = append(missing, component.ID)
		}
	}
	sort.Strings(missing)
	return strings.Join(missing, ", ")
}
