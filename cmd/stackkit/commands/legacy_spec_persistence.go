package commands

import (
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/stackspecadmission"
	"github.com/kombifyio/stackkits/pkg/models"
)

// persistLegacyV06StackSpec is the sole command-layer write seam for the
// one-minor models.StackSpec compatibility document. Classification guards at
// command entry remain mandatory; this second build-version check prevents a
// future missed guard from reopening v1 writes in development or M+1.
func persistLegacyV06StackSpec(loader *config.Loader, spec *models.StackSpec, path, operation string) error {
	if loader == nil || spec == nil {
		return fmt.Errorf("%s: legacy v0.6 StackSpec persistence requires loader and spec", strings.TrimSpace(operation))
	}
	if stackspecadmission.RejectOperationalV1(version) {
		return fmt.Errorf(
			"%s: StackSpec v1 persistence is restricted to an explicit v0.6 build; build %q requires canonical Architecture v2",
			strings.TrimSpace(operation),
			version,
		)
	}
	return loader.SaveLegacyStackSpecV06(spec, path)
}

func requireLegacyV06Command(operation, nativeV2Boundary string) error {
	operation = strings.TrimSpace(operation)
	if !stackspecadmission.RejectOperationalV1(version) {
		return nil
	}
	return fmt.Errorf(
		"%s is unavailable on the native StackSpec v2 line (build %q): %s",
		operation,
		version,
		strings.TrimSpace(nativeV2Boundary),
	)
}
