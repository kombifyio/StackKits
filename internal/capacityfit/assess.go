package capacityfit

import (
	"fmt"
	"math"
	"slices"
)

func Overall(verdicts ...Verdict) Verdict {
	result := Pass
	for _, verdict := range verdicts {
		if verdict == Fail {
			return Fail
		}
		if verdict == Unverified {
			result = Unverified
		} else if verdict == Warning && result == Pass {
			result = Warning
		}
	}
	return result
}

func validateCapacity(capacity NodeCapacity) error {
	if capacity.Origin != Declared && capacity.Origin != Inventory && capacity.Origin != Unavailable {
		return fmt.Errorf("capacity origin must be declared, inventory or unavailable")
	}
	return validateVector(capacity.Values, true)
}

func comparisonMinimum(value float64, origin Origin) float64 {
	if origin == Inventory {
		return math.Ceil(value)
	}
	return value
}

// InventoryMinimum is the exact whole-unit threshold used by current compiler
// inventory admission. It never changes the underlying persisted budgets.
func InventoryMinimum(minimum Vector) Vector {
	var result Vector
	for _, axis := range axes {
		if value := minimum.Value(axis); value != nil {
			result.Set(axis, comparisonMinimum(*value, Inventory))
		}
	}
	return result
}

// AdmitMinimum is the compiler's existing hard numeric gate. It evaluates only
// declared requirements; recommendations and incomplete profile declarations
// stay outside this narrow call, as in the original admission composition.
func AdmitMinimum(minimum Vector, capacity NodeCapacity) (Verdict, error) {
	if err := validateVector(minimum, false); err != nil {
		return "", err
	}
	if err := validateCapacity(capacity); err != nil {
		return "", err
	}
	result := Pass
	for _, axis := range axes {
		wanted := minimum.Value(axis)
		if wanted == nil {
			continue
		}
		observed := capacity.Values.Value(axis)
		if observed == nil {
			result = Overall(result, Unverified)
		} else if *observed < comparisonMinimum(*wanted, capacity.Origin) {
			result = Fail
		}
	}
	return result, nil
}

func validatePolicy(policy *Policy) error {
	if policy == nil {
		return nil
	}
	if policy.Ref == "" {
		return fmt.Errorf("capacity policy requires its CUE source reference")
	}
	if policy.HeadroomFactor != nil && (math.IsNaN(*policy.HeadroomFactor) || *policy.HeadroomFactor <= 0 || *policy.HeadroomFactor > 1) {
		return fmt.Errorf("CUE headroomFactor must be greater than zero and at most one")
	}
	if policy.MaxAlwaysOnPerCPU != nil && (math.IsNaN(*policy.MaxAlwaysOnPerCPU) || math.IsInf(*policy.MaxAlwaysOnPerCPU, 0) || *policy.MaxAlwaysOnPerCPU <= 0) {
		return fmt.Errorf("CUE maxAlwaysOnPerCPU must be finite and positive")
	}
	return nil
}

