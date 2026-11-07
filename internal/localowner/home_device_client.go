package localowner

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"

	"github.com/go-jose/go-jose/v4"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

const (
	// HomeDeviceClientID is the Pocket ID OIDC client through which a device
	// client (Hub PWA, Companion, Techstack desktop) signs its person in with a
	// passkey: for enrollment, for the 24 hour user verification of renewal and
	// for a person's step-up from their own enrolled device. It is a public
	// PKCE client that requires a fresh authentication on every sign-in and
	// admits only the Homelab groups.
	HomeDeviceClientID = "stackkit-home-device"
	// HomeDeviceCallbackPath is the device client's redirect on the Hub
	// origin (the base host of the identity runtime address).
	HomeDeviceCallbackPath = "/device/callback"
	// HomeDeviceKombifyCallback is the kombify app's own redirect: a person
	// with a linked kombify account reaches the homelab through the kombify
	// app's OIDC page, and the same public PKCE client serves it.
	HomeDeviceKombifyCallback = "https://kombify.io/app/homelab-oidc"

	homeDeviceClientLockPath = ".stackkit/custody/home-verifier/pocketid-device-client"
)

// HomeDeviceTrust is the live Pocket ID material the Authority pins in its
// Owner-signed Home trust: the issuer (the passkey relying party), the
// current signing keys and the Owner's Pocket ID subject.
type HomeDeviceTrust struct {
	Issuer       string
	OwnerSubject string
	OwnerRef     string
	HomeSiteRef  string
	Keys         jose.JSONWebKeySet
}

// PrepareHomeDeviceTrust registers the device sign-in client in the local
// Pocket ID on first use, verifies an existing one against the exact policy
// (never rewriting it silently), and returns the current pin material. It is
// the production path behind the Home trust binding; it creates no user and
// grants no access by itself.
func (s *Service) PrepareHomeDeviceTrust(ctx context.Context) (HomeDeviceTrust, error) {
	binding, err := s.Verify(ctx)
	if err != nil {
		return HomeDeviceTrust{}, err
	}
	owner, client, err := s.ready(ctx)
	if err != nil {
		return HomeDeviceTrust{}, err
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(s.workspaceRoot)
	if err != nil {
		return HomeDeviceTrust{}, err
	}
	hubCallback, err := HomeDeviceCallbackURL(s.workspaceRoot)
	if err != nil {
		return HomeDeviceTrust{}, err
	}
	callbacks := []string{hubCallback, HomeDeviceKombifyCallback}
	// The lock lives in the custody directory, so the host CLI and the core
	// server (which sees custody through a bind mount) exclude each other.
	release, err := localevidence.AcquireCustodyLock(s.workspaceRoot, homeDeviceClientLockPath, 1)
	if err != nil {
		return HomeDeviceTrust{}, err
	}
	defer release()
	groups, err := exactRequiredGroupIDs(ctx, client)
	if err != nil {
		return HomeDeviceTrust{}, err
	}
	registered, err := client.GetOIDCClient(ctx, HomeDeviceClientID)
	if errors.Is(err, pocketid.ErrNotFound) {
		registered, err = client.RegisterOIDCClient(ctx, pocketid.RegisterClientRequest{
			ID: HomeDeviceClientID, Name: "StackKits device sign-in", CallbackURLs: callbacks,
			IsPublic: true, PkceEnabled: true, RequiresReauthentication: true, IsGroupRestricted: true,
		})
		if err != nil {
			return HomeDeviceTrust{}, ErrStepUpRejected
		}
	}
	// A client registered before the kombify app callback existed carries
	// exactly the Hub redirect: converge it to the current set. Any other
	// callback list is foreign and stays refused below.
	if updater, ok := client.(interface {
		UpdateOIDCClient(context.Context, string, pocketid.RegisterClientRequest) (*pocketid.OIDCClient, error)
	}); ok && err == nil && registered != nil && slices.Equal(registered.CallbackURLs, []string{hubCallback}) {
		registered, err = updater.UpdateOIDCClient(ctx, HomeDeviceClientID, pocketid.RegisterClientRequest{
			Name: registered.Name, CallbackURLs: callbacks, IsPublic: true, PkceEnabled: true, RequiresReauthentication: true, IsGroupRestricted: true,
		})
		if err == nil {
			registered, err = client.GetOIDCClient(ctx, HomeDeviceClientID)
		}
	}
	if err == nil && registered != nil && len(registered.AllowedUserGroups) == 0 {
		// First registration, or the resume of an interrupted one: the same
		// owned client gets exactly the Homelab groups.
		registered, err = client.UpdateOIDCClientAllowedUserGroups(ctx, HomeDeviceClientID, groups)
	}
	if err != nil || registered == nil || registered.ID != HomeDeviceClientID || !registered.IsPublic ||
		len(registered.Credentials.FederatedIdentities) != 0 || !registered.PkceEnabled || !registered.RequiresReauthentication ||
		!registered.IsGroupRestricted || !slices.Equal(registered.CallbackURLs, callbacks) ||
		!samePocketIDGroupIDs(registered.AllowedUserGroups, groups) {
		return HomeDeviceTrust{}, ErrStepUpRejected
	}
	var keys jose.JSONWebKeySet
	if err = stepUpJSON(ctx, http.MethodGet, pocketIDLocalAPI+"/.well-known/jwks.json", nil, &keys); err != nil {
		return HomeDeviceTrust{}, err
	}
	return HomeDeviceTrust{
		Issuer: address.PocketIDOrigin(), OwnerSubject: binding.PocketIDSubject, OwnerRef: binding.OwnerRef,
		HomeSiteRef: owner.Binding.SiteRef, Keys: keys,
	}, nil
}

// HomeDeviceCallbackURL is the device sign-in client's one redirect: the Hub
// origin (the base host of the identity runtime address) plus
// HomeDeviceCallbackPath.
// It reads the Authority's public record, so the core's identity workspace,
// which holds no runtime custody, resolves it as the host does.
func HomeDeviceCallbackURL(workspaceRoot string) (string, error) {
	authority, err := localevidence.CurrentHomeAuthority(workspaceRoot)
	if err != nil {
		return "", err
	}
	address, err := authority.IdentityRuntimeAddress()
	if err != nil {
		return "", err
	}
	return "https://" + address.ServiceHost("base") + HomeDeviceCallbackPath, nil
}

// ExchangeHomeDeviceCode redeems one authorization code of the public device
// sign-in client (PKCE, no secret) at the local Pocket ID and returns the ID
// token. It grants nothing by itself: the token's nonce binds it to one device
// key and proof, and every consumer verifies it against the Owner-signed Home
// trust. The Hub uses it because a browser cannot rely on Pocket ID's token
// endpoint answering cross-origin requests.
func ExchangeHomeDeviceCode(ctx context.Context, workspaceRoot, code, verifier string) (string, error) {
	if code == "" || len(code) > 4096 || len(verifier) < 43 || len(verifier) > 128 {
		return "", ErrStepUpRejected
	}
	callback, err := HomeDeviceCallbackURL(workspaceRoot)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {HomeDeviceClientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {callback}}
	var response struct {
		IDToken string `json:"id_token"`
	}
	if err := stepUpJSON(ctx, http.MethodPost, pocketIDLocalAPI+"/api/oidc/token", form, &response); err != nil {
		return "", err
	}
	if response.IDToken == "" {
		return "", ErrStepUpRejected
	}
	return response.IDToken, nil
}
