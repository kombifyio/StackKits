package architecturev2

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/capacityfit"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

// Feasibility report identity. Techstack validates a proposed layout against
// this contract and never interprets a ResolvedPlan itself.
const (
	FeasibilityAPIVersion = "stackkit.kombify.io/feasibility/v1"
	FeasibilityKind       = "FeasibilityReport"
)

// Workload and outcome verdicts.
const (
	VerdictFeasible   = "feasible"
	VerdictInfeasible = "infeasible"
	VerdictUnknown    = "unknown"
)

// Reason codes produced by the feasibility operation itself. Capacity reason
// codes are the capacityfit codes verbatim (CAPACITY_BELOW_MINIMUM, ...).
const (
	ReasonNodeRefUnknown          = "node_ref_unknown"
	ReasonNodeRefDisabled         = "node_ref_disabled"
	ReasonNodeRefNotEligible      = "node_ref_not_eligible"
	ReasonRuntimeUnsatisfied      = "runtime_admission_unsatisfied"
	ReasonRuntimeUnverified       = "runtime_admission_unverified"
	ReasonInventoryFactMissing    = "inventory_fact_missing"
	ReasonInventoryNodeMissing    = "inventory_node_missing"
	ReasonAMD64LevelBelowMinimum  = "amd64_microarchitecture_below_minimum"
	ReasonDemandUndeclared        = "module_demand_undeclared"
	ReasonCapacityUnassessed      = "capacity_unassessed"
	ReasonNotEvaluated            = "not_evaluated"
	ReasonAssessmentUnavailable   = "capacity_assessment_unavailable"
	reasonResidentLoadExceedsHost = "RESIDENT_LOAD_EXCEEDS_HOST"
)

// FeasibilityInput is the proposal to check: desired StackSpec intent, with the
// nodeRefs the proposal pins, and the exact observed Inventory of those nodes.
type FeasibilityInput struct {
	StackSpec []byte
	Inventory []byte
}

// SourcePolicy marks a reason that comes from a Kombify support policy threshold
// declared in CUE (the kit's resident-load policy) rather than from an observed
// or declared requirement of a module.
const SourcePolicy = "policy"

// FeasibilityReason explains one workload verdict. NodeRef and Axis are set
// when the reason belongs to one node or one capacity axis; Source is set to
// "policy" when a policy threshold, not a module requirement, produced it.
type FeasibilityReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	NodeRef string `json:"nodeRef,omitempty"`
	Axis    string `json:"axis,omitempty"`
	Source  string `json:"source,omitempty"`
}

// FeasibilityAdvisory is evidence the outcome does not depend on: the node's
// resident-load policy check is unverified because a module declares no load
// (MODULE_LOAD_UNDECLARED) or the resident memory budget is incomplete
// (RESIDENT_BUDGET_UNDECLARED), or its application data is checked against no
// budget because the StackSpec declares none (DATA_CAPACITY_INTENT_MISSING).
// A caller that needs the node fully verified reads these instead of inferring
// them from the node verdict.
type FeasibilityAdvisory struct {
	Code    string `json:"code"`
	NodeRef string `json:"nodeRef"`
}

// FeasibilityWorkload is the verdict for one selected workload.
// RequestedNodeRefs is what the spec pinned (empty when it pinned nothing) and
// ResolvedNodeRefs is where the compiler placed the workload.
type FeasibilityWorkload struct {
	Workload          string              `json:"workload"`
	Modules           []string            `json:"modules"`
	RequestedNodeRefs []string            `json:"requestedNodeRefs"`
	ResolvedNodeRefs  []string            `json:"resolvedNodeRefs"`
	Verdict           string              `json:"verdict"`
	Reasons           []FeasibilityReason `json:"reasons"`
}

// FeasibilityNode is the capacity verdict of one node that hosts modules.
type FeasibilityNode struct {
	NodeRef     string   `json:"nodeRef"`
	Verdict     string   `json:"verdict"`
	ReasonCodes []string `json:"reasonCodes"`
}

