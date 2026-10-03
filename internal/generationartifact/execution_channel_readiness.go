package generationartifact

import (
	"sort"
	"strings"
)

// ExecutionChannelUnboundBlocker is the Apply readiness blocker for a runtime
// target whose Site/node has no execution channel on this host. The compiler
// cannot see channels: they are Inventory or local custody facts. Without one,
// Product Apply refuses the target before any mutation, so readiness must not
// report ready.
const ExecutionChannelUnboundBlocker = "execution-channel-unbound"

// ExecutionChannelBlockers returns one blocker per Site/node that a runtime
// target this host executes names but bound does not cover. A nil scope means
// the host executes the whole plan; otherwise only the targets the scope
// includes count, which is the same rule host-scoped Apply uses.
func (p VerifiedPlan) ExecutionChannelBlockers(scope *ApplyExecutionScope, bound func(siteRef, nodeRef string) bool) []ReadinessBlocker {
	unbound := map[string]ReadinessBlocker{}
	for _, target := range p.applyRequirements.RuntimeInstances {
		if scope != nil && !scope.Includes(target) {
			continue
		}
		if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 {
			unbound["runtime:"+target.ID] = ReadinessBlocker{
				Code: ExecutionChannelUnboundBlocker, Refs: []string{"runtime:" + target.ID},
			}
			continue
		}
		siteRef, nodeRef := target.SiteRefs[0], target.NodeRefs[0]
		if bound != nil && bound(siteRef, nodeRef) {
			continue
		}
		unbound[siteRef+"\x00"+nodeRef] = ReadinessBlocker{
			Code: ExecutionChannelUnboundBlocker, Refs: []string{"node:" + nodeRef, "site:" + siteRef},
		}
	}
	keys := make([]string, 0, len(unbound))
	for key := range unbound {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	blockers := make([]ReadinessBlocker, 0, len(keys))
	for _, key := range keys {
		blockers = append(blockers, unbound[key])
	}
	return blockers
}

// RequireExecutionChannels fails Apply readiness when a runtime target this
// host executes has no execution channel, with the same typed error as the
// compiler's readiness decision.
func (p VerifiedPlan) RequireExecutionChannels(scope *ApplyExecutionScope, bound func(siteRef, nodeRef string) bool) error {
	blockers := p.ExecutionChannelBlockers(scope, bound)
	if len(blockers) == 0 {
		return nil
	}
	parts := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		parts = append(parts, blocker.Code+"["+strings.Join(blocker.Refs, ",")+"]")
	}
	return &Error{
		Code:     ErrReadinessBlocked,
		Path:     "resolvedPlan.executionReadiness." + string(ExecutionPhaseApply),
		Message:  "blocked by " + strings.Join(parts, "; "),
		Phase:    ExecutionPhaseApply,
		Blockers: blockers,
	}
}

// WithApplyBlockers returns the inspection with host-local Apply blockers
// added to the compiler's decision. Generation readiness is unchanged.
func (inspection PlanInspection) WithApplyBlockers(blockers []ReadinessBlocker) PlanInspection {
	if len(blockers) == 0 {
		return inspection
	}
	merged := append([]PlanInspectionBlocker(nil), inspection.Readiness.Apply.Blockers...)
	for _, blocker := range blockers {
		merged = append(merged, PlanInspectionBlocker{Code: blocker.Code, Refs: append([]string(nil), blocker.Refs...)})
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Code != merged[j].Code {
			return merged[i].Code < merged[j].Code
		}
		return strings.Join(merged[i].Refs, "\x00") < strings.Join(merged[j].Refs, "\x00")
	})
	inspection.Readiness.Apply = PlanInspectionPhase{Status: "blocked", Blockers: merged}
	return inspection
}
