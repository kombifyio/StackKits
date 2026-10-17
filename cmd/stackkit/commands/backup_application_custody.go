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
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
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
		if requested.WorkloadRef == "marketplace" {
			d, err := nativeBackupMarketplaceDocument(ctx, authority)
			if err != nil {
				return nil, err
			}
			return nativehost.ObserveMarketplaceContainerCustody(ctx, authority.WorkspaceRoot, d)
		}
		deployment := deployments[requested.ComposeProject]
		if deployment.RuntimeAdapter.ID == "coolify" {
			return nativehost.ObserveCoolifyContainerCustody(ctx, authority.WorkspaceRoot, deployment)
		}
		if deployment.RuntimeAdapter.ID == "komodo" {
			return nativehost.ObserveKomodoContainerCustody(ctx, authority.WorkspaceRoot, deployment)
		}
		return nativehost.ObserveStandaloneComposeContainerCustody(ctx, authority.WorkspaceRoot, deployment)
	}, nil
}

// nativeBackupApplicationDeployments selects each application bundle from the
// signed Apply authority and checks it against the held source graph. Restore
// verification and source custody therefore use one exact deployment map.
func nativeBackupApplicationDeployments(authority nativeV2BackupAuthority) (map[string]nativehost.SelectedPaaSWorkloadDeployment, error) {
	runtimes := authority.Policy.SourceProjection().ApplicationRuntimes
	if len(runtimes) == 0 {
		return nil, nil
	}
	if (authority.AppliedAuthority == nil || authority.AppliedAuthority.Lineage != authority.Lineage || authority.AppliedAuthority.WorkspaceRoot != authority.WorkspaceRoot) && authority.HistoricalCustody == nil {
		return nil, errors.New("application backup requires the current signed application Apply authority")
	}
	deployments := make(map[string]nativehost.SelectedPaaSWorkloadDeployment, len(runtimes))
	for _, graph := range runtimes {
		if graph.WorkloadRef == "marketplace" {
			continue
		}
		var deployment nativehost.SelectedPaaSWorkloadDeployment
		var err error
		if authority.AppliedAuthority != nil {
			deployment, err = nativeAppliedWorkloadDeploymentForBackup(*authority.AppliedAuthority, graph.WorkloadRef)
		} else {
			h := authority.HistoricalCustody
			if h.Binding() != authority.Lineage.Binding || h.ManifestHash() != authority.Lineage.ManifestHash || h.OutputRoot() != authority.OutputRoot {
				return nil, errors.New("historical application custody differs from the held source lineage")
			}
			owner, loadErr := localevidence.LoadOwnerCustody(authority.WorkspaceRoot)
			if loadErr != nil {
				return nil, loadErr
			}
			data := nativeAppliedWorkloadData{WorkspaceRoot: authority.WorkspaceRoot, Owner: owner, Manifest: authority.HistoricalManifest, AppliedWorkloads: h.AppliedWorkloads()}
			deployment, err = nativeAppliedWorkloadDeploymentDataForLifecycle(data, h.Requirements(), func(module, unit string) (string, bool, error) {
				return architecturev2renderer.HistoricalRuntimeEntryComponentRef(authority.HistoricalPlan, module, unit)
			}, graph.WorkloadRef, false)
		}
		if err != nil {
			return nil, err
		}
		bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(deployment.Bundle)
		if err != nil {
			return nil, err
		}
		if bundle.SiteRef != graph.SiteRef || bundle.NodeRef != graph.NodeRef || len(bundle.Components) != len(graph.Components) {
			return nil, fmt.Errorf("backup graph of workload %s differs from its signed workload placement or component set", graph.WorkloadRef)
		}
		for _, component := range graph.Components {
			index := slices.IndexFunc(bundle.Components, func(candidate architecturev2renderer.ApplicationDeliveryComponentDescriptor) bool {
				return candidate.ID == component.ComponentRef
			})
			if index < 0 {
				return nil, fmt.Errorf("backup graph component %s is absent from the signed workload %s", component.ComponentRef, graph.WorkloadRef)
			}
			selected := bundle.Components[index]
			dependencies := slices.Clone(selected.DependsOn)
			slices.Sort(dependencies)
			healthFailure := component.HealthFailure
			if healthFailure == "" {
				healthFailure = "blocking"
			}
			if selected.Role != component.Role || selected.Lifecycle != component.Lifecycle || selected.HealthFailure != healthFailure || selected.ImageRef != component.ImageRef || selected.ImageDigest != component.ImageDigest || !slices.Equal(dependencies, component.DependsOn) {
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
	for _, graph := range runtimes {
		if graph.WorkloadRef == "marketplace" {
			d, err := nativeBackupMarketplaceDocument(ctx, authority)
			if err != nil {
				return err
			}
			if err := nativehost.VerifyMarketplaceReady(ctx, authority.WorkspaceRoot, d); err != nil {
				return fmt.Errorf("verify restored Marketplace node installation: %w", err)
			}
		}
	}
	if len(deployments) == 0 {
		return nil
	}
	operations, err := nativehost.NewOSStandaloneComposeWorkloadOperations(authority.WorkspaceRoot)
	if err != nil {
		return fmt.Errorf("initialize selected application restore verifier: %w", err)
	}
	validator, ok := operations.(nativehost.SelectedPaaSWorkloadObservationValidator)
	if !ok {
		return errors.New("selected application restore verifier has no product-owned observation validator")
	}
	for _, graph := range runtimes {
		if graph.WorkloadRef == "marketplace" {
			continue
		}
		deployment, exists := deployments[graph.ComposeProject]
		if !exists {
			return errors.New("selected application restore verifier has no exact signed deployment")
		}
		var selectedOperations interface {
			ObserveWorkload(context.Context, nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSWorkloadObservation, error)
		} = operations
		selectedValidator := validator
		if deployment.RuntimeAdapter.ID == "coolify" {
			selectedOperations, err = nativehost.NewOSCoolifyWorkloadOperations(authority.WorkspaceRoot)
			if err != nil {
				return err
			}
			selectedValidator = selectedOperations.(nativehost.SelectedPaaSWorkloadObservationValidator)
		}
		if deployment.RuntimeAdapter.ID == "komodo" {
			selectedOperations, err = nativehost.NewOSKomodoWorkloadOperations(authority.WorkspaceRoot)
			if err != nil {
				return err
			}
			selectedValidator = selectedOperations.(nativehost.SelectedPaaSWorkloadObservationValidator)
		}
		observation, err := selectedOperations.ObserveWorkload(ctx, deployment)
		if err != nil {
			return fmt.Errorf("observe restored selected application %q: %w", graph.WorkloadRef, err)
		}
		if err := selectedValidator.ValidateWorkloadObservation(deployment, observation); err != nil {
			return fmt.Errorf("validate restored selected application %q: %w", graph.WorkloadRef, err)
		}
	}
	return nil
}
