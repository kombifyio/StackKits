package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applyledger"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeobservation"
	"github.com/kombifyio/stackkits/internal/servicecontrol"
	stackverify "github.com/kombifyio/stackkits/internal/verify"
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
	CloudVerify        *runtimeexecutorlocal.CloudCoreVerifyObservation
	Context            context.Context
	HTTPProbe          bool
	HTTPClient         *http.Client
}

type architectureV2ApplyCommandResult struct {
	SchemaVersion string                            `json:"schemaVersion"`
	Status        string                            `json:"status"`
	Apply         architecturev2.ApplyResultSummary `json:"apply"`
	Observations  []runtimeobservation.Observation  `json:"observations"`
	EvidenceLinks []runtimeobservation.EvidenceLink `json:"evidenceLinks"`
	// Outcomes is the per-unit account of the run. It is additive on
	// stackkit.apply-result/v2: a consumer that only reads the summary keeps
	// working, and one that wants to know which module came up can.
	Outcomes *applyledger.Ledger `json:"outcomes,omitempty"`
}

type runtimeObservationPlanProjection struct {
	StackID string `json:"stackId"`
	Modules []struct {
		ID          string `json:"id"`
		RenderUnits []struct {
			ServiceEndpoints []struct {
				ServiceRef string `json:"serviceRef"`
				HealthRef  string `json:"healthRef"`
			} `json:"serviceEndpoints"`
			Instances []struct {
				ID      string `json:"id"`
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
	ref, healthRef, instanceRef, site, node string
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
	cloudDigest, cloudProbes, err := runtimeObservationCloudVerification(input.CloudVerify)
	if err != nil {
		return nil, err
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
		status, observationRef, observationDigest := outcome.Status, outcome.ObservationRef, outcome.ObservationDigest
		if input.CloudVerify != nil && outcome.InstanceRef == input.CloudVerify.ProjectRef {
			status = input.CloudVerify.Status
			observationRef = "runtime-observation://cloud-core/" + input.CloudVerify.ProjectRef
			observationDigest = cloudDigest
		}
		observation.Runtime = append(observation.Runtime, runtimeobservation.RuntimeEvidence{
			RequirementID: outcome.RequirementID, InstanceRef: outcome.InstanceRef, Status: status,
			ObservationRef: observationRef, ObservationDigest: observationDigest,
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
		status, observationRef, observationDigest := outcome.Status, outcome.ObservationRef, outcome.ObservationDigest
		if liveStatus, ok := cloudProbes[outcome.RequirementID]; ok {
			status = liveStatus
			observationRef = "health-observation://cloud-core/" + outcome.RequirementID
			observationDigest = cloudDigest
		}
		healthStatus[requirement.SourceRef] = status
		for scope, observation := range groups {
			if !scopeMatches(requirement.SiteRefs, requirement.NodeRefs, scope) {
				continue
			}
			observation.Health = append(observation.Health, runtimeobservation.HealthEvidence{
				RequirementID: outcome.RequirementID, TargetRef: outcome.TargetRef, SourceRef: requirement.SourceRef,
				Status: status, ObservationRef: observationRef, ObservationDigest: observationDigest,
				SiteRef: scope.site, NodeRef: scope.node,
			})
		}
	}

	placements := runtimeObservationServicePlacements(projection, input)
	cloudServices := runtimeObservationCloudServices(input.CloudVerify)
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
			// Health proves reachability of the probe target, not that the
			// workload runtime is running. Keep those axes independent and
			// derive service status only from the bound runtime evidence.
			service.Status = runtimeObservationServiceStatus(observation.Runtime, placement.instanceRef)
			switch healthStatus[placement.healthRef] {
			case "healthy":
				service.Health = runtimeobservation.HealthHealthy
			case "unhealthy":
				service.Health = runtimeobservation.HealthUnhealthy
			case "starting":
				service.Health = runtimeobservation.HealthStarting
			case "not-required":
				service.Health = runtimeobservation.HealthNotRequired
			}
			if live, ok := cloudServices[placement.ref]; ok {
				service.Status = runtimeobservation.ServiceStatus(live.Status)
				service.Health = runtimeobservation.HealthStatus(live.Health)
			}
			observation.Services = append(observation.Services, service)
		}
		if len(observation.Services) == 0 {
			for _, access := range accessByRef {
				observation.Services = append(observation.Services, runtimeobservation.Service{Ref: access.Key, URL: access.URL, Status: runtimeobservation.StatusUnknown, Health: runtimeobservation.HealthUnknown})
			}
		}
	}
	if input.HTTPProbe {
		if err := appendArchitectureV2HTTPProbes(input, projection.StackID, requirements, hostChannel, groups); err != nil {
			return nil, err
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

func appendArchitectureV2HTTPProbes(
	input architectureV2RuntimeObservationInput,
	stackID string,
	requirements generationartifact.ApplyRequirements,
	hostChannel map[string]string,
	groups map[runtimeObservationScope]*runtimeobservation.Observation,
) error {
	access, err := architectureV2HTTPProbeAccess(input, stackID, requirements)
	if err != nil {
		return err
	}
	ctx := input.Context
	if ctx == nil {
		ctx = context.Background()
	}
	probes, err := stackverify.ProbeHTTPRoutes(ctx, access, input.HTTPClient)
	if err != nil {
		return fmt.Errorf("HTTP route probe requested: %w", err)
	}
	for _, probe := range probes {
		if probe.Vantage != runtimeobservation.VantageVerifierHost {
			return fmt.Errorf("HTTP route probe %q returned an unsupported vantage %q", probe.Key, probe.Vantage)
		}
		evidence, err := runtimeHTTPProbeEvidence(probe)
		if err != nil {
			return err
		}
		scopes := architectureV2HTTPProbeScopes(probe, requirements, groups)
		if len(scopes) == 0 {
			return fmt.Errorf("HTTP route probe %q has no exact plan workload or runtime scope", probe.Key)
		}
		for _, scope := range scopes {
			observation := groups[scope]
			if observation == nil {
				continue
			}
			observation.HTTPProbes = append(observation.HTTPProbes, evidence)
		}
	}
	return nil
}

func architectureV2HTTPProbeFailed(observations []runtimeobservation.Observation) bool {
	for _, observation := range observations {
		for _, probe := range observation.HTTPProbes {
			if probe.Status == runtimeobservation.HTTPProbeUnreachable || !probe.Reached {
				return true
			}
		}
	}
	return false
}

func architectureV2HTTPProbeAccess(
	input architectureV2RuntimeObservationInput,
	stackID string,
	requirements generationartifact.ApplyRequirements,
) (*stackverify.AccessSummary, error) {
	access := input.Access
	if access == nil {
		return nil, errors.New("HTTP route probe requested but the verified Access manifest is unavailable; run stackkit generate/apply first")
	}
	if access.SchemaVersion != "stackkit.access-manifest/v2" && access.SchemaVersion != architectureV2AccessSchemaVersion {
		return nil, fmt.Errorf("HTTP route probe requires a verified Access manifest, got schema %q", access.SchemaVersion)
	}
	if access.StackID != stackID || access.PlanHash != input.Plan.Binding().PlanHash || access.ApplyResultHash != input.Apply.ResultHash {
		return nil, errors.New("HTTP route probe Access manifest is not bound to the verified Plan and Apply result")
	}
	workloadsByService := map[string][]generationartifact.ApplyWorkloadRequirement{}
	for _, workload := range requirements.Workloads {
		key := architectureV2AccessServiceKey(workload.ServiceRef)
		if key != "" {
			workloadsByService[key] = append(workloadsByService[key], workload)
		}
	}
	runtimeOwnerByService := map[string]string{}
	for _, runtime := range access.RuntimeServices {
		serviceKey := architectureV2AccessServiceKey(runtime.ServiceKey)
		ownerKey := architectureV2AccessServiceKey(runtime.ApplicationKey)
		if serviceKey == "" || ownerKey == "" {
			continue
		}
		if previous, ok := runtimeOwnerByService[serviceKey]; ok && previous != ownerKey {
			return nil, fmt.Errorf("verified Access runtime service %q maps to multiple Plan workloads", serviceKey)
		}
		runtimeOwnerByService[serviceKey] = ownerKey
	}
	result := &stackverify.AccessSummary{Services: make([]stackverify.AccessService, 0, len(access.Services))}
	for _, service := range access.Services {
		key := strings.TrimSpace(service.Key)
		url := strings.TrimSpace(service.URL)
		if key == "" || url == "" {
			return nil, errors.New("verified Access manifest contains a route without an exact service key or URL")
		}
		serviceKey := architectureV2AccessServiceKey(key)
		workloadKey := serviceKey
		if ownerKey := runtimeOwnerByService[serviceKey]; ownerKey != "" {
			workloadKey = ownerKey
		}
		candidates := workloadsByService[workloadKey]
		if len(candidates) == 0 {
			return nil, fmt.Errorf("verified Access manifest route %q has no exact Plan workload binding", key)
		}
		if len(candidates) > 1 {
			return nil, fmt.Errorf("verified Access manifest route %q maps to multiple Plan workloads", key)
		}
		serviceRef := key
		workloadRef := ""
		if len(candidates) == 1 {
			serviceRef = candidates[0].ServiceRef
			workloadRef = candidates[0].ID
		}
		routeRef := strings.TrimSpace(service.RouteRef)
		if routeRef == "" {
			// Home routes do not carry a network route object; their catalog
			// slug is still part of the verified Access manifest and is the
			// stable route identity available to this verifier.
			routeRef = strings.TrimSpace(service.RouteSlug)
			if routeRef != "" {
				routeRef = "route:" + routeRef
			}
		}
		if routeRef == "" {
			return nil, fmt.Errorf("verified Access manifest route %q has no route identity", key)
		}
		result.Services = append(result.Services, stackverify.AccessService{
			Key: key, URL: url, ServiceRef: serviceRef, RouteRef: routeRef, WorkloadRef: workloadRef,
		})
	}
	return result, nil
}

func architectureV2HTTPProbeScopes(
	probe stackverify.HTTPRouteProbe,
	requirements generationartifact.ApplyRequirements,
	groups map[runtimeObservationScope]*runtimeobservation.Observation,
) []runtimeObservationScope {
	seen := map[runtimeObservationScope]bool{}
	result := []runtimeObservationScope{}
	if workloadRef := strings.TrimSpace(probe.WorkloadRef); workloadRef != "" {
		for _, workload := range requirements.Workloads {
			if workload.ID != workloadRef {
				continue
			}
			if architectureV2AccessServiceKey(workload.ServiceRef) != architectureV2AccessServiceKey(probe.ServiceRef) {
				return nil
			}
			for candidate := range groups {
				if scopeMatches(workload.SiteRefs, workload.NodeRefs, candidate) {
					seen[candidate] = true
					result = append(result, candidate)
				}
			}
			break
		}
		return sortedRuntimeObservationScopes(result)
	}
	serviceRef := architectureV2AccessServiceKey(probe.ServiceRef)
	if serviceRef == "" {
		serviceRef = architectureV2AccessServiceKey(probe.Key)
	}
	if serviceRef == "" {
		return nil
	}
	matchedWorkloads := 0
	for _, workload := range requirements.Workloads {
		if architectureV2AccessServiceKey(workload.ServiceRef) != serviceRef {
			continue
		}
		matchedWorkloads++
		for candidate := range groups {
			if scopeMatches(workload.SiteRefs, workload.NodeRefs, candidate) && !seen[candidate] {
				seen[candidate] = true
				result = append(result, candidate)
			}
		}
	}
	if matchedWorkloads != 1 {
		return nil
	}
	return sortedRuntimeObservationScopes(result)
}

func sortedRuntimeObservationScopes(scopes []runtimeObservationScope) []runtimeObservationScope {
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].site != scopes[j].site {
			return scopes[i].site < scopes[j].site
		}
		if scopes[i].node != scopes[j].node {
			return scopes[i].node < scopes[j].node
		}
		return scopes[i].channel < scopes[j].channel
	})
	return scopes
}

func runtimeHTTPProbeEvidence(probe stackverify.HTTPRouteProbe) (runtimeobservation.HTTPProbeEvidence, error) {
	raw, err := json.Marshal(probe)
	if err != nil {
		return runtimeobservation.HTTPProbeEvidence{}, fmt.Errorf("encode HTTP route probe evidence: %w", err)
	}
	sum := sha256.Sum256(raw)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	status := runtimeobservation.HTTPProbeUnreachable
	failureClass := probe.FailureClass
	if probe.Reached {
		status = runtimeobservation.HTTPProbeReachable
	} else if failureClass == "" {
		failureClass = "http-status"
	}
	return runtimeobservation.HTTPProbeEvidence{
		Vantage: probe.Vantage, ObservedAt: probe.ObservedAt.UTC(), WorkloadRef: probe.WorkloadRef,
		ServiceRef: probe.ServiceRef, RouteRef: probe.RouteRef, URL: probe.URL, Method: probe.Method,
		Status: status, Reached: probe.Reached, StatusCode: probe.StatusCode,
		FailureClass:   failureClass,
		ObservationRef: "runtime-observation://http-probe/" + strings.TrimPrefix(digest, "sha256:"), ObservationDigest: digest,
	}, nil
}

func runtimeObservationCloudVerification(observation *runtimeexecutorlocal.CloudCoreVerifyObservation) (string, map[string]string, error) {
	probes := map[string]string{}
	if observation == nil {
		return "", probes, nil
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		return "", nil, fmt.Errorf("encode live Cloud verification observation: %w", err)
	}
	sum := sha256.Sum256(raw)
	for _, probe := range observation.Probes {
		probes[probe.RequirementID] = probe.Status
	}
	return "sha256:" + hex.EncodeToString(sum[:]), probes, nil
}

func runtimeObservationCloudServices(observation *runtimeexecutorlocal.CloudCoreVerifyObservation) map[string]runtimeexecutorlocal.BasementCoreServiceObservation {
	result := map[string]runtimeexecutorlocal.BasementCoreServiceObservation{}
	if observation == nil {
		return result
	}
	serviceRefs := map[string]string{"hub": "base", "pocketid": "id", "tinyauth": "auth", "coolify": "coolify"}
	for _, service := range observation.Services {
		if ref := serviceRefs[service.Ref]; ref != "" {
			result[ref] = service
		}
	}
	return result
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
	if input.CloudVerify != nil {
		links = append(links, runtimeobservation.EvidenceLink{Kind: "cloud-core-artifact", Ref: "platform/cloud-core/compose.yaml", Digest: input.CloudVerify.ArtifactDigest})
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
					ID      string `json:"id"`
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
					result = append(result, runtimeObservationServicePlacement{ref: ref, healthRef: endpoint.HealthRef, instanceRef: instance.ID, site: instance.SiteRef, node: instance.NodeRef})
				}
			}
		}
	}
	return result
}

