package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/runtimeobservation"
)

type architectureV2RuntimeObservationInput struct {
	Plan               generationartifact.VerifiedPlan
	Access             *accessSummary
	Phase              runtimeobservation.Phase
	Source             runtimeobservation.Source
	Live               bool
	ObservedAt         time.Time
	RunID              string
	Apply              architecturev2.ApplyResultSummary
	Outcomes           architecturev2.ApplyObservationSummary
	FallbackSiteRef    string
	FallbackNodeRef    string
	FallbackChannelRef string
	RolloutEvidence    bool
	AccessEvidence     bool
	ProcessChannelRefs map[string]bool
}

type architectureV2ApplyCommandResult struct {
	SchemaVersion string                            `json:"schemaVersion"`
	Status        string                            `json:"status"`
	Apply         architecturev2.ApplyResultSummary `json:"apply"`
	Observations  []runtimeobservation.Observation  `json:"observations"`
	EvidenceLinks []runtimeobservation.EvidenceLink `json:"evidenceLinks"`
}

type runtimeObservationPlanProjection struct {
	StackID string `json:"stackId"`
	Modules []struct {
		RenderUnits []struct {
			ServiceEndpoints []struct {
				ServiceRef string `json:"serviceRef"`
				HealthRef  string `json:"healthRef"`
			} `json:"serviceEndpoints"`
			Instances []struct {
				SiteRef string `json:"siteRef"`
				NodeRef string `json:"nodeRef"`
			} `json:"instances"`
		} `json:"renderUnits"`
	} `json:"modules"`
}

type runtimeObservationScope struct {
	site, node, channel string
}

type runtimeObservationServicePlacement struct {
	ref, healthRef, site, node string
}

