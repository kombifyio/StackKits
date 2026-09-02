package applicationlifecycle

import (
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeobservation"
)

// ExperienceSchemaVersion identifies the derived, read-only application
// experience projection. It is deliberately separate from the durable
// lifecycle state: the projection may be recomputed from the current Plan and
// the owner-custodied evidence at any time.
const ExperienceSchemaVersion = "stackkit.application-experience/v1"

const (
	AxisVerified    = "verified"
	AxisUnverified  = "unverified"
	AxisRequired    = "required"
	AxisBlocked     = "blocked"
	AxisAbsent      = "absent"
	AxisNotRequired = "not-required"

	FreshnessLive       = "live"
	FreshnessHistorical = "historical"
	FreshnessNone       = "none"
)

// ExperienceEvidence is a secret-free pointer to the proof used for one
// axis. A reference without a digest remains explanatory evidence only and
// cannot turn an axis into a verified state by itself.
type ExperienceEvidence struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	Digest string `json:"digest,omitempty"`
}

// ExperienceAxis is one independently evaluated application state axis.
// Status is intentionally explicit: missing or stale evidence is
// unverified, never silently promoted to ready.
type ExperienceAxis struct {
	Status     string               `json:"status"`
	Reason     string               `json:"reason,omitempty"`
	Freshness  string               `json:"freshness"`
	ObservedAt time.Time            `json:"observedAt,omitempty"`
	Evidence   []ExperienceEvidence `json:"evidence,omitempty"`
}

// ApplicationExperience is a derived user-facing view of one selected
// Application workload. It is not a second lifecycle state machine.
type ApplicationExperience struct {
	SchemaVersion string `json:"schemaVersion"`
	WorkloadRef   string `json:"workloadRef"`
	PackageRef    string `json:"packageRef"`
	// LifecycleVersion identifies the version of the lifecycle contract used
	// for this projection. It is deliberately named so consumers do not read
	// it as the version of the deployed application artifact.
	LifecycleVersion string `json:"lifecycleVersion"`
	ServiceRef       string `json:"serviceRef,omitempty"`
	URL              string `json:"url,omitempty"`

	Installed   ExperienceAxis `json:"installed"`
	Reachable   ExperienceAxis `json:"reachable"`
	Setup       ExperienceAxis `json:"setup"`
	Usable      ExperienceAxis `json:"usable"`
	Recoverable ExperienceAxis `json:"recoverable"`

	NextActions []string                `json:"nextActions,omitempty"`
	SetupAction *ApplicationSetupAction `json:"setupAction,omitempty"`
}

// ApplicationSetupAction points to the shared operation catalog. The CLI
// still re-admits the current Plan and exact runtime before any mutation.
type ApplicationSetupAction struct {
	OperationRef                 string   `json:"operationRef"`
	WorkloadRef                  string   `json:"workloadRef"`
	Title                        string   `json:"title"`
	CredentialFields             []string `json:"credentialFields"`
	CredentialsFile              string   `json:"credentialsFile"`
	GuideURL                     string   `json:"guideUrl"`
	SupportsOnboardingCompletion bool     `json:"supportsOnboardingCompletion"`
}

// RuntimeTarget binds a runtime observation requirement to the selected
// workload. Requirement and instance identities come from the verified Plan.
type RuntimeTarget struct {
	RequirementID string
	InstanceRef   string
}

// SetupRun is the secret-free setup state consumed by the derived projection.
// PlanHash is populated only for an operation whose lifecycle Authority exactly
// matches the current Plan. Legacy and prior-Plan records leave it empty and
// cannot affect the current setup axis.
type SetupRun struct {
	// Set by SetupRuns for current-authority operations, even before a terminal
	// setup receipt exists. Completion still requires authenticated evidence.
	PlanHash      string
	authenticated bool
	WorkloadRef   string
	AppName       string
	DropName      string
	RunID         string
	Policy        string
	Status        string
	Phase         string
	Message       string
	Error         string
	Evidence      []ExperienceEvidence
	LastRequested time.Time
	LastStarted   time.Time
	LastFinished  time.Time
}

