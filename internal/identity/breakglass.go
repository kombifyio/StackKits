package identity

// Break-glass account provisioning.
//
// PocketID v2 is passkey-only: there is no password field on user creation.
// The classical "store the password in a sealed envelope" pattern doesn't
// apply. Instead, the break-glass flow stores a long-TTL one-time-access
// token in the recovery bundle (Task 9). On first redemption the recoverer
// clicks the SetupURL, registers a WebAuthn credential, the token is
// consumed, and the account becomes a normal passkey-protected admin.
//
// One break-glass account is provisioned per node. The synthetic email and
// username encode the node name so multi-node deployments produce distinct
// records that can be revoked or rotated independently.

import (
	"context"
	"fmt"
	"time"

	"github.com/kombifyio/stackkits/internal/pocketid"
)

// defaultBreakGlassTokenTTL is how long the WebAuthn-enrollment token in a
// break-glass bundle stays valid. One year balances "still useful when the
// owner finally needs it" against "blast radius if the bundle leaks".
const defaultBreakGlassTokenTTL = 365 * 24 * time.Hour

// BreakGlassCredential is the materialized result of a break-glass admin
// provisioning. Username, SetupToken, SetupURL and UserID are intended to be
// embedded into the recovery bundle (Task 9).
type BreakGlassCredential struct {
	// Username is the synthetic local handle, of the form
	// "bg-<nodename>@local". It is unique per node so multiple homelabs
	// don't collide on a shared PocketID.
	Username string

	// SetupToken is the raw one-time-access token issued by PocketID. It
	// is single-use and consumed when the recoverer registers a passkey.
	SetupToken string

	// SetupURL is the full URL the recoverer clicks to complete WebAuthn
	// enrollment, of the form "https://id.<domain>/setup-account?token=<t>".
	SetupURL string

	// Group is the PocketID group the account was added to (typically
	// "owners").
	Group string

	// UserID is the PocketID-assigned UUID for this account, used by later
	// rotation/revoke operations.
	UserID string
}

// BreakGlassGenerator creates a per-node break-glass admin in PocketID and
// returns a credential bundle suitable for sealing into the recovery
// envelope.
type BreakGlassGenerator struct {
	// Client is the PocketID admin-API client (real or fake). Reuses the
	// same PocketIDClient interface defined in owner.go.
	Client PocketIDClient

	// NodeName identifies the homelab firstnode this break-glass account
	// belongs to. Required.
	NodeName string

	// PocketIDURL is the public origin of the PocketID instance, used to
	// build SetupURL. Example: "https://id.example.com" (no trailing slash).
	PocketIDURL string

	// OwnersGroup is the name of the group the break-glass admin is added
	// to. Defaults to "owners" when empty.
	OwnersGroup string

	// TokenTTL controls how long the one-time-access token remains valid.
	// Defaults to 365 days when zero.
	TokenTTL time.Duration
}

// Generate provisions a break-glass admin account on PocketID for the
// configured node and returns its BreakGlassCredential. The account is
// created with admin privileges and added to the owners group; a long-TTL
// one-time-access token is issued so the recoverer can enroll a WebAuthn
// credential when (and only when) they actually open the recovery bundle.
func (g *BreakGlassGenerator) Generate(ctx context.Context) (*BreakGlassCredential, error) {
	if g.NodeName == "" {
		return nil, fmt.Errorf("break-glass: NodeName required")
	}

	username := fmt.Sprintf("bg-%s@local", g.NodeName)
	// PocketID requires a syntactically-valid email, but we don't want this
	// to be a real address — the .invalid TLD (RFC 6761) guarantees the
	// address can never resolve, which is exactly what we want for a
	// node-bound emergency credential.
	email := fmt.Sprintf("%s.invalid", username)

	groupName := g.OwnersGroup
	if groupName == "" {
		groupName = "owners"
	}

	tokenTTL := g.TokenTTL
	if tokenTTL == 0 {
		tokenTTL = defaultBreakGlassTokenTTL
	}

	// 1. Create the user record with admin scope. No password field — the
	//    account is unusable until the recoverer redeems the setup token.
	user, err := g.Client.CreateUser(ctx, pocketid.CreateUserRequest{
		Username:  username,
		Email:     email,
		FirstName: username,
		IsAdmin:   true,
	})
	if err != nil {
		return nil, fmt.Errorf("break-glass: create user: %w", err)
	}

	// 2. Resolve the owners group to its UUID and add the user.
	groupID, err := g.Client.GetGroupIDByName(ctx, groupName)
	if err != nil {
		return nil, fmt.Errorf("break-glass: lookup %s group: %w", groupName, err)
	}
	if groupID == "" {
		return nil, fmt.Errorf("break-glass: lookup %s group: no group by that name exists", groupName)
	}
	if err := g.Client.AddUserToGroup(ctx, user.ID, groupID); err != nil {
		return nil, fmt.Errorf("break-glass: add user to %s: %w", groupName, err)
	}

	// 3. Issue the long-TTL one-time-access token that will live in the
	//    sealed recovery bundle.
	token, err := g.Client.CreateOneTimeAccessToken(ctx, user.ID, tokenTTL)
	if err != nil {
		return nil, fmt.Errorf("break-glass: create setup token: %w", err)
	}

	return &BreakGlassCredential{
		Username:   username,
		SetupToken: token,
		SetupURL:   fmt.Sprintf("%s/setup-account?token=%s", g.PocketIDURL, token),
		Group:      groupName,
		UserID:     user.ID,
	}, nil
}
