package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"errors"
	"io/fs"
	"log/slog"
	"slices"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
)

// HomeDeviceClientID is the Pocket ID OIDC client through which a device
// client signs its person in with a passkey: enrollment, the 24 hour user
// verification of renewal and a person's step-up. Its ID tokens are the only
// accepted person assertions.
const HomeDeviceClientID = localowner.HomeDeviceClientID

// homeTrustRefreshInterval keeps the 24 hour Home trust current with margin;
// a failed refresh is retried every homeTrustRetryInterval.
const (
	homeTrustRefreshInterval = time.Hour
	homeTrustRetryInterval   = time.Minute
	homeTrustIdleInterval    = 10 * time.Minute
)

// RefreshHomePublicTrust is the production path of the Owner-signed Home
// trust. It registers the device sign-in client in the local Pocket ID on
// first use (or verifies the registered one), reads the live issuer, signing
// keys and Owner subject, and binds them as the Home trust for 24 hours. The
// new trust carries every account the previous trust revoked and every
// account the removal chain recorded, so a refresh never readmits one.
func RefreshHomePublicTrust(ctx context.Context, root string, now time.Time) (HomePublicTrust, error) {
	service, err := localowner.NewService(root)
	if err != nil {
		return HomePublicTrust{}, err
	}
	live, err := service.PrepareHomeDeviceTrust(ctx)
	if err != nil {
		return HomePublicTrust{}, err
	}
	keys, err := homePinnedKeys(live.Keys)
	if err != nil {
		return HomePublicTrust{}, err
	}
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return HomePublicTrust{}, err
	}
	issued := now.UTC().Truncate(time.Second)
	revoked := append([]string{}, registry.RemovedAccounts...)
	prior, err := loadOwnerSignedHomeVerifierState(root)
	switch {
	case err == nil:
		for _, account := range prior.Trust.RevokedSubjects {
			if !slices.Contains(revoked, account) {
				revoked = append(revoked, account)
			}
		}
		if !issued.After(prior.Trust.IssuedAt) {
			issued = prior.Trust.IssuedAt.Add(time.Second)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return HomePublicTrust{}, err
	}
	// The Owner is never a revoked account of their own Home trust.
	revoked = slices.DeleteFunc(revoked, func(account string) bool { return account == live.OwnerSubject })
	slices.Sort(revoked)
	trust := HomePublicTrust{
		HomeSiteRef: live.HomeSiteRef,
		PocketID: HomePocketIDTrust{
			Issuer: live.Issuer, ClientID: HomeDeviceClientID, Subject: live.OwnerSubject, Keys: keys,
		},
		RevokedSubjects: revoked, IssuedAt: issued, ExpiresAt: issued.Add(homeVerifierMaxTrustLifetime),
	}
	if err := BindHomePublicTrust(root, trust); err != nil {
		return HomePublicTrust{}, err
	}
	state, err := loadHomeVerifierState(root)
	if err != nil {
		return HomePublicTrust{}, err
	}
	invalidateHomeVerifier(root)
	return state.Trust, nil
}

// KeepHomePublicTrustCurrent refreshes the Home trust at once and then every
// hour on a server that holds Owner custody, retrying a failed refresh every
// minute, until ctx ends. A server without Owner custody (yet) is checked
// again every homeTrustIdleInterval without a warning. While a refresh
// fails, the last trust stays valid to its own expiry; after that every
// passkey verification, enrollment and step-up fails closed.
func KeepHomePublicTrustCurrent(ctx context.Context, root string) {
	delay := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if _, err := localevidence.LoadOwnerCustody(root); err != nil {
			delay = homeTrustIdleInterval
			continue
		}
		refresh, cancel := context.WithTimeout(ctx, time.Minute)
		trust, err := RefreshHomePublicTrust(refresh, root, time.Now())
		cancel()
		if err != nil {
			slog.Warn("Home trust refresh failed; enrollments and step-ups fail closed once the bound trust expires", "error", err)
			delay = homeTrustRetryInterval
			continue
		}
		slog.Info("Home trust refreshed", "issuer", trust.PocketID.Issuer, "expiresAt", trust.ExpiresAt, "revokedAccounts", len(trust.RevokedSubjects))
		delay = homeTrustRefreshInterval
	}
}

// homePinnedKeys turns the live Pocket ID key set into the pinned form: a
// signing use and one algorithm per key, derived from the key type when the
// key set omits it, so a token can never choose its verification algorithm.
// Keys for another use are left out.
func homePinnedKeys(set jose.JSONWebKeySet) (jose.JSONWebKeySet, error) {
	pinned := jose.JSONWebKeySet{}
	for _, key := range set.Keys {
		if key.Use == "" {
			key.Use = "sig"
		}
		if key.Use != "sig" {
			continue
		}
		if key.Algorithm == "" {
			switch material := key.Key.(type) {
			case ed25519.PublicKey:
				key.Algorithm = string(jose.EdDSA)
			case *ecdsa.PublicKey:
				if material.Curve == elliptic.P256() {
					key.Algorithm = string(jose.ES256)
				}
			case *rsa.PublicKey:
				key.Algorithm = string(jose.RS256)
			}
		}
		pinned.Keys = append(pinned.Keys, key.Public())
	}
	if !validHomePublicKeys(pinned) {
		return jose.JSONWebKeySet{}, errors.New("identity: the Pocket ID signing keys cannot be pinned")
	}
	return pinned, nil
}
