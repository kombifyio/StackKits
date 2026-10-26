package commands

import (
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
)

func architectureV2LocalCoolifyRegistrations(workspace, version string) ([]architecturev2.ProductRuntimeOwnerRegistration, error) {
	operations, err := opentofu.NewCoolifyWorkloadOperations(opentofu.Runtime{WorkspaceRoot: workspace})
	if err != nil {
		return nil, err
	}
	constructors := []func(string, string, string, nativehost.SelectedPaaSWorkloadOperations) (architecturev2.ProductRuntimeOwnerRegistration, error){
		architecturev2.NewProductEsphomeSelectedPaaSRegistration,
		architecturev2.NewProductImmichSelectedPaaSRegistration,
		architecturev2.NewProductCloudreveSelectedPaaSRegistration,
		architecturev2.NewProductVaultwardenSelectedPaaSRegistration,
		architecturev2.NewProductJellyfinSelectedPaaSRegistration,
		architecturev2.NewProductHomeAssistantSelectedPaaSRegistration,
	}
	var registrations []architecturev2.ProductRuntimeOwnerRegistration
	for _, constructor := range constructors {
		registration, err := constructor(version, "coolify", "stackkits-coolify-runtime", operations)
		if err != nil {
			return nil, err
		}
		registrations = append(registrations, registration)
	}
	return registrations, nil
}
