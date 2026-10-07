package applyoutcome

import "errors"

// DegradedUnitError is a runtime unit that did not reach its postcondition
// only because a prerequisite another authority delivers later is not yet on
// the node. Nothing else in the rollout depends on that postcondition, so the
// unit does not stop the rollout: it is reported degraded with a closed,
// retryable class while every other unit is still attempted.
//
// It is never inferred from text. Only an owner that proves the exact missing
// prerequisite returns it; every other failure keeps its criticality.
type DegradedUnitError struct {
	RequirementID string
	Class         Class
	Err           error
}

func (e *DegradedUnitError) Error() string {
	if e == nil || e.Err == nil {
		return "runtime unit completed degraded"
	}
	return e.Err.Error()
}

func (e *DegradedUnitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// DegradedUnit is the per-unit account of one degraded completion.
type DegradedUnit struct {
	Class   Class
	Message string
}

// DegradedOnly reports whether err consists of degraded units and nothing
// else. A single failure of any other kind anywhere in the error tree makes it
// false, so a joined set can never hide a real failure behind a degraded one.
func DegradedOnly(err error) bool {
	if err == nil {
		return false
	}
	if unit, ok := err.(*DegradedUnitError); ok {
		return unit != nil && unit.RequirementID != "" && unit.Class != ""
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		for _, child := range children {
			if !DegradedOnly(child) {
				return false
			}
		}
		return len(children) != 0
	}
	if child := errors.Unwrap(err); child != nil {
		return DegradedOnly(child)
	}
	return false
}

// DegradedUnits returns every degraded unit named anywhere in err, keyed by
// runtime requirement ID.
func DegradedUnits(err error) map[string]DegradedUnit {
	units := map[string]DegradedUnit{}
	var visit func(error)
	visit = func(candidate error) {
		if candidate == nil {
			return
		}
		if unit, ok := candidate.(*DegradedUnitError); ok {
			if unit != nil && unit.RequirementID != "" && unit.Class != "" {
				units[unit.RequirementID] = DegradedUnit{Class: unit.Class, Message: unit.Error()}
			}
			return
		}
		switch unwrapped := candidate.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range unwrapped.Unwrap() {
				visit(child)
			}
		case interface{ Unwrap() error }:
			visit(unwrapped.Unwrap())
		}
	}
	visit(err)
	return units
}
