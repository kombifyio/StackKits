package localowner

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

// ApplicationOIDCClientRequest names one application's confidential Pocket ID
// client. The client ID is stable per workload; SecretRef is the custody ref
// the issued client secret is stored under.
type ApplicationOIDCClientRequest struct {
	ClientID    string
	Name        string
	CallbackURL string
	SecretRef   string
}

// ApplicationOIDCClient is the secret-free result: the client ID and the
// issuer URL the application discovers Pocket ID at.
type ApplicationOIDCClient struct {
	ClientID string
	Issuer   string
}

var applicationOIDCClientIDPattern = regexp.MustCompile(`^stackkit-[a-z][a-z0-9-]{0,62}$`)

// ApplicationOIDCClientID is the stable Pocket ID client ID of a workload.
func ApplicationOIDCClientID(workloadRef string) string { return "stackkit-" + workloadRef }

// ApplicationOIDCClientSecretRef is the custody ref of a workload's issued
// Pocket ID client secret.
func ApplicationOIDCClientSecretRef(workloadRef string) string {
	return "secret://workloads/" + workloadRef + "/pocketid-client-secret"
}

type oidcClientUpdater interface {
	UpdateOIDCClient(context.Context, string, pocketid.RegisterClientRequest) (*pocketid.OIDCClient, error)
}

// EnsureApplicationOIDCClient registers or converges a confidential,
// PKCE-enabled Pocket ID client for one application, restricted to the same
// owner groups as the TinyAuth gate, and keeps its secret in local custody as
// an issued secret. An existing client keeps its secret while custody holds
// it; a client whose secret custody was lost receives a new secret.
func (s *Service) EnsureApplicationOIDCClient(ctx context.Context, request ApplicationOIDCClientRequest) (ApplicationOIDCClient, error) {
	if !applicationOIDCClientIDPattern.MatchString(request.ClientID) || strings.TrimSpace(request.Name) == "" ||
		!strings.HasPrefix(request.CallbackURL, "https://") || !strings.HasPrefix(request.SecretRef, "secret://") {
		return ApplicationOIDCClient{}, errors.New("localowner: application OIDC client request is invalid")
	}
	_, client, err := s.ready(ctx)
	if err != nil {
		return ApplicationOIDCClient{}, err
	}
	if err := verifyPocketIDAdmin(ctx, client); err != nil {
		return ApplicationOIDCClient{}, err
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(s.workspaceRoot)
	if err != nil {
		return ApplicationOIDCClient{}, err
	}
	groupIDs, err := exactRequiredGroupIDs(ctx, client)
	if err != nil {
		return ApplicationOIDCClient{}, err
	}
	desired := pocketid.RegisterClientRequest{
		ID: request.ClientID, Name: request.Name, CallbackURLs: []string{request.CallbackURL},
		IsPublic: false, PkceEnabled: true, IsGroupRestricted: true,
	}
	existing, err := client.GetOIDCClient(ctx, request.ClientID)
	secret := ""
	switch {
	case errors.Is(err, pocketid.ErrNotFound):
		registered, registerErr := client.RegisterOIDCClient(ctx, desired)
		if registerErr != nil || registered == nil || strings.TrimSpace(registered.Secret) == "" {
			return ApplicationOIDCClient{}, errors.New("localowner: Pocket ID application client registration failed")
		}
		secret = registered.Secret
	case err != nil:
		return ApplicationOIDCClient{}, errors.New("localowner: Pocket ID application client readback failed")
	default:
		if existing.Name != desired.Name || !slices.Equal(existing.CallbackURLs, desired.CallbackURLs) ||
			existing.IsPublic || !existing.PkceEnabled || !existing.IsGroupRestricted {
			updater, ok := client.(oidcClientUpdater)
			if !ok {
				return ApplicationOIDCClient{}, errors.New("localowner: Pocket ID application client differs and cannot be converged")
			}
			if _, err := updater.UpdateOIDCClient(ctx, request.ClientID, desired); err != nil {
				return ApplicationOIDCClient{}, errors.New("localowner: Pocket ID application client update failed")
			}
		}
		if _, custodyErr := localevidence.ResolveLocalSecretMaterial(s.workspaceRoot, request.SecretRef); custodyErr != nil {
			if secret, err = client.CreateOIDCClientSecret(ctx, request.ClientID); err != nil {
				return ApplicationOIDCClient{}, errors.New("localowner: Pocket ID application client secret rotation failed")
			}
		}
	}
	if _, err := client.UpdateOIDCClientAllowedUserGroups(ctx, request.ClientID, groupIDs); err != nil {
		return ApplicationOIDCClient{}, errors.New("localowner: Pocket ID application client group binding failed")
	}
	if secret != "" {
		material := []byte(secret)
		defer clear(material)
		if err := localevidence.StoreLocalIssuedSecret(s.workspaceRoot, request.SecretRef, material); err != nil {
			return ApplicationOIDCClient{}, fmt.Errorf("localowner: custody application client secret: %w", err)
		}
	}
	return ApplicationOIDCClient{ClientID: request.ClientID, Issuer: address.PocketIDOrigin()}, nil
}
