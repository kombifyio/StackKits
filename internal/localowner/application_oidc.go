package localowner

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

// ApplicationOIDCClientRequest names one application's governed Pocket ID
// client. The client ID is stable per workload; SecretRef is the custody ref
// the issued client secret is stored under.
type ApplicationOIDCClientRequest struct {
	// RequiredPrivilege comes from the validated catalog endpoint. Empty keeps
	// the existing household application policy; vault always requires vault.
	RequiredPrivilege string
	// Public is admitted only for the governed Smart Home PKCE integration.
	Public   bool
	ClientID string
	Name     string
	// CallbackURL keeps the single-callback contract for existing callers.
	// CallbackURLs is used by applications such as Immich that own several
	// exact browser and mobile callbacks. Callers set exactly one form.
	CallbackURL  string
	CallbackURLs []string
	SecretRef    string
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

// EnsureApplicationOIDCClient registers or converges a governed,
// PKCE-enabled Pocket ID client for one application, restricted to the same
// owner groups as the TinyAuth gate, and keeps its secret in local custody as
// an issued secret. An existing client keeps its secret while custody holds
// it; a client whose secret custody was lost receives a new secret. The exact
// public Smart Home client never issues or custodies a secret.
func (s *Service) EnsureApplicationOIDCClient(ctx context.Context, request ApplicationOIDCClientRequest) (ApplicationOIDCClient, error) {
	callbackURLs, callbackErr := applicationOIDCCallbackURLs(request)
	if !applicationOIDCClientIDPattern.MatchString(request.ClientID) || strings.TrimSpace(request.Name) == "" ||
		callbackErr != nil || (!request.Public && !strings.HasPrefix(request.SecretRef, "secret://")) ||
		(request.Public && (request.ClientID != ApplicationOIDCClientID("smart-home") || request.SecretRef != "" || request.RequiredPrivilege != "user")) {
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
	groupIDs, err := applicationOIDCGroupIDs(ctx, client, request)
	if err != nil {
		return ApplicationOIDCClient{}, err
	}
	desired := pocketid.RegisterClientRequest{
		ID: request.ClientID, Name: request.Name, CallbackURLs: callbackURLs,
		IsPublic: request.Public, PkceEnabled: true, IsGroupRestricted: true,
	}
	existing, err := client.GetOIDCClient(ctx, request.ClientID)
	secret := ""
	switch {
	case errors.Is(err, pocketid.ErrNotFound):
		registered, registerErr := client.RegisterOIDCClient(ctx, desired)
		if registerErr != nil || registered == nil || (!request.Public && strings.TrimSpace(registered.Secret) == "") {
			return ApplicationOIDCClient{}, errors.New("localowner: Pocket ID application client registration failed")
		}
		if !request.Public {
			secret = registered.Secret
		}
	case err != nil:
		return ApplicationOIDCClient{}, errors.New("localowner: Pocket ID application client readback failed")
	default:
		if existing.Name != desired.Name || !slices.Equal(existing.CallbackURLs, desired.CallbackURLs) ||
			existing.IsPublic != request.Public || !existing.PkceEnabled || !existing.IsGroupRestricted {
			updater, ok := client.(oidcClientUpdater)
			if !ok {
				return ApplicationOIDCClient{}, errors.New("localowner: Pocket ID application client differs and cannot be converged")
			}
			if _, err := updater.UpdateOIDCClient(ctx, request.ClientID, desired); err != nil {
				return ApplicationOIDCClient{}, errors.New("localowner: Pocket ID application client update failed")
			}
		}
		if !request.Public {
			if _, custodyErr := localevidence.ResolveLocalSecretMaterial(s.workspaceRoot, request.SecretRef); custodyErr != nil {
				if secret, err = client.CreateOIDCClientSecret(ctx, request.ClientID); err != nil {
					return ApplicationOIDCClient{}, errors.New("localowner: Pocket ID application client secret rotation failed")
				}
			}
		}
	}
	if _, err := client.UpdateOIDCClientAllowedUserGroups(ctx, request.ClientID, groupIDs); err != nil {
		return ApplicationOIDCClient{}, errors.New("localowner: Pocket ID application client group binding failed")
	}
	if request.RequiredPrivilege == "vault" || request.Public {
		observed, err := client.GetOIDCClient(ctx, request.ClientID)
		if err != nil || observed == nil || observed.ID != request.ClientID || observed.Name != desired.Name ||
			observed.IsPublic != request.Public || !observed.PkceEnabled || !observed.IsGroupRestricted ||
			!slices.Equal(observed.CallbackURLs, callbackURLs) || !samePocketIDGroupIDs(observed.AllowedUserGroups, groupIDs) {
			return ApplicationOIDCClient{}, errors.New("localowner: application OIDC admission readback differs")
		}
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

func applicationOIDCCallbackURLs(request ApplicationOIDCClientRequest) ([]string, error) {
	if (strings.TrimSpace(request.CallbackURL) == "") == (len(request.CallbackURLs) == 0) {
		return nil, errors.New("exactly one callback representation is required")
	}
	callbacks := request.CallbackURLs
	if request.CallbackURL != "" {
		callbacks = []string{request.CallbackURL}
	}
	result := make([]string, 0, len(callbacks))
	seen := make(map[string]struct{}, len(callbacks))
	for _, raw := range callbacks {
		callback := strings.TrimSpace(raw)
		parsed, err := url.Parse(callback)
		if err != nil || parsed.Fragment != "" || parsed.RawQuery != "" ||
			(parsed.Scheme != "https" && callback != "app.immich:///oauth-callback") ||
			(parsed.Scheme == "https" && (parsed.Host == "" || !strings.HasPrefix(parsed.Path, "/"))) {
			return nil, errors.New("callback URL is invalid")
		}
		if _, duplicate := seen[callback]; duplicate {
			return nil, errors.New("callback URL is duplicated")
		}
		seen[callback] = struct{}{}
		result = append(result, callback)
	}
	return result, nil
}

// Match the catalog's route admission at the IdP as well as the outer gate.
// Callers cannot accidentally widen the stable Vault client by omitting policy.
func applicationOIDCGroupIDs(ctx context.Context, client pocketIDOwnerClient, request ApplicationOIDCClientRequest) ([]string, error) {
	if request.ClientID == ApplicationOIDCClientID("vault") && request.RequiredPrivilege != "vault" {
		return nil, errors.New("localowner: Vault OIDC requires its catalog vault privilege")
	}
	names := []string{"admins", "owners", "household"}
	switch request.RequiredPrivilege {
	case "", "user":
	case "vault", "admin", "identity", "secrets", "recovery":
		names = []string{"admins", "owners"}
	default:
		return nil, errors.New("localowner: unknown application OIDC privilege")
	}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		id, err := client.GetGroupIDByName(ctx, name)
		if err != nil || strings.TrimSpace(id) == "" {
			return nil, errors.New("localowner: required application PocketID group is missing")
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}
