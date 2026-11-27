package exposureproof

import (
	"fmt"
	"net/netip"
	"sort"
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeobservation"
)

// State is the judgement of the whole report.
type State string

const (
	StateCompliant State = "compliant"
	StateDrifted   State = "drifted"
	StateUnknown   State = "unknown"
)

// Verdict is the judgement of one probed port.
type Verdict string

const (
	// VerdictExposed: reachable directly although the declared policy forbids it.
	VerdictExposed Verdict = "exposed"
	// VerdictContained: observed closed or filtered from an external vantage.
	VerdictContained Verdict = "contained"
	// VerdictIntended: reachable directly and the declared policy allows it.
	VerdictIntended Verdict = "intended"
	// VerdictUnknown: the evidence does not decide; Entry.Reason says why.
	VerdictUnknown Verdict = "unknown"
)

// Stable reasons of an unknown entry or route.
const (
	ReasonNoObservation        = "no_observation"
	ReasonStaleObservation     = "stale_observation"
	ReasonFutureEvidence       = "future_dated_evidence"
	ReasonStaleAddressBinding  = "stale_address_binding"
	ReasonVantageNotExternal   = "vantage_not_external"
	ReasonVantageUnavailable   = "vantage_unavailable"
	ReasonAddressNotPublic     = "address_not_public"
	ReasonNoConnectivityWitess = "no_connectivity_witness"
	ReasonProbeError           = "probe_error"
	ReasonNoJourney            = "no_journey_observation"
	ReasonStaleJourney         = "stale_journey_observation"
)

// Finding kinds.
const (
	// FindingOriginBypass: an origin address behind an intended Connect or
	// relay route answers directly, so the route can be bypassed.
	FindingOriginBypass = "origin_bypass"
	// FindingDirectExposure: an owned address answers directly on a port its
	// policy forbids and no intended route is declared for it.
	FindingDirectExposure = "direct_exposure"
	// FindingRouteFailed: the intended route did not work when observed.
	FindingRouteFailed = "intended_route_failed"
)

// CorrectionOwner is who corrects a finding. Guard only observes.
const CorrectionOwner = "route_or_firewall_owner"

// Policy bounds how old evidence may be before it stops deciding anything.
type Policy struct {
	// MaxObservationAge is how old a probe or journey observation may be.
	MaxObservationAge time.Duration
	// MaxAddressAge is how old the owner binding of an address may be.
	MaxAddressAge time.Duration
	// MaxClockSkew is how far into the future evidence may claim to be.
	MaxClockSkew time.Duration
}

// DefaultPolicy returns the shipped bounds.
func DefaultPolicy() Policy {
	return Policy{MaxObservationAge: 30 * time.Minute, MaxAddressAge: 24 * time.Hour, MaxClockSkew: 5 * time.Minute}
}

// Normalize replaces a non-positive bound with its default.
func (p Policy) Normalize() Policy {
	d := DefaultPolicy()
	if p.MaxObservationAge <= 0 {
		p.MaxObservationAge = d.MaxObservationAge
	}
	if p.MaxAddressAge <= 0 {
		p.MaxAddressAge = d.MaxAddressAge
	}
	if p.MaxClockSkew <= 0 {
		p.MaxClockSkew = d.MaxClockSkew
	}
	return p
}

// Journey is one observation of whether an intended Connect or relay route
// carries a user to the workload.
type Journey struct {
	RouteRef   string    `json:"routeRef"`
	Reached    bool      `json:"reached"`
	ObservedAt time.Time `json:"observedAt"`
	Vantage    string    `json:"vantage"`
}

// JourneyFromHTTPProbe adapts the plan-bound HTTP route probe of the runtime
// observation contract, so the intended-route check reuses the existing
// authority instead of a second probe.
func JourneyFromHTTPProbe(probe runtimeobservation.HTTPProbeEvidence) Journey {
	return Journey{RouteRef: probe.RouteRef, Reached: probe.Reached, ObservedAt: probe.ObservedAt, Vantage: probe.Vantage}
}

