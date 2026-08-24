// Package applyledger records what one Apply actually did, unit by unit.
//
// A rollout that half-succeeded used to reach the operator as a single error
// string, and a rollout that succeeded reached dashboards as a single green
// result. Neither said which module was running and which was not. The ledger
// is that missing per-unit truth: it names every runtime requirement the plan
// declared, what happened to it, why, whether retrying can help, and what to do
// next.
//
// It is a projection, not an authority: the sealed Apply result and the durable
// journal remain the evidence. The ledger exists so an operator, an installer,
// and a dashboard can read the same account of the run.
package applyledger

import (
	"sort"
	"time"

	"github.com/kombifyio/stackkits/internal/applyoutcome"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/runtimeapplyv2"
)

// SchemaVersion identifies the machine-readable ledger.
const SchemaVersion = "stackkit.apply-outcome-ledger/v1"

// Outcome is the closed per-unit vocabulary.
type Outcome string

const (
	// OutcomeApplied means the unit was applied and its health targets passed.
	OutcomeApplied Outcome = "applied"
	// OutcomeDegraded means the runtime applied but at least one health target
	// did not pass.
	OutcomeDegraded Outcome = "degraded"
	// OutcomeFailed means the unit did not apply.
	OutcomeFailed Outcome = "failed"
	// OutcomeSkipped means the unit was never attempted, because an earlier
	// critical failure stopped execution or a gate excluded it.
	OutcomeSkipped Outcome = "skipped"
	// OutcomeUnverified means the unit applied but its health could not be
	// observed. It is never reported as applied.
	OutcomeUnverified Outcome = "unverified"
)

// Overall is the closed aggregate vocabulary for a whole Apply.
type Overall string

const (
	// OverallApplied means every unit applied.
	OverallApplied Overall = "applied"
	// OverallCompletedDegraded means the core came up but at least one unit did
	// not. The rollout is usable and incomplete, and says so.
	OverallCompletedDegraded Overall = "completed_degraded"
	// OverallFailed means a unit the stack depends on did not apply.
	OverallFailed Overall = "failed"
	// OverallBlocked means host admission refused before anything was mutated.
	OverallBlocked Overall = "blocked"
)

// Criticality decides whether one unit's failure fails the whole Apply. It is
// declared by the kit authority, never inferred here.
type Criticality string

const (
	// CriticalityCore is the runtime the stack cannot exist without.
	CriticalityCore Criticality = "core"
	// CriticalityPlatform is the delivery machinery workloads depend on.
	CriticalityPlatform Criticality = "platform"
	// CriticalityWorkload is one user-facing application.
	CriticalityWorkload Criticality = "workload"
	// CriticalityAddon is an optional capability.
	CriticalityAddon Criticality = "addon"
)

// Critical reports whether a failure of this tier must fail the whole Apply.
func (c Criticality) Critical() bool {
	return c == CriticalityCore || c == CriticalityPlatform || c == ""
}

// Subject identifies one unit using the same keys the verified Apply result
// already exposes, so a consumer can correlate a ledger row with applied
// workload identities without a second vocabulary.
type Subject struct {
	WorkloadRef         string `json:"workloadRef,omitempty"`
	RequirementID       string `json:"requirementId"`
	InstanceRef         string `json:"instanceRef,omitempty"`
	RuntimeOwnerRef     string `json:"runtimeOwnerRef,omitempty"`
	ModuleRef           string `json:"moduleRef,omitempty"`
	SiteRef             string `json:"siteRef,omitempty"`
	NodeRef             string `json:"nodeRef,omitempty"`
	ExecutionChannelRef string `json:"executionChannelRef,omitempty"`
}

// Failure states why a unit did not apply, in terms a caller can act on.
type Failure struct {
	Class       string   `json:"class"`
	Code        string   `json:"code,omitempty"`
	Retryable   bool     `json:"retryable"`
	Transient   bool     `json:"transient"`
	Message     string   `json:"message,omitempty"`
	Remediation []string `json:"remediation,omitempty"`
}

// HealthOutcome is one health target's observed state.
type HealthOutcome struct {
	RequirementID string `json:"requirementId"`
	TargetRef     string `json:"targetRef,omitempty"`
	Status        string `json:"status"`
}

// Unit is one runtime requirement and everything known about its fate.
type Unit struct {
	Ref          string          `json:"ref"`
	Kind         string          `json:"kind"`
	Subject      Subject         `json:"subject"`
	Criticality  Criticality     `json:"criticality"`
	Outcome      Outcome         `json:"outcome"`
	Failure      *Failure        `json:"failure,omitempty"`
	Health       []HealthOutcome `json:"health,omitempty"`
	StepID       string          `json:"stepId,omitempty"`
	JournalState string          `json:"journalState,omitempty"`
}

