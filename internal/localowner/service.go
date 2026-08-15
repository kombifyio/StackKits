// Package localowner realizes and verifies the init-owned human identity in
// the local PocketID runtime without exposing its bootstrap credential.
package localowner

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

const (
	pocketIDLocalAPI       = "http://127.0.0.1:1411"
	tinyAuthClientName     = "StackKits TinyAuth"
	ownerEnrollmentTTL     = 24 * time.Hour
	pocketIDHealthTimeout  = 2 * time.Minute
	ownerEnrollmentRelPath = ".stackkit/custody/pocketid-owner-enrollment.json"
)

type pocketIDOwnerClient interface {
	WaitHealthy(context.Context, time.Duration) error
	BootstrapInitialAdmin(context.Context, string, string, string) (string, error)
	GetGroupIDByName(context.Context, string) (string, error)
	CreateUserGroup(context.Context, pocketid.CreateUserGroupRequest) (*pocketid.UserGroup, error)
	FindUsersByUsername(context.Context, string) ([]pocketid.User, error)
	CreateUser(context.Context, pocketid.CreateUserRequest) (*pocketid.User, error)
	GetUser(context.Context, string) (*pocketid.User, error)
	UpdateUserGroups(context.Context, string, []string) (*pocketid.User, error)
	CreateOneTimeAccessToken(context.Context, string, time.Duration) (string, error)
	GetOIDCClient(context.Context, string) (*pocketid.OIDCClient, error)
	RegisterOIDCClient(context.Context, pocketid.RegisterClientRequest) (*pocketid.OIDCClient, error)
	CreateOIDCClientSecret(context.Context, string) (string, error)
	UpdateOIDCClientAllowedUserGroups(context.Context, string, []string) (*pocketid.OIDCClient, error)
}

type Service struct {
	workspaceRoot string
	client        pocketIDOwnerClient
	now           func() time.Time
}

type Result struct {
	Binding        localevidence.OwnerRuntimeBinding
	EnrollmentPath string
}

func NewService(workspaceRoot string) (*Service, error) {
	absolute, err := filepath.Abs(workspaceRoot)
	if err != nil || strings.TrimSpace(workspaceRoot) == "" {
		return nil, errors.New("localowner: workspace root is required")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("localowner: workspace root must be an existing plain directory")
	}
	return &Service{workspaceRoot: filepath.Clean(absolute), now: time.Now}, nil
}

func newServiceForTest(workspaceRoot string, client pocketIDOwnerClient, now time.Time) *Service {
	return &Service{workspaceRoot: workspaceRoot, client: client, now: func() time.Time { return now }}
}

// errPocketIDRuntimeLost marks the one divergence a rollout may repair on its
// own: PocketID no longer holds a resource this workspace recorded, because the
// host's containers and volumes were removed underneath it. A resource that is
// present but different is never this error — that is a genuine conflict, and
// overwriting it would take an identity the workspace does not own.
var errPocketIDRuntimeLost = errors.New("localowner: PocketID no longer holds the recorded runtime identity")

