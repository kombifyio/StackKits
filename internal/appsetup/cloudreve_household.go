package appsetup

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
)

const cloudreveHouseholdGroupID = 2

// CloudreveHouseholdRequest binds one approved PocketID household identity to
// a local Cloudreve account. The owner and household passwords remain in
// memory and are never copied into the result or error fields.
type CloudreveHouseholdRequest struct {
	OwnerEmail      string
	OwnerPassword   string
	Email           string
	Nickname        string
	Password        string
	ExpectedVersion string
}

// CloudreveHouseholdResult is the credential-free account readback.
type CloudreveHouseholdResult struct {
	UserID    string `json:"userId"`
	Email     string `json:"email"`
	Nickname  string `json:"nickname"`
	GroupID   int    `json:"groupId"`
	GroupName string `json:"groupName"`
	Created   bool   `json:"created"`
}

type cloudreveAdminGroup struct {
	ID          int             `json:"id"`
	Name        string          `json:"name"`
	Permissions json.RawMessage `json:"permissions"`
}

type cloudreveAdminUser struct {
	ID         int    `json:"id"`
	Email      string `json:"email"`
	Nickname   string `json:"nick"`
	Status     string `json:"status"`
	GroupUsers int    `json:"group_users"`
	Edges      struct {
		Group *cloudreveAdminGroup `json:"group"`
	} `json:"edges"`
}

type cloudreveAdminUserList struct {
	Users []cloudreveAdminUser `json:"users"`
}

// EnsureCloudreveHouseholdUser creates or verifies one ordinary Files account
// through Cloudreve 4.18's admin API. Existing accounts are adopted only when
// the supplied owner-custodied password authenticates the exact requested
// identity and the admin readback still proves the non-admin User group.
func EnsureCloudreveHouseholdUser(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	request CloudreveHouseholdRequest,
) (result CloudreveHouseholdResult, returnErr error) {
	email := strings.TrimSpace(request.Email)
	nickname := strings.TrimSpace(request.Nickname)
	if email == "" || nickname == "" || strings.TrimSpace(request.Password) == "" {
		return CloudreveHouseholdResult{}, skerrors.NewValidationError(
			"setup_cloudreve_household_identity_invalid",
			"Cloudreve household provisioning requires an email, nickname, and owner-custodied password",
		)
	}
	if client == nil {
		client = NewCloudreveHTTPClient()
		defer client.CloseIdleConnections()
	}

	owner, ownerErr := BootstrapCloudreveOwner(ctx, client, baseURL, CloudreveOwnerRequest{
		Email: request.OwnerEmail, Password: request.OwnerPassword,
		ExpectedVersion: request.ExpectedVersion, AllowFirstOwnerRegistration: false,
	})
	if ownerErr != nil {
		return CloudreveHouseholdResult{}, ownerErr
	}
	defer func() {
		if cleanupErr := owner.Cleanup(context.Background(), client, baseURL); cleanupErr != nil {
			result = CloudreveHouseholdResult{}
			returnErr = joinCloudreveHouseholdError(returnErr, cleanupErr)
		}
	}()
	token := owner.AccessTokenForHandoff()

	groupData, apiErr := CloudreveJSON(ctx, client, http.MethodGet, baseURL, "/admin/group/2", token, nil)
	if apiErr != nil {
		return CloudreveHouseholdResult{}, cloudreveOwnerDependencyError(
			"cloudreve_household_group_readback_failed", "failed to read the Cloudreve household group", "GET /admin/group/2", apiErr,
		)
	}
	var group cloudreveAdminGroup
	if json.Unmarshal(groupData, &group) != nil || !cloudreveOrdinaryGroup(group) {
		return CloudreveHouseholdResult{}, skerrors.NewAuthError(
			"cloudreve_household_group_privileged",
			"Cloudreve's ordinary user group is missing or has administrator privileges",
		)
	}

	users, listErr := listCloudreveAdminUsers(ctx, client, baseURL, token, "user_email", email)
	if listErr != nil {
		return CloudreveHouseholdResult{}, listErr
	}
	exact := exactCloudreveAdminUsers(users, func(candidate cloudreveAdminUser) bool {
		return strings.EqualFold(strings.TrimSpace(candidate.Email), email)
	})
	if len(exact) > 1 {
		return CloudreveHouseholdResult{}, cloudreveHouseholdConflict()
	}

	created := false
	var account cloudreveAdminUser
	if len(exact) == 1 {
		account = exact[0]
	} else {
		nickUsers, nickErr := listCloudreveAdminUsers(ctx, client, baseURL, token, "user_nick", nickname)
		if nickErr != nil {
			return CloudreveHouseholdResult{}, nickErr
		}
		if len(exactCloudreveAdminUsers(nickUsers, func(candidate cloudreveAdminUser) bool {
			return strings.TrimSpace(candidate.Nickname) == nickname && !strings.EqualFold(strings.TrimSpace(candidate.Email), email)
		})) != 0 {
			return CloudreveHouseholdResult{}, cloudreveHouseholdConflict()
		}
		payload := map[string]any{
			"user": map[string]any{
				"email": email, "nick": nickname, "status": "active", "group_users": cloudreveHouseholdGroupID,
			},
			"password": request.Password,
		}
		createdData, createErr := CloudreveJSON(ctx, client, http.MethodPut, baseURL, "/admin/user", token, payload)
		if createErr != nil {
			return CloudreveHouseholdResult{}, cloudreveOwnerDependencyError(
				"cloudreve_household_create_failed", "failed to create the Cloudreve household account", "PUT /admin/user", createErr,
			)
		}
		if json.Unmarshal(createdData, &account) != nil || account.ID <= 0 {
			return CloudreveHouseholdResult{}, skerrors.NewDependencyError(
				"cloudreve_household_create_readback_invalid", "Cloudreve did not return the created household account",
			)
		}
		created = true
	}

	readbackData, readbackErr := CloudreveJSON(ctx, client, http.MethodGet, baseURL, "/admin/user/"+strconv.Itoa(account.ID), token, nil)
	if readbackErr != nil {
		return CloudreveHouseholdResult{}, cloudreveOwnerDependencyError(
			"cloudreve_household_readback_failed", "failed to read back the Cloudreve household account", "GET /admin/user/:id", readbackErr,
		)
	}
	if json.Unmarshal(readbackData, &account) != nil || !cloudreveHouseholdIdentityMatches(account, email, nickname) {
		return CloudreveHouseholdResult{}, cloudreveHouseholdConflict()
	}

	_, login, loginErr := CloudreveLogin(ctx, client, baseURL, email, request.Password)
	defer func() {
		login.Token.AccessToken = ""
		login.Token.RefreshToken = ""
	}()
	if loginErr != nil || !strings.EqualFold(strings.TrimSpace(login.User.Email), email) ||
		strings.TrimSpace(login.Token.AccessToken) == "" || strings.TrimSpace(login.Token.RefreshToken) == "" {
		return CloudreveHouseholdResult{}, skerrors.NewAuthError(
			"cloudreve_household_login_failed", "Cloudreve did not authenticate the exact owner-custodied household account",
		)
	}
	if cleanupErr := LogoutCloudreveSession(context.Background(), client, baseURL, login.Token.RefreshToken); cleanupErr != nil {
		return CloudreveHouseholdResult{}, cleanupErr
	}

	return CloudreveHouseholdResult{
		UserID: strconv.Itoa(account.ID), Email: strings.TrimSpace(account.Email), Nickname: strings.TrimSpace(account.Nickname),
		GroupID: account.GroupUsers, GroupName: strings.TrimSpace(account.Edges.Group.Name), Created: created,
	}, nil
}

