package restoreactivation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"gopkg.in/yaml.v3"
)

// MarketplaceRuntimeCustody retains the native installation selected by the
// verified plan. It grants no authority over applications deployed by a PaaS.
type MarketplaceRuntimeCustody struct {
	Document architecturev2renderer.MarketplaceCoolifyDocument
	Runtime  ComposeRuntime
	Volumes  []Volume
}

func DeriveMarketplaceRuntimeCustody(workspace string, plan generationartifact.VerifiedPlan, manifest generationartifact.ArtifactManifest, operationID string) (*MarketplaceRuntimeCustody, error) {
	if !activationOperationPattern.MatchString(operationID) || manifest.Binding != plan.Binding() {
		return nil, errors.New("restoreactivation: Marketplace requires the verified plan, manifest and activation identity")
	}
	var document map[string]any
	if err := json.Unmarshal(plan.Canonical(), &document); err != nil {
		return nil, err
	}
	return deriveMarketplaceRuntimeCustody(workspace, document, plan.OutputRoot(), manifest, operationID)
}

func deriveMarketplaceRuntimeCustody(workspace string, plan map[string]any, outputRoot string, manifest generationartifact.ArtifactManifest, operationID string) (*MarketplaceRuntimeCustody, error) {
	graphs, err := deriveStandaloneComposeRuntimeContracts(plan)
	if err != nil {
		return nil, err
	}
	graph, selected := graphs[localbackuppolicy.MarketplaceComposeProject]
	if !selected {
		return nil, nil
	}
	if graph.WorkloadRef != "marketplace" {
		return nil, errors.New("restoreactivation: Marketplace installation graph has a different owner")
	}
	workloads, err := array(plan, "workloads")
	if err != nil {
		return nil, err
	}
	var alternative map[string]any
	for _, raw := range workloads {
		workload, ok := raw.(map[string]any)
		if !ok || text(workload, "id") != "marketplace" {
			continue
		}
		candidate, candidateErr := object(workload, "alternative")
		runtime, runtimeErr := object(candidate, "runtime")
		nodes, nodeErr := stringArray(workload, "nodeRefs")
		sites, siteErr := stringArray(workload, "siteRefs")
		if alternative != nil || candidateErr != nil || runtimeErr != nil || text(workload, "kind") != "application" ||
			text(candidate, "moduleRef") != architecturev2renderer.MarketplaceCoolifyModuleRef || text(candidate, "providerRef") != "stackkits-marketplace" ||
			text(runtime, "kind") != "host" || text(runtime, "delivery") != "stackkit" || runtime["adapter"] != nil ||
			nodeErr != nil || !slices.Equal(nodes, []string{graph.NodeRef}) || siteErr != nil || !slices.Equal(sites, []string{graph.SiteRef}) {
			return nil, errors.New("restoreactivation: Marketplace requires its exact native installation placement")
		}
		alternative = candidate
	}
	if alternative == nil {
		return nil, errors.New("restoreactivation: Marketplace backup graph has no selected native installation")
	}
	artifact, err := marketplaceInstallationArtifact(plan, outputRoot, manifest, graph)
	if err != nil {
		return nil, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer transaction.Close()
	raw, err := readStandaloneComposeCustodyFile(transaction, artifact.Path, false)
	if err != nil || runtimeFileDigest(raw) != artifact.SHA256 {
		return nil, errors.New("restoreactivation: Marketplace installation differs from its verified generation manifest")
	}
	d, err := architecturev2renderer.ParseMarketplaceCoolifyDocument(raw)
	if err != nil || d.SiteRef != graph.SiteRef || d.NodeRef != graph.NodeRef || len(d.Components) != len(graph.Components) {
		return nil, errors.New("restoreactivation: Marketplace recipe differs from its selected recovery graph")
	}
	for _, component := range graph.Components {
		index := slices.IndexFunc(d.Components, func(image architecturev2renderer.KomodoBootstrapImage) bool {
			return image.ID == component.ComponentRef
		})
		if index < 0 || component.Lifecycle != "daemon" || component.HealthFailure != "" ||
			component.ImageRef != d.Components[index].ImageRef || component.ImageDigest != d.Components[index].ImageDigest {
			return nil, errors.New("restoreactivation: Marketplace installation image or lifecycle differs from its recovery graph")
		}
	}
	infrastructure, err := object(alternative, "infrastructure")
	if err != nil {
		return nil, err
	}
	storage, err := object(infrastructure, "storageAllocation")
	if err != nil {
		return nil, err
	}
	allocations, err := array(storage, "allocations")
	if err != nil {
		return nil, err
	}
	volumes, logical, targets, err := marketplaceRecoveryVolumes(allocations, graph.NodeRef, operationID)
	if err != nil {
		return nil, err
	}
	composePath := ".stackkit/runtime/marketplace/compose.yaml"
	environmentPath := ".stackkit/runtime/marketplace/coolify.env"
	custody, err := bindComposeRuntimeCustody(workspace, graph.ComposeProject, composePath, environmentPath, logical, graph)
	if err != nil {
		return nil, fmt.Errorf("restoreactivation: bind native Marketplace runtime: %w", err)
	}
	compose, err := readStandaloneComposeCustodyFile(transaction, composePath, false)
	if err != nil || runtimeFileDigest(compose) != custody.Runtime.Digest {
		return nil, errors.New("restoreactivation: Marketplace runtime changed during admission")
	}
	var runtime standaloneComposeCustodyDocument
	if yaml.Unmarshal(compose, &runtime) != nil {
		return nil, errors.New("restoreactivation: Marketplace runtime is not a Compose document")
	}
	for component, service := range runtime.Services {
		for _, rawMount := range service.Volumes {
			mount, ok := rawMount.(string)
			if !ok {
				return nil, errors.New("restoreactivation: Marketplace volume mount has an unsupported format")
			}
			source, target, found := strings.Cut(mount, ":")
			if !found {
				return nil, errors.New("restoreactivation: Marketplace volume mount is incomplete")
			}
			if strings.HasPrefix(source, "marketplace-") && targets[component+"\x00"+source] != target {
				return nil, errors.New("restoreactivation: Marketplace volume is mounted outside its selected component or target")
			}
		}
	}
	return &MarketplaceRuntimeCustody{Document: d, Runtime: custody.Runtime, Volumes: volumes}, nil
}

func marketplaceRecoveryVolumes(allocations []any, node, operationID string) ([]Volume, []string, map[string]string, error) {
	volumes := make([]Volume, 0, len(allocations))
	logical := make([]string, 0, len(allocations))
	targets := map[string]string{}
	operationHash := sha256.Sum256([]byte(operationID))
	for _, raw := range allocations {
		allocation, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, nil, errors.New("restoreactivation: Marketplace storage allocation is invalid")
		}
		component, volume := text(allocation, "componentRef"), text(allocation, "volumeRef")
		target, admitted := localbackuppolicy.MarketplaceVolumeTarget(component, volume)
		name := localbackuppolicy.ApplicationLogicalVolumeName("marketplace", component, volume)
		key := component + "\x00" + name
		if !admitted || targets[key] != "" || allocation["backup"] != true || text(allocation, "class") != "persistent" || text(allocation, "target") != target {
			return nil, nil, nil, errors.New("restoreactivation: Marketplace recovery admits only its selected persistent installation volumes")
		}
		targets[key] = target
		logical = append(logical, name)
		live := localbackuppolicy.ApplicationVolumeName("marketplace", node, component, volume)
		volumes = append(volumes, Volume{ComponentRef: component, LogicalName: name, ComposeProject: localbackuppolicy.MarketplaceComposeProject, LiveName: live, RollbackName: live + "-rollback-" + hex.EncodeToString(operationHash[:8])})
	}
	if len(volumes) != 8 {
		return nil, nil, nil, errors.New("restoreactivation: Marketplace recovery must retain its complete installation volume set")
	}
	return volumes, logical, targets, nil
}

