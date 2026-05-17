// Package identity provisions PocketID owner and break-glass accounts.
//
// PocketID v2 is passkey-only: there is no password field on user creation.
// The owner-activation flow is therefore three calls:
//
//  1. POST /api/users               -- create the user record (no password).
//  2. PUT  /api/users/:id/user-groups -- add to the "owners" group.
//  3. POST /api/users/:id/one-time-access-token -- issue an enrollment token.
//
// The token is rendered into a setup URL of the form
//
//	https://id.<domain>/setup-account?token=<token>
//
// which the owner clicks once to register a WebAuthn credential. The token
// is single-use and consumed by PocketID on first redemption.
//
// The local provisioner only supports Source=="local" (a daily-admin owner
// provisioned locally on the first node). Source=="cloud" is orchestrator
// managed by TechStack/kombify Cloud and must not call this local provisioner.
package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/kombifyio/stackkits/internal/pocketid"
)

// PocketIDClient is the subset of pocketid.Client used by provisioners.
// Defined as an interface so tests can supply fakes without standing up an
// HTTP server.
type PocketIDClient interface {
	CreateUser(ctx context.Context, req pocketid.CreateUserRequest) (*pocketid.User, error)
	AddUserToGroup(ctx context.Context, userID, groupID string) error
	GetGroupIDByName(ctx context.Context, groupName string) (string, error)
	CreateOneTimeAccessToken(ctx context.Context, userID string, ttl time.Duration) (string, error)
}

// OwnerSpec describes the daily-admin owner of a homelab instance.
type OwnerSpec struct {
	// Source controls the provisioning path. This local provisioner supports
	// "local" only; "cloud" is handled by TechStack/kombify Cloud.
	Source string

	// Email is the owner's address (also used as the WebAuthn account label).
	Email string

	// Username is the PocketID login handle.
	Username string

	// DisplayName is what PocketID renders in the UI; defaults to Username
	// when empty. PocketID v2 stores this in the FirstName column on the
	// underlying user record.
	DisplayName string

	// ForeignSubjectID is the external IdP subject for Source=="cloud".
	// Ignored when Source=="local".
	ForeignSubjectID string
}

// ProvisionResult is what Provision returns on success.
type ProvisionResult struct {
	// UserID is the PocketID-assigned UUID for the new owner record.
	UserID string

	// SetupURL is the one-time link the owner clicks to enroll a WebAuthn
	// credential. It embeds a single-use token with a 24-hour TTL. Local
	// source only.
	SetupURL string
}

// OwnerProvisioner creates the owner record in PocketID and adds them to
// the owners group, then issues a one-time-access token for WebAuthn
// enrollment.
type OwnerProvisioner struct {
	// Client is the PocketID admin-API client (real or fake).
	Client PocketIDClient

	// PocketIDURL is the public origin of the PocketID instance, used to
	// build SetupURL. Example: "https://id.example.com" (no trailing slash).
	PocketIDURL string

	// OwnersGroup is the name of the group new owners are added to.
	// Defaults to "owners" when empty.
	OwnersGroup string
}

// setupTokenTTL is how long the WebAuthn-enrollment token is valid. 24 hours
// gives the owner enough time to complete enrollment while keeping the
// blast radius small if the URL leaks.
const setupTokenTTL = 24 * time.Hour

// Provision creates the owner user in PocketID, adds them to the owners
// group, and returns a ProvisionResult containing the user's UUID and a
// setup URL the owner clicks once to enroll a WebAuthn credential.
//
// Source=="local" is the only locally provisioned path. Source=="cloud"
// returns an orchestrator-managed error. Any other Source value is rejected as
// invalid.
func (p *OwnerProvisioner) Provision(ctx context.Context, spec OwnerSpec) (*ProvisionResult, error) {
	if spec.Source == "cloud" {
		return nil, fmt.Errorf("cloud-source owner is orchestrator-managed; local provisioner only supports source local")
	}
	if spec.Source != "local" {
		return nil, fmt.Errorf("invalid owner source %q", spec.Source)
	}
	if spec.Email == "" || spec.Username == "" {
		return nil, fmt.Errorf("owner spec incomplete: need email + username")
	}

	displayName := spec.DisplayName
	if displayName == "" {
		displayName = spec.Username
	}

	// 1. Create the user record. PocketID v2 has no password field; the
	//    account is unusable until the owner enrolls a WebAuthn credential
	//    via the one-time-access token below.
	user, err := p.Client.CreateUser(ctx, pocketid.CreateUserRequest{
		Username:  spec.Username,
		Email:     spec.Email,
		FirstName: displayName,
		IsAdmin:   true,
	})
	if err != nil {
		return nil, fmt.Errorf("create owner user: %w", err)
	}

	// 2. Resolve the owners group name to its UUID, then add the user.
	//    PocketID's group API is keyed by ID, not name.
	groupName := p.OwnersGroup
	if groupName == "" {
		groupName = "owners"
	}
	groupID, err := p.Client.GetGroupIDByName(ctx, groupName)
	if err != nil {
		return nil, fmt.Errorf("lookup %s group: %w", groupName, err)
	}
	if groupID == "" {
		return nil, fmt.Errorf("lookup %s group: no group by that name exists", groupName)
	}
	if err := p.Client.AddUserToGroup(ctx, user.ID, groupID); err != nil {
		return nil, fmt.Errorf("add owner to %s: %w", groupName, err)
	}

	// 3. Issue a one-time-access token so the owner can enroll a WebAuthn
	//    credential. The token is single-use and expires after setupTokenTTL.
	token, err := p.Client.CreateOneTimeAccessToken(ctx, user.ID, setupTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("create owner setup token: %w", err)
	}

	setupURL := fmt.Sprintf("%s/setup-account?token=%s", p.PocketIDURL, token)
	return &ProvisionResult{UserID: user.ID, SetupURL: setupURL}, nil
}
