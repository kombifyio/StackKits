package commands

import (
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

func homeAssistantOIDCOwnerCustody(workspace string) (appsetup.HomeAssistantOIDCBinding, error) {
	binding, err := localevidence.LoadOwnerRuntimeBinding(workspace)
	if err != nil {
		return appsetup.HomeAssistantOIDCBinding{}, err
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(workspace)
	if err != nil {
		return appsetup.HomeAssistantOIDCBinding{}, err
	}
	return appsetup.HomeAssistantOIDCBinding{Issuer: address.PocketIDOrigin(), Subject: binding.PocketIDSubject}, nil
}