// runtimeObservationServiceStatus derives process state from runtime evidence
// for the exact rendered instance. A health result is intentionally excluded:
// a reachable probe can coexist with a stale or independently managed runtime.
func runtimeObservationServiceStatus(evidence []runtimeobservation.RuntimeEvidence, instanceRef string) runtimeobservation.ServiceStatus {
	if strings.TrimSpace(instanceRef) == "" {
		return runtimeobservation.StatusUnknown
	}
	status := runtimeobservation.StatusUnknown
	for _, item := range evidence {
		if item.InstanceRef != instanceRef {
			continue
		}
		candidate := runtimeObservationStatus(item.Status)
		if candidate == runtimeobservation.StatusError {
			return candidate
		}
		if candidate == runtimeobservation.StatusRunning || (candidate == runtimeobservation.StatusStarting && status == runtimeobservation.StatusUnknown) {
			status = candidate
		}
		if candidate == runtimeobservation.StatusStopped && status == runtimeobservation.StatusUnknown {
			status = candidate
		}
	}
	return status
}

func runtimeObservationStatus(value string) runtimeobservation.ServiceStatus {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "running", "ready", "applied", "succeeded", "success", "healthy", "enforced":
		return runtimeobservation.StatusRunning
	case "starting", "pending", "queued", "in-progress":
		return runtimeobservation.StatusStarting
	case "stopped", "removed", "absent":
		return runtimeobservation.StatusStopped
	case "failed", "error", "unhealthy", "recovery-required":
		return runtimeobservation.StatusError
	default:
		return runtimeobservation.StatusUnknown
	}
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