func marketplaceInstallationArtifact(plan map[string]any, outputRoot string, manifest generationartifact.ArtifactManifest, graph localbackuppolicy.ApplicationRuntime) (generationartifact.RenderedArtifact, error) {
	modules, err := array(plan, "modules")
	if err != nil {
		return generationartifact.RenderedArtifact{}, err
	}
	contract := architecturev2renderer.MarketplaceCoolifyRendererContract()
	var selected *generationartifact.RenderedArtifact
	for _, rawModule := range modules {
		module, ok := rawModule.(map[string]any)
		if !ok || text(module, "id") != architecturev2renderer.MarketplaceCoolifyModuleRef {
			continue
		}
		units, _ := array(module, "renderUnits")
		for _, rawUnit := range units {
			unit, ok := rawUnit.(map[string]any)
			if !ok || text(unit, "id") != architecturev2renderer.MarketplaceCoolifyUnitRef {
				continue
			}
			if text(module, "renderTarget") != renderTargetCompose || text(unit, "kind") != contract.Kind || text(unit, "rendererRef") != contract.RendererRef || text(unit, "templateRef") != contract.TemplateRef || text(unit, "version") != contract.Version || text(unit, "contractHash") != contract.ContractHash {
				return generationartifact.RenderedArtifact{}, errors.New("restoreactivation: Marketplace installation renderer is outside its current native contract")
			}
			instances, _ := array(unit, "instances")
			for _, rawInstance := range instances {
				instance, ok := rawInstance.(map[string]any)
				if !ok || text(instance, "siteRef") != graph.SiteRef || text(instance, "nodeRef") != graph.NodeRef || text(instance, "scope") != "node-local" {
					continue
				}
				outputs, _ := array(instance, "outputs")
				for _, rawOutput := range outputs {
					output, ok := rawOutput.(map[string]any)
					if !ok || text(output, "ref") != architecturev2renderer.MarketplaceCoolifyOutputRef {
						continue
					}
					for _, artifact := range manifest.Artifacts {
						if artifact.ID != text(output, "artifactRef") {
							continue
						}
						if selected != nil || artifact.Path != path.Join(outputRoot, text(output, "path")) || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0600" {
							return generationartifact.RenderedArtifact{}, errors.New("restoreactivation: Marketplace installation artifact has an ambiguous or different identity")
						}
						copy := artifact
						selected = &copy
					}
				}
			}
		}
	}
	if selected == nil {
		return generationartifact.RenderedArtifact{}, errors.New("restoreactivation: Marketplace installation artifact is absent from the verified manifest")
	}
	return *selected, nil
}
