package commands

import (
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
)

func audiobookshelfOIDCCustody(workspace string, bundle []byte) (*appsetup.AudiobookshelfOIDCRequest, error) {
	deployment, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(bundle)
	if err != nil {
		return nil, err
	}
	for _, component := range deployment.Components {
		if component.PocketIDClient == nil {
			continue
		}
		if _, err := localevidence.LoadOwnerRuntimeBinding(workspace); err != nil {
			return nil, err
		}
		address, err := localevidence.LocalIdentityRuntimeAddress(workspace)
		if err != nil {
			return nil, err
		}
		secret, err := localevidence.ResolveLocalSecretMaterial(workspace, localowner.ApplicationOIDCClientSecretRef("media-audiobooks"))
		if err != nil {
			return nil, err
		}
		defer clear(secret)
		return &appsetup.AudiobookshelfOIDCRequest{Issuer: address.PocketIDOrigin(), ClientID: localowner.ApplicationOIDCClientID("media-audiobooks"), ClientSecret: string(secret)}, nil
	}
	return nil, nil // Previously applied bundles keep their native login policy.
}
