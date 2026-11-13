package localowner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/householdapps"
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
	Audience          *householdapps.Audience
	// Public is admitted only for the governed Smart Home and kombify account
	// link PKCE clients.
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

// applicationAudienceMarkerPrefix marks the Pocket ID client of an application
// that carries an explicit audience decision. The decision follows the prefix
// as its canonical JSON, so member-grant reconciliation keys on the decision
// instead of inferring it from the client's group pair.
const applicationAudienceMarkerPrefix = "stackkit.application-audience/v1 "

// applicationAudienceDescription renders the explicit decision that is recorded
// on the Pocket ID client.
func applicationAudienceDescription(audience householdapps.Audience) (string, error) {
	raw, err := json.Marshal(audience)
	if err != nil {
		return "", err
	}
	return applicationAudienceMarkerPrefix + string(raw), nil
}

// applicationAudienceFromDescription reads the explicit decision back from the
// Pocket ID client. A description that is not our marker, or that does not
// carry the canonical decision, carries no decision at all.
func applicationAudienceFromDescription(description string) (*householdapps.Audience, bool) {
	trimmed := strings.TrimSpace(description)
	if !strings.HasPrefix(trimmed, applicationAudienceMarkerPrefix) {
		return nil, false
	}
	raw := strings.TrimPrefix(trimmed, applicationAudienceMarkerPrefix)
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var audience householdapps.Audience
	if err := decoder.Decode(&audience); err != nil {
		return nil, false
	}
	canonical, err := json.Marshal(audience)
	if err != nil || string(canonical) != raw {
		return nil, false
	}
	if _, err := audience.Groups(); err != nil {
		return nil, false
	}
	return &audience, true
}

// applicationAudienceDescriptionDiffers reports whether the recorded decision
// must be rewritten: a decision that is present but different, or a decision
// that was withdrawn. A client without our marker keeps the description its
// owner gave it.
func applicationAudienceDescriptionDiffers(existing, desired string) bool {
	if desired != "" {
		return existing != desired
	}
	_, marked := applicationAudienceFromDescription(existing)
	return marked
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
		(request.Public && ((request.ClientID != ApplicationOIDCClientID("smart-home") && request.ClientID != KombifyLinkClientID) || request.SecretRef != "" || request.RequiredPrivilege != "user")) {
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
	if request.Audience != nil {
		marker, markerErr := applicationAudienceDescription(*request.Audience)
		if markerErr != nil {
			return ApplicationOIDCClient{}, errors.New("localowner: application audience decision is invalid")
		}
		desired.Description = marker
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
			existing.IsPublic != request.Public || !existing.PkceEnabled || !existing.IsGroupRestricted ||
			applicationAudienceDescriptionDiffers(existing.Description, desired.Description) {
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
	// Clients restricted to owners and admins (Vault, Marketplace) read their
	// group binding back, so a household user is provably refused at Pocket ID.
	if (request.RequiredPrivilege != "" && request.RequiredPrivilege != "user") || request.Public || request.Audience != nil {
		observed, err := client.GetOIDCClient(ctx, request.ClientID)
		if err != nil || observed == nil || observed.ID != request.ClientID || observed.Name != desired.Name ||
			observed.IsPublic != request.Public || !observed.PkceEnabled || !observed.IsGroupRestricted ||
			!slices.Equal(observed.CallbackURLs, callbackURLs) || !samePocketIDGroupIDs(observed.AllowedUserGroups, groupIDs) ||
			(request.Audience != nil && observed.Description != desired.Description) {
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
		// A grantable app admits the open household group (members without an
		// explicit app list) or its own app group. The kombify account link
		// and unlisted clients keep the whole household.
		if app := strings.TrimPrefix(request.ClientID, "stackkit-"); householdapps.IsGrantable(app) && request.ClientID == ApplicationOIDCClientID(app) {
			names = []string{"admins", "owners", householdOpenGroupName, householdapps.AppGroup(app)}
		}
	case "vault", "admin", "identity", "secrets", "recovery":
		names = []string{"admins", "owners"}
	default:
		return nil, errors.New("localowner: unknown application OIDC privilege")
	}
	if request.Audience != nil {
		if request.RequiredPrivilege != "user" || !householdapps.IsGrantable(strings.TrimPrefix(request.ClientID, "stackkit-")) {
			return nil, errors.New("localowner: application audience requires a governed user application")
		}
		var err error
		names, err = request.Audience.Groups()
		if err != nil {
			return nil, err
		}
	}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		var id string
		var err error
		switch {
		case name == householdOpenGroupName:
			id, err = ensureHouseholdOpenGroup(ctx, client)
		case strings.HasPrefix(name, householdAppGroupPrefix):
			id, _, err = ensureNamedGroup(ctx, client, name, "Household app "+strings.TrimPrefix(name, householdAppGroupPrefix))
		default:
			id, err = client.GetGroupIDByName(ctx, name)
		}
		if err != nil || strings.TrimSpace(id) == "" {
			return nil, errors.New("localowner: required application PocketID group is missing")
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}