// SetupInput carries plan-declared setup actions and the currently observed
// setup records. The caller is responsible for selecting records by the exact
// workload identity from the current Plan.
type SetupInput struct {
	PlanHash    string
	WorkloadRef string
	Policy      string
	ActionRefs  []string
	Runs        []SetupRun
}

// ExperienceInput contains only current-plan bindings and read-only evidence
// already accepted by the CLI. No function here performs runtime, network, or
// backup operations.
type ExperienceInput struct {
	Contract       Contract
	State          State
	ServiceRef     string
	RouteRef       string
	URL            string
	HealthRef      string
	RuntimeTargets []RuntimeTarget
	Setup          SetupInput
	Observations   []runtimeobservation.Observation
}

// ProjectExperience deterministically derives the five independent
// experience axes for one workload. It is intentionally conservative:
// healthy health evidence can establish the health of the bound probe target,
// but it cannot establish client-view route reachability, installation,
// runtime, setup completion, or usability. A staged restore result likewise
// never establishes application recovery until a typed activation receipt is
// validated by a caller.
func ProjectExperience(input ExperienceInput) (ApplicationExperience, error) {
	if err := validateContract(input.Contract); err != nil {
		return ApplicationExperience{}, err
	}

	experience := ApplicationExperience{
		SchemaVersion:    ExperienceSchemaVersion,
		WorkloadRef:      input.Contract.WorkloadRef,
		PackageRef:       input.Contract.PackageRef,
		LifecycleVersion: input.Contract.Version,
		ServiceRef:       strings.TrimSpace(input.ServiceRef),
		URL:              strings.TrimSpace(input.URL),
	}

	stateBound := input.State.APIVersion == APIVersion &&
		input.State.WorkloadRef == input.Contract.WorkloadRef &&
		input.State.Authority == authorityFromContract(input.Contract)
	if !stateBound {
		experience.Installed = unverifiedAxis(FreshnessNone, "lifecycle evidence is not bound to the current Plan and workload")
		experience.Recoverable = unverifiedAxis(FreshnessNone, "lifecycle evidence is not bound to the current Plan and workload")
	} else {
		experience.Installed = installedAxis(input.State)
		experience.Recoverable = recoveryAxis(input.Contract, input.State)
	}

	observation, service, health, httpProbe, runtime, targetEvidence := latestWorkloadObservation(input)
	if experience.URL == "" && service != nil {
		experience.URL = service.URL
	}
	experience.Reachable = reachableAxis(observation, service, health, httpProbe)
	experience.Setup = setupAxis(input.Contract, input.Setup)
	experience.Usable = usableAxis(experience, observation, service, health, httpProbe, runtime, targetEvidence)
	experience.NextActions = experienceNextActions(experience)
	return experience, nil
}

func installedAxis(state State) ExperienceAxis {
	for index := len(state.Operations) - 1; index >= 0; index-- {
		operation := state.Operations[index]
		switch operation.Stage {
		case "remove":
			return operationAxis(operation, "application has been removed", AxisAbsent)
		case "install", "upgrade":
			return operationAxis(operation, "installation is verified against the lifecycle evidence", AxisVerified)
		case "restore":
			// A staged restore changes no live application state. Continue to
			// the last known install/upgrade operation for this axis.
		}
	}
	return unverifiedAxis(FreshnessNone, "no completed install or upgrade lifecycle evidence is recorded")
}

func operationAxis(operation Operation, fallbackReason, terminalStatus string) ExperienceAxis {
	axis := ExperienceAxis{Status: AxisUnverified, Freshness: FreshnessNone, ObservedAt: operation.UpdatedAt.UTC()}
	axis.Evidence = experienceEvidence(operation.Evidence)
	if !operation.UpdatedAt.IsZero() {
		axis.Freshness = FreshnessHistorical
	}
	switch operation.Status {
	case StatusSucceeded, StatusRecovered:
		axis.Status = terminalStatus
		axis.Reason = fallbackReason
	case StatusRunning:
		axis.Status = AxisUnverified
		axis.Reason = "application lifecycle operation is still running"
	case StatusRecoveryRequired:
		axis.Status = AxisBlocked
		axis.Reason = strings.TrimSpace(operation.LastError)
		if axis.Reason == "" {
			axis.Reason = "application lifecycle operation requires recovery"
		}
	case StatusFailed:
		axis.Status = AxisBlocked
		axis.Reason = strings.TrimSpace(operation.LastError)
		if axis.Reason == "" {
			axis.Reason = "application lifecycle operation failed"
		}
	default:
		axis.Reason = fallbackReason
	}
	return axis
}