// FeasibilityIssue is a compile or assessment error that no workload owns.
type FeasibilityIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// FeasibilityReport is the side-effect-free answer to "does this proposal fit
// this inventory". CLIVersion is set by the CLI adapter, which owns the build.
type FeasibilityReport struct {
	APIVersion      string                `json:"apiVersion"`
	Kind            string                `json:"kind"`
	CLIVersion      string                `json:"cliVersion"`
	SpecDigest      string                `json:"specDigest"`
	InventoryDigest string                `json:"inventoryDigest"`
	Outcome         string                `json:"outcome"`
	Workloads       []FeasibilityWorkload `json:"workloads"`
	Nodes           []FeasibilityNode     `json:"nodes"`
	Errors          []FeasibilityIssue    `json:"errors"`
	Advisories      []FeasibilityAdvisory `json:"advisories"`
}

// AssessFeasibility resolves the proposal read-only and reports, per workload
// and per node, whether it fits the inventory. It builds on Resolve and the
// capacity assessment only: it never attests host facts, persists a plan,
// writes state or reaches the network.
//
// A report is returned whenever the inputs decode, whatever the outcome. The
// error return is reserved for inputs that cannot be decoded as a native
// StackSpec or an Inventory, which carry no workload to report on, and for an
// internal fault (an error that is not a compiler decision).
func (s *Service) AssessFeasibility(input FeasibilityInput) (FeasibilityReport, error) {
	if s == nil || s.authority == nil || s.compiler == nil || s.validator == nil {
		return FeasibilityReport{}, resolveError(ErrResolveFailed, "service is not initialized", nil)
	}
	if err := s.requireNativeStackSpec(input); err != nil {
		return FeasibilityReport{}, err
	}
	spec, err := decodeYAMLObject(input.StackSpec, "StackSpec")
	if err != nil {
		return FeasibilityReport{}, resolveError(ErrInvalidStackSpec, err.Error(), err)
	}
	inventoryData := input.Inventory
	if len(strings.TrimSpace(string(inventoryData))) == 0 {
		inventoryData = []byte(emptyInventoryDocument)
	}
	inventory, err := decodeYAMLObject(inventoryData, "Inventory")
	if err != nil {
		return FeasibilityReport{}, resolveError(ErrInvalidInventory, err.Error(), err)
	}
	report := FeasibilityReport{
		APIVersion: FeasibilityAPIVersion, Kind: FeasibilityKind,
		Workloads: []FeasibilityWorkload{}, Nodes: []FeasibilityNode{}, Errors: []FeasibilityIssue{}, Advisories: []FeasibilityAdvisory{},
	}
	if report.SpecDigest, err = resolvedplan.CanonicalSHA256(spec); err != nil {
		return FeasibilityReport{}, resolveError(ErrInvalidStackSpec, "digest StackSpec: "+err.Error(), err)
	}
	if report.InventoryDigest, err = resolvedplan.CanonicalSHA256(inventory); err != nil {
		return FeasibilityReport{}, resolveError(ErrInvalidInventory, "digest Inventory: "+err.Error(), err)
	}
	requested := requestedWorkloads(spec)
	if s.reportUndeclaredPins(&report, requested, specNodeFacts(spec)) {
		return report, nil
	}

	result, err := s.Resolve(ResolveInput{StackSpec: input.StackSpec, Inventory: input.Inventory})
	if err != nil {
		var resolveErr *ResolveError
		if errors.As(err, &resolveErr) {
			switch resolveErr.Code {
			case ErrInvalidStackSpec, ErrInvalidInventory, ErrMigrationRequired, ErrMigrationBlocked:
				return FeasibilityReport{}, err
			}
		}
		var compileErr *resolvedplan.CompileError
		if !errors.As(err, &compileErr) {
			// Only a compiler decision is a verdict. Anything else (a marshal,
			// renderer or authority fault) says nothing about the proposal.
			return FeasibilityReport{}, err
		}
		s.reportCompileFailure(&report, requested, inventory, err)
		return report, nil
	}
	s.reportResolvedPlan(&report, requested, spec, inventory, result)
	return report, nil
}

