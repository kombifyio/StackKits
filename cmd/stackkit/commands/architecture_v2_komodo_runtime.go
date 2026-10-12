package commands

import (
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

func architectureV2LocalKomodoRegistrations(workspace, version string) ([]architecturev2.ProductRuntimeOwnerRegistration, error) {
	bootstrapOperations, err := nativehost.NewOSKomodoBootstrapOperations(workspace)
	if err != nil {
		return nil, err
	}
	bootstrapRegistration, err := architecturev2.NewProductKomodoBootstrapRegistration(version, bootstrapOperations)
	if err != nil {
		return nil, err
	}
	operations, err := nativehost.NewOSKomodoWorkloadOperations(workspace)
	if err != nil {
		return nil, err
	}
	constructors := []func(string, string, string, nativehost.SelectedPaaSWorkloadOperations) (architecturev2.ProductRuntimeOwnerRegistration, error){
		architecturev2.NewProductImmichSelectedPaaSRegistration,
		architecturev2.NewProductCloudreveSelectedPaaSRegistration,
		architecturev2.NewProductVaultwardenSelectedPaaSRegistration,
		architecturev2.NewProductJellyfinSelectedPaaSRegistration,
		architecturev2.NewProductHomeAssistantSelectedPaaSRegistration,
	}
	registrations := make([]architecturev2.ProductRuntimeOwnerRegistration, 0, len(constructors))
	registrations = append(registrations, bootstrapRegistration)
	for _, constructor := range constructors {
		registration, err := constructor(version, "komodo", "stackkits-komodo-core-runtime", operations)
		if err != nil {
			return nil, err
		}
		registrations = append(registrations, registration)
	}
	return registrations, nil
}