// Realize creates the exact desired PocketID owner once, persists a private
// one-time passkey-enrollment URL, and records a secret-free signed binding to
// ownerRef and the step-ca certificate chain.
//
// A reset host answers as an empty PocketID: reachable, unbootstrapped, and
// without the subject, groups, or client the workspace recorded. Realize treats
// that as work to redo rather than as a reason to refuse, so re-running an
// install rebuilds the owner from the custody that survived on disk.
func (s *Service) Realize(ctx context.Context) (Result, error) {
	owner, client, err := s.ready(ctx)
	if err != nil {
		return Result{}, err
	}
	// Admission runs before any recorded state is read: a rebuilt PocketID needs
	// its initial admin accepted again before the owner can be recreated.
	if err := verifyPocketIDAdmin(ctx, client); err != nil {
		return Result{}, err
	}
	recorded, loadErr := localevidence.LoadOwnerRuntimeBinding(s.workspaceRoot)
	switch {
	case loadErr == nil:
		result, realizeErr := s.realizeBoundOwner(ctx, client, owner, recorded)
		if realizeErr == nil {
			return result, nil
		}
		if !errors.Is(realizeErr, errPocketIDRuntimeLost) {
			return Result{}, realizeErr
		}
		if err := localevidence.DiscardOwnerRuntimeBinding(s.workspaceRoot); err != nil {
			return Result{}, err
		}
	case !errors.Is(loadErr, localevidence.ErrOwnerRuntimeBindingMissing):
		return Result{}, loadErr
	}
	users, err := client.FindUsersByUsername(ctx, owner.PocketID.Username)
	if err != nil {
		return Result{}, errors.New("localowner: PocketID owner lookup failed")
	}
	if len(users) > 1 {
		return Result{}, errors.New("localowner: PocketID contains more than one exact owner username")
	}
	if len(users) == 1 {
		if err := validatePocketIDOwnerFields(owner, users[0]); err != nil {
			return Result{}, err
		}
	}
	groupIDs, err := ensureRequiredGroups(ctx, client)
	if err != nil {
		return Result{}, err
	}
	var subject string
	if len(users) == 0 {
		created, createErr := client.CreateUser(ctx, pocketid.CreateUserRequest{
			Username: owner.PocketID.Username, Email: owner.PocketID.Email,
			FirstName: owner.PocketID.DisplayName, DisplayName: owner.PocketID.DisplayName,
			IsAdmin: true, UserGroupIDs: append([]string(nil), groupIDs...),
		})
		if createErr != nil || created == nil || strings.TrimSpace(created.ID) == "" {
			return Result{}, errors.New("localowner: PocketID owner creation failed")
		}
		subject = created.ID
	} else {
		subject = users[0].ID
	}
	readback, err := client.GetUser(ctx, subject)
	if err != nil || readback == nil {
		return Result{}, errors.New("localowner: PocketID owner readback failed")
	}
	if err := validatePocketIDOwnerFields(owner, *readback); err != nil {
		return Result{}, err
	}
	if !hasRequiredGroups(readback.UserGroups) {
		completeGroupIDs := requiredAndExistingGroupIDs(groupIDs, readback.UserGroups)
		readback, err = client.UpdateUserGroups(ctx, subject, completeGroupIDs)
		if err != nil || readback == nil {
			return Result{}, errors.New("localowner: PocketID owner group binding failed")
		}
	}
	if err := validatePocketIDOwner(owner, *readback); err != nil {
		return Result{}, err
	}
	if err := s.ensureTinyAuthPocketIDBinding(ctx, client, owner, groupIDs); err != nil {
		return Result{}, err
	}
	token, err := client.CreateOneTimeAccessToken(ctx, subject, ownerEnrollmentTTL)
	if err != nil || strings.TrimSpace(token) == "" {
		return Result{}, errors.New("localowner: PocketID owner enrollment creation failed")
	}
	expiresAt := s.now().UTC().Add(ownerEnrollmentTTL).Truncate(time.Second)
	runtimeCustody, err := localevidence.LoadBasementRuntimeCustody(s.workspaceRoot)
	if err != nil {
		return Result{}, err
	}
	enrollmentPath, err := localevidence.PersistPocketIDOwnerEnrollment(
		s.workspaceRoot,
		localevidence.PocketIDOwnerEnrollment{
			OwnerRef: owner.OwnerRef, PocketIDSubject: subject,
			SetupURL:  "http://id." + runtimeCustody.Domain + "/setup-account?token=" + url.QueryEscape(token),
			ExpiresAt: expiresAt,
		},
	)
	if err != nil {
		return Result{}, err
	}
	binding, err := localevidence.EstablishOwnerRuntimeBinding(
		s.workspaceRoot,
		localevidence.OwnerRuntimeObservation{
			PocketIDSubject: subject, Username: readback.Username,
			Email: readback.Email, DisplayName: effectiveDisplayName(*readback),
			Groups: pocketIDGroupNames(readback.UserGroups),
		},
	)
	if err != nil {
		return Result{}, err
	}
	return Result{Binding: binding, EnrollmentPath: enrollmentPath}, nil
}