func buildArchitectureV2RuntimeObservations(input architectureV2RuntimeObservationInput) ([]runtimeobservation.Observation, error) {
	if len(input.Plan.Canonical()) == 0 {
		return nil, errors.New("runtime observation requires a verified ResolvedPlan")
	}
	var projection runtimeObservationPlanProjection
	if err := json.Unmarshal(input.Plan.Canonical(), &projection); err != nil {
		return nil, fmt.Errorf("decode runtime observation plan projection: %w", err)
	}
	projection.StackID = strings.TrimSpace(projection.StackID)
	if projection.StackID == "" || input.Apply.ResultHash == "" || input.ObservedAt.IsZero() || strings.TrimSpace(input.RunID) == "" {
		return nil, errors.New("runtime observation requires stack, Apply result, time, and run identities")
	}

	requirements := input.Plan.ApplyRequirements()
	hostChannel := make(map[string]string, len(requirements.Hosts))
	for _, host := range requirements.Hosts {
		if host.NodeRef != "" && host.ExecutionChannelRef != "" {
			hostChannel[host.NodeRef] = host.ExecutionChannelRef
		}
	}
	runtimeByID := make(map[string]generationartifact.ApplyRuntimeRequirement, len(requirements.RuntimeInstances))
	for _, item := range requirements.RuntimeInstances {
		runtimeByID[item.ID] = item
	}
	healthByID := make(map[string]generationartifact.ApplyHealthRequirement, len(requirements.HealthRequirements))
	for _, item := range requirements.HealthRequirements {
		healthByID[item.ID] = item
	}

	groups := map[runtimeObservationScope]*runtimeobservation.Observation{}
	for _, outcome := range input.Outcomes.Runtime {
		requirement, ok := runtimeByID[outcome.RequirementID]
		if !ok || requirement.InstanceRef != outcome.InstanceRef {
			return nil, fmt.Errorf("runtime observation outcome %q is not bound to the verified plan", outcome.RequirementID)
		}
		scope, err := observationScope(requirement.SiteRefs, requirement.NodeRefs, hostChannel, input)
		if err != nil {
			return nil, fmt.Errorf("runtime observation outcome %q: %w", outcome.RequirementID, err)
		}
		observation := groups[scope]
		if observation == nil {
			created := newRuntimeObservation(input, projection.StackID, scope)
			observation = &created
			groups[scope] = observation
		}
		observation.Runtime = append(observation.Runtime, runtimeobservation.RuntimeEvidence{
			RequirementID: outcome.RequirementID, InstanceRef: outcome.InstanceRef, Status: outcome.Status,
			ObservationRef: outcome.ObservationRef, ObservationDigest: outcome.ObservationDigest,
			SiteRef: scope.site, NodeRef: scope.node, ExecutionChannelRef: scope.channel,
		})
	}
	if len(groups) == 0 {
		scope := runtimeObservationScope{site: strings.TrimSpace(input.FallbackSiteRef), node: strings.TrimSpace(input.FallbackNodeRef), channel: strings.TrimSpace(input.FallbackChannelRef)}
		if scope.site == "" || scope.node == "" || scope.channel == "" {
			return nil, errors.New("runtime observation has no exact runtime or fallback Site/node/channel identity")
		}
		created := newRuntimeObservation(input, projection.StackID, scope)
		groups[scope] = &created
	}

	healthStatus := make(map[string]string, len(input.Outcomes.Health))
	for _, outcome := range input.Outcomes.Health {
		requirement, ok := healthByID[outcome.RequirementID]
		if !ok || requirement.TargetRef != outcome.TargetRef {
			return nil, fmt.Errorf("health observation outcome %q is not bound to the verified plan", outcome.RequirementID)
		}
		healthStatus[requirement.SourceRef] = outcome.Status
		for scope, observation := range groups {
			if !scopeMatches(requirement.SiteRefs, requirement.NodeRefs, scope) {
				continue
			}
			observation.Health = append(observation.Health, runtimeobservation.HealthEvidence{
				RequirementID: outcome.RequirementID, TargetRef: outcome.TargetRef, SourceRef: requirement.SourceRef,
				Status: outcome.Status, ObservationRef: outcome.ObservationRef, ObservationDigest: outcome.ObservationDigest,
				SiteRef: scope.site, NodeRef: scope.node,
			})
		}
	}

	placements := runtimeObservationServicePlacements(projection, input)
	accessByRef := map[string]accessService{}
	if input.Access != nil {
		for _, service := range input.Access.Services {
			accessByRef[service.Key] = service
		}
	}
	for scope, observation := range groups {
		seen := map[string]bool{}
		for _, placement := range placements {
			if placement.site != scope.site || placement.node != scope.node || seen[placement.ref] {
				continue
			}
			seen[placement.ref] = true
			service := runtimeobservation.Service{Ref: placement.ref, Status: runtimeobservation.StatusUnknown, Health: runtimeobservation.HealthUnknown, HealthRef: placement.healthRef}
			if access, ok := accessByRef[placement.ref]; ok {
				service.URL = access.URL
			}
			if healthStatus[placement.healthRef] == "healthy" {
				service.Status, service.Health = runtimeobservation.StatusRunning, runtimeobservation.HealthHealthy
			} else if healthStatus[placement.healthRef] != "" {
				service.Status, service.Health = runtimeobservation.StatusError, runtimeobservation.HealthUnhealthy
			}
			observation.Services = append(observation.Services, service)
		}
		if len(observation.Services) == 0 {
			for _, access := range accessByRef {
				observation.Services = append(observation.Services, runtimeobservation.Service{Ref: access.Key, URL: access.URL, Status: runtimeobservation.StatusUnknown, Health: runtimeobservation.HealthUnknown})
			}
		}
	}

	keys := make([]runtimeObservationScope, 0, len(groups))
	for scope := range groups {
		keys = append(keys, scope)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].site != keys[j].site {
			return keys[i].site < keys[j].site
		}
		if keys[i].node != keys[j].node {
			return keys[i].node < keys[j].node
		}
		return keys[i].channel < keys[j].channel
	})
	result := make([]runtimeobservation.Observation, 0, len(keys))
	for _, scope := range keys {
		observation := *groups[scope]
		sort.Slice(observation.Services, func(i, j int) bool { return observation.Services[i].Ref < observation.Services[j].Ref })
		sort.Slice(observation.Runtime, func(i, j int) bool {
			return observation.Runtime[i].RequirementID < observation.Runtime[j].RequirementID
		})
		sort.Slice(observation.Health, func(i, j int) bool { return observation.Health[i].RequirementID < observation.Health[j].RequirementID })
		sort.Slice(observation.EvidenceLinks, func(i, j int) bool {
			if observation.EvidenceLinks[i].Kind != observation.EvidenceLinks[j].Kind {
				return observation.EvidenceLinks[i].Kind < observation.EvidenceLinks[j].Kind
			}
			return observation.EvidenceLinks[i].Ref < observation.EvidenceLinks[j].Ref
		})
		if err := observation.Validate(); err != nil {
			return nil, err
		}
		result = append(result, observation)
	}
	return result, nil
}