func readArchitectureV2AccessSummary(workspaceRoot string, plan generationartifact.VerifiedPlan, apply architecturev2.ApplyResultSummary) (*accessSummary, error) {
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
	expected, err := buildArchitectureV2AccessSummary(plan, apply)
	if err != nil {
		return nil, err
	}
	if (summary.SchemaVersion != "stackkit.access-manifest/v2" && summary.SchemaVersion != expected.SchemaVersion) || summary.StackID != expected.StackID || summary.PlanHash != expected.PlanHash ||
		summary.ApplyResultHash != expected.ApplyResultHash || summary.StackKit != expected.StackKit ||
		summary.StackKitVersion != expected.StackKitVersion || summary.Mode != expected.Mode || summary.Domain != expected.Domain ||
		summary.SubdomainPrefix != expected.SubdomainPrefix || summary.HubURL != expected.HubURL ||
		!slices.Equal(summary.SetupActions, expected.SetupActions) || !summary.GeneratedAt.Equal(expected.GeneratedAt) ||
		!exactArchitectureV2AccessServices(summary.Services, expected.Services) {
		return nil, errors.New("Architecture v2 access manifest differs from the verified plan or Apply result")
	}
	if summary.SchemaVersion == expected.SchemaVersion && !exactArchitectureV2RuntimeServices(summary.RuntimeServices, expected.RuntimeServices) {
		return nil, errors.New("Architecture v2 access manifest runtime services differ from the verified plan")
	}
	return &summary, nil
}

