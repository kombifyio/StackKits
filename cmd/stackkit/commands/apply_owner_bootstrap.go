package commands

import (
	"fmt"

	"github.com/kombifyio/stackkits/pkg/models"
)

type ownerBootstrapForApply struct {
	Owner                  models.OwnerConfig
	RecoveryPassphraseHash string
}

func resolveOwnerBootstrapForApply(_ string, spec *models.StackSpec) (ownerBootstrapForApply, bool, error) {
	if spec == nil {
		return ownerBootstrapForApply{}, false, nil
	}
	mode := spec.Owner.EffectiveBootstrapMode()
	switch mode {
	case "", models.OwnerBootstrapModeNone:
		return ownerBootstrapForApply{}, false, nil
	case models.OwnerBootstrapModeCustom:
		return ownerBootstrapForApply{
			Owner:                  spec.Owner,
			RecoveryPassphraseHash: spec.Owner.RecoveryPassphraseHash,
		}, true, nil
	case models.OwnerBootstrapModeAuto:
		return ownerBootstrapForApply{}, false, fmt.Errorf(
			"owner bootstrap mode %q requires an external orchestrator; standalone apply accepts local owner custody only",
			models.OwnerBootstrapModeAuto,
		)
	default:
		return ownerBootstrapForApply{}, false, fmt.Errorf("invalid owner bootstrapMode %q", mode)
	}
}
