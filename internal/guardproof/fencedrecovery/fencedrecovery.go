// Package fencedrecovery is the deterministic admission state machine behind
// Guard F: fenced multi-site recovery and failback for one warm-standby
// database/application pair on two sites.
//
// Exactly one site holds the writer epoch at any time. The standby may become
// the writer only after an independently controlled fencing method has
// verifiably stopped the old primary at a higher epoch and the old primary's
// write lease has expired; failback needs a clean drain of the promoted writer
// and a primary that has reconciled to the current epoch. Every transition is
// fail-closed: missing, stale, failed or unavailable evidence is a Refusal, the
// state stays where it was, and the refusal is itself recorded in the receipt.
//
// Process-generation fencing is not database-primary fencing. A restarted or
// superseded process generation says nothing about whether the old database
// primary can still commit, so FenceProcessGeneration is never admissible.
//
// The package decides and records; it neither fences, promotes nor replicates.
// Callers supply observed evidence and the clock, which keeps every decision
// replayable from the receipt alone.
package fencedrecovery

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// APIVersion of the structured fenced-recovery and failback receipt.
const APIVersion = "stackkit.fenced-recovery-receipt/v1"

// Scope is the exact supported slice a receipt speaks for.
const Scope = "one Linux warm-standby database/application pair on two sites with one independently controlled writer-fencing method"

// SiteRef names one site. Epoch is a monotonic writer token.
type (
	SiteRef string
	Phase   string
	Kind    string
	Outcome string
	Code    string
	// FenceMethod names the independent writer-fencing control.
	FenceMethod string
	// FenceResult is the fencing controller's own verdict.
	FenceResult string
	// ReplicationMode is how the standby follows the primary.
	ReplicationMode string
)

const (
	PhaseSteady   Phase = "steady"   // one writer, no recovery in flight
	PhaseFenced   Phase = "fenced"   // old primary fenced, no writer
	PhasePromoted Phase = "promoted" // standby is the single writer
	PhaseDraining Phase = "draining" // promoted writer drained, no writer

	KindFence    Kind = "fence"
	KindPromote  Kind = "promote"
	KindDrain    Kind = "drain"
	KindFailback Kind = "failback"

	OutcomeApplied Outcome = "applied"
	OutcomeRefused Outcome = "refused"

	// FenceProcessGeneration is deliberately not an admissible method.
	FenceProcessGeneration FenceMethod = "process-generation"
	FenceStorageToken      FenceMethod = "storage-token"
	FenceNetworkIsolation  FenceMethod = "network-isolation"
	FencePowerIsolation    FenceMethod = "power-isolation"
	FenceDatabaseLock      FenceMethod = "database-primary-lock"

	FenceFenced      FenceResult = "fenced"
	FenceFailed      FenceResult = "failed"
	FenceUnavailable FenceResult = "unavailable"

	ModeAsynchronous ReplicationMode = "asynchronous"
	ModeSynchronous  ReplicationMode = "synchronous"
)

// Refusal codes. A refusal never changes state.
const (
	CodePhaseMismatch         Code = "phase_mismatch"
	CodeEvidenceMalformed     Code = "evidence_malformed"
	CodeEvidenceStale         Code = "evidence_stale"
	CodeTargetNotWriter       Code = "target_not_writer"
	CodeFenceNotDatabase      Code = "fence_not_database_primary"
	CodeFenceMethodMismatch   Code = "fence_method_mismatch"
	CodeFenceNotIndependent   Code = "fence_not_independent"
	CodeFenceUnavailable      Code = "fence_unavailable"
	CodeFenceFailed           Code = "fence_failed"
	CodeFenceUnverified       Code = "fence_unverified"
	CodeEpochNotMonotonic     Code = "epoch_not_monotonic"
	CodeEpochMismatch         Code = "epoch_mismatch"
	CodeWriteProbeNotRefused  Code = "write_probe_not_refused"
	CodeLeaseNotExpired       Code = "lease_not_expired"
	CodeFenceNotVerified      Code = "fence_not_verified"
	CodeSiteMismatch          Code = "site_mismatch"
	CodeReplicaUnhealthy      Code = "replica_unhealthy"
	CodeDrainRequired         Code = "drain_required"
	CodeDrainNotClean         Code = "drain_not_clean"
	CodePositionRegressed     Code = "position_regressed"
	CodePrimaryUnreachable    Code = "primary_unreachable"
	CodeEpochNotReconciled    Code = "epoch_not_reconciled"
	CodeReplicaNotCaughtUp    Code = "replica_not_caught_up"
	CodeDivergenceUnquaranted Code = "divergence_unquarantined"
	CodeStandbyNotDemoted     Code = "standby_not_demoted"
)