func listCloudreveAdminUsers(ctx context.Context, client *http.Client, baseURL, token, condition, value string) ([]cloudreveAdminUser, *skerrors.StackKitError) {
	payload := map[string]any{
		"page": 1, "page_size": 100, "order_by": "id", "order_direction": "asc",
		"conditions": map[string]string{condition: value}, "searches": map[string]string{},
	}
	data, apiErr := CloudreveJSON(ctx, client, http.MethodPost, baseURL, "/admin/user", token, payload)
	if apiErr != nil {
		return nil, cloudreveOwnerDependencyError(
			"cloudreve_household_lookup_failed", "failed to look up the Cloudreve household account", "POST /admin/user", apiErr,
		)
	}
	var list cloudreveAdminUserList
	if json.Unmarshal(data, &list) != nil {
		return nil, skerrors.NewDependencyError(
			"cloudreve_household_lookup_invalid", "Cloudreve household account lookup returned an invalid result",
		)
	}
	return list.Users, nil
}

func exactCloudreveAdminUsers(users []cloudreveAdminUser, keep func(cloudreveAdminUser) bool) []cloudreveAdminUser {
	result := make([]cloudreveAdminUser, 0, 1)
	for _, user := range users {
		if keep(user) {
			result = append(result, user)
		}
	}
	return result
}

func cloudreveOrdinaryGroup(group cloudreveAdminGroup) bool {
	if group.ID != cloudreveHouseholdGroupID || strings.TrimSpace(group.Name) == "" {
		return false
	}
	var encoded string
	if json.Unmarshal(group.Permissions, &encoded) != nil {
		return false
	}
	permissions, err := base64.StdEncoding.DecodeString(encoded)
	return err == nil && (len(permissions) == 0 || permissions[0]&1 == 0)
}

func cloudreveHouseholdIdentityMatches(account cloudreveAdminUser, email, nickname string) bool {
	return account.ID > 1 && strings.EqualFold(strings.TrimSpace(account.Email), email) &&
		strings.TrimSpace(account.Nickname) == nickname && account.Status == "active" &&
		account.GroupUsers == cloudreveHouseholdGroupID && account.Edges.Group != nil && cloudreveOrdinaryGroup(*account.Edges.Group)
}

func cloudreveHouseholdConflict() *skerrors.StackKitError {
	return skerrors.NewAuthError(
		"cloudreve_household_identity_conflict",
		"Cloudreve already contains a conflicting or privileged Files identity; refusing to adopt or change it",
	)
}

func joinCloudreveHouseholdError(left error, right *skerrors.StackKitError) error {
	if left == nil {
		return right
	}
	return skerrors.NewDependencyError(
		"cloudreve_household_session_cleanup_failed", "Cloudreve household provisioning failed and its temporary session cleanup also failed",
		skerrors.WithCause(errors.Join(left, right)),
	)
}

// CloudreveHouseholdCredentialRef is a stable opaque locator for the account
// password. Raw usernames and email addresses never enter a custody path.
func CloudreveHouseholdCredentialRef(username, email string) (string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	email = strings.ToLower(strings.TrimSpace(email))
	if username == "" || email == "" || strings.ContainsAny(username, "/?#") {
		return "", fmt.Errorf("Cloudreve household credential identity is invalid")
	}
	digest := sha256String(username + "\x00" + email)
	return "secret://workloads/files/household/" + digest + "/password", nil
}

func sha256String(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:])
}
