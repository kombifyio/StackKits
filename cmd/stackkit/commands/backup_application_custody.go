package commands

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
)

// nativeBackupApplicationCustody uses the same signed-Apply bundle selector as
// application setup, then the existing runtime adapter's persisted Compose
// observation. The backup engine receives no artifact path or daemon choice.
func nativeBackupApplicationCustody(authority nativeV2BackupAuthority) (backupexec.ApplicationContainerCustodyVerifier, error) {
	deployments, err := nativeBackupApplicationDeployments(authority)
	if err != nil {
		return nil, err
	}
	runtimes := authority.Policy.SourceProjection().ApplicationRuntimes
	if len(runtimes) == 0 {
		return nil, nil
	}
	return func(ctx context.Context, requested localbackuppolicy.ApplicationRuntime) (map[string]string, error) {
		index := slices.IndexFunc(runtimes, func(graph localbackuppolicy.ApplicationRuntime) bool {
			return graph.ComposeProject == requested.ComposeProject
		})
		if index < 0 || !reflect.DeepEqual(requested, runtimes[index]) {
			return nil, errors.New("application backup custody request differs from the held source graph")
		}
		return runtimeexecutorlocal.ObserveStandaloneComposeContainerCustody(ctx, authority.WorkspaceRoot, deployments[requested.ComposeProject])
	}, nil
}

// nativeBackupApplicationDeployments selects each application bundle from the
// signed Apply authority and checks it against the held source graph. Restore
// verification and source custody therefore use one exact deployment map.
func nativeBackupApplicationDeployments(authority nativeV2BackupAuthority) (map[string]runtimeexecutorlocal.SelectedPaaSWorkloadDeployment, error) {
	runtimes := authority.Policy.SourceProjection().ApplicationRuntimes
	if len(runtimes) == 0 {
		return nil, nil
	}
	if authority.AppliedAuthority == nil || authority.AppliedAuthority.Lineage != authority.Lineage || authority.AppliedAuthority.WorkspaceRoot != authority.WorkspaceRoot {
		return nil, errors.New("application backup requires the current signed application Apply authority")
	}
	deployments := make(map[string]runtimeexecutorlocal.SelectedPaaSWorkloadDeployment, len(runtimes))
	for _, graph := range runtimes {
		deployment, err := nativeAppliedWorkloadDeployment(*authority.AppliedAuthority, graph.WorkloadRef)
		if err != nil {
			return nil, err
		}
		bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(deployment.Bundle)
		if err != nil {
			return nil, err
		}
		if bundle.SiteRef != graph.SiteRef || bundle.NodeRef != graph.NodeRef || len(bundle.Components) != len(graph.Components) {
			return nil, errors.New("backup graph differs from its signed workload placement or component set")
		}
		for _, component := range graph.Components {
			index := slices.IndexFunc(bundle.Components, func(candidate architecturev2renderer.ApplicationDeliveryComponentDescriptor) bool {
				return candidate.ID == component.ComponentRef
			})
			if index < 0 {
				return nil, errors.New("backup graph component is absent from the signed workload")
			}
			selected := bundle.Components[index]
			dependencies := slices.Clone(selected.DependsOn)
			slices.Sort(dependencies)
			if selected.Role != component.Role || selected.Lifecycle != component.Lifecycle || selected.ImageRef != component.ImageRef || selected.ImageDigest != component.ImageDigest || !slices.Equal(dependencies, component.DependsOn) {
				return nil, errors.New("backup graph differs from the signed workload component contract")
			}
		}
		if _, duplicate := deployments[graph.ComposeProject]; duplicate {
			return nil, errors.New("application backup has an ambiguous Compose placement")
		}
		deployments[graph.ComposeProject] = deployment
	}
	return deployments, nil
}

func verifyNativeV2BackupApplications(ctx context.Context, authority nativeV2BackupAuthority) error {
	runtimes := authority.Policy.SourceProjection().ApplicationRuntimes
	if len(runtimes) == 0 {
		return nil
	}
	deployments, err := nativeBackupApplicationDeployments(authority)
	if err != nil {
		return err
	}
	operations, err := runtimeexecutorlocal.NewOSStandaloneComposeWorkloadOperations(authority.WorkspaceRoot)
	if err != nil {
		return fmt.Errorf("initialize selected application restore verifier: %w", err)
	}
	validator, ok := operations.(runtimeexecutorlocal.SelectedPaaSWorkloadObservationValidator)
	if !ok {
		return errors.New("selected application restore verifier has no product-owned observation validator")
	}
	for _, graph := range runtimes {
		deployment, exists := deployments[graph.ComposeProject]
		if !exists {
			return errors.New("selected application restore verifier has no exact signed deployment")
		}
		observation, err := operations.ObserveWorkload(ctx, deployment)
		if err != nil {
			return fmt.Errorf("observe restored selected application %q: %w", graph.WorkloadRef, err)
		}
		if err := validator.ValidateWorkloadObservation(deployment, observation); err != nil {
			return fmt.Errorf("validate restored selected application %q: %w", graph.WorkloadRef, err)
		}
	}
	return nil
}