// Refusal is the typed fail-closed error of every refused transition.
type Refusal struct {
	Code   Code
	Detail string
}

func (r *Refusal) Error() string { return fmt.Sprintf("fencedrecovery: %s: %s", r.Code, r.Detail) }

func refuse(code Code, format string, args ...any) *Refusal {
	return &Refusal{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Topology is the fixed two-site slice. ControlRef names the control plane
// each site is managed by; FenceControllerRef names the control that applies
// the fence and must be independent of both, because a partitioned primary's
// own control plane cannot be trusted to stop it.
type Topology struct {
	StackRef           string          `json:"stackRef"`
	Primary            SiteRef         `json:"primary"`
	Standby            SiteRef         `json:"standby"`
	PrimaryControlRef  string          `json:"primaryControlRef"`
	StandbyControlRef  string          `json:"standbyControlRef"`
	FenceMethod        FenceMethod     `json:"fenceMethod"`
	FenceControllerRef string          `json:"fenceControllerRef"`
	Replication        ReplicationMode `json:"replication"`
	DatabaseEngine     string          `json:"databaseEngine"`
	DatabaseVersion    string          `json:"databaseVersion"`
	StackKitsVersion   string          `json:"stackkitsVersion"`
}

// Policy bounds how old evidence may be and how much clock disagreement is
// tolerated between the sites and the evaluating process.
type Policy struct {
	MaxEvidenceAge time.Duration `json:"maxEvidenceAge"`
	ClockSkew      time.Duration `json:"clockSkew"`
}

// DefaultPolicy is two minutes of evidence age and five seconds of skew.
func DefaultPolicy() Policy {
	return Policy{MaxEvidenceAge: 2 * time.Minute, ClockSkew: 5 * time.Second}
}

var refPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// Validate fails closed on any topology that cannot support the slice.
func (t Topology) Validate() error {
	for name, v := range map[string]string{
		"stackRef": t.StackRef, "primary": string(t.Primary), "standby": string(t.Standby),
		"primaryControlRef": t.PrimaryControlRef, "standbyControlRef": t.StandbyControlRef,
		"fenceControllerRef": t.FenceControllerRef,
	} {
		if !refPattern.MatchString(v) {
			return fmt.Errorf("fencedrecovery: %s %q is not a portable reference", name, v)
		}
	}
	if t.Primary == t.Standby || t.PrimaryControlRef == t.StandbyControlRef {
		return errors.New("fencedrecovery: sites and their control planes must be distinct")
	}
	if t.FenceControllerRef == t.PrimaryControlRef || t.FenceControllerRef == t.StandbyControlRef {
		return errors.New("fencedrecovery: the fence controller must be independent of both site control planes")
	}
	switch t.FenceMethod {
	case FenceStorageToken, FenceNetworkIsolation, FencePowerIsolation, FenceDatabaseLock:
	case FenceProcessGeneration:
		return errors.New("fencedrecovery: process-generation fencing is not database-primary fencing")
	default:
		return fmt.Errorf("fencedrecovery: unsupported fence method %q", t.FenceMethod)
	}
	if t.Replication != ModeAsynchronous && t.Replication != ModeSynchronous {
		return fmt.Errorf("fencedrecovery: unsupported replication mode %q", t.Replication)
	}
	if t.DatabaseEngine == "" || t.DatabaseVersion == "" || t.StackKitsVersion == "" {
		return errors.New("fencedrecovery: exact database engine, database version and StackKits version are required")
	}
	return nil
}

// Validate bounds the policy so a typo cannot disable freshness checking.
func (p Policy) Validate() error {
	if p.MaxEvidenceAge <= 0 || p.MaxEvidenceAge > 10*time.Minute || p.ClockSkew < 0 || p.ClockSkew > 30*time.Second {
		return errors.New("fencedrecovery: evidence age must be in (0, 10m] and clock skew in [0, 30s]")
	}
	return nil
}

func (p Policy) fresh(now, t time.Time) bool {
	return !t.IsZero() && !t.After(now.Add(p.ClockSkew)) && now.Sub(t) <= p.MaxEvidenceAge
}

// WriteProbe is a write attempted with a given epoch token and its outcome.
type WriteProbe struct {
	Epoch    uint64    `json:"epoch"`
	Refused  bool      `json:"refused"`
	ProbedAt time.Time `json:"probedAt"`
}

// FenceEvidence is the fencing controller's report after fencing the writer.
// EnforcedEpoch is the lowest writer epoch the data tier now accepts.
type FenceEvidence struct {
	Method               FenceMethod `json:"method"`
	ControllerRef        string      `json:"controllerRef"`
	Target               SiteRef     `json:"target"`
	Result               FenceResult `json:"result"`
	EnforcedEpoch        uint64      `json:"enforcedEpoch"`
	PartitionDetectedAt  time.Time   `json:"partitionDetectedAt"`
	WriterLeaseExpiresAt time.Time   `json:"writerLeaseExpiresAt"`
	AppliedAt            time.Time   `json:"appliedAt"`
	Probe                WriteProbe  `json:"probe"`
}

// PromotionEvidence is the standby's report that it is ready to be the writer.
// OldPrimaryProbe re-proves, immediately before promotion, that the old
// primary still cannot write. PrimaryLastKnownPosition is nil when the old
// primary's commit position was never observed.
type PromotionEvidence struct {
	Site                     SiteRef    `json:"site"`
	Epoch                    uint64     `json:"epoch"`
	ReplicaHealthy           bool       `json:"replicaHealthy"`
	ReplicaAppliedPosition   uint64     `json:"replicaAppliedPosition"`
	PrimaryLastKnownPosition *uint64    `json:"primaryLastKnownPosition,omitempty"`
	OldPrimaryProbe          WriteProbe `json:"oldPrimaryProbe"`
	ObservedAt               time.Time  `json:"observedAt"`
	ServiceReadyAt           time.Time  `json:"serviceReadyAt"`
}

// DrainEvidence is the promoted writer's report that it stopped accepting
// writes with nothing in flight, so failback starts from a final position.
type DrainEvidence struct {
	Site             SiteRef    `json:"site"`
	Epoch            uint64     `json:"epoch"`
	PrimaryReachable bool       `json:"primaryReachable"`
	InFlightWrites   uint64     `json:"inFlightWrites"`
	FinalPosition    uint64     `json:"finalPosition"`
	Probe            WriteProbe `json:"probe"`
	ObservedAt       time.Time  `json:"observedAt"`
}

// FailbackEvidence is the original primary's report after it followed the
// promoted writer. DivergentTransactions are commits the old primary made that
// never reached the standby: the exact, measured loss of the recovery. They
// must be quarantined, never silently dropped or replayed.
type FailbackEvidence struct {
	Site                   SiteRef   `json:"site"`
	NewEpoch               uint64    `json:"newEpoch"`
	PrimaryEpochSeen       uint64    `json:"primaryEpochSeen"`
	PrimaryAppliedPosition uint64    `json:"primaryAppliedPosition"`
	DivergentTransactions  uint64    `json:"divergentTransactions"`
	DivergentQuarantineRef string    `json:"divergentQuarantineRef,omitempty"`
	StandbyDemoted         bool      `json:"standbyDemoted"`
	ObservedAt             time.Time `json:"observedAt"`
	ServiceReadyAt         time.Time `json:"serviceReadyAt"`
}

// State is the complete machine state. Writer is empty while no site may write.
type State struct {
	Phase     Phase           `json:"phase"`
	Writer    SiteRef         `json:"writer,omitempty"`
	Epoch     uint64          `json:"epoch"`
	Fence     *FenceEvidence  `json:"fence,omitempty"`
	Promotion *PromotionState `json:"promotion,omitempty"`
	Drain     *DrainEvidence  `json:"drain,omitempty"`
}

// PromotionState is what failback needs to remember of the promotion.
type PromotionState struct {
	AppliedPosition          uint64    `json:"appliedPosition"`
	PrimaryLastKnownPosition *uint64   `json:"primaryLastKnownPosition,omitempty"`
	ServiceReadyAt           time.Time `json:"serviceReadyAt"`
}

func initialState(t Topology) State { return State{Phase: PhaseSteady, Writer: t.Primary, Epoch: 1} }

// step is the single, pure transition function. It is used both when a
// transition is first attempted and when a receipt is replayed.
func step(t Topology, p Policy, st State, e *Entry) (State, *Refusal) {
	evidence := 0
	for _, present := range []bool{e.Fence != nil, e.Promote != nil, e.Drain != nil, e.Failback != nil} {
		if present {
			evidence++
		}
	}
	if evidence != 1 {
		return st, refuse(CodeEvidenceMalformed, "exactly one evidence document is required")
	}
	switch {
	case e.Kind == KindFence && e.Fence != nil:
		return stepFence(t, p, st, *e.Fence, e.At)
	case e.Kind == KindPromote && e.Promote != nil:
		return stepPromote(t, p, st, *e.Promote, e.At)
	case e.Kind == KindDrain && e.Drain != nil:
		return stepDrain(t, p, st, *e.Drain, e.At)
	case e.Kind == KindFailback && e.Failback != nil:
		return stepFailback(t, p, st, *e.Failback, e.At)
	}
	return st, refuse(CodeEvidenceMalformed, "evidence does not match transition %q", e.Kind)
}

func stepFence(t Topology, p Policy, st State, ev FenceEvidence, now time.Time) (State, *Refusal) {
	if st.Phase != PhaseSteady {
		return st, refuse(CodePhaseMismatch, "fencing starts from %q, not %q", PhaseSteady, st.Phase)
	}
	if ev.Target != st.Writer {
		return st, refuse(CodeTargetNotWriter, "fence target %q is not the writer %q", ev.Target, st.Writer)
	}
	switch {
	case ev.Method == FenceProcessGeneration:
		return st, refuse(CodeFenceNotDatabase, "process-generation fencing does not stop the database primary")
	case ev.Method != t.FenceMethod:
		return st, refuse(CodeFenceMethodMismatch, "fence method %q is not the declared %q", ev.Method, t.FenceMethod)
	case ev.ControllerRef != t.FenceControllerRef:
		return st, refuse(CodeFenceNotIndependent, "fence controller %q is not the declared independent controller", ev.ControllerRef)
	}
	switch ev.Result {
	case FenceFenced:
	case FenceUnavailable:
		return st, refuse(CodeFenceUnavailable, "the fencing controller is unavailable; promotion stays blocked")
	case FenceFailed:
		return st, refuse(CodeFenceFailed, "the fencing controller reported failure; promotion stays blocked")
	default:
		return st, refuse(CodeFenceUnverified, "fence result %q is not a verified fence", ev.Result)
	}
	if ev.EnforcedEpoch != st.Epoch+1 {
		return st, refuse(CodeEpochNotMonotonic, "enforced epoch %d must be exactly the current epoch %d plus one", ev.EnforcedEpoch, st.Epoch)
	}
	if !ev.Probe.Refused || ev.Probe.Epoch != st.Epoch || !ev.Probe.ProbedAt.After(ev.AppliedAt) {
		return st, refuse(CodeWriteProbeNotRefused, "the old primary's write at epoch %d was not proven refused after the fence was applied", st.Epoch)
	}
	if !p.fresh(now, ev.AppliedAt) || !p.fresh(now, ev.Probe.ProbedAt) {
		return st, refuse(CodeEvidenceStale, "fence evidence is missing, older than %s or from the future", p.MaxEvidenceAge)
	}
	if ev.PartitionDetectedAt.IsZero() || ev.PartitionDetectedAt.After(ev.AppliedAt) || ev.WriterLeaseExpiresAt.IsZero() {
		return st, refuse(CodeEvidenceMalformed, "partition detection must precede the fence and the writer lease expiry must be stated")
	}
	return State{Phase: PhaseFenced, Epoch: ev.EnforcedEpoch, Fence: &ev}, nil
}

func stepPromote(t Topology, p Policy, st State, ev PromotionEvidence, now time.Time) (State, *Refusal) {
	if st.Phase != PhaseFenced || st.Fence == nil {
		return st, refuse(CodeFenceNotVerified, "promotion needs a verified fence; phase is %q", st.Phase)
	}
	if ev.Site != t.Standby {
		return st, refuse(CodeSiteMismatch, "only the declared standby %q may be promoted", t.Standby)
	}
	if ev.Epoch != st.Epoch {
		return st, refuse(CodeEpochMismatch, "promotion epoch %d is not the fenced epoch %d", ev.Epoch, st.Epoch)
	}
	if now.Before(st.Fence.WriterLeaseExpiresAt.Add(p.ClockSkew)) {
		return st, refuse(CodeLeaseNotExpired, "the old writer's lease runs until %s", st.Fence.WriterLeaseExpiresAt.Format(time.RFC3339))
	}
	if !ev.OldPrimaryProbe.Refused || ev.OldPrimaryProbe.Epoch != st.Epoch-1 || !ev.OldPrimaryProbe.ProbedAt.After(st.Fence.AppliedAt) || ev.OldPrimaryProbe.ProbedAt.After(ev.ServiceReadyAt) {
		return st, refuse(CodeWriteProbeNotRefused, "the old primary's write must be proven refused strictly after the fence and before the standby serves")
	}
	if !ev.ReplicaHealthy {
		return st, refuse(CodeReplicaUnhealthy, "the standby is not a healthy replica")
	}
	if !p.fresh(now, ev.ObservedAt) || !p.fresh(now, ev.OldPrimaryProbe.ProbedAt) || !p.fresh(now, ev.ServiceReadyAt) {
		return st, refuse(CodeEvidenceStale, "promotion evidence is missing, older than %s or from the future", p.MaxEvidenceAge)
	}
	if ev.ServiceReadyAt.Before(st.Fence.AppliedAt) {
		return st, refuse(CodeEvidenceMalformed, "the standby cannot have served before the fence was applied")
	}
	promotion := PromotionState{AppliedPosition: ev.ReplicaAppliedPosition, PrimaryLastKnownPosition: ev.PrimaryLastKnownPosition, ServiceReadyAt: ev.ServiceReadyAt}
	return State{Phase: PhasePromoted, Writer: t.Standby, Epoch: st.Epoch, Fence: st.Fence, Promotion: &promotion}, nil
}

func stepDrain(_ Topology, p Policy, st State, ev DrainEvidence, now time.Time) (State, *Refusal) {
	if st.Phase != PhasePromoted || st.Promotion == nil {
		return st, refuse(CodePhaseMismatch, "draining starts from %q, not %q", PhasePromoted, st.Phase)
	}
	if ev.Site != st.Writer || ev.Epoch != st.Epoch {
		return st, refuse(CodeSiteMismatch, "drain must come from writer %q at epoch %d", st.Writer, st.Epoch)
	}
	if !ev.PrimaryReachable {
		return st, refuse(CodePrimaryUnreachable, "the original primary is unreachable; draining would only add an outage")
	}
	if ev.InFlightWrites != 0 || !ev.Probe.Refused || ev.Probe.Epoch != st.Epoch {
		return st, refuse(CodeDrainNotClean, "writes in flight %d, or a write at epoch %d was not refused", ev.InFlightWrites, st.Epoch)
	}
	if ev.FinalPosition < st.Promotion.AppliedPosition {
		return st, refuse(CodePositionRegressed, "final position %d is behind the promotion position %d", ev.FinalPosition, st.Promotion.AppliedPosition)
	}
	if !p.fresh(now, ev.ObservedAt) || !p.fresh(now, ev.Probe.ProbedAt) {
		return st, refuse(CodeEvidenceStale, "drain evidence is missing, older than %s or from the future", p.MaxEvidenceAge)
	}
	next := st
	next.Phase, next.Writer, next.Drain = PhaseDraining, "", &ev
	return next, nil
}

func stepFailback(t Topology, p Policy, st State, ev FailbackEvidence, now time.Time) (State, *Refusal) {
	if st.Phase == PhasePromoted {
		return st, refuse(CodeDrainRequired, "the promoted writer must be drained before failback")
	}
	if st.Phase != PhaseDraining || st.Drain == nil {
		return st, refuse(CodePhaseMismatch, "failback starts from %q, not %q", PhaseDraining, st.Phase)
	}
	if ev.Site != t.Primary {
		return st, refuse(CodeSiteMismatch, "failback returns the writer to %q", t.Primary)
	}
	if ev.NewEpoch != st.Epoch+1 {
		return st, refuse(CodeEpochNotMonotonic, "new epoch %d must be exactly the current epoch %d plus one", ev.NewEpoch, st.Epoch)
	}
	if ev.PrimaryEpochSeen != st.Epoch {
		return st, refuse(CodeEpochNotReconciled, "the primary has seen epoch %d, not the current %d", ev.PrimaryEpochSeen, st.Epoch)
	}
	if ev.PrimaryAppliedPosition < st.Drain.FinalPosition {
		return st, refuse(CodeReplicaNotCaughtUp, "the primary applied %d of the drained final position %d", ev.PrimaryAppliedPosition, st.Drain.FinalPosition)
	}
	if ev.DivergentTransactions > 0 && ev.DivergentQuarantineRef == "" {
		return st, refuse(CodeDivergenceUnquaranted, "%d divergent transactions need a quarantine reference", ev.DivergentTransactions)
	}
	if !ev.StandbyDemoted {
		return st, refuse(CodeStandbyNotDemoted, "the standby must be demoted to a read-only replica so there is one writer")
	}
	if !p.fresh(now, ev.ObservedAt) || !p.fresh(now, ev.ServiceReadyAt) || ev.ServiceReadyAt.Before(st.Drain.ObservedAt) {
		return st, refuse(CodeEvidenceStale, "failback evidence is missing, older than %s, from the future or before the drain", p.MaxEvidenceAge)
	}
	return State{Phase: PhaseSteady, Writer: t.Primary, Epoch: ev.NewEpoch}, nil
}