func newRuntimeObservation(input architectureV2RuntimeObservationInput, stackID string, scope runtimeObservationScope) runtimeobservation.Observation {
	hash := strings.TrimPrefix(input.Apply.ResultHash, "sha256:")
	links := []runtimeobservation.EvidenceLink{
		{Kind: "resolved-plan", Ref: path.Join(input.Plan.OutputRoot(), ".stackkit/resolved-plan.json"), Digest: input.Plan.Binding().PlanHash},
		{Kind: "apply-result", Ref: ".stackkit/evidence/apply/results/" + hash + ".json", Digest: input.Apply.ResultHash},
		{Kind: "apply-result-receipt", Ref: ".stackkit/evidence/apply/receipts/" + hash + ".json"},
	}
	if input.AccessEvidence {
		links = append(links, runtimeobservation.EvidenceLink{Kind: "access-manifest", Ref: ".stackkit/access.json"})
	}
	if input.RolloutEvidence {
		links = append(links,
			runtimeobservation.EvidenceLink{Kind: "rollout-log", Ref: ".stackkit/logs/" + input.RunID + ".jsonl"},
			runtimeobservation.EvidenceLink{Kind: "rollout-summary", Ref: ".stackkit/runs/" + input.RunID + "/summary.json"},
		)
	}
	source := input.Source
	if source != runtimeobservation.SourceVerifiedApplyEvidence && input.ProcessChannelRefs[scope.channel] {
		source = runtimeobservation.SourceStandardProcess
	}
	return runtimeobservation.Observation{
		SchemaVersion: runtimeobservation.SchemaVersionV2, Phase: input.Phase, Source: source,
		ObservedAt: input.ObservedAt.UTC(), Live: input.Live,
		Identity: runtimeobservation.Identity{
			StackID: stackID, PlanHash: input.Plan.Binding().PlanHash, ApplyResultHash: input.Apply.ResultHash, RunID: input.RunID,
			SiteRef: scope.site, NodeRef: scope.node, ExecutionChannelRef: scope.channel,
		},
		Services: []runtimeobservation.Service{}, Runtime: []runtimeobservation.RuntimeEvidence{}, Health: []runtimeobservation.HealthEvidence{}, EvidenceLinks: links,
	}
}

func runtimeObservationProcessChannelRefs(configured *architectureV2ConfiguredStandardRuntime) map[string]bool {
	refs := map[string]bool{}
	if configured == nil {
		return refs
	}
	for _, binding := range configured.bindings {
		if channelRef := strings.TrimSpace(binding.ChannelRef); channelRef != "" {
			refs[channelRef] = true
		}
	}
	return refs
}