func recoveryAxis(contract Contract, state State) ExperienceAxis {
	for index := len(state.Operations) - 1; index >= 0; index-- {
		operation := state.Operations[index]
		if operation.Stage != "restore" {
			continue
		}
		axis := operationAxis(operation, "restore evidence is incomplete", AxisUnverified)
		if operation.Status == StatusRecoveryRequired {
			axis.Status = AxisRequired
			axis.Reason = strings.TrimSpace(operation.LastError)
			if axis.Reason == "" {
				axis.Reason = "restore activation requires explicit recovery"
			}
			if operation.RecoveryRef != "" {
				axis.Evidence = append(axis.Evidence, ExperienceEvidence{Kind: "recovery-reference", Ref: operation.RecoveryRef})
			}
			return axis
		}
		if operation.Status == StatusSucceeded || operation.Status == StatusRecovered {
			axis.Status = AxisRequired
			axis.Reason = "restore bytes are staged; typed live application activation verification is not available"
			return axis
		}
		return axis
	}
	if contract.Delivery.Kind == "stackkit" || (contract.Delivery.Capabilities != nil && contract.Delivery.Capabilities.BackupRestore) {
		return unverifiedAxis(FreshnessNone, "restore capability is declared, but no completed restore activation is recorded")
	}
	return unverifiedAxis(FreshnessNone, "the selected delivery has no current verified restore activation evidence")
}

func reachableAxis(observation *runtimeobservation.Observation, service *runtimeobservation.Service, health *runtimeobservation.HealthEvidence, httpProbe *runtimeobservation.HTTPProbeEvidence) ExperienceAxis {
	if observation == nil {
		return unverifiedAxis(FreshnessNone, "no current-plan runtime observation is available")
	}
	axis := ExperienceAxis{Freshness: observationFreshness(*observation), ObservedAt: observation.ObservedAt.UTC()}
	if httpProbe != nil {
		axis.Evidence = append(axis.Evidence, ExperienceEvidence{Kind: "http-probe", Ref: httpProbe.ObservationRef, Digest: httpProbe.ObservationDigest})
		axis.Freshness = FreshnessLive
		axis.ObservedAt = httpProbe.ObservedAt.UTC()
		if httpProbe.Status == runtimeobservation.HTTPProbeReachable && httpProbe.Reached {
			axis.Status = AxisVerified
			axis.Reason = "the plan-bound route was reached from the verifier host; other clients and networks are unverified"
			return axis
		}
		axis.Status = AxisBlocked
		axis.Reason = "the plan-bound route was not reached from the verifier host"
		if httpProbe.FailureClass != "" {
			axis.Reason += " (" + httpProbe.FailureClass + ")"
		}
		axis.Reason += "; other clients and networks are unverified"
		return axis
	}
	if health != nil {
		axis.Evidence = append(axis.Evidence, ExperienceEvidence{Kind: "health-observation", Ref: health.ObservationRef, Digest: health.ObservationDigest})
		switch strings.ToLower(strings.TrimSpace(health.Status)) {
		case string(runtimeobservation.HealthHealthy):
			axis.Status, axis.Reason = AxisUnverified, "the bound internal health probe is healthy, but no client-view route probe is available"
			return axis
		case string(runtimeobservation.HealthUnhealthy):
			axis.Status, axis.Reason = AxisBlocked, "the bound internal health probe is unhealthy; client-view route reachability is unverified"
			return axis
		case string(runtimeobservation.HealthNotRequired):
			axis.Status, axis.Reason = AxisUnverified, "the current Plan declares no health probe and no client-view route probe is available"
			return axis
		}
	}
	if service != nil {
		axis.Evidence = append(axis.Evidence, ExperienceEvidence{Kind: "runtime-observation", Ref: observationLink(observation)})
		switch service.Health {
		case runtimeobservation.HealthHealthy:
			axis.Status, axis.Reason = AxisUnverified, "the bound internal service health is healthy, but no client-view route probe is available"
			return axis
		case runtimeobservation.HealthUnhealthy:
			axis.Status, axis.Reason = AxisBlocked, "the bound internal service health is unhealthy; client-view route reachability is unverified"
			return axis
		case runtimeobservation.HealthNotRequired:
			axis.Status, axis.Reason = AxisUnverified, "the current Plan declares no health probe and no client-view route probe is available"
			return axis
		}
	}
	return unverifiedAxisWithObservation(axis, "the bound workload has no healthy reachability evidence")
}