// Entry is the judgement of one declared port of one owned address.
type Entry struct {
	TargetRef     string      `json:"targetRef"`
	Address       netip.Addr  `json:"address"`
	Family        Family      `json:"family"`
	Port          int         `json:"port"`
	Direct        DirectReach `json:"direct"`
	Exposure      string      `json:"exposure"`
	OriginOfRoute string      `json:"originOfRoute,omitempty"`
	Verdict       Verdict     `json:"verdict"`
	Reason        string      `json:"reason,omitempty"`
	Result        Result      `json:"result,omitempty"`
	Vantage       string      `json:"vantage,omitempty"`
	ObservedAt    time.Time   `json:"observedAt,omitzero"`
	// AddressValidatedAt and AddressSource record the freshness of the owner
	// binding of the address and, when known, of its prefix.
	AddressValidatedAt time.Time     `json:"addressValidatedAt"`
	AddressSource      AddressSource `json:"addressSource"`
	Prefix             netip.Prefix  `json:"prefix,omitzero"`
}

// Key identifies the probed address and port within a scope.
func (e Entry) Key() string { return probeKey(e.Address, e.Port) }

// RouteEntry is the judgement of one intended route journey.
type RouteEntry struct {
	RouteRef   string    `json:"routeRef"`
	Status     string    `json:"status"` // ok, failed or unknown
	Reason     string    `json:"reason,omitempty"`
	Vantage    string    `json:"vantage,omitempty"`
	ObservedAt time.Time `json:"observedAt,omitzero"`
}

// Route statuses.
const (
	RouteOK      = "ok"
	RouteFailed  = "failed"
	RouteUnknown = "unknown"
)

// Finding is one structured exposure finding for the route or firewall owner.
type Finding struct {
	Key           string        `json:"key"`
	Kind          string        `json:"kind"`
	Family        Family        `json:"family,omitempty"`
	TargetRef     string        `json:"targetRef,omitempty"`
	Address       netip.Addr    `json:"address,omitzero"`
	Port          int           `json:"port,omitempty"`
	RouteRef      string        `json:"routeRef,omitempty"`
	Expected      string        `json:"expected"`
	Observed      string        `json:"observed"`
	Vantage       string        `json:"vantage,omitempty"`
	ObservedAt    time.Time     `json:"observedAt,omitzero"`
	CorrectionBy  string        `json:"correctionBy"`
	AddressSource AddressSource `json:"addressSource,omitempty"`
}

// Report is the stackkit.exposure-proof/v1 document.
type Report struct {
	Schema      string       `json:"schema"`
	GeneratedAt time.Time    `json:"generatedAt"`
	ScopeDigest string       `json:"scopeDigest"`
	Policy      ReportPolicy `json:"policy"`
	Overall     State        `json:"overall"`
	Entries     []Entry      `json:"entries"`
	Routes      []RouteEntry `json:"routes,omitempty"`
	Findings    []Finding    `json:"findings"`
	// Discarded counts observations that named an address or port outside the
	// scope. They are never judged.
	Discarded int `json:"discarded,omitempty"`
}

// ReportPolicy records the bounds the report was judged with.
type ReportPolicy struct {
	MaxObservationAgeSeconds int `json:"maxObservationAgeSeconds"`
	MaxAddressAgeSeconds     int `json:"maxAddressAgeSeconds"`
}

