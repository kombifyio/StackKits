package hostcustody

import (
	"time"
)

// DetachReport names what the owner must rotate upstream after a detach. It
// carries references and versions only, never a value.
type DetachReport struct {
	Generation uint64          `json:"generation"`
	DetachedAt string          `json:"detachedAt"`
	Rotate     []DeliveredItem `json:"rotate"`
}

// Detach ends host delivery for this node's current binding: every delivered
// secret is marked as needing rotation, every retired recipient key is
// destroyed, and no delivery opens until the node is re-enrolled with a
// recipient rotation. Values already in local custody stay usable here; the
// report lists the references to rotate where they came from. Detaching again
// keeps the first detach time and reports the same references.
func Detach(store Store, now time.Time) (DetachReport, error) {
	current, err := readRecipient(store)
	if err != nil {
		return DetachReport{}, err
	}
	state, existing, err := readState(store, current.Generation)
	if err != nil {
		return DetachReport{}, err
	}
	if state.DetachedAt == "" {
		state.DetachedAt = now.UTC().Format(time.RFC3339Nano)
	}
	for i := range state.Delivered {
		state.Delivered[i].NeedsRotation = true
	}
	if err := writeState(store, state, existing); err != nil {
		return DetachReport{}, err
	}
	if err := store.Remove(retiredName); err != nil {
		return DetachReport{}, err
	}
	rotate := append([]DeliveredItem{}, state.Delivered...)
	return DetachReport{Generation: current.Generation, DetachedAt: state.DetachedAt, Rotate: rotate}, nil
}
