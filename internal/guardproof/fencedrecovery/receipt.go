package fencedrecovery

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/contracts/canonicaljson"
)

// Entry is one hash-chained receipt row: the attempted transition, the exact
// evidence it was decided on, its outcome and the resulting state. Refused
// attempts are recorded too; a blocked promotion is evidence, not silence.
type Entry struct {
	Seq                int                `json:"seq"`
	Kind               Kind               `json:"kind"`
	At                 time.Time          `json:"at"`
	Outcome            Outcome            `json:"outcome"`
	Code               Code               `json:"code,omitempty"`
	Detail             string             `json:"detail,omitempty"`
	EvidenceAgeSeconds float64            `json:"evidenceAgeSeconds"`
	Fence              *FenceEvidence     `json:"fence,omitempty"`
	Promote            *PromotionEvidence `json:"promote,omitempty"`
	Drain              *DrainEvidence     `json:"drain,omitempty"`
	Failback           *FailbackEvidence  `json:"failback,omitempty"`
	After              State              `json:"after"`
	PrevDigest         string             `json:"prevDigest"`
	Digest             string             `json:"digest"`
}

// Loss is a transaction-loss figure together with how it was obtained.
type Loss struct {
	Count uint64 `json:"count"`
	Basis string `json:"basis"`
}

// Measurements are derived from the entries of the latest recovery cycle only.
// Nil means not measured yet; it never means zero.
type Measurements struct {
	Cycle               int      `json:"cycle"`
	RecoverySeconds     *float64 `json:"recoverySeconds,omitempty"`
	FailbackSeconds     *float64 `json:"failbackSeconds,omitempty"`
	PromotionLoss       *Loss    `json:"promotionLoss,omitempty"`
	ReconciledLoss      *Loss    `json:"reconciledLoss,omitempty"`
	ZeroDataLossClaimed bool     `json:"zeroDataLossClaimed"`
	Unknowns            []string `json:"unknowns"`
}

// Receipt is the structured fenced-recovery and failback receipt. It carries
// the exact scope, versions, policy, every attempt and the derived
// measurements; Verify replays it without trusting any stored field.
type Receipt struct {
	APIVersion   string       `json:"apiVersion"`
	Scope        string       `json:"scope"`
	Topology     Topology     `json:"topology"`
	Policy       Policy       `json:"policy"`
	Genesis      string       `json:"genesis"`
	Entries      []Entry      `json:"entries"`
	Final        State        `json:"final"`
	Measurements Measurements `json:"measurements"`
	Digest       string       `json:"digest"`
}

// Machine records transitions into a receipt. It is safe for concurrent use;
// transitions are serialized, so two callers can never both win a promotion.
type Machine struct {
	mu       sync.Mutex
	topology Topology
	policy   Policy
	state    State
	entries  []Entry
}

// New starts a machine with the declared primary holding epoch 1.
func New(t Topology, p Policy) (*Machine, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &Machine{topology: t, policy: p, state: initialState(t)}, nil
}

// Restore resumes a machine from a verified receipt, so a restart can neither
// lose an epoch nor forget a fence.
func Restore(r Receipt) (*Machine, error) {
	if err := Verify(r); err != nil {
		return nil, err
	}
	return &Machine{topology: r.Topology, policy: r.Policy, state: r.Final, entries: append([]Entry(nil), r.Entries...)}, nil
}

// State is the current state.
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Fence admits fencing of the current writer. A failed or unavailable fence is
// refused and leaves the machine unable to promote.
func (m *Machine) Fence(ev FenceEvidence, now time.Time) error {
	return m.apply(Entry{Kind: KindFence, Fence: &ev}, now)
}

// Promote makes the standby the single writer at the fenced epoch.
func (m *Machine) Promote(ev PromotionEvidence, now time.Time) error {
	return m.apply(Entry{Kind: KindPromote, Promote: &ev}, now)
}

// Drain stops the promoted writer cleanly, the first step of failback.
func (m *Machine) Drain(ev DrainEvidence, now time.Time) error {
	return m.apply(Entry{Kind: KindDrain, Drain: &ev}, now)
}

