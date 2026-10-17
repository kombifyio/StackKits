package applicationlifecycle

import (
	"errors"
	"time"
)

// MarkSetupCreateDispatch records the resource intent before a non-idempotent
// setup create is sent. A failed/running dispatch is observation-only, even if
// a caller selects a new operation ID or a newer Plan. Only a later successful
// setup for the same resource supersedes that uncertainty. The caller must
// observe that resource before completing such a setup successfully.
func (store Store) MarkSetupCreateDispatch(contract Contract, id, intent string) (bool, error) {
	allowed := false
	_, err := store.mutate(contract, func(state *State) error {
		op := currentOperation(*state)
		if op == nil || op.ID != id || op.Stage != "setup" || op.OperationRef != "stackkit.setup" || op.Status != StatusRunning || op.IntentDigest != intent || !digestPattern.MatchString(intent) {
			return errors.New("setup create has no exact durable resource intent")
		}
		if op.Authority != authorityFromContract(contract) || state.Authority != op.Authority {
			return errors.New("setup create belongs to different ResolvedPlan authority")
		}
		for i := len(state.Operations) - 1; i >= 0; i-- {
			previous := state.Operations[i]
			if previous.Stage != "setup" || previous.OperationRef != "stackkit.setup" || previous.IntentDigest != intent {
				continue
			}
			if previous.Status == StatusSucceeded {
				break
			}
			if previous.Dispatched {
				return nil
			}
		}
		op.Dispatched = true
		op.UpdatedAt = time.Now().UTC()
		op.Digest = operationDigest(*op)
		allowed = true
		return nil
	})
	return allowed && err == nil, err
}