// Evaluate judges the observations and route journeys against the declared
// scope. Time is the caller's clock; evidence is never trusted for its own
// freshness beyond the policy bounds.
func Evaluate(scope Scope, observations []Observation, journeys []Journey, policy Policy, now time.Time) (Report, error) {
	if err := scope.Validate(); err != nil {
		return Report{}, err
	}
	policy = policy.Normalize()
	now = now.UTC()

	latest, discarded := indexObservations(scope, observations)
	witness := connectivityWitnesses(latest, policy, now)

	report := Report{
		Schema: Schema, GeneratedAt: now, ScopeDigest: scope.Digest(), Discarded: discarded,
		Policy:   ReportPolicy{MaxObservationAgeSeconds: int(policy.MaxObservationAge / time.Second), MaxAddressAgeSeconds: int(policy.MaxAddressAge / time.Second)},
		Entries:  []Entry{},
		Findings: []Finding{},
	}
	routes := map[string]bool{}
	for _, target := range scope.Targets {
		if target.OriginOfRoute != "" {
			routes[target.OriginOfRoute] = true
		}
		for _, port := range target.Ports {
			entry := judge(target, port, latest[probeKey(target.Address, port.Port)], witness, policy, now)
			report.Entries = append(report.Entries, entry)
			if entry.Verdict == VerdictExposed {
				report.Findings = append(report.Findings, exposureFinding(entry))
			}
		}
	}
	sort.Slice(report.Entries, func(i, j int) bool {
		a, b := report.Entries[i], report.Entries[j]
		if a.TargetRef != b.TargetRef {
			return a.TargetRef < b.TargetRef
		}
		if a.Address != b.Address {
			return a.Address.Less(b.Address)
		}
		return a.Port < b.Port
	})

	refs := make([]string, 0, len(routes))
	for ref := range routes {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		route := judgeRoute(ref, journeys, policy, now)
		report.Routes = append(report.Routes, route)
		if route.Status == RouteFailed {
			report.Findings = append(report.Findings, Finding{
				Key: "route:" + ref, Kind: FindingRouteFailed, RouteRef: ref,
				Expected: fmt.Sprintf("intended route %s carries the user journey", ref),
				Observed: "journey observed failing", Vantage: route.Vantage, ObservedAt: route.ObservedAt,
				CorrectionBy: CorrectionOwner,
			})
		}
	}
	sort.Slice(report.Findings, func(i, j int) bool { return report.Findings[i].Key < report.Findings[j].Key })
	report.Overall = overall(report)
	return report, nil
}

func indexObservations(scope Scope, observations []Observation) (map[string]Observation, int) {
	inScope := map[string]string{}
	for _, target := range scope.Targets {
		for _, port := range target.Ports {
			inScope[probeKey(target.Address, port.Port)] = target.Ref
		}
	}
	latest := map[string]Observation{}
	discarded := 0
	for _, observation := range observations {
		ref, ok := inScope[observation.key()]
		if !ok || ref != observation.TargetRef {
			discarded++
			continue
		}
		if current, seen := latest[observation.key()]; !seen || observation.ObservedAt.After(current.ObservedAt) {
			latest[observation.key()] = observation
		}
	}
	return latest, discarded
}

// connectivityWitnesses marks each external vantage and family that, in this
// run, demonstrably completed or was refused a connection: only then does a
// silent timeout from that vantage and family say anything.
func connectivityWitnesses(latest map[string]Observation, policy Policy, now time.Time) map[string]bool {
	witness := map[string]bool{}
	for _, observation := range latest {
		if observation.Vantage.Kind != VantageExternal || !fresh(observation.ObservedAt, policy.MaxObservationAge, policy.MaxClockSkew, now) {
			continue
		}
		if observation.Result == ResultReachable || observation.Result == ResultClosed {
			witness[witnessKey(observation.Vantage.ID, FamilyOf(observation.Address))] = true
		}
	}
	return witness
}

func witnessKey(vantage string, family Family) string { return vantage + "|" + string(family) }

func fresh(at time.Time, maxAge, skew time.Duration, now time.Time) bool {
	return !at.IsZero() && !at.After(now.Add(skew)) && now.Sub(at) <= maxAge
}