// Failback returns the writer role to the reconciled original primary at a new epoch.
func (m *Machine) Failback(ev FailbackEvidence, now time.Time) error {
	return m.apply(Entry{Kind: KindFailback, Failback: &ev}, now)
}

func (m *Machine) apply(e Entry, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := len(m.entries); n > 0 && now.Before(m.entries[n-1].At) {
		return errors.New("fencedrecovery: the clock moved backwards; refusing to record an unverifiable receipt")
	}
	e.At = now.UTC()
	e.Seq = len(m.entries) + 1
	e.PrevDigest = m.tip()
	next, refusal := step(m.topology, m.policy, m.state, &e)
	e.Outcome, e.After = OutcomeApplied, next
	if refusal != nil {
		e.Outcome, e.Code, e.Detail, e.After = OutcomeRefused, refusal.Code, refusal.Detail, m.state
	}
	e.EvidenceAgeSeconds = evidenceAge(&e)
	e.Digest = digest(e)
	m.entries = append(m.entries, e)
	if refusal != nil {
		return refusal
	}
	m.state = next
	return nil
}

func (m *Machine) tip() string {
	if len(m.entries) == 0 {
		return genesis(m.topology, m.policy)
	}
	return m.entries[len(m.entries)-1].Digest
}

// Receipt snapshots the receipt of everything recorded so far.
func (m *Machine) Receipt() Receipt {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Receipt{
		APIVersion: APIVersion, Scope: Scope, Topology: m.topology, Policy: m.policy,
		Genesis: genesis(m.topology, m.policy), Entries: append([]Entry(nil), m.entries...),
		Final: m.state, Measurements: measure(m.topology, m.entries), Digest: m.tip(),
	}
}

// Verify admits a receipt only when every stored fact reproduces: the chain,
// each decision replayed through the same transition function, the strictly
// increasing epochs, the final state and the measurements. Any difference is
// an error, so a receipt cannot claim a recovery the evidence does not support.
func Verify(r Receipt) error {
	if r.APIVersion != APIVersion || r.Scope != Scope {
		return errors.New("fencedrecovery: unsupported receipt version or scope")
	}
	if err := r.Topology.Validate(); err != nil {
		return err
	}
	if err := r.Policy.Validate(); err != nil {
		return err
	}
	prev, state, last := genesis(r.Topology, r.Policy), initialState(r.Topology), time.Time{}
	if r.Genesis != prev {
		return errors.New("fencedrecovery: genesis digest does not bind the topology and policy")
	}
	for i, e := range r.Entries {
		if e.Seq != i+1 || e.PrevDigest != prev || e.At.Before(last) {
			return fmt.Errorf("fencedrecovery: entry %d breaks the chain or time order", i+1)
		}
		next, refusal := step(r.Topology, r.Policy, state, &e)
		wantOutcome, wantCode := OutcomeApplied, Code("")
		if refusal != nil {
			wantOutcome, wantCode, next = OutcomeRefused, refusal.Code, state
		}
		if e.Outcome != wantOutcome || e.Code != wantCode || !same(e.After, next) {
			return fmt.Errorf("fencedrecovery: entry %d does not replay to its recorded decision", i+1)
		}
		if e.EvidenceAgeSeconds != evidenceAge(&e) {
			return fmt.Errorf("fencedrecovery: entry %d misstates its evidence age", i+1)
		}
		if next.Epoch < state.Epoch {
			return fmt.Errorf("fencedrecovery: entry %d moves the epoch backwards", i+1)
		}
		if e.Digest != digest(e) {
			return fmt.Errorf("fencedrecovery: entry %d digest does not match its content", i+1)
		}
		prev, state, last = e.Digest, next, e.At
	}
	if r.Digest != prev || !same(r.Final, state) {
		return errors.New("fencedrecovery: receipt tip or final state does not match the replay")
	}
	if !same(r.Measurements, measure(r.Topology, r.Entries)) {
		return errors.New("fencedrecovery: measurements do not match the evidence")
	}
	return nil
}

// same compares by canonical JSON: time values round-trip by instant and
// offset, which reflect.DeepEqual does not guarantee.
func same(a, b any) bool { return hash(a) == hash(b) }

func withoutDigest(e Entry) Entry { e.Digest = ""; return e }

