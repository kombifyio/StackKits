//go:build !publisher

package commands

import (
	"fmt"

	"github.com/kombifyio/stackkits/pkg/models"
)

func resolveAutoOwnerBootstrapForApply(string) (ownerBootstrapForApply, bool, error) {
	return ownerBootstrapForApply{}, false, fmt.Errorf(
		"owner bootstrap mode %q requires an external orchestrator; standalone apply accepts local owner custody only",
		models.OwnerBootstrapModeAuto,
	)
}