// Summary counts units per outcome so a caller can render a headline without
// walking the list.
type Summary struct {
	Applied    int `json:"applied"`
	Degraded   int `json:"degraded"`
	Failed     int `json:"failed"`
	Skipped    int `json:"skipped"`
	Unverified int `json:"unverified"`
}

// Next tells the caller how to continue after an incomplete Apply.
type Next struct {
	Resumable bool   `json:"resumable"`
	Command   string `json:"command,omitempty"`
}

// Ledger is the per-unit account of one Apply.
type Ledger struct {
	SchemaVersion string    `json:"schemaVersion"`
	Phase         string    `json:"phase"`
	PlanHash      string    `json:"planHash,omitempty"`
	OperationID   string    `json:"operationId,omitempty"`
	ObservedAt    time.Time `json:"observedAt"`
	Overall       Overall   `json:"overall"`
	Summary       Summary   `json:"summary"`
	Units         []Unit    `json:"units"`
	Next          *Next     `json:"next,omitempty"`
}

// Blocked returns the ledger for an Apply that host admission refused. Nothing
// was mutated, so every declared unit is reported as never attempted.
func Blocked(requirements generationartifact.ApplyRequirements, planHash string, observedAt time.Time) Ledger {
	ledger := Ledger{
		SchemaVersion: SchemaVersion, Phase: "apply", PlanHash: planHash,
		ObservedAt: observedAt, Overall: OverallBlocked,
	}
	for _, requirement := range requirements.RuntimeInstances {
		ledger.Units = append(ledger.Units, Unit{
			Ref: unitRef(requirement.ID), Kind: "runtime",
			Subject: subjectFor(requirement), Criticality: criticalityFor(requirement),
			Outcome: OutcomeSkipped,
		})
	}
	finalize(&ledger)
	return ledger
}

// Applied returns the ledger for an Apply where every declared unit came up.
// It is built from the plan the sealed result verified, so a fully successful
// run reports the same shape as a partial one.
func Applied(requirements generationartifact.ApplyRequirements, planHash string, observedAt time.Time) Ledger {
	ledger := Ledger{
		SchemaVersion: SchemaVersion, Phase: "apply", PlanHash: planHash,
		ObservedAt: observedAt, Overall: OverallApplied,
	}
	healthByRuntime := healthByRuntimeRequirement(requirements)
	for _, requirement := range requirements.RuntimeInstances {
		ledger.Units = append(ledger.Units, Unit{
			Ref: unitRef(requirement.ID), Kind: "runtime",
			Subject: subjectFor(requirement), Criticality: criticalityFor(requirement),
			Outcome: OutcomeApplied, Health: healthByRuntime[requirement.ID],
		})
	}
	finalize(&ledger)
	return ledger
}

// StepOutcome is one journaled child step, paired with its recorded state.
type StepOutcome struct {
	Step  runtimeapply.Step
	State runtimeapply.StepSnapshot
}

// FromJournal builds the ledger for an Apply that stopped part-way, using the
// durable journal as the account of what each step did. Steps the journal never
// reached are reported skipped rather than failed: they were not attempted, and
// saying otherwise would send an operator after the wrong problem.
func FromJournal(
	requirements generationartifact.ApplyRequirements,
	steps []StepOutcome,
	planHash, operationID, cause string,
	observedAt time.Time,
) Ledger {
	ledger := Ledger{
		SchemaVersion: SchemaVersion, Phase: "apply", PlanHash: planHash,
		OperationID: operationID, ObservedAt: observedAt,
	}
	stateByRequirement := map[string]StepOutcome{}
	for _, step := range steps {
		for _, runtime := range step.Step.Runtime {
			stateByRequirement[runtime.RequirementID] = step
		}
	}
	healthByRuntime := healthByRuntimeRequirement(requirements)

	for _, requirement := range requirements.RuntimeInstances {
		unit := Unit{
			Ref: unitRef(requirement.ID), Kind: "runtime",
			Subject: subjectFor(requirement), Criticality: criticalityFor(requirement),
			Outcome: OutcomeSkipped, Health: healthByRuntime[requirement.ID],
		}
		if step, journaled := stateByRequirement[requirement.ID]; journaled {
			unit.StepID = step.State.StepID
			unit.JournalState = string(step.State.State)
			unit.Outcome = outcomeForStepState(step.State.State)
			if unit.Outcome == OutcomeFailed {
				unit.Failure = failureFor(step.State.FailureCode, cause)
			}
			if unit.Outcome != OutcomeApplied {
				unit.Health = markHealthNotObserved(unit.Health)
			}
		} else {
			unit.Health = markHealthNotObserved(unit.Health)
		}
		ledger.Units = append(ledger.Units, unit)
	}
	finalize(&ledger)
	if ledger.Overall != OverallApplied {
		ledger.Next = &Next{Resumable: operationID != "", Command: "stackkit apply"}
	}
	return ledger
}

