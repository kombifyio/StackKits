package lifecyclemutation

import "errors"

// ErrUpgradeDataActivationRequired prevents a prior runtime from opening data
// that a newer runtime may already have changed. Isolated backup staging alone
// is not proof that the live data belongs to the prior runtime.
var ErrUpgradeDataActivationRequired = errors.New("lifecycle mutation: prior-runtime rollback requires verified live data activation after target Apply or an ambiguous recovery phase")

// RequirePreApplyUpgradeRollback admits executor-only recovery while the signed
// journal proves that no target Apply was admitted. Once Apply may have run,
// recovery must restore the complete prior data set before starting old images.
// A bare rollback-started record does not retain that proof and is denied too.
func (record Record) RequirePreApplyUpgradeRollback() error {
	if record.Kind != KindUpgrade || record.Status != StatusActive {
		return ErrUpgradeDataActivationRequired
	}
	switch record.Phase {
	case PhasePrepared, PhaseTargetGenerateStarted, PhaseTargetGenerateSucceeded:
		return nil
	default:
		return ErrUpgradeDataActivationRequired
	}
}