// realizeBoundOwner confirms that the runtime still carries the exact owner,
// groups, and TinyAuth client this workspace already recorded. It reports
// errPocketIDRuntimeLost when the runtime simply no longer has them, which the
// caller repairs by rebuilding rather than by refusing the rollout.
func (s *Service) realizeBoundOwner(
	ctx context.Context,
	client pocketIDOwnerClient,
	owner localevidence.OwnerCustody,
	binding localevidence.OwnerRuntimeBinding,
) (Result, error) {
	if err := s.verifyBoundOwner(ctx, client, owner, binding); err != nil {
		return Result{}, err
	}
	groupIDs, err := exactRequiredGroupIDs(ctx, client)
	if err != nil {
		return Result{}, err
	}
	if err := s.ensureTinyAuthPocketIDBinding(ctx, client, owner, groupIDs); err != nil {
		return Result{}, err
	}
	return Result{
		Binding:        binding,
		EnrollmentPath: filepath.Join(s.workspaceRoot, filepath.FromSlash(ownerEnrollmentRelPath)),
	}, nil
}

// Verify performs only authenticated readback. It verifies custody and the
// signed binding before any network request and never creates or updates a
// PocketID resource.
func (s *Service) Verify(ctx context.Context) (localevidence.OwnerRuntimeBinding, error) {
	owner, client, err := s.ready(ctx)
	if err != nil {
		return localevidence.OwnerRuntimeBinding{}, err
	}
	binding, err := localevidence.LoadOwnerRuntimeBinding(s.workspaceRoot)
	if err != nil {
		return localevidence.OwnerRuntimeBinding{}, err
	}
	if err := verifyPocketIDAdmin(ctx, client); err != nil {
		return localevidence.OwnerRuntimeBinding{}, err
	}
	if err := s.verifyBoundOwner(ctx, client, owner, binding); err != nil {
		return localevidence.OwnerRuntimeBinding{}, err
	}
	groupIDs, err := exactRequiredGroupIDs(ctx, client)
	if err != nil {
		return localevidence.OwnerRuntimeBinding{}, err
	}
	if err := s.verifyTinyAuthPocketIDBinding(ctx, client, owner, groupIDs); err != nil {
		return localevidence.OwnerRuntimeBinding{}, err
	}
	return binding, nil
}

func (s *Service) ensureTinyAuthPocketIDBinding(
	ctx context.Context,
	client pocketIDOwnerClient,
	owner localevidence.OwnerCustody,
	groupIDs []string,
) error {
	_, loadErr := localevidence.LoadBasementTinyAuthPocketIDBinding(s.workspaceRoot)
	switch {
	case loadErr == nil:
		verifyErr := s.verifyTinyAuthPocketIDBinding(ctx, client, owner, groupIDs)
		if verifyErr == nil {
			return nil
		}
		if !errors.Is(verifyErr, errPocketIDRuntimeLost) {
			return verifyErr
		}
		// The recorded secret only authenticates against the client that issued
		// it, so a runtime that lost the client makes this custody unusable.
		if err := localevidence.DiscardBasementTinyAuthPocketIDBinding(s.workspaceRoot); err != nil {
			return err
		}
	case !errors.Is(loadErr, localevidence.ErrTinyAuthPocketIDBindingMissing):
		return loadErr
	}
	runtimeCustody, err := localevidence.LoadBasementRuntimeCustody(s.workspaceRoot)
	if err != nil {
		return err
	}
	callbackURL := localevidence.TinyAuthPocketIDCallbackURL(runtimeCustody.Domain)
	oidcClient, err := client.GetOIDCClient(ctx, localevidence.TinyAuthPocketIDClientID)
	var secret string
	if errors.Is(err, pocketid.ErrNotFound) {
		oidcClient, err = client.RegisterOIDCClient(ctx, pocketid.RegisterClientRequest{
			ID: localevidence.TinyAuthPocketIDClientID, Name: tinyAuthClientName,
			CallbackURLs: []string{callbackURL},
			IsPublic:     false, IsGroupRestricted: true,
		})
		if err == nil && oidcClient != nil {
			secret = oidcClient.Secret
		}
	} else if err == nil {
		secret, err = client.CreateOIDCClientSecret(ctx, localevidence.TinyAuthPocketIDClientID)
	}
	if err != nil || oidcClient == nil || strings.TrimSpace(secret) == "" {
		return errors.New("localowner: PocketID TinyAuth client registration failed")
	}
	if _, err := client.UpdateOIDCClientAllowedUserGroups(
		ctx, localevidence.TinyAuthPocketIDClientID, append([]string(nil), groupIDs...),
	); err != nil {
		return errors.New("localowner: PocketID TinyAuth group binding failed")
	}
	if _, err := localevidence.BindBasementTinyAuthPocketID(
		s.workspaceRoot,
		localevidence.TinyAuthPocketIDBindingRequest{
			ClientID:     localevidence.TinyAuthPocketIDClientID,
			ClientSecret: secret, GroupIDs: append([]string(nil), groupIDs...),
		},
	); err != nil {
		return err
	}
	return s.verifyTinyAuthPocketIDBinding(ctx, client, owner, groupIDs)
}