// requestedWorkload is what the spec asked for one workload, before the
// compiler narrowed or dropped anything.
// requireNativeStackSpec rejects a document that is not a native v2 StackSpec of
// a governed kit before anything is judged: a feasibility report about a
// document that is no StackSpec would be a verdict about nothing. A v1 spec
// returns the migration diagnostic Resolve owns.
func (s *Service) requireNativeStackSpec(input FeasibilityInput) error {
	document, err := stackspecmigration.Read(input.StackSpec)
	if err != nil {
		return resolveError(ErrInvalidStackSpec, err.Error(), err)
	}
	if document.Version == stackspecmigration.SourceVersionV1 {
		_, resolveErr := s.Resolve(ResolveInput{StackSpec: input.StackSpec})
		var migration *ResolveError
		if errors.As(resolveErr, &migration) && (migration.Code == ErrMigrationRequired || migration.Code == ErrMigrationBlocked) {
			return resolveErr
		}
		return resolveError(ErrMigrationRequired, "StackSpec v1 must be explicitly migrated before feasibility", nil)
	}
	if !document.Version.IsV2() || document.V2 == nil {
		return resolveError(ErrInvalidStackSpec, "the document is not a StackSpec v2", nil)
	}
	if _, exists := s.authority.definitions[document.V2.KitProfile]; !exists {
		return resolveError(ErrInvalidStackSpec, fmt.Sprintf("no governed Definition exists for %q", document.V2.KitProfile), nil)
	}
	return nil
}

type requestedWorkload struct {
	id          string
	alternative string
	nodeRefs    []string
}