func setupAxis(contract Contract, input SetupInput) ExperienceAxis {
	axis := ExperienceAxis{Freshness: FreshnessNone}
	actions := uniqueStrings(input.ActionRefs)
	policy := strings.ToLower(strings.TrimSpace(input.Policy))
	if policy == "" {
		return unverifiedAxis(FreshnessNone, "setup policy and action metadata are not available for the current Plan")
	}
	if len(actions) == 0 && (policy == "none" || policy == "not-required") {
		axis.Status = AxisNotRequired
		axis.Reason = "the current Plan explicitly declares that this workload has no setup requirement"
		return axis
	}
	if policy == "manual" && len(actions) == 0 {
		// Manual is an explicit operator-owned setup policy. An empty action
		// list means that the user-facing setup guide is the authority, not
		// that setup is unnecessary.
		axis.Status = AxisRequired
		axis.Reason = "the current Plan declares manual operator setup for this workload"
		return axis
	}
	if len(actions) == 0 {
		// Automatic and on-demand policies are required by the CUE contract to
		// name actions. Treat a missing list as incomplete projection data,
		// rather than turning absence of metadata into success.
		return unverifiedAxis(FreshnessNone, "the current Plan setup policy has no declared action metadata")
	}
	axis.Status = AxisRequired
	axis.Reason = "the current Plan declares setup actions that are not verified"

	// A setup journal can contain several attempts for one action. Evaluate
	// only the newest plan-relevant record for each action so an older
	// completed run cannot overwrite a newer failure or in-progress attempt.
	latest := map[string]SetupRun{}
	for _, run := range input.Runs {
		if run.PlanHash != input.PlanHash || run.PlanHash != contract.PlanHash {
			continue
		}
		if run.WorkloadRef != "" && run.WorkloadRef != contract.WorkloadRef {
			continue
		}
		if run.AppName != "" && run.AppName != contract.WorkloadRef && run.WorkloadRef == "" {
			continue
		}
		action := strings.TrimSpace(run.DropName)
		if action == "" || !containsString(actions, action) {
			continue
		}
		previous, ok := latest[action]
		if !ok || setupRunAfter(run, previous) {
			latest[action] = run
		}
	}

	seenCompleted := map[string]bool{}
	for _, action := range actions {
		run, ok := latest[action]
		if !ok {
			continue
		}
		if run.LastFinished.After(axis.ObservedAt) {
			axis.ObservedAt = run.LastFinished.UTC()
		}
		if run.LastStarted.After(axis.ObservedAt) {
			axis.ObservedAt = run.LastStarted.UTC()
		}
		if run.LastRequested.After(axis.ObservedAt) {
			axis.ObservedAt = run.LastRequested.UTC()
		}
		axis.Freshness = FreshnessHistorical
		axis.Evidence = append(axis.Evidence, run.Evidence...)
		switch {
		case strings.EqualFold(strings.TrimSpace(run.Status), "failed"):
			axis.Status = AxisBlocked
			axis.Reason = firstNonEmpty(run.Error, run.Message, "setup action failed")
		case strings.EqualFold(strings.TrimSpace(run.Status), "running") || strings.EqualFold(strings.TrimSpace(run.Status), "waiting"):
			if axis.Status != AxisBlocked {
				axis.Status = AxisUnverified
				axis.Reason = firstNonEmpty(run.Message, "setup action is still in progress")
			}
		case strings.EqualFold(strings.TrimSpace(run.Status), "completed") && strings.EqualFold(strings.TrimSpace(run.Phase), "verified"):
			if run.authenticated && run.PlanHash == input.PlanHash && run.PlanHash == contract.PlanHash && (run.WorkloadRef == contract.WorkloadRef || run.AppName == contract.WorkloadRef) {
				seenCompleted[action] = true
			} else if axis.Status != AxisBlocked {
				axis.Status = AxisUnverified
				axis.Reason = "setup completed, but its record is not bound to the current Plan"
			}
		}
	}
	allCompleted := true
	for _, action := range actions {
		if !seenCompleted[action] {
			allCompleted = false
			break
		}
	}
	if allCompleted {
		axis.Status = AxisVerified
		axis.Reason = "all Plan-declared setup actions have current-plan verified results"
	}
	return axis
}