func Assess(input Input) (Assessment, error) {
	if err := validatePolicy(input.Policy); err != nil {
		return Assessment{}, err
	}
	demands, err := Aggregate(input.Modules)
	if err != nil {
		return Assessment{}, err
	}
	if len(demands) == 0 {
		return Assessment{}, fmt.Errorf("capacity assessment requires placed resource-bearing modules")
	}
	capacities := map[string]NodeCapacity{}
	for _, capacity := range input.Capacities {
		if capacity.NodeRef == "" {
			return Assessment{}, fmt.Errorf("capacity node reference is required")
		}
		if _, duplicate := capacities[capacity.NodeRef]; duplicate {
			return Assessment{}, fmt.Errorf("duplicate capacity node %s", capacity.NodeRef)
		}
		if err := validateCapacity(capacity); err != nil {
			return Assessment{}, err
		}
		capacities[capacity.NodeRef] = capacity
	}
	result := Assessment{SchemaVersion: SchemaVersion, Nodes: []NodeAssessment{}, Overall: Pass}
	if input.Policy != nil {
		result.PolicyRef = input.Policy.Ref
	}
	for _, demand := range demands {
		capacity, found := capacities[demand.NodeRef]
		if !found {
			capacity = NodeCapacity{NodeRef: demand.NodeRef, Origin: Unavailable}
		}
		assessment := NodeAssessment{NodeRef: demand.NodeRef, Origin: capacity.Origin, Demand: demand, Overall: Pass, Checks: []Check{}, ReasonCodes: []string{}, Advice: []string{}}
		minimum := Minimum(demand)
		for _, axis := range axes {
			check := Check{Axis: axis, Observed: capacity.Values.Value(axis), Minimum: minimum.Value(axis), Recommended: demand.Recommended.Value(axis)}
			switch {
			case check.Observed == nil:
				check.Status, check.ReasonCode = Unverified, "CAPACITY_UNDECLARED"
			case check.Minimum != nil && *check.Observed < comparisonMinimum(*check.Minimum, capacity.Origin):
				check.Status, check.ReasonCode = Fail, "CAPACITY_BELOW_MINIMUM"
			case check.Minimum == nil || slices.Contains(demand.UnverifiedAxes, axis):
				check.Status, check.ReasonCode = Unverified, "MODULE_CAPACITY_UNDECLARED"
			case check.Recommended != nil && *check.Observed < *check.Recommended:
				check.Status, check.ReasonCode = Warning, "CAPACITY_BELOW_RECOMMENDATION"
			default:
				check.Status, check.ReasonCode = Pass, "CAPACITY_PASS"
			}
			assessment.Checks = append(assessment.Checks, check)
			assessment.Overall = Overall(assessment.Overall, check.Status)
			if check.Status != Pass {
				assessment.ReasonCodes = append(assessment.ReasonCodes, check.ReasonCode)
			}
		}
		if input.Policy != nil {
			verdict, reasons := residentAssessment(input.Modules, capacity, *input.Policy)
			assessment.Overall = Overall(assessment.Overall, verdict)
			assessment.ReasonCodes = append(assessment.ReasonCodes, reasons...)
		}
		assessment.ReasonCodes = uniqueStrings(assessment.ReasonCodes)
		switch assessment.Overall {
		case Fail:
			assessment.Advice = []string{"reduce-workloads-on-node", "split-across-nodes"}
		case Unverified:
			assessment.Advice = []string{"complete-capacity-and-profile-facts"}
		case Warning:
			assessment.Advice = []string{"add-recommended-headroom"}
		}
		result.Nodes = append(result.Nodes, assessment)
		result.Overall = Overall(result.Overall, assessment.Overall)
	}
	return result, nil
}

// Resident limits exist only when supplied by CUE. Missing load or reservation
// facts remain unknown, but a known lower bound exceeding the host still fails.
func residentAssessment(modules []Module, capacity NodeCapacity, policy Policy) (Verdict, []string) {
	if policy.HeadroomFactor == nil && policy.MaxAlwaysOnPerCPU == nil {
		return Pass, nil
	}
	residentRAM, alwaysOn := 0.0, 0
	unknownLoad, unknownRAM := false, false
	for _, module := range modules {
		if !slices.Contains(module.NodeRefs, capacity.NodeRef) {
			continue
		}
		if module.Load == nil {
			unknownLoad = true
			continue
		}
		switch module.Load.Residency {
		case "always-on":
			alwaysOn++
		case "on-demand", "scheduled":
			continue
		default:
			unknownLoad = true
			continue
		}
		switch module.Load.Baseline {
		case "none", "idle-resident":
			continue
		case "active-resident":
		default:
			unknownLoad = true
			continue
		}
		reservation := module.Compute.Reservation
		for _, orthogonal := range module.OrthogonalReservations {
			reservation = Add(reservation, orthogonal)
		}
		if reservation.RAMGB == nil {
			unknownRAM = true
		} else {
			residentRAM += *reservation.RAMGB
		}
	}
	result := Pass
	var reasons []string
	if policy.HeadroomFactor != nil {
		if observed := capacity.Values.RAMGB; observed != nil && residentRAM > *observed**policy.HeadroomFactor {
			return Fail, []string{"RESIDENT_LOAD_EXCEEDS_HOST"}
		}
		if capacity.Values.RAMGB == nil || unknownRAM {
			result = Unverified
			reasons = append(reasons, "RESIDENT_BUDGET_UNDECLARED")
		}
	}
	if policy.MaxAlwaysOnPerCPU != nil {
		if observed := capacity.Values.CPUCores; observed != nil && float64(alwaysOn) > *observed**policy.MaxAlwaysOnPerCPU {
			return Fail, []string{"RESIDENT_LOAD_EXCEEDS_HOST"}
		}
		if capacity.Values.CPUCores == nil {
			result = Unverified
			reasons = append(reasons, "CAPACITY_UNDECLARED")
		}
	}
	if unknownLoad {
		result = Unverified
		reasons = append(reasons, "MODULE_LOAD_UNDECLARED")
	}
	return result, reasons
}