func requestedWorkloads(spec map[string]any) []requestedWorkload {
	selections, _ := spec["workloads"].(map[string]any)
	result := make([]requestedWorkload, 0, len(selections))
	for id, raw := range selections {
		selection, _ := raw.(map[string]any)
		entry := requestedWorkload{id: id}
		entry.alternative, _ = selection["alternative"].(string)
		placement, _ := selection["placement"].(map[string]any)
		refs, _ := placement["nodeRefs"].([]any)
		for _, ref := range refs {
			if text, ok := ref.(string); ok && text != "" {
				entry.nodeRefs = append(entry.nodeRefs, text)
			}
		}
		entry.nodeRefs = sortedUnique(entry.nodeRefs)
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	result := slices.Clone(values)
	sort.Strings(result)
	return slices.Compact(result)
}

// effect is how one reason moves a workload verdict.
type effect int

const (
	effectNone effect = iota
	effectUnknown
	effectInfeasible
)

type verdictBuilder struct {
	workload FeasibilityWorkload
	effect   effect
	seen     map[FeasibilityReason]bool
}

func (b *verdictBuilder) add(reason FeasibilityReason, moves effect) {
	if !b.seen[reason] {
		b.seen[reason] = true
		b.workload.Reasons = append(b.workload.Reasons, reason)
	}
	b.effect = max(b.effect, moves)
}

func (b *verdictBuilder) finish() FeasibilityWorkload {
	switch b.effect {
	case effectInfeasible:
		b.workload.Verdict = VerdictInfeasible
	case effectUnknown:
		b.workload.Verdict = VerdictUnknown
	default:
		b.workload.Verdict = VerdictFeasible
	}
	return b.workload
}

func newVerdictBuilder(id string, modules, requested, resolved []string) *verdictBuilder {
	return &verdictBuilder{
		workload: FeasibilityWorkload{
			Workload: id, Modules: sortedUnique(modules), RequestedNodeRefs: sortedUnique(requested),
			ResolvedNodeRefs: sortedUnique(resolved), Reasons: []FeasibilityReason{},
		},
		seen: map[FeasibilityReason]bool{},
	}
}

var (
	workloadPathPattern = regexp.MustCompile(`^spec\.workloads\.([^.\[\]]+)`)
	modulePathPattern   = regexp.MustCompile(`^spec\.modules\.([^.\[\]]+)`)
	inventoryNodePath   = regexp.MustCompile(`^inventory\.nodes\.([^.\[\]]+)`)
)

// reportCompileFailure maps a fail-fast compile error. The compiler stops at
// its first error, so only the workload that error names can be judged: an
// error under spec.workloads.<id> (or spec.modules.<module> of that workload's
// module) makes that workload infeasible; every other workload was not
// evaluated and stays unknown. An error no workload owns is reported in
// errors and makes the outcome infeasible, unless it names an inventory fact
// the inventory does not carry: a missing fact is unknown, only a fact that is
// present and contradicts the proposal is infeasible.
func (s *Service) reportCompileFailure(report *FeasibilityReport, requested []requestedWorkload, inventory map[string]any, cause error) {
	issue := FeasibilityIssue{Code: string(ErrResolveFailed), Message: diagnosticSummary(cause.Error())}
	var compileErr *resolvedplan.CompileError
	if errors.As(cause, &compileErr) {
		issue = FeasibilityIssue{Code: string(compileErr.Code), Path: compileErr.Path, Message: diagnosticSummary(compileErr.Message)}
	}
	owner := ""
	if match := workloadPathPattern.FindStringSubmatch(issue.Path); match != nil {
		owner = match[1]
	} else if match := modulePathPattern.FindStringSubmatch(issue.Path); match != nil {
		for _, workload := range requested {
			if slices.Contains(s.catalogModules(workload), match[1]) {
				owner = workload.id
			}
		}
	}
	nodeRef := ""
	if match := inventoryNodePath.FindStringSubmatch(issue.Path); match != nil {
		nodeRef = match[1]
	}
	attributed := false
	for _, workload := range requested {
		builder := newVerdictBuilder(workload.id, s.catalogModules(workload), workload.nodeRefs, nil)
		if workload.id == owner {
			attributed = true
			builder.add(FeasibilityReason{Code: issue.Code, Message: issue.Message, NodeRef: nodeRef}, effectInfeasible)
		} else {
			builder.add(FeasibilityReason{
				Code:    ReasonNotEvaluated,
				Message: fmt.Sprintf("compilation stopped at %s (%s) before this workload was evaluated", firstNonEmpty(issue.Path, "the StackSpec"), issue.Code),
			}, effectUnknown)
		}
		report.Workloads = append(report.Workloads, builder.finish())
	}
	unowned := ownedError
	if !attributed {
		report.Errors = append(report.Errors, issue)
		unowned = blockingError
		if nodeRef != "" && inventoryFactAbsent(inventory, issue.Path) {
			unowned = missingEvidenceError
		}
	}
	report.Outcome = feasibilityOutcome(report.Workloads, unowned)
}

// inventoryFactAbsent reports whether the inventory path a compile error names
// is missing from the Inventory document (the node, the fact or a value of it).
func inventoryFactAbsent(inventory map[string]any, path string) bool {
	var current any = inventory
	for _, segment := range strings.Split(strings.TrimPrefix(path, "inventory."), ".") {
		if index := strings.IndexByte(segment, '['); index >= 0 {
			segment = segment[:index]
		}
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		value, exists := object[segment]
		if !exists || value == nil {
			return true
		}
		current = value
	}
	return false
}

// diagnosticSummary keeps the first line of a multi-line compiler diagnostic:
// CUE reports carry one line per source position, with host-local overlay
// paths a caller cannot act on.
func diagnosticSummary(message string) string {
	first, rest, multiline := strings.Cut(strings.TrimSpace(message), "\n")
	first = strings.TrimSpace(first)
	truncated := multiline && strings.TrimSpace(rest) != ""
	if len(first) > 480 {
		first, truncated = first[:480], true
	}
	if truncated {
		return first + " (diagnostic truncated)"
	}
	return first
}

// reportUndeclaredPins reports workloads that pin a node the StackSpec does
// not declare or has disabled. The governed CUE binding rejects such a spec
// before the compiler places anything, with a diagnostic no caller can map to
// the pin, so the pin is judged here. It returns true when it produced the
// report: the other workloads were then not evaluated.
func (s *Service) reportUndeclaredPins(report *FeasibilityReport, requested []requestedWorkload, specNodes map[string]specNodeFact) bool {
	type finding struct{ reasons []FeasibilityReason }
	findings := map[string]*finding{}
	var first string
	for _, workload := range requested {
		for _, nodeRef := range workload.nodeRefs {
			fact, declared := specNodes[nodeRef]
			if declared && fact.enabled {
				continue
			}
			if findings[workload.id] == nil {
				findings[workload.id] = &finding{}
				if first == "" {
					first = fmt.Sprintf("workload %q pins node %q", workload.id, nodeRef)
				}
			}
			findings[workload.id].reasons = append(findings[workload.id].reasons, droppedNodeReason(workload.id, nodeRef, nil, specNodes))
		}
	}
	if len(findings) == 0 {
		return false
	}
	for _, workload := range requested {
		builder := newVerdictBuilder(workload.id, s.catalogModules(workload), workload.nodeRefs, nil)
		if found := findings[workload.id]; found != nil {
			for _, reason := range found.reasons {
				builder.add(reason, effectInfeasible)
			}
		} else {
			builder.add(FeasibilityReason{
				Code:    ReasonNotEvaluated,
				Message: first + ", which the StackSpec does not declare or has disabled; resolution was not attempted",
			}, effectUnknown)
		}
		report.Workloads = append(report.Workloads, builder.finish())
	}
	report.Outcome = feasibilityOutcome(report.Workloads, ownedError)
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// catalogModules names the module a requested workload alternative resolves
// to in the governed catalog. It is only used when no plan exists.
func (s *Service) catalogModules(workload requestedWorkload) []string {
	for _, contract := range s.authority.catalog.Workloads {
		metadata, _ := contract["metadata"].(map[string]any)
		if id, _ := metadata["id"].(string); id != workload.id {
			continue
		}
		alternatives, _ := contract["alternatives"].([]any)
		for _, raw := range alternatives {
			alternative, _ := raw.(map[string]any)
			if id, _ := alternative["id"].(string); id == workload.alternative {
				if moduleRef, _ := alternative["moduleRef"].(string); moduleRef != "" {
					return []string{moduleRef}
				}
			}
		}
	}
	return []string{}
}

// unownedError classifies the compile error no workload owns.
type unownedError int

const (
	ownedError unownedError = iota
	blockingError
	missingEvidenceError
)

func feasibilityOutcome(workloads []FeasibilityWorkload, unowned unownedError) string {
	outcome := VerdictFeasible
	if unowned == blockingError {
		return VerdictInfeasible
	}
	if unowned == missingEvidenceError {
		outcome = VerdictUnknown
	}
	for _, workload := range workloads {
		switch workload.Verdict {
		case VerdictInfeasible:
			return VerdictInfeasible
		case VerdictUnknown:
			outcome = VerdictUnknown
		}
	}
	return outcome
}

// reportResolvedPlan judges a compiled plan. A workload is infeasible when the
// compiler dropped a node the spec pinned, a selected module is not admitted on
// its nodes, or a node it runs on fails a capacity axis; it is unknown when the
// evidence that would settle it is missing; otherwise it is feasible.
func (s *Service) reportResolvedPlan(report *FeasibilityReport, requested []requestedWorkload, spec, inventory map[string]any, result Result) {
	plan := result.Plan
	modulesByID := map[string]map[string]any{}
	rawModules, _ := plan["modules"].([]any)
	for _, raw := range rawModules {
		if module, ok := raw.(map[string]any); ok {
			if id, _ := module["id"].(string); id != "" {
				modulesByID[id] = module
			}
		}
	}
	requestedByID := map[string]requestedWorkload{}
	for _, workload := range requested {
		requestedByID[workload.id] = workload
	}

	assessed, assessErr := s.assessCompiledCapacity(result)
	nodes := map[string]capacityfit.NodeAssessment{}
	if assessErr == nil {
		for _, node := range assessed.Assessment.Nodes {
			nodes[node.NodeRef] = node
			report.Nodes = append(report.Nodes, FeasibilityNode{
				NodeRef: node.NodeRef, Verdict: string(node.Overall), ReasonCodes: nonNilStrings(node.ReasonCodes),
			})
			for _, code := range []string{"MODULE_LOAD_UNDECLARED", "RESIDENT_BUDGET_UNDECLARED", "DATA_CAPACITY_INTENT_MISSING"} {
				if slices.Contains(node.ReasonCodes, code) {
					report.Advisories = append(report.Advisories, FeasibilityAdvisory{Code: code, NodeRef: node.NodeRef})
				}
			}
		}
	} else {
		report.Errors = append(report.Errors, FeasibilityIssue{
			Code: ReasonAssessmentUnavailable, Path: "resolvedPlan.modules",
			Message: "the compiled plan carries no assessable resource demand: " + assessErr.Error(),
		})
	}
	specNodes := specNodeFacts(spec)
	inventoryNodes, _ := inventory["nodes"].(map[string]any)

	workloads, _ := plan["workloads"].([]any)
	sort.SliceStable(workloads, func(i, j int) bool { return planWorkloadID(workloads[i]) < planWorkloadID(workloads[j]) })
	for _, raw := range workloads {
		workload, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := workload["id"].(string)
		alternative, _ := workload["alternative"].(map[string]any)
		moduleRef, _ := alternative["moduleRef"].(string)
		resolved := planStrings(workload["nodeRefs"])
		pinned := requestedByID[id].nodeRefs
		builder := newVerdictBuilder(id, []string{moduleRef}, pinned, resolved)

		for _, nodeRef := range pinned {
			if slices.Contains(resolved, nodeRef) {
				continue
			}
			builder.add(droppedNodeReason(id, nodeRef, resolved, specNodes), effectInfeasible)
		}
		module := modulesByID[moduleRef]
		hasData := len(planStrings(workload["dataClasses"])) > 0
		for _, nodeRef := range resolved {
			s.addNodeReasons(builder, moduleRef, module, hasData, nodeRef, nodes, inventoryNodes)
		}
		s.addModuleReasons(builder, moduleRef, module, resolved, inventoryNodes)
		report.Workloads = append(report.Workloads, builder.finish())
	}
	report.Outcome = feasibilityOutcome(report.Workloads, ownedError)
}

func planWorkloadID(raw any) string {
	workload, _ := raw.(map[string]any)
	id, _ := workload["id"].(string)
	return id
}

func planStrings(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

type specNodeFact struct{ enabled bool }

func specNodeFacts(spec map[string]any) map[string]specNodeFact {
	facts := map[string]specNodeFact{}
	nodes, _ := spec["nodes"].([]any)
	for _, raw := range nodes {
		node, _ := raw.(map[string]any)
		if id, _ := node["id"].(string); id != "" {
			enabled, declared := node["enabled"].(bool)
			facts[id] = specNodeFact{enabled: !declared || enabled}
		}
	}
	return facts
}

// droppedNodeReason explains why a pinned node is absent from the resolved
// placement. The compiler narrows silently; the proposal must not.
func droppedNodeReason(workload, nodeRef string, resolved []string, specNodes map[string]specNodeFact) FeasibilityReason {
	fact, declared := specNodes[nodeRef]
	switch {
	case !declared:
		return FeasibilityReason{Code: ReasonNodeRefUnknown, NodeRef: nodeRef,
			Message: fmt.Sprintf("workload %q pins node %q, which the StackSpec does not declare", workload, nodeRef)}
	case !fact.enabled:
		return FeasibilityReason{Code: ReasonNodeRefDisabled, NodeRef: nodeRef,
			Message: fmt.Sprintf("workload %q pins node %q, which is disabled", workload, nodeRef)}
	default:
		return FeasibilityReason{Code: ReasonNodeRefNotEligible, NodeRef: nodeRef,
			Message: fmt.Sprintf("workload %q pins node %q, but it resolved only to %v: the node does not satisfy the workload's site kind, required roles or module placement", workload, nodeRef, resolved)}
	}
}

// addModuleReasons judges the module-level runtime admission the compiler
// already computed against the same inventory.
func (s *Service) addModuleReasons(builder *verdictBuilder, moduleRef string, module map[string]any, nodeRefs []string, inventoryNodes map[string]any) {
	if module == nil {
		builder.add(FeasibilityReason{Code: ReasonDemandUndeclared, Message: fmt.Sprintf("module %q is not part of the compiled plan", moduleRef)}, effectUnknown)
		return
	}
	if _, declared := module["computeProfileBinding"]; !declared {
		builder.add(FeasibilityReason{Code: ReasonDemandUndeclared, Message: fmt.Sprintf("module %q declares no compute profile, so its resource demand is unknown", moduleRef)}, effectUnknown)
	}
	admission, _ := module["runtimeAdmission"].(map[string]any)
	status, _ := admission["status"].(string)
	requirements, _ := module["runtimeRequirements"].(map[string]any)
	switch status {
	case "unsatisfied":
		reasons := unsatisfiedAdmissionReasons(moduleRef, requirements, nodeRefs, inventoryNodes)
		if len(reasons) == 0 && builder.effect < effectInfeasible {
			reasons = []FeasibilityReason{{Code: ReasonRuntimeUnsatisfied, Message: fmt.Sprintf("module %q does not satisfy the requirements of its nodes %v in the inventory", moduleRef, nodeRefs)}}
		}
		for _, reason := range reasons {
			builder.add(reason, effectInfeasible)
		}
	case "unverified":
		reasons := missingAdmissionFacts(moduleRef, requirements, nodeRefs, inventoryNodes)
		if len(reasons) == 0 {
			reasons = []FeasibilityReason{{Code: ReasonRuntimeUnverified, Message: fmt.Sprintf("module %q cannot be admitted on %v: the inventory lacks facts its runtime requirements need", moduleRef, nodeRefs)}}
		}
		for _, reason := range reasons {
			builder.add(reason, effectUnknown)
		}
	}
}

func inventoryFacts(inventoryNodes map[string]any, nodeRef string) map[string]any {
	facts, _ := inventoryNodes[nodeRef].(map[string]any)
	return facts
}

// unsatisfiedAdmissionReasons names the requirement a node provably misses.
// Capacity axes are reported by the node assessment, so only the facts that
// assessment does not cover appear here.
func unsatisfiedAdmissionReasons(moduleRef string, requirements map[string]any, nodeRefs []string, inventoryNodes map[string]any) []FeasibilityReason {
	var reasons []FeasibilityReason
	wanted, hasLevel := requirementInt(requirements, "minAMD64MicroarchitectureLevel")
	for _, nodeRef := range nodeRefs {
		facts := inventoryFacts(inventoryNodes, nodeRef)
		if !hasLevel || facts["arch"] != "amd64" {
			continue
		}
		if observed, known := requirementInt(facts, "amd64MicroarchitectureLevel"); known && observed < wanted {
			reasons = append(reasons, FeasibilityReason{
				Code: ReasonAMD64LevelBelowMinimum, NodeRef: nodeRef,
				Message: fmt.Sprintf("module %q needs amd64 microarchitecture level %d; node %q reports level %d", moduleRef, wanted, nodeRef, observed),
			})
		}
	}
	return reasons
}

// missingAdmissionFacts lists the inventory facts a module's requirements need
// and a node does not report.
func missingAdmissionFacts(moduleRef string, requirements map[string]any, nodeRefs []string, inventoryNodes map[string]any) []FeasibilityReason {
	var reasons []FeasibilityReason
	needs := func(nodeRef, fact, why string) {
		reasons = append(reasons, FeasibilityReason{
			Code: ReasonInventoryFactMissing, NodeRef: nodeRef,
			Message: fmt.Sprintf("module %q needs %s, but node %q reports no %s", moduleRef, why, nodeRef, fact),
		})
	}
	required := planStrings(requirements["requireInventoryFacts"])
	_, hasLevel := requirementInt(requirements, "minAMD64MicroarchitectureLevel")
	_, hasFilesystem := requirements["storageFilesystem"].(map[string]any)
	for _, nodeRef := range nodeRefs {
		facts := inventoryFacts(inventoryNodes, nodeRef)
		for _, fact := range required {
			if value, exists := facts[fact]; !exists || value == nil {
				needs(nodeRef, fact, "this fact")
			}
		}
		if hasLevel && facts["arch"] == "amd64" {
			if _, known := requirementInt(facts, "amd64MicroarchitectureLevel"); !known {
				needs(nodeRef, "amd64MicroarchitectureLevel", "an observed amd64 microarchitecture level")
			}
		}
		if hasFilesystem {
			if _, known := facts["storageCapacity"].(map[string]any); !known {
				needs(nodeRef, "storageCapacity", "the observed free space and filesystem of its data root")
			}
		}
	}
	return reasons
}

func requirementInt(values map[string]any, name string) (int, bool) {
	switch number := values[name].(type) {
	case float64:
		return int(number), true
	case int:
		return number, true
	case interface{ Int64() (int64, error) }:
		parsed, err := number.Int64()
		return int(parsed), err == nil
	}
	return 0, false
}

// addNodeReasons applies the node's capacity assessment to one workload.
// A failed axis makes every workload that counts toward it infeasible; missing
// evidence makes only the workloads it concerns unknown. An undeclared data
// budget is no missing evidence but absent intent, so it stays a reason only.
func (s *Service) addNodeReasons(builder *verdictBuilder, moduleRef string, module map[string]any, hasData bool, nodeRef string, nodes map[string]capacityfit.NodeAssessment, inventoryNodes map[string]any) {
	if _, observed := inventoryNodes[nodeRef]; !observed {
		builder.add(FeasibilityReason{Code: ReasonInventoryNodeMissing, NodeRef: nodeRef,
			Message: fmt.Sprintf("the inventory has no entry for node %q, so nothing about it can be verified", nodeRef)}, effectUnknown)
		return
	}
	node, assessed := nodes[nodeRef]
	if !assessed {
		builder.add(FeasibilityReason{Code: ReasonCapacityUnassessed, NodeRef: nodeRef,
			Message: fmt.Sprintf("the capacity of node %q was not assessed", nodeRef)}, effectUnknown)
		return
	}
	unverifiedModule := slices.Contains(node.Demand.UnverifiedModuleRefs, moduleRef)
	fromChecks := map[string]bool{}
	for _, check := range node.Checks {
		fromChecks[check.ReasonCode] = true
		axis := string(check.Axis)
		if check.Axis == capacityfit.DataStorage && !hasData {
			continue
		}
		reason := FeasibilityReason{Code: check.ReasonCode, NodeRef: nodeRef, Axis: axis, Message: capacityMessage(check, nodeRef)}
		switch check.Status {
		case capacityfit.Fail:
			builder.add(reason, effectInfeasible)
		case capacityfit.Warning:
			builder.add(reason, effectNone)
		case capacityfit.Unverified:
			if check.ReasonCode == "MODULE_CAPACITY_UNDECLARED" && !unverifiedModule {
				continue
			}
			// No declared data budget is no demand to miss: the host floor still
			// counts storage, a declared budget that does not fit stays a failure,
			// and the advisory names the unbudgeted node. A budget is user intent,
			// so none is assumed.
			if check.ReasonCode == "DATA_CAPACITY_INTENT_MISSING" {
				builder.add(reason, effectNone)
				continue
			}
			builder.add(reason, effectUnknown)
		}
	}
	profile, _ := module["computeProfile"].(string)
	_, loadDeclared := s.authority.moduleLoads[moduleRef][profile]
	for _, code := range node.ReasonCodes {
		if fromChecks[code] {
			continue
		}
		switch code {
		case reasonResidentLoadExceedsHost:
			builder.add(FeasibilityReason{Code: code, NodeRef: nodeRef, Source: SourcePolicy,
				Message: fmt.Sprintf("the always-on and active-resident modules on node %q exceed its observed CPU or memory under the kit's resident-load policy", nodeRef)}, effectInfeasible)
		case "MODULE_LOAD_UNDECLARED":
			if !loadDeclared {
				builder.add(FeasibilityReason{Code: code, NodeRef: nodeRef,
					Message: fmt.Sprintf("module %q declares no load for its compute profile, so the resident-load policy cannot count it on node %q", moduleRef, nodeRef)}, effectNone)
			}
		case "RESIDENT_BUDGET_UNDECLARED":
			builder.add(FeasibilityReason{Code: code, NodeRef: nodeRef,
				Message: fmt.Sprintf("the resident memory budget of node %q is incomplete: a resident module declares no RAM reservation or the node reports no RAM", nodeRef)}, effectNone)
		}
	}
}

func capacityMessage(check capacityfit.Check, nodeRef string) string {
	number := func(value *float64) string {
		if value == nil {
			return "unknown"
		}
		return fmt.Sprintf("%g", *value)
	}
	switch check.ReasonCode {
	case "CAPACITY_BELOW_MINIMUM":
		return fmt.Sprintf("node %q reports %s %s; the modules placed on it need at least %s", nodeRef, check.Axis, number(check.Observed), number(check.Minimum))
	case "CAPACITY_BELOW_RECOMMENDATION":
		return fmt.Sprintf("node %q reports %s %s; %s is recommended for the modules placed on it", nodeRef, check.Axis, number(check.Observed), number(check.Recommended))
	case "CAPACITY_UNDECLARED":
		return fmt.Sprintf("node %q reports no %s in the inventory", nodeRef, check.Axis)
	case "MODULE_CAPACITY_UNDECLARED":
		return fmt.Sprintf("a module placed on node %q declares no complete %s demand", nodeRef, check.Axis)
	case "DATA_CAPACITY_UNSATISFIED":
		return fmt.Sprintf("node %q has %s GiB free for application data; the declared data budget needs %s GiB", nodeRef, number(check.Observed), number(check.Minimum))
	case "DATA_CAPACITY_UNVERIFIED":
		return fmt.Sprintf("node %q reports no free space for its application data root", nodeRef)
	case "DATA_CAPACITY_INTENT_MISSING":
		return fmt.Sprintf("the StackSpec declares no data capacity budget for the application data on node %q", nodeRef)
	}
	return fmt.Sprintf("node %q %s: %s", nodeRef, check.Axis, check.ReasonCode)
}
