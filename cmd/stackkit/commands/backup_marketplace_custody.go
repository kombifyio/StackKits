package commands

import (
	"context"
	"errors"
	"slices"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

// nativeBackupMarketplaceDocument uses the existing signed Apply request
// custody. Native Marketplace has no execution adapter and therefore cannot
// be admitted through an adapter bundle or mutable Compose discovery.
func nativeBackupMarketplaceDocument(ctx context.Context, authority nativeV2BackupAuthority) (architecturev2renderer.MarketplaceCoolifyDocument, error) {
	var graph *localbackuppolicy.ApplicationRuntime
	for _, candidate := range authority.Policy.SourceProjection().ApplicationRuntimes {
		if candidate.WorkloadRef == "marketplace" {
			if graph != nil {
				return architecturev2renderer.MarketplaceCoolifyDocument{}, errors.New("Marketplace backup has an ambiguous node graph")
			}
			copy := candidate
			graph = &copy
		}
	}
	if graph == nil {
		return architecturev2renderer.MarketplaceCoolifyDocument{}, errors.New("Marketplace backup requires its current signed node Apply authority")
	}
	owner, err := localevidence.LoadOwnerCustody(authority.WorkspaceRoot)
	if err != nil {
		return architecturev2renderer.MarketplaceCoolifyDocument{}, err
	}
	var request runtimeexecutor.ExecutionRequest
	if a := authority.AppliedAuthority; a != nil && a.Lineage == authority.Lineage && a.WorkspaceRoot == authority.WorkspaceRoot {
		request, err = loadArchitectureV2AppliedRuntimeRequest(ctx, a.WorkspaceRoot, a.Plan, a.Manifest)
	} else if h := authority.HistoricalCustody; h != nil && h.Binding() == authority.Lineage.Binding && h.ManifestHash() == authority.Lineage.ManifestHash && h.OutputRoot() == authority.OutputRoot {
		request = h.Request()
	} else {
		return architecturev2renderer.MarketplaceCoolifyDocument{}, errors.New("Marketplace backup requires signed node Apply custody")
	}
	if err != nil {
		return architecturev2renderer.MarketplaceCoolifyDocument{}, err
	}
	d, err := nativeBackupMarketplaceInstallationDocument(request, nativehost.LocalTargetBinding{
		SiteRef: owner.Binding.SiteRef, NodeRef: owner.Binding.NodeRef, ExecutionChannelRef: owner.Binding.ChannelRef,
	})
	if err != nil {
		return d, err
	}
	if d.SiteRef != graph.SiteRef || d.NodeRef != graph.NodeRef || graph.ComposeProject != localbackuppolicy.MarketplaceComposeProject || len(d.Components) != len(graph.Components) {
		return architecturev2renderer.MarketplaceCoolifyDocument{}, errors.New("Marketplace backup graph differs from its signed installation placement")
	}
	roles := map[string]string{"coolify": "application", "postgres": "database", "redis": "cache"}
	for _, component := range graph.Components {
		index := slices.IndexFunc(d.Components, func(c architecturev2renderer.KomodoBootstrapImage) bool { return c.ID == component.ComponentRef })
		dependencies := []string{}
		if component.ComponentRef == "coolify" {
			dependencies = []string{"postgres", "redis"}
		}
		if index < 0 || roles[component.ComponentRef] != component.Role || component.Lifecycle != "daemon" || (component.HealthFailure != "" && component.HealthFailure != "blocking") || component.ImageRef != d.Components[index].ImageRef || component.ImageDigest != d.Components[index].ImageDigest || !slices.Equal(dependencies, component.DependsOn) {
			return architecturev2renderer.MarketplaceCoolifyDocument{}, errors.New("Marketplace backup graph differs from its signed installation component contract")
		}
	}
	return d, nil
}

func nativeBackupMarketplaceInstallationDocument(request runtimeexecutor.ExecutionRequest, binding nativehost.LocalTargetBinding) (architecturev2renderer.MarketplaceCoolifyDocument, error) {
	var document *architecturev2renderer.MarketplaceCoolifyDocument
	for _, target := range request.RuntimeTargets {
		if target.WorkloadRef != "marketplace" {
			continue
		}
		if document != nil {
			return architecturev2renderer.MarketplaceCoolifyDocument{}, errors.New("Marketplace backup has multiple signed installation targets")
		}
		// Match the central dispatcher's child artifact scope. The parent is
		// already authenticated; unrelated application inputs grant no backup
		// authority and must not reach the strict installation selector.
		artifacts := make([]runtimeexecutor.Artifact, 0)
		for _, artifact := range request.Artifacts {
			if artifact.OwnerKind == "plan" || slices.Contains(target.ArtifactRefs, artifact.ID) {
				artifacts = append(artifacts, artifact)
			}
		}
		d, err := nativehost.MarketplaceInstallationDocument(target, artifacts, binding)
		if err != nil {
			return d, err
		}
		document = &d
	}
	if document == nil {
		return architecturev2renderer.MarketplaceCoolifyDocument{}, errors.New("Marketplace backup has no signed installation target")
	}
	return *document, nil
}