func (s *Service) verifyTinyAuthPocketIDBinding(
	ctx context.Context,
	client pocketIDOwnerClient,
	owner localevidence.OwnerCustody,
	groupIDs []string,
) error {
	binding, err := localevidence.LoadBasementTinyAuthPocketIDBinding(s.workspaceRoot)
	if err != nil {
		return err
	}
	runtimeCustody, err := localevidence.LoadBasementRuntimeCustody(s.workspaceRoot)
	if err != nil {
		return err
	}
	oidcClient, err := client.GetOIDCClient(ctx, binding.ClientID)
	if errors.Is(err, pocketid.ErrNotFound) {
		return errPocketIDRuntimeLost
	}
	if err != nil || oidcClient == nil ||
		oidcClient.ID != localevidence.TinyAuthPocketIDClientID ||
		oidcClient.Name != tinyAuthClientName ||
		!slices.Equal(oidcClient.CallbackURLs, []string{localevidence.TinyAuthPocketIDCallbackURL(runtimeCustody.Domain)}) ||
		oidcClient.IsPublic || !oidcClient.IsGroupRestricted ||
		!samePocketIDGroupIDs(oidcClient.AllowedUserGroups, groupIDs) ||
		binding.OwnerRef != owner.OwnerRef ||
		!slices.Equal(binding.GroupIDs, sortedCopy(groupIDs)) {
		return errors.New("localowner: PocketID TinyAuth binding differs from local owner custody")
	}
	return nil
}

