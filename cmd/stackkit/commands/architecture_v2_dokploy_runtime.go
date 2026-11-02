package commands

import (
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
)

// architectureV2LocalDokployRegistrations installs the node's Dokploy through
// its bootstrap owner and deploys Photos, Files and Vault through it.
func architectureV2LocalDokployRegistrations(workspace, version string) ([]architecturev2.ProductRuntimeOwnerRegistration, error) {
	bootstrapOperations, err := nativehost.NewOSDokployBootstrapOperations(workspace)
	if err != nil {
		return nil, err
	}
	bootstrapRegistration, err := architecturev2.NewProductDokployBootstrapRegistration(version, bootstrapOperations)
	if err != nil {
		return nil, err
	}
	operations, err := opentofu.NewDokployWorkloadOwner(opentofu.Runtime{WorkspaceRoot: workspace})
	if err != nil {
		return nil, err
	}
	constructors := []func(string, string, string, nativehost.SelectedPaaSWorkloadOperations) (architecturev2.ProductRuntimeOwnerRegistration, error){
		architecturev2.NewProductImmichSelectedPaaSRegistration,
		architecturev2.NewProductCloudreveSelectedPaaSRegistration,
		architecturev2.NewProductVaultwardenSelectedPaaSRegistration,
	}
	registrations := []architecturev2.ProductRuntimeOwnerRegistration{bootstrapRegistration}
	for _, constructor := range constructors {
		registration, err := constructor(version, "dokploy", "stackkits-dokploy-runtime", operations)
		if err != nil {
			return nil, err
		}
		registrations = append(registrations, registration)
	}
	return registrations, nil
}
