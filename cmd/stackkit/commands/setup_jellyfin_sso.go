package commands

import (
	"context"
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
	"net/http"
)

func configureJellyfinSSO(ctx context.Context, client *http.Client, baseURL, workspace string, observed *appsetup.JellyfinOwnerResult) error {
	binding, err := localevidence.LoadOwnerRuntimeBinding(workspace)
	if err != nil {
		return err
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(workspace)
	if err != nil {
		return err
	}
	secret, err := localevidence.ResolveLocalSecretMaterial(workspace, localowner.ApplicationOIDCClientSecretRef("media"))
	if err != nil {
		return err
	}
	defer clear(secret)
	return observed.EnsureSSO(ctx, client, baseURL, appsetup.JellyfinSSORequest{Issuer: address.PocketIDOrigin(), ClientID: localowner.ApplicationOIDCClientID("media"), ClientSecret: string(secret), OwnerSubject: binding.PocketIDSubject})
}
