package capacityfit

import (
	"fmt"
	"math"
)

// DataStorageCheck compares the CUE-derived reservation with free GiB for the
// exact resolved filesystem. Total disk size must never substitute for free space.
func DataStorageCheck(required float64, observed *float64) (Check, error) {
	if math.IsNaN(required) || math.IsInf(required, 0) || required <= 0 {
		return Check{}, fmt.Errorf("data storage demand must be finite and positive")
	}
	if observed != nil && (math.IsNaN(*observed) || math.IsInf(*observed, 0) || *observed < 0) {
		return Check{}, fmt.Errorf("observed free GiB must be finite and nonnegative")
	}
	check := Check{Axis: DataStorage, Minimum: &required, Observed: observed, Status: Pass, ReasonCode: "DATA_CAPACITY_PASS"}
	if observed == nil {
		check.Status, check.ReasonCode = Unverified, "DATA_CAPACITY_UNVERIFIED"
	} else if *observed < required {
		check.Status, check.ReasonCode = Fail, "DATA_CAPACITY_UNSATISFIED"
	}
	return check, nil
}

// WithDataStorageChecks composes the same compiler checks into advisory fit.
// It accepts already bound checks; it neither calculates new budgets nor
// authorizes Apply. Known data-space failure outranks a passing module floor.
func WithDataStorageChecks(assessment Assessment, checks map[string]Check) (Assessment, error) {
	for index := range assessment.Nodes {
		node := &assessment.Nodes[index]
		check, found := checks[node.NodeRef]
		if !found {
			continue
		}
		node.Checks = append(node.Checks, check)
		node.Overall = Overall(node.Overall, check.Status)
		if check.Status != Pass {
			node.ReasonCodes = uniqueStrings(append(node.ReasonCodes, check.ReasonCode))
		}
		if check.Status == Fail {
			node.Advice = uniqueStrings(append(node.Advice, "reduce-data-budget-or-provide-storage"))
		} else if check.Status == Unverified {
			advice := "observe-resolved-storage-free-space"
			if check.ReasonCode == "DATA_CAPACITY_INTENT_MISSING" {
				advice = "declare-data-capacity-intent"
			}
			node.Advice = uniqueStrings(append(node.Advice, advice))
		}
		assessment.Overall = Overall(assessment.Overall, check.Status)
	}
	for nodeRef := range checks {
		found := false
		for _, node := range assessment.Nodes {
			if node.NodeRef == nodeRef {
				found = true
				break
			}
		}
		if !found {
			return Assessment{}, fmt.Errorf("data budget targets a node absent from module capacity: %s", nodeRef)
		}
	}
	return assessment, nil
}