func judge(target Target, port PortPolicy, observation Observation, witness map[string]bool, policy Policy, now time.Time) Entry {
	entry := Entry{
		TargetRef: target.Ref, Address: target.Address, Family: FamilyOf(target.Address), Port: port.Port,
		Direct: port.Direct, Exposure: target.Exposure, OriginOfRoute: target.OriginOfRoute, Verdict: VerdictUnknown,
		AddressValidatedAt: target.ValidatedAt, AddressSource: target.Source, Prefix: target.Prefix,
	}
	if observation.Address.IsValid() {
		entry.Result, entry.Vantage, entry.ObservedAt = observation.Result, observation.Vantage.ID, observation.ObservedAt
	}
	unknown := func(reason string) Entry { entry.Reason = reason; return entry }

	switch {
	case target.ValidatedAt.After(now.Add(policy.MaxClockSkew)):
		return unknown(ReasonFutureEvidence)
	case now.Sub(target.ValidatedAt) > policy.MaxAddressAge:
		return unknown(ReasonStaleAddressBinding)
	case !observation.Address.IsValid():
		return unknown(ReasonNoObservation)
	case observation.ObservedAt.IsZero() || observation.ObservedAt.After(now.Add(policy.MaxClockSkew)):
		return unknown(ReasonFutureEvidence)
	case now.Sub(observation.ObservedAt) > policy.MaxObservationAge:
		return unknown(ReasonStaleObservation)
	case observation.Vantage.Validate() != nil || observation.Vantage.Kind != VantageExternal:
		return unknown(ReasonVantageNotExternal)
	case !observation.Vantage.speaks(entry.Family):
		return unknown(ReasonVantageUnavailable)
	case !publiclyRoutable(target.Address):
		return unknown(ReasonAddressNotPublic)
	}

	switch observation.Result {
	case ResultReachable:
		if port.Direct == DirectAllowed {
			entry.Verdict = VerdictIntended
		} else {
			entry.Verdict = VerdictExposed
		}
	case ResultClosed:
		entry.Verdict = VerdictContained
	case ResultFiltered:
		if !witness[witnessKey(observation.Vantage.ID, entry.Family)] {
			return unknown(ReasonNoConnectivityWitess)
		}
		entry.Verdict = VerdictContained
	case ResultVantageUnavailable:
		return unknown(ReasonVantageUnavailable)
	default:
		return unknown(ReasonProbeError)
	}
	return entry
}

func exposureFinding(entry Entry) Finding {
	kind := FindingDirectExposure
	expected := fmt.Sprintf("tcp/%d on %s is not reachable directly from outside", entry.Port, entry.Address)
	if entry.OriginOfRoute != "" {
		kind = FindingOriginBypass
		expected = fmt.Sprintf("tcp/%d on origin %s is reached only through route %s", entry.Port, entry.Address, entry.OriginOfRoute)
	}
	return Finding{
		Key: entry.Key(), Kind: kind, Family: entry.Family, TargetRef: entry.TargetRef, Address: entry.Address, Port: entry.Port,
		RouteRef: entry.OriginOfRoute, Expected: expected,
		Observed: fmt.Sprintf("tcp/%d on %s answered a direct %s connection from vantage %s", entry.Port, entry.Address, entry.Family, entry.Vantage),
		Vantage:  entry.Vantage, ObservedAt: entry.ObservedAt, CorrectionBy: CorrectionOwner, AddressSource: entry.AddressSource,
	}
}

func judgeRoute(ref string, journeys []Journey, policy Policy, now time.Time) RouteEntry {
	route := RouteEntry{RouteRef: ref, Status: RouteUnknown, Reason: ReasonNoJourney}
	var best *Journey
	for index := range journeys {
		journey := journeys[index]
		if journey.RouteRef != ref {
			continue
		}
		if best == nil || journey.ObservedAt.After(best.ObservedAt) {
			best = &journey
		}
	}
	if best == nil {
		return route
	}
	route.Vantage, route.ObservedAt = best.Vantage, best.ObservedAt
	if !fresh(best.ObservedAt, policy.MaxObservationAge, policy.MaxClockSkew, now) {
		route.Reason = ReasonStaleJourney
		if best.ObservedAt.After(now.Add(policy.MaxClockSkew)) {
			route.Reason = ReasonFutureEvidence
		}
		return route
	}
	route.Reason = ""
	if best.Reached {
		route.Status = RouteOK
	} else {
		route.Status = RouteFailed
	}
	return route
}