func setupRunAfter(candidate, previous SetupRun) bool {
	candidateTime := latestSetupRunTime(candidate)
	previousTime := latestSetupRunTime(previous)
	if candidateTime.After(previousTime) {
		return true
	}
	if candidateTime.Before(previousTime) {
		return false
	}
	return candidate.RunID > previous.RunID
}

func latestSetupRunTime(run SetupRun) time.Time {
	latest := run.LastRequested
	if run.LastStarted.After(latest) {
		latest = run.LastStarted
	}
	if run.LastFinished.After(latest) {
		latest = run.LastFinished
	}
	return latest
}

func usableAxis(experience ApplicationExperience, observation *runtimeobservation.Observation, service *runtimeobservation.Service, health *runtimeobservation.HealthEvidence, httpProbe *runtimeobservation.HTTPProbeEvidence, runtime []runtimeobservation.RuntimeEvidence, targetEvidence []ExperienceEvidence) ExperienceAxis {
	axis := ExperienceAxis{Freshness: FreshnessNone}
	if experience.Installed.Status == AxisAbsent {
		axis.Status, axis.Reason = AxisAbsent, "application is not installed"
		return axis
	}
	if experience.Installed.Status == AxisBlocked {
		axis.Status, axis.Reason = AxisBlocked, "installation lifecycle is blocked"
		return axis
	}
	if experience.Installed.Status != AxisVerified {
		axis.Status, axis.Reason = AxisUnverified, "installation is not verified against the current lifecycle evidence"
		return axis
	}
	if experience.Reachable.Status == AxisBlocked {
		axis.Status, axis.Reason = AxisBlocked, "the bound application route is unhealthy"
		return axis
	}
	if experience.Reachable.Status != AxisVerified {
		axis.Status, axis.Reason = AxisUnverified, "reachability is not verified"
		return axis
	}
	if experience.Setup.Status == AxisRequired || experience.Setup.Status == AxisBlocked {
		axis.Status, axis.Reason = experience.Setup.Status, "setup must be completed before the application is usable"
		return axis
	}
	if experience.Setup.Status != AxisVerified && experience.Setup.Status != AxisNotRequired {
		axis.Status, axis.Reason = AxisUnverified, "setup evidence is not verified against the current Plan"
		return axis
	}
	if httpProbe != nil && (httpProbe.StatusCode == 401 || httpProbe.StatusCode == 403) {
		axis.Status = AxisUnverified
		axis.Freshness = FreshnessLive
		axis.ObservedAt = httpProbe.ObservedAt.UTC()
		axis.Evidence = append(axis.Evidence, ExperienceEvidence{Kind: "http-probe", Ref: httpProbe.ObservationRef, Digest: httpProbe.ObservationDigest})
		axis.Reason = "the verifier host reached the route, but authentication or authorization prevented a functional client check"
		return axis
	}
	if observation == nil || !observation.Live {
		axis.Status, axis.Reason = AxisUnverified, "runtime and health evidence is historical; a live verification is required"
		if observation != nil {
			axis.Freshness, axis.ObservedAt = observationFreshness(*observation), observation.ObservedAt.UTC()
		}
		return axis
	}
	if service == nil || service.Status != runtimeobservation.StatusRunning {
		axis.Status, axis.Reason = AxisUnverified, "the application runtime is not verified as running"
		return axis
	}
	if health == nil && (service.Health != runtimeobservation.HealthHealthy && service.Health != runtimeobservation.HealthNotRequired) {
		axis.Status, axis.Reason = AxisUnverified, "no bound health result is available"
		return axis
	}
	if health != nil && health.Status != string(runtimeobservation.HealthHealthy) && health.Status != string(runtimeobservation.HealthNotRequired) {
		axis.Status, axis.Reason = AxisUnverified, "the bound health result is not healthy"
		return axis
	}
	if len(runtime) == 0 && service.Status != runtimeobservation.StatusRunning {
		axis.Status, axis.Reason = AxisUnverified, "no runtime evidence is available"
		return axis
	}
	axis.Status, axis.Reason, axis.Freshness = AxisVerified, "installation, live runtime, reachability, and setup evidence agree", FreshnessLive
	axis.ObservedAt = observation.ObservedAt.UTC()
	if experience.Reachable.ObservedAt.After(axis.ObservedAt) {
		axis.ObservedAt = experience.Reachable.ObservedAt
	}
	axis.Evidence = append(axis.Evidence, targetEvidence...)
	axis.Evidence = append(axis.Evidence, experience.Reachable.Evidence...)
	return axis
}