func outcomeForStepState(state runtimeapply.StepState) Outcome {
	switch state {
	case runtimeapply.StepSucceeded:
		return OutcomeApplied
	case runtimeapply.StepFailed:
		return OutcomeFailed
	case runtimeapply.StepRunning:
		// The process died while this step was in flight, so what it changed on
		// the host is genuinely unknown.
		return OutcomeUnverified
	default:
		return OutcomeSkipped
	}
}

func failureFor(code runtimeapply.FailureCode, cause string) *Failure {
	classification := applyoutcome.Classify(cause)
	failure := &Failure{
		Class:       string(classification.Class),
		Code:        string(code),
		Retryable:   classification.Retryable,
		Transient:   classification.Transient,
		Message:     cause,
		Remediation: classification.Remediation,
	}
	if classification.Class == applyoutcome.ClassUnknown && code == runtimeapply.FailureCancelled {
		failure.Class = string(applyoutcome.ClassCancelled)
		failure.Retryable = applyoutcome.Retryable(applyoutcome.ClassCancelled)
		failure.Remediation = applyoutcome.Remediation(applyoutcome.ClassCancelled)
	}
	return failure
}

func markHealthNotObserved(health []HealthOutcome) []HealthOutcome {
	for index := range health {
		health[index].Status = "unverified"
	}
	return health
}

func healthByRuntimeRequirement(requirements generationartifact.ApplyRequirements) map[string][]HealthOutcome {
	result := map[string][]HealthOutcome{}
	for _, health := range requirements.HealthRequirements {
		result[health.RuntimeRequirementID] = append(result[health.RuntimeRequirementID], HealthOutcome{
			RequirementID: health.ID, TargetRef: health.TargetRef, Status: "healthy",
		})
	}
	return result
}

func subjectFor(requirement generationartifact.ApplyRuntimeRequirement) Subject {
	subject := Subject{
		RequirementID: requirement.ID, InstanceRef: requirement.InstanceRef,
		RuntimeOwnerRef: requirement.OwnerRef, ModuleRef: requirement.ModuleRef,
		WorkloadRef: requirement.WorkloadRef,
	}
	if len(requirement.SiteRefs) == 1 {
		subject.SiteRef = requirement.SiteRefs[0]
	}
	if len(requirement.NodeRefs) == 1 {
		subject.NodeRef = requirement.NodeRefs[0]
	}
	return subject
}

// criticalityFor reports the declared tier of one requirement. A requirement
// bound to a workload is a workload; everything else is core until the module
// catalog declares otherwise, because refusing to guess is safer than treating
// an undeclared runtime as optional.
func criticalityFor(requirement generationartifact.ApplyRuntimeRequirement) Criticality {
	if requirement.WorkloadRef != "" {
		return CriticalityWorkload
	}
	return CriticalityCore
}

func unitRef(requirementID string) string { return "runtime:" + requirementID }

// finalize counts the units and derives the aggregate. The rule is worst-of
// weighted by criticality: a failing core unit fails the Apply, while a failing
// workload leaves a usable, explicitly incomplete stack.
func finalize(ledger *Ledger) {
	sort.SliceStable(ledger.Units, func(i, j int) bool { return ledger.Units[i].Ref < ledger.Units[j].Ref })
	criticalFailure := false
	incomplete := false
	for _, unit := range ledger.Units {
		switch unit.Outcome {
		case OutcomeApplied:
			ledger.Summary.Applied++
		case OutcomeDegraded:
			ledger.Summary.Degraded++
			incomplete = true
		case OutcomeFailed:
			ledger.Summary.Failed++
			incomplete = true
			if unit.Criticality.Critical() {
				criticalFailure = true
			}
		case OutcomeSkipped:
			ledger.Summary.Skipped++
			incomplete = true
		case OutcomeUnverified:
			ledger.Summary.Unverified++
			incomplete = true
		}
	}
	if ledger.Overall == OverallBlocked {
		return
	}
	switch {
	case criticalFailure:
		ledger.Overall = OverallFailed
	case incomplete:
		ledger.Overall = OverallCompletedDegraded
	default:
		ledger.Overall = OverallApplied
	}
}
