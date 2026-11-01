package commands

import (
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

// architectureV2LocalRuntipiRegistrations registers Files on the node's
// installed Runtipi Marketplace. The native owner reuses that installation's
// node custody; it neither bootstraps Runtipi nor accepts external credentials.
func architectureV2LocalRuntipiRegistrations(workspace, version string) ([]architecturev2.ProductRuntimeOwnerRegistration, error) {
	operations, err := nativehost.NewOSRuntipiWorkloadOperations(workspace)
	if err != nil {
		return nil, err
	}
	registration, err := architecturev2.NewProductCloudreveSelectedPaaSRegistration(version, "runtipi", "stackkits-runtipi-runtime", operations)
	if err != nil {
		return nil, err
	}
	return []architecturev2.ProductRuntimeOwnerRegistration{registration}, nil
}