func overall(report Report) State {
	if len(report.Findings) > 0 {
		return StateDrifted
	}
	if len(report.Entries) == 0 {
		return StateUnknown
	}
	for _, entry := range report.Entries {
		if entry.Verdict == VerdictUnknown {
			return StateUnknown
		}
	}
	for _, route := range report.Routes {
		if route.Status != RouteOK {
			return StateUnknown
		}
	}
	return StateCompliant
}

// ClosureState is the outcome of one finding between two reports.
type ClosureState string

const (
	ClosureClosed       ClosureState = "closed"
	ClosureStillExposed ClosureState = "still_exposed"
	ClosureUnknown      ClosureState = "unknown"
	ClosureOutOfScope   ClosureState = "not_in_scope"
)

// ClosureFinding is the outcome of one exposure finding of the earlier report.
type ClosureFinding struct {
	Key    string       `json:"key"`
	State  ClosureState `json:"state"`
	Reason string       `json:"reason,omitempty"`
}

// ClosureResult says whether an authorized correction closed what an earlier
// report found while the intended journey still works. Nothing is closed on
// unknown evidence.
type ClosureResult struct {
	Findings []ClosureFinding `json:"findings"`
	// JourneyOK is true when every intended route behind a corrected finding
	// was observed working in the later report.
	JourneyOK bool `json:"journeyOk"`
	// Closed is true only when there was something to close, all of it is
	// observed closed, and the intended journey works.
	Closed bool `json:"closed"`
}

// Closure compares a report that found exposure with a later report of the
// same scope taken after the correction.
func Closure(before, after Report) ClosureResult {
	result := ClosureResult{Findings: []ClosureFinding{}, JourneyOK: true}
	afterEntries := map[string]Entry{}
	for _, entry := range after.Entries {
		afterEntries[entry.Key()] = entry
	}
	afterRoutes := map[string]RouteEntry{}
	for _, route := range after.Routes {
		afterRoutes[route.RouteRef] = route
	}
	beforeEntries := map[string]Entry{}
	for _, entry := range before.Entries {
		beforeEntries[entry.Key()] = entry
	}

	closed := 0
	needRoutes := map[string]bool{}
	for _, finding := range before.Findings {
		if finding.Kind == FindingRouteFailed {
			if finding.RouteRef != "" {
				needRoutes[finding.RouteRef] = true
			}
			continue
		}
		item := ClosureFinding{Key: finding.Key}
		entry, ok := afterEntries[finding.Key]
		switch {
		case !after.GeneratedAt.After(before.GeneratedAt):
			item.State, item.Reason = ClosureUnknown, "later_report_not_newer"
		case !ok:
			item.State = ClosureOutOfScope
		case entry.Verdict == VerdictExposed:
			item.State = ClosureStillExposed
		case entry.Verdict == VerdictContained:
			item.State = ClosureClosed
			closed++
		default:
			item.State, item.Reason = ClosureUnknown, entry.Reason
		}
		if finding.RouteRef != "" {
			needRoutes[finding.RouteRef] = true
		}
		result.Findings = append(result.Findings, item)
	}
	for ref := range needRoutes {
		if route, ok := afterRoutes[ref]; !ok || route.Status != RouteOK {
			result.JourneyOK = false
		}
	}
	exposureFindings := 0
	for _, finding := range before.Findings {
		if finding.Kind != FindingRouteFailed {
			exposureFindings++
		}
	}
	result.Closed = exposureFindings > 0 && closed == exposureFindings && result.JourneyOK
	return result
}