func latestWorkloadObservation(input ExperienceInput) (*runtimeobservation.Observation, *runtimeobservation.Service, *runtimeobservation.HealthEvidence, *runtimeobservation.HTTPProbeEvidence, []runtimeobservation.RuntimeEvidence, []ExperienceEvidence) {
	targets := map[string]RuntimeTarget{}
	for _, target := range input.RuntimeTargets {
		if target.RequirementID != "" || target.InstanceRef != "" {
			targets[target.RequirementID+"\x00"+target.InstanceRef] = target
		}
	}
	var selected *runtimeobservation.Observation
	var selectedProbe *runtimeobservation.HTTPProbeEvidence
	for index := range input.Observations {
		candidate := &input.Observations[index]
		if candidate.Identity.PlanHash != input.Contract.PlanHash {
			continue
		}
		candidateProbe := latestMatchingHTTPProbe(*candidate, input)
		if candidateProbe != nil && (selectedProbe == nil || candidateProbe.ObservedAt.After(selectedProbe.ObservedAt) ||
			(candidateProbe.ObservedAt.Equal(selectedProbe.ObservedAt) && (selected == nil || candidate.ObservedAt.After(selected.ObservedAt)))) {
			selected = candidate
			selectedProbe = candidateProbe
			continue
		}
		if selectedProbe != nil {
			continue
		}
		if selected == nil || candidate.ObservedAt.After(selected.ObservedAt) || (candidate.ObservedAt.Equal(selected.ObservedAt) && candidate.Live && !selected.Live) {
			selected = candidate
		}
	}
	if selected == nil {
		return nil, nil, nil, nil, nil, nil
	}
	var service *runtimeobservation.Service
	for index := range selected.Services {
		if experienceServiceRefMatches(selected.Services[index].Ref, input.ServiceRef) {
			service = &selected.Services[index]
			break
		}
	}
	var health *runtimeobservation.HealthEvidence
	for index := range selected.Health {
		candidate := &selected.Health[index]
		if candidate.SourceRef == input.HealthRef || candidate.TargetRef == input.HealthRef {
			if health == nil || candidate.Status == string(runtimeobservation.HealthHealthy) {
				health = candidate
			}
		}
	}
	runtime := []runtimeobservation.RuntimeEvidence{}
	evidence := []ExperienceEvidence{}
	for _, candidate := range selected.Runtime {
		if _, ok := targets[candidate.RequirementID+"\x00"+candidate.InstanceRef]; !ok {
			continue
		}
		runtime = append(runtime, candidate)
		evidence = append(evidence, ExperienceEvidence{Kind: "runtime-observation", Ref: candidate.ObservationRef, Digest: candidate.ObservationDigest})
	}
	return selected, service, health, selectedProbe, runtime, evidence
}

