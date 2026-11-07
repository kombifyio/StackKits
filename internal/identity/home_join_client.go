package identity

import (
	"context"
	"crypto/ecdh"
	"crypto/hpke"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

// The OIDC client of a joined server's TinyAuth (homelab identity target §4):
// the Authority registers it in its Pocket ID under an Owner step-up of class
// client-registration, and the secret reaches the joined server only through
// the homelab's Wallet by host delivery. The Authority never prints the
// secret: it seals it with HPKE to the carrier key Techstack generated for
// this one registration and holds only in memory, and Techstack opens it only
// to seal it straight into the Wallet. The joined server then binds TinyAuth
// to the Wallet item it imported.
const (
	HomeJoinClientRequestSchema = "stackkit.homelab-oidc-client-request/v1"
	HomeJoinClientSchema        = "stackkit.homelab-oidc-client/v1"
	HomeJoinClientBindingSchema = "stackkit.homelab-oidc-client-binding/v1"
	// HomeJoinClientCaptureInfo is the HPKE info prefix of the sealed secret;
	// the carrier rebuilds it with HomeJoinClientCaptureContext.
	HomeJoinClientCaptureInfo = "stackkit.homelab-oidc-client-capture/v1\x00"
	homeJoinClientNamePrefix  = "stackkit-tinyauth-"
)

// HomeJoinClientRequest is what Techstack hands the Authority for one joined
// server's TinyAuth client.
type HomeJoinClientRequest struct {
	Schema string `json:"schema"`
	// ServerRef is the joined server's owner reference.
	ServerRef    string `json:"serverRef"`
	TinyAuthHost string `json:"tinyAuthHost"`
	// CarrierKey is the X25519 public key (base64url, 32 bytes) the secret is
	// sealed to.
	CarrierKey string `json:"carrierKey"`
}

// HomeJoinClient is the Authority's answer: the public client and its secret
// sealed to the carrier key. It is safe to store and log.
type HomeJoinClient struct {
	Schema           string    `json:"schema"`
	AuthoritySiteRef string    `json:"authoritySiteRef"`
	ServerRef        string    `json:"serverRef"`
	ClientID         string    `json:"clientId"`
	Issuer           string    `json:"issuer"`
	CallbackURL      string    `json:"callbackUrl"`
	CarrierKeySHA256 string    `json:"carrierKeySha256"`
	SealedSecret     string    `json:"sealedSecret"`
	IssuedAt         time.Time `json:"issuedAt"`
}

// HomeJoinClientBinding is what the joined server binds: the public client
// and the Wallet item that carries its secret to this server.
type HomeJoinClientBinding struct {
	Schema    string `json:"schema"`
	ClientID  string `json:"clientId"`
	Issuer    string `json:"issuer"`
	TenantRef string `json:"tenantRef"`
	ItemRef   string `json:"itemRef"`
}

// HomeJoinClientCaptureContext is the HPKE info the secret is sealed under:
// it binds the ciphertext to the Authority, the server, the client, the
// issuer, the callback and the issue time.
func HomeJoinClientCaptureContext(client HomeJoinClient) []byte {
	return []byte(HomeJoinClientCaptureInfo + strings.Join([]string{
		client.AuthoritySiteRef, client.ServerRef, client.ClientID, client.Issuer, client.CallbackURL,
		client.CarrierKeySHA256, client.IssuedAt.UTC().Format(time.RFC3339),
	}, "\n"))
}

// HomeJoinClientID is the stable Pocket ID client id of a joined server's
// TinyAuth on this Authority, derived from its owner reference.
func HomeJoinClientID(server HomeJoinServer) string {
	sum := sha256.Sum256([]byte(server.OwnerRef))
	return homeJoinClientNamePrefix + hex.EncodeToString(sum[:8])
}

// ParseHomeJoinClientRequest decodes a carried client request strictly.
func ParseHomeJoinClientRequest(raw []byte) (HomeJoinClientRequest, []byte, error) {
	var request HomeJoinClientRequest
	if len(raw) == 0 || len(raw) > homeJoinMaxBytes || decodeHomeJoinStrict(raw, &request) != nil || request.Schema != HomeJoinClientRequestSchema ||
		!homeJoinRefPattern.MatchString(request.ServerRef) || !homeJoinHostname.MatchString(request.TinyAuthHost) {
		return HomeJoinClientRequest{}, nil, ErrHomeJoinRefused
	}
	carrier, err := base64.RawURLEncoding.Strict().DecodeString(request.CarrierKey)
	if err != nil || len(carrier) != 32 {
		return HomeJoinClientRequest{}, nil, ErrHomeJoinRefused
	}
	if _, err := ecdh.X25519().NewPublicKey(carrier); err != nil {
		return HomeJoinClientRequest{}, nil, ErrHomeJoinRefused
	}
	return request, carrier, nil
}

// HomeJoinClientAction is the step-up action of class client-registration for
// exactly this server, host and carrier key on this Authority.
func HomeJoinClientAction(root string, request HomeJoinClientRequest) (HomeStepUpAction, error) {
	authority, err := homeAuthority(root)
	if err != nil {
		return HomeStepUpAction{}, err
	}
	return NewHomeStepUpAction(HomeStepUpClientRegistration, struct {
		AuthoritySiteRef string                `json:"authoritySiteRef"`
		Request          HomeJoinClientRequest `json:"request"`
	}{authority.SiteRef, request})
}

// RegisterHomeJoinedClient runs on the Authority's host. The server must be a
// current joined server and the TinyAuth host one of the names the Owner
// approved for it (never an identity name). After the Owner's passkey
// step-up the Authority's Pocket ID registers or converges the server's
// confidential PKCE client (group-restricted to the Authority TinyAuth's
// groups) and sets a fresh secret, which leaves this host only sealed to the
// carrier key.
//
//nolint:gocyclo // One ordered list of independent refusals before one effect.
func RegisterHomeJoinedClient(ctx context.Context, root string, raw []byte, approve func(context.Context, HomeStepUpAction, string) error, now time.Time) (HomeJoinClient, error) {
	request, carrier, err := ParseHomeJoinClientRequest(raw)
	if err != nil {
		return HomeJoinClient{}, err
	}
	now = now.UTC().Truncate(time.Second)
	servers, err := loadHomeJoinedServers(root)
	if err != nil {
		return HomeJoinClient{}, errors.New("identity: this Authority has no joined servers")
	}
	index := slices.IndexFunc(servers.Servers, func(server homeJoinedServer) bool {
		return server.Server.OwnerRef == request.ServerRef && !server.Removed && now.Before(server.ExpiresAt)
	})
	if index < 0 {
		return HomeJoinClient{}, errors.New("identity: " + request.ServerRef + " is not a current joined server of this Authority")
	}
	server := servers.Servers[index].Server
	address, err := homeIdentityAddress(root)
	if err != nil {
		return HomeJoinClient{}, err
	}
	authority, err := homeAuthority(root)
	if err != nil {
		return HomeJoinClient{}, err
	}
	inside := func(name, tree string) bool { return name == tree || strings.HasSuffix(name, "."+tree) }
	if !slices.ContainsFunc(server.Hostnames, func(tree string) bool { return inside(request.TinyAuthHost, tree) }) {
		return HomeJoinClient{}, errors.New("identity: the TinyAuth host lies outside the names the Owner approved for " + server.NodeRef)
	}
	for _, name := range append(homeJoinIdentityNames(address), loadHomeAuthorityHosts(root)...) {
		if inside(request.TinyAuthHost, name) || inside(name, request.TinyAuthHost) {
			return HomeJoinClient{}, errors.New("identity: the TinyAuth host cannot cover the Authority's name " + name)
		}
	}
	authorityBinding, err := localevidence.LoadBasementTinyAuthPocketIDBinding(root)
	if err != nil {
		return HomeJoinClient{}, errors.New("identity: the Authority's own TinyAuth is not bound to its Pocket ID yet; apply the Authority first")
	}
	adminKey, err := localevidence.ReadBasementRuntimePocketIDAdminKey(root)
	if err != nil {
		return HomeJoinClient{}, err
	}
	action, err := HomeJoinClientAction(root, request)
	if err != nil {
		return HomeJoinClient{}, err
	}
	carrierSum := sha256.Sum256(carrier)
	summary := "Sign-in client for " + server.NodeRef + " at " + request.TinyAuthHost + " (carrier " + hex.EncodeToString(carrierSum[:4]) + ")"
	if len(summary) > homeStepUpSummaryMax {
		return HomeJoinClient{}, ErrHomeJoinRefused
	}
	if approve == nil {
		return HomeJoinClient{}, ErrHomeStepUpDenied
	}
	if err := approve(ctx, action, summary); err != nil {
		return HomeJoinClient{}, err
	}
	client := HomeJoinClient{
		Schema: HomeJoinClientSchema, AuthoritySiteRef: authority.SiteRef, ServerRef: request.ServerRef,
		ClientID: HomeJoinClientID(server), Issuer: address.PocketIDOrigin(),
		CallbackURL:      "https://" + request.TinyAuthHost + "/api/oauth/callback/pocketid",
		CarrierKeySHA256: hex.EncodeToString(carrierSum[:]), IssuedAt: now,
	}
	pocket := pocketid.NewClient(localowner.PocketIDLocalAPI(), adminKey)
	registration := pocketid.RegisterClientRequest{
		Name: "StackKits TinyAuth " + request.TinyAuthHost, CallbackURLs: []string{client.CallbackURL},
		IsPublic: false, PkceEnabled: true, IsGroupRestricted: true,
	}
	var secret string
	existing, err := pocket.GetOIDCClient(ctx, client.ClientID)
	switch {
	case errors.Is(err, pocketid.ErrNotFound):
		registration.ID = client.ClientID
		registered, registerErr := pocket.RegisterOIDCClient(ctx, registration)
		if registerErr != nil || registered == nil {
			return HomeJoinClient{}, errors.New("identity: the Authority's Pocket ID did not register the joined server's client")
		}
		secret = registered.Secret
	case err != nil:
		return HomeJoinClient{}, err
	default:
		if existing == nil || existing.ID != client.ClientID {
			return HomeJoinClient{}, errors.New("identity: the joined server's Pocket ID client identity differs")
		}
		if _, err := pocket.UpdateOIDCClient(ctx, client.ClientID, registration); err != nil {
			return HomeJoinClient{}, errors.New("identity: the joined server's Pocket ID client could not be converged")
		}
		if secret, err = pocket.CreateOIDCClientSecret(ctx, client.ClientID); err != nil {
			return HomeJoinClient{}, errors.New("identity: the joined server's client secret could not be rotated")
		}
	}
	if strings.TrimSpace(secret) == "" {
		return HomeJoinClient{}, errors.New("identity: the Authority's Pocket ID issued no client secret")
	}
	if _, err := pocket.UpdateOIDCClientAllowedUserGroups(ctx, client.ClientID, append([]string(nil), authorityBinding.GroupIDs...)); err != nil {
		return HomeJoinClient{}, errors.New("identity: the joined server's client could not be restricted to the household groups")
	}
	public, err := ecdh.X25519().NewPublicKey(carrier)
	if err != nil {
		return HomeJoinClient{}, ErrHomeJoinRefused
	}
	key, err := hpke.NewDHKEMPublicKey(public)
	if err != nil {
		return HomeJoinClient{}, ErrHomeJoinRefused
	}
	plaintext := []byte(secret)
	sealed, err := hpke.Seal(key, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), HomeJoinClientCaptureContext(client), plaintext)
	clear(plaintext)
	if err != nil {
		return HomeJoinClient{}, errors.New("identity: the client secret could not be sealed to the carrier")
	}
	client.SealedSecret = base64.RawURLEncoding.EncodeToString(sealed)
	return client, nil
}

