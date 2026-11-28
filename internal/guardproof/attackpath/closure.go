package attackpath

import "fmt"

// Verdict is the outcome of verifying a repair.
type Verdict string

const (
	// VerdictRepaired means fresh evidence shows the path broke and every
	// intended access still works.
	VerdictRepaired Verdict = "repaired"
	// VerdictNotRepaired means every edge of the path is still open.
	VerdictNotRepaired Verdict = "not_repaired"
	// VerdictBrokeIntendedAccess means an intended access no longer works.
	VerdictBrokeIntendedAccess Verdict = "broke_intended_access"
	// VerdictUnverified means the evidence cannot decide. It is the answer
	// whenever evidence is missing, stale, unreadable or older than the repair.
	VerdictUnverified Verdict = "unverified"
)

// Intent names one access the owner expects to keep working: a route and the
// backend behind it.
type Intent struct {
	ID    string `json:"id"`
	Route string `json:"route"`
}

// IntentResult is what the after report says about one intent.
type IntentResult struct {
	ID     string    `json:"id"`
	Route  string    `json:"route"`
	State  EdgeState `json:"state"`
	Detail string    `json:"detail"`
}

// ClosureResult is the verification of one repair.
type ClosureResult struct {
	PathID       string         `json:"path_id"`
	Verdict      Verdict        `json:"verdict"`
	BrokenEdges  []string       `json:"broken_edges,omitempty"`
	OpenEdges    []string       `json:"open_edges,omitempty"`
	UnknownEdges []string       `json:"unknown_edges,omitempty"`
	Intended     []IntentResult `json:"intended,omitempty"`
	// NewPaths lists confirmed paths present after the repair and absent
	// before it. They never turn a repair into a failure by themselves, but
	// the owner must see them.
	NewPaths []string `json:"new_paths,omitempty"`
	Reasons  []string `json:"reasons"`
}

// Closure verifies that a repair broke the named path without breaking the
// intended access. It decides only from the two reports:
//
//   - the path must be confirmed in before;
//   - after must have been evaluated later than before;
//   - an edge counts as broken only when after shows it closed on an
//     observation made after before was evaluated; a missing, stale or
//     unknown edge is never read as closed;
//   - every intent must be open in before and open in after; at least one
//     intent is required.
func Closure(before, after Report, pathID string, intents []Intent) ClosureResult {
	result := ClosureResult{PathID: pathID, Verdict: VerdictUnverified}
	unverified := func(format string, args ...any) ClosureResult {
		result.Verdict = VerdictUnverified
		result.Reasons = append(result.Reasons, fmt.Sprintf(format, args...))
		return result
	}
	path, found := before.path(pathID)
	switch {
	case !found:
		return unverified("path %s is not in the before report", pathID)
	case path.Status != PathConfirmed:
		return unverified("path %s was not confirmed before the repair", pathID)
	case !after.EvaluatedAt.After(before.EvaluatedAt):
		return unverified("the after report is not newer than the before report")
	}

	for _, id := range path.EdgeIDs {
		edge, present := after.edge(id)
		switch {
		case !present:
			result.UnknownEdges = append(result.UnknownEdges, id)
			result.Reasons = append(result.Reasons, "edge "+id+" is absent from the after report")
		case edge.State == EdgeClosed && edge.ObservedAt.After(before.EvaluatedAt):
			result.BrokenEdges = append(result.BrokenEdges, id)
		case edge.State == EdgeClosed:
			result.UnknownEdges = append(result.UnknownEdges, id)
			result.Reasons = append(result.Reasons, "edge "+id+" is closed on evidence that predates the before report")
		case edge.State == EdgeOpen:
			result.OpenEdges = append(result.OpenEdges, id)
		default:
			result.UnknownEdges = append(result.UnknownEdges, id)
		}
	}
	for _, candidate := range after.Paths {
		if candidate.Status != PathConfirmed {
			continue
		}
		if _, existed := before.path(candidate.ID); !existed {
			result.NewPaths = append(result.NewPaths, candidate.ID)
		}
	}

	if len(result.BrokenEdges) == 0 {
		if len(result.UnknownEdges) == 0 {
			result.Verdict = VerdictNotRepaired
			result.Reasons = append(result.Reasons, "every edge of the path is still open")
			return result
		}
		return unverified("no edge was observed closed after the repair and %d edge(s) are unknown", len(result.UnknownEdges))
	}

	if len(intents) == 0 {
		return unverified("no intended access was named, so the repair cannot be shown to preserve normal use")
	}
	broken, unknown := false, false
	for _, intent := range intents {
		outcome := IntentResult{ID: intent.ID, Route: intent.Route, State: EdgeUnknown, Detail: "route not found in the report"}
		beforeState, afterState := serviceState(before, intent.Route), serviceState(after, intent.Route)
		switch {
		case beforeState.State != EdgeOpen:
			outcome.Detail = "the access was not shown to work before the repair"
		case afterState.State == EdgeOpen:
			outcome.State, outcome.Detail = EdgeOpen, afterState.Detail
		default:
			outcome.State, outcome.Detail = afterState.State, afterState.Detail
		}
		broken = broken || outcome.State == EdgeClosed
		unknown = unknown || outcome.State == EdgeUnknown
		result.Intended = append(result.Intended, outcome)
	}
	switch {
	case broken:
		result.Verdict = VerdictBrokeIntendedAccess
		result.Reasons = append(result.Reasons, "an intended access no longer works")
	case unknown:
		return unverified("an intended access is not shown to work")
	default:
		result.Verdict = VerdictRepaired
		result.Reasons = append(result.Reasons, fmt.Sprintf("%d edge(s) observed closed, every intended access still works", len(result.BrokenEdges)))
	}
	return result
}

func serviceState(report Report, route string) ServiceState {
	for _, service := range report.Services {
		if service.Route == route {
			return service
		}
	}
	return ServiceState{Route: route, State: EdgeUnknown}
}