func runtimeObservationExecutionMode(observations []runtimeobservation.Observation, processChannels map[string]bool) string {
	hasProcess, hasLocal := false, false
	for _, observation := range observations {
		if processChannels[observation.Identity.ExecutionChannelRef] {
			hasProcess = true
		} else {
			hasLocal = true
		}
	}
	switch {
	case hasProcess && hasLocal:
		return "hybrid-runtime"
	case hasProcess:
		return "standard-process"
	default:
		return "local-runtime"
	}
}

func observationScope(siteRefs, nodeRefs []string, hostChannel map[string]string, input architectureV2RuntimeObservationInput) (runtimeObservationScope, error) {
	if len(siteRefs) != 1 || len(nodeRefs) != 1 {
		return runtimeObservationScope{}, errors.New("requires exactly one Site and node")
	}
	scope := runtimeObservationScope{site: siteRefs[0], node: nodeRefs[0], channel: hostChannel[nodeRefs[0]]}
	if scope.channel == "" && scope.site == input.FallbackSiteRef && scope.node == input.FallbackNodeRef {
		scope.channel = input.FallbackChannelRef
	}
	if scope.channel == "" {
		return runtimeObservationScope{}, errors.New("has no exact execution channel")
	}
	return scope, nil
}

func scopeMatches(siteRefs, nodeRefs []string, scope runtimeObservationScope) bool {
	return len(siteRefs) == 1 && len(nodeRefs) == 1 && siteRefs[0] == scope.site && nodeRefs[0] == scope.node
}

func runtimeObservationServicePlacements(projection runtimeObservationPlanProjection, input architectureV2RuntimeObservationInput) []runtimeObservationServicePlacement {
	var result []runtimeObservationServicePlacement
	for _, module := range projection.Modules {
		for _, unit := range module.RenderUnits {
			instances := unit.Instances
			if len(instances) == 0 && input.FallbackSiteRef != "" && input.FallbackNodeRef != "" {
				instances = append(instances, struct {
					SiteRef string `json:"siteRef"`
					NodeRef string `json:"nodeRef"`
				}{SiteRef: input.FallbackSiteRef, NodeRef: input.FallbackNodeRef})
			}
			for _, endpoint := range unit.ServiceEndpoints {
				ref := architectureV2AccessServiceKey(endpoint.ServiceRef)
				if ref == "" {
					continue
				}
				for _, instance := range instances {
					result = append(result, runtimeObservationServicePlacement{ref: ref, healthRef: endpoint.HealthRef, site: instance.SiteRef, node: instance.NodeRef})
				}
			}
		}
	}
	return result
}

func runtimeObservationRunID(now time.Time) string {
	if rolloutRecorder != nil {
		if runID := strings.TrimSpace(rolloutRecorder.RunID()); runID != "" {
			return runID
		}
	}
	if deployLog != nil {
		if runID := strings.TrimSpace(deployLog.RunID()); runID != "" {
			return runID
		}
	}
	return "runtime-observation-" + now.UTC().Format("20060102T150405.000000000Z")
}

func historicalRuntimeObservationIdentity(appliedAt time.Time) (time.Time, string) {
	observedAt := appliedAt.UTC()
	return observedAt, "runtime-observation-" + observedAt.Format("20060102T150405.000000000Z")
}

func unavailableArchitectureV2ProcessRuntime() *architectureV2RuntimeVerifySummary {
	return &architectureV2RuntimeVerifySummary{
		ExecutionMode: "standard-process",
		Live:          false,
		Status:        "unavailable",
		ServiceCount:  0,
		ProbeCount:    0,
	}
}

func readArchitectureV2AccessSummary(workspaceRoot string) (*accessSummary, error) {
	path := filepath.Join(workspaceRoot, ".stackkit", "access.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Architecture v2 access manifest: %w", err)
	}
	var summary accessSummary
	if err := json.Unmarshal(raw, &summary); err != nil {
		return nil, fmt.Errorf("decode Architecture v2 access manifest: %w", err)
	}
	return &summary, nil
}