// ParseHomeJoinClientBinding decodes a carried client binding strictly.
func ParseHomeJoinClientBinding(raw []byte) (HomeJoinClientBinding, error) {
	var binding HomeJoinClientBinding
	if len(raw) == 0 || len(raw) > homeJoinMaxBytes || decodeHomeJoinStrict(raw, &binding) != nil || binding.Schema != HomeJoinClientBindingSchema {
		return HomeJoinClientBinding{}, ErrHomeJoinRefused
	}
	return binding, nil
}

// BindHomeJoinedClient runs on the joined server's host: TinyAuth signs in
// against the Authority's Pocket ID the installed bundle names, with the
// secret of the Wallet item this server imported.
func BindHomeJoinedClient(root string, binding HomeJoinClientBinding) (localevidence.TinyAuthPocketIDBinding, error) {
	bundle, err := LoadHomeJoinedTrust(root)
	if err != nil {
		return localevidence.TinyAuthPocketIDBinding{}, errors.New("identity: this server has not joined an Authority")
	}
	if binding.Issuer != bundle.PocketID.Issuer || binding.ClientID != HomeJoinClientID(bundle.Server) {
		return localevidence.TinyAuthPocketIDBinding{}, errors.New("identity: the client binding names another Authority or another server's client")
	}
	return localevidence.BindJoinedTinyAuthPocketID(root, localevidence.JoinedTinyAuthRequest{
		ClientID: binding.ClientID, Issuer: binding.Issuer, TenantRef: binding.TenantRef, ItemRef: binding.ItemRef,
	})
}