func exactArchitectureV2RuntimeServices(actual, expected []accessRuntimeService) bool {
	if len(actual) != len(expected) {
		return false
	}
	byIdentity := make(map[string]accessRuntimeService, len(actual))
	for _, service := range actual {
		key := service.RuntimeIdentity.RuntimeRef + "\x00" + service.ServiceKey
		if service.ServiceKey == "" || service.ApplicationKey == "" || service.DisplayName == "" || service.Role == "" || service.Lifecycle == "" || service.OperationalImpact == "" || service.RuntimeIdentity.Kind == "" || service.RuntimeIdentity.Adapter == "" || service.RuntimeIdentity.RuntimeRef == "" {
			return false
		}
		if _, duplicate := byIdentity[key]; duplicate {
			return false
		}
		byIdentity[key] = service
	}
	for _, want := range expected {
		got, ok := byIdentity[want.RuntimeIdentity.RuntimeRef+"\x00"+want.ServiceKey]
		if !ok || got != want {
			return false
		}
	}
	return true
}

func exactArchitectureV2AccessServices(actual, expected []accessService) bool {
	if len(actual) != len(expected) {
		return false
	}
	byKey := make(map[string]accessService, len(actual))
	for _, service := range actual {
		if service.Key == "" {
			return false
		}
		if _, duplicate := byKey[service.Key]; duplicate {
			return false
		}
		byKey[service.Key] = service
	}
	for _, want := range expected {
		got, ok := byKey[want.Key]
		if !ok || got.Name != want.Name || got.DisplayName != want.DisplayName || got.ToolName != want.ToolName ||
			got.ModuleSlug != want.ModuleSlug || got.RouteSlug != want.RouteSlug || got.RouteRef != want.RouteRef ||
			got.Section != want.Section || got.URL != want.URL || got.Host != want.Host || got.Status != want.Status ||
			!slices.Equal(got.LegacyAliases, want.LegacyAliases) || !validArchitectureV2ServiceControlProjection(got, want) {
			return false
		}
	}
	return true
}

func validArchitectureV2ServiceControlProjection(got, expected accessService) bool {
	if got.DesiredState == expected.DesiredState && slices.Equal(got.AllowedActions, expected.AllowedActions) && got.EvidenceRef == expected.EvidenceRef {
		return true
	}
	if len(expected.AllowedActions) == 0 {
		return false
	}
	if (got.DesiredState != servicecontrol.DesiredRunning && got.DesiredState != servicecontrol.DesiredStopped) ||
		!slices.Equal(got.AllowedActions, expected.AllowedActions) ||
		!strings.HasPrefix(got.EvidenceRef, "stackkit-evidence://service-control/") ||
		!architectureV2AccessDigestPattern.MatchString(strings.TrimPrefix(got.EvidenceRef, "stackkit-evidence://service-control/")) {
		return false
	}
	return got.DesiredState != servicecontrol.DesiredStopped || slices.Contains(got.AllowedActions, servicecontrol.ActionStop)
}