func latestMatchingHTTPProbe(observation runtimeobservation.Observation, input ExperienceInput) *runtimeobservation.HTTPProbeEvidence {
	var selected *runtimeobservation.HTTPProbeEvidence
	for index := range observation.HTTPProbes {
		candidate := &observation.HTTPProbes[index]
		if candidate.WorkloadRef != "" {
			if candidate.WorkloadRef != input.Contract.WorkloadRef || !experienceServiceRefMatches(candidate.ServiceRef, input.ServiceRef) {
				continue
			}
		} else if !experienceServiceRefMatches(candidate.ServiceRef, input.ServiceRef) {
			continue
		}
		if routeRef := strings.TrimSpace(input.RouteRef); routeRef != "" && candidate.RouteRef != routeRef {
			continue
		}
		if selected == nil || candidate.ObservedAt.After(selected.ObservedAt) {
			selected = candidate
		}
	}
	return selected
}

func experienceServiceRefMatches(candidate, expected string) bool {
	canonical := func(value string) string {
		value = strings.TrimSpace(value)
		switch value {
		case "basement-hub", "dashboard":
			return "base"
		default:
			return value
		}
	}
	return canonical(candidate) != "" && canonical(candidate) == canonical(expected)
}

func experienceNextActions(experience ApplicationExperience) []string {
	actions := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || containsString(actions, value) {
			return
		}
		actions = append(actions, value)
	}
	switch experience.Installed.Status {
	case AxisUnverified:
		add("Complete and verify the install lifecycle for this workload against the current Plan.")
	case AxisBlocked:
		add("Inspect the failed install lifecycle operation and resolve its diagnostic before retrying.")
	case AxisAbsent:
		add("Apply the current Plan again if this workload should be installed.")
	}
	switch experience.Reachable.Status {
	case AxisUnverified:
		add("Run `stackkit verify --http --json` to verify this Plan-bound route from the verifier host; other clients remain unverified.")
	case AxisBlocked:
		add("Inspect the bound route and verifier-host HTTP/health probe before using the application.")
	}
	switch experience.Setup.Status {
	case AxisRequired:
		add("Complete the declared setup action in the State Console.")
	case AxisBlocked:
		add("Review the failed setup action and retry it after correcting the reported cause.")
	case AxisUnverified:
		add("Record or rerun setup against the current Plan before treating the application as usable.")
	}
	switch experience.Recoverable.Status {
	case AxisRequired:
		add("Complete or recover the staged restore activation using its recorded recovery reference.")
	case AxisBlocked:
		add("Resolve the restore lifecycle recovery requirement before further mutation.")
	case AxisUnverified:
		add("Run and verify a restore drill before relying on this workload's recoverability.")
	}
	if experience.Usable.Status != AxisVerified && experience.Usable.Status != AxisAbsent {
		add("Run the live checks above before treating this application as ready to use.")
	}
	return actions
}

func experienceEvidence(values []Evidence) []ExperienceEvidence {
	result := make([]ExperienceEvidence, 0, len(values))
	for _, value := range values {
		result = append(result, ExperienceEvidence{Kind: value.Kind, Ref: value.Ref, Digest: value.Digest})
	}
	return result
}

func observationFreshness(observation runtimeobservation.Observation) string {
	if observation.Live {
		return FreshnessLive
	}
	return FreshnessHistorical
}

func unverifiedAxis(freshness, reason string) ExperienceAxis {
	return ExperienceAxis{Status: AxisUnverified, Freshness: freshness, Reason: reason}
}

func unverifiedAxisWithObservation(axis ExperienceAxis, reason string) ExperienceAxis {
	axis.Status = AxisUnverified
	axis.Reason = reason
	return axis
}

func observationLink(observation *runtimeobservation.Observation) string {
	if observation == nil {
		return ""
	}
	if len(observation.EvidenceLinks) > 0 {
		return observation.EvidenceLinks[0].Ref
	}
	return "runtime-observation://" + observation.Identity.RunID
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