func exactRequiredGroupIDs(ctx context.Context, client pocketIDOwnerClient) ([]string, error) {
	result := make([]string, 0, 2)
	for _, name := range []string{"admins", "owners"} {
		id, err := client.GetGroupIDByName(ctx, name)
		if err != nil || strings.TrimSpace(id) == "" {
			return nil, errors.New("localowner: required PocketID group is missing")
		}
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

func samePocketIDGroupIDs(groups []pocketid.UserGroup, want []string) bool {
	got := make([]string, 0, len(groups))
	for _, group := range groups {
		got = append(got, group.ID)
	}
	sort.Strings(got)
	return slices.Equal(got, sortedCopy(want))
}

func sortedCopy(input []string) []string {
	result := append([]string(nil), input...)
	sort.Strings(result)
	return result
}

func (s *Service) ready(ctx context.Context) (localevidence.OwnerCustody, pocketIDOwnerClient, error) {
	if ctx == nil {
		return localevidence.OwnerCustody{}, nil, errors.New("localowner: context is required")
	}
	if err := ctx.Err(); err != nil {
		return localevidence.OwnerCustody{}, nil, err
	}
	if s == nil || s.workspaceRoot == "" || s.now == nil {
		return localevidence.OwnerCustody{}, nil, errors.New("localowner: service is not initialized")
	}
	owner, err := localevidence.LoadOwnerCustody(s.workspaceRoot)
	if err != nil {
		return localevidence.OwnerCustody{}, nil, err
	}
	key, err := localevidence.ReadBasementRuntimePocketIDAdminKey(s.workspaceRoot)
	if err != nil {
		return localevidence.OwnerCustody{}, nil, err
	}
	client := s.client
	if client == nil {
		client = pocketid.NewClient(pocketIDLocalAPI, key)
	}
	return owner, client, nil
}

func verifyPocketIDAdmin(ctx context.Context, client pocketIDOwnerClient) error {
	if err := client.WaitHealthy(ctx, pocketIDHealthTimeout); err != nil {
		return errors.New("localowner: PocketID is not healthy")
	}
	if _, err := client.BootstrapInitialAdmin(ctx, "", "", ""); err != nil &&
		!errors.Is(err, pocketid.ErrAlreadyBootstrapped) {
		return errors.New("localowner: PocketID rejected local bootstrap custody")
	}
	return nil
}

func ensureRequiredGroups(ctx context.Context, client pocketIDOwnerClient) ([]string, error) {
	required := []struct {
		name, friendly string
	}{{"admins", "Admins"}, {"owners", "Owners"}}
	ids := make([]string, 0, len(required))
	for _, group := range required {
		id, err := client.GetGroupIDByName(ctx, group.name)
		if err != nil {
			return nil, errors.New("localowner: PocketID group lookup failed")
		}
		if id == "" {
			created, createErr := client.CreateUserGroup(ctx, pocketid.CreateUserGroupRequest{
				Name: group.name, FriendlyName: group.friendly,
			})
			if errors.Is(createErr, pocketid.ErrAlreadyExists) {
				id, createErr = client.GetGroupIDByName(ctx, group.name)
			}
			if id != "" && createErr == nil {
				ids = append(ids, id)
				continue
			}
			if createErr != nil || created == nil || created.ID == "" || created.Name != group.name {
				return nil, errors.New("localowner: PocketID group creation failed")
			}
			id = created.ID
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Service) verifyBoundOwner(
	ctx context.Context,
	client pocketIDOwnerClient,
	owner localevidence.OwnerCustody,
	binding localevidence.OwnerRuntimeBinding,
) error {
	user, err := client.GetUser(ctx, binding.PocketIDSubject)
	if errors.Is(err, pocketid.ErrNotFound) {
		return errPocketIDRuntimeLost
	}
	if err != nil || user == nil || user.ID != binding.PocketIDSubject {
		return errors.New("localowner: bound PocketID subject readback failed")
	}
	if err := validatePocketIDOwner(owner, *user); err != nil {
		return err
	}
	if binding.PocketIDUsername != user.Username ||
		binding.PocketIDEmail != user.Email ||
		binding.PocketIDDisplayName != effectiveDisplayName(*user) {
		return errors.New("localowner: PocketID subject differs from signed owner runtime binding")
	}
	return nil
}

func validatePocketIDOwner(owner localevidence.OwnerCustody, user pocketid.User) error {
	if err := validatePocketIDOwnerFields(owner, user); err != nil {
		return err
	}
	if !hasRequiredGroups(user.UserGroups) {
		return errors.New("localowner: PocketID owner lacks required owner groups")
	}
	return nil
}

func validatePocketIDOwnerFields(owner localevidence.OwnerCustody, user pocketid.User) error {
	if strings.TrimSpace(user.ID) == "" ||
		user.Username != owner.PocketID.Username ||
		user.Email != owner.PocketID.Email ||
		effectiveDisplayName(user) != owner.PocketID.DisplayName ||
		!user.IsAdmin || user.Disabled {
		return errors.New("localowner: PocketID owner differs from desired local projection")
	}
	return nil
}

func effectiveDisplayName(user pocketid.User) string {
	if strings.TrimSpace(user.DisplayName) != "" {
		return strings.TrimSpace(user.DisplayName)
	}
	return strings.TrimSpace(user.FirstName)
}

func hasRequiredGroups(groups []pocketid.UserGroup) bool {
	names := pocketIDGroupNames(groups)
	return slices.Contains(names, "admins") && slices.Contains(names, "owners")
}

func pocketIDGroupNames(groups []pocketid.UserGroup) []string {
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		if strings.TrimSpace(group.Name) != "" {
			names = append(names, group.Name)
		}
	}
	sort.Strings(names)
	return slices.Compact(names)
}

func requiredAndExistingGroupIDs(required []string, existing []pocketid.UserGroup) []string {
	ids := append([]string(nil), required...)
	for _, group := range existing {
		if strings.TrimSpace(group.ID) != "" {
			ids = append(ids, group.ID)
		}
	}
	sort.Strings(ids)
	return slices.Compact(ids)
}

var _ pocketIDOwnerClient = (*pocketid.Client)(nil)
