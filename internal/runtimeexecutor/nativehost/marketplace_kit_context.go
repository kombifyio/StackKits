package nativehost

import (
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// requireMarketplaceKitContext uses the same owner-signed identity custody
// and immutable Core-to-network mapping as other native applications.
func requireMarketplaceKitContext(workspace string, d architecturev2renderer.MarketplaceCoolifyDocument) error {
	address, err := localevidence.LocalIdentityRuntimeAddress(workspace)
	if err != nil {
		return fmt.Errorf("Marketplace requires its established Kit identity custody: %w", err)
	}
	if address.Domain != d.DomainBase || address.SubdomainPrefix != d.SubdomainPrefix || address.ServiceHost("marketplace") != d.Host() {
		return errors.New("Marketplace address differs from its owner-signed Kit identity custody")
	}
	switch d.KitRef {
	case "basement-kit":
		_, err = localevidence.LoadBasementRuntimeCustody(workspace)
	case "cloud-kit":
		_, err = localevidence.LoadCloudRuntimeCustody(workspace)
	default:
		return errors.New("Marketplace names an unsupported Kit")
	}
	if err != nil {
		return fmt.Errorf("Marketplace Kit differs from its established node custody: %w", err)
	}
	return nil
}

func marketplaceCoreRuntime(d architecturev2renderer.MarketplaceCoolifyDocument) (project, network string, err error) {
	module, supported := architecturev2renderer.MarketplaceCoreModuleRef(d.KitRef)
	if !supported {
		return "", "", errors.New("Marketplace names an unsupported Core contract")
	}
	_, project, supported = NativeComposeProject(module)
	if !supported {
		return "", "", errors.New("Marketplace Core has no native installed project")
	}
	network, err = standaloneComposeCoreNetwork(module)
	return project, network, err
}