func digest(e Entry) string { return hash(withoutDigest(e)) }

func genesis(t Topology, p Policy) string {
	return hash(struct {
		APIVersion string   `json:"apiVersion"`
		Scope      string   `json:"scope"`
		Topology   Topology `json:"topology"`
		Policy     Policy   `json:"policy"`
	}{APIVersion, Scope, t, p})
}

func hash(v any) string {
	raw, err := canonicaljson.CanonicalJSON(v)
	if err != nil { // plain data types only; unreachable
		panic(err)
	}
	return canonicaljson.SHA256Hex(raw)
}

// evidenceAge is how old the oldest observation behind a decision was when it
// was decided, in seconds.
func evidenceAge(e *Entry) float64 {
	var times []time.Time
	switch {
	case e.Fence != nil:
		times = []time.Time{e.Fence.AppliedAt, e.Fence.Probe.ProbedAt}
	case e.Promote != nil:
		times = []time.Time{e.Promote.ObservedAt, e.Promote.OldPrimaryProbe.ProbedAt, e.Promote.ServiceReadyAt}
	case e.Drain != nil:
		times = []time.Time{e.Drain.ObservedAt, e.Drain.Probe.ProbedAt}
	case e.Failback != nil:
		times = []time.Time{e.Failback.ObservedAt, e.Failback.ServiceReadyAt}
	}
	age := time.Duration(0)
	for _, t := range times {
		if d := e.At.Sub(t); !t.IsZero() && d > age {
			age = d
		}
	}
	return age.Seconds()
}

const (
	basisLowerBound = "asynchronous-replication-lower-bound: commits after the old primary's last observed position are unobservable until it is reconciled"
	basisReconciled = "reconciled-divergence: commits on the old primary that never reached the standby, counted at failback"
)

// measure derives the figures of the latest recovery cycle from applied
// entries. A cycle starts at an applied fence from the steady phase.
func measure(t Topology, entries []Entry) Measurements {
	m := Measurements{Unknowns: []string{}}
	var fence *FenceEvidence
	var promotion *PromotionState
	var drainAt time.Time
	for _, e := range entries {
		if e.Outcome != OutcomeApplied {
			continue
		}
		switch e.Kind {
		case KindFence:
			m = Measurements{Cycle: m.Cycle + 1, Unknowns: []string{}}
			fence, promotion, drainAt = e.Fence, nil, time.Time{}
		case KindPromote:
			promotion = e.After.Promotion
			secs := promotion.ServiceReadyAt.Sub(fence.PartitionDetectedAt).Seconds()
			m.RecoverySeconds = &secs
			if known := promotion.PrimaryLastKnownPosition; known != nil {
				var lost uint64
				if *known > promotion.AppliedPosition {
					lost = *known - promotion.AppliedPosition
				}
				m.PromotionLoss = &Loss{Count: lost, Basis: basisLowerBound}
			}
		case KindDrain:
			drainAt = e.Drain.ObservedAt
		case KindFailback:
			secs := e.Failback.ServiceReadyAt.Sub(drainAt).Seconds()
			m.FailbackSeconds = &secs
			m.ReconciledLoss = &Loss{Count: e.Failback.DivergentTransactions, Basis: basisReconciled}
			// Only synchronous replication with nothing divergent may say so.
			m.ZeroDataLossClaimed = t.Replication == ModeSynchronous && e.Failback.DivergentTransactions == 0
		}
	}
	if m.Cycle == 0 {
		m.Unknowns = append(m.Unknowns, "no recovery has been attempted: recovery time and transaction loss are unmeasured")
		return m
	}
	if promotion == nil {
		m.Unknowns = append(m.Unknowns, "the standby has not been promoted: recovery time is unmeasured")
	} else if m.PromotionLoss == nil {
		m.Unknowns = append(m.Unknowns, "the old primary's last commit position was never observed: transaction loss at promotion is unknown")
	}
	if m.ReconciledLoss == nil {
		m.Unknowns = append(m.Unknowns, "exact lost transactions are unknown until the old primary is reconciled at failback")
	}
	if t.Replication == ModeAsynchronous {
		m.Unknowns = append(m.Unknowns, "asynchronous replication: zero data loss is not claimed")
	}
	return m
}
