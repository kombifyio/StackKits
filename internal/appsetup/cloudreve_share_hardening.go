package appsetup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
)

// Cloudreve share links currently serve more than the shared item
// (kombify-StackKits-fusz; present in the pinned 4.18.0 and in 4.19.1, no
// upstream fix yet). The affected requests carry the share URI in both query
// strings and JSON bodies, so no ingress rule can filter them reliably.
//
// Cloudreve refuses every share-link resolution for a viewer who is not the
// share owner unless the viewer's group holds the ShareDownload permission.
// Clearing that bit on every non-admin group (the ordinary "User" group, the
// "Anonymous" group and any custom group) closes the exposure for household
// members and anonymous visitors alike. Owners keep full access to their own
// files; administrators already reach every file through the admin file
// manager. Share links therefore stop opening for other viewers until a fixed
// Cloudreve release is pinned.
const (
	cloudreveGroupPermissionIsAdmin       = 0
	cloudreveGroupPermissionShareDownload = 7
	cloudreveGroupListPageSize            = 100
	cloudreveGroupListMaxPages            = 20
)

type cloudreveAdminGroupList struct {
	Groups []cloudreveAdminGroup `json:"groups"`
}

// restrictCloudreveShareLinkAccess removes the ShareDownload permission from
// every non-admin Cloudreve group through the admin API. It is idempotent:
// groups that already lack the permission are not written.
func restrictCloudreveShareLinkAccess(ctx context.Context, client *http.Client, baseURL, token string) *skerrors.StackKitError {
	groups, listErr := listCloudreveAdminGroups(ctx, client, baseURL, token)
	if listErr != nil {
		return listErr
	}
	for _, group := range groups {
		permissions, ok := decodeCloudreveGroupPermissions(group.Permissions)
		if !ok || group.ID <= 0 {
			return cloudreveShareHardeningInvalid("Cloudreve group list returned an unreadable group")
		}
		if cloudrevePermissionEnabled(permissions, cloudreveGroupPermissionIsAdmin) ||
			!cloudrevePermissionEnabled(permissions, cloudreveGroupPermissionShareDownload) {
			continue
		}
		if err := clearCloudreveGroupShareDownload(ctx, client, baseURL, token, group.ID); err != nil {
			return err
		}
	}
	return nil
}

func listCloudreveAdminGroups(ctx context.Context, client *http.Client, baseURL, token string) ([]cloudreveAdminGroup, *skerrors.StackKitError) {
	var groups []cloudreveAdminGroup
	for page := 1; page <= cloudreveGroupListMaxPages; page++ {
		payload := map[string]any{
			"page": page, "page_size": cloudreveGroupListPageSize, "order_by": "id", "order_direction": "asc",
			"conditions": map[string]string{}, "searches": map[string]string{},
		}
		data, apiErr := CloudreveJSON(ctx, client, http.MethodPost, baseURL, "/admin/group", token, payload)
		if apiErr != nil {
			return nil, cloudreveOwnerDependencyError(
				"cloudreve_share_hardening_group_list_failed", "failed to list Cloudreve groups for share-link hardening", "POST /admin/group", apiErr,
			)
		}
		var list cloudreveAdminGroupList
		if json.Unmarshal(data, &list) != nil {
			return nil, cloudreveShareHardeningInvalid("Cloudreve group list was invalid")
		}
		groups = append(groups, list.Groups...)
		if len(list.Groups) < cloudreveGroupListPageSize {
			return groups, nil
		}
	}
	return nil, cloudreveShareHardeningInvalid("Cloudreve group list exceeded the bounded page count")
}

// clearCloudreveGroupShareDownload reads the full group (including its
// storage-policy edge, which Cloudreve's update clears when omitted), flips
// only the ShareDownload bit and writes the same object back.
func clearCloudreveGroupShareDownload(ctx context.Context, client *http.Client, baseURL, token string, id int) *skerrors.StackKitError {
	path := "/admin/group/" + strconv.Itoa(id)
	data, apiErr := CloudreveJSON(ctx, client, http.MethodGet, baseURL, path, token, nil)
	if apiErr != nil {
		return cloudreveOwnerDependencyError(
			"cloudreve_share_hardening_group_read_failed", "failed to read a Cloudreve group for share-link hardening", "GET /admin/group/:id", apiErr,
		)
	}
	var group map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&group) != nil {
		return cloudreveShareHardeningInvalid("Cloudreve group readback was invalid")
	}
	var readID int
	if json.Unmarshal(group["id"], &readID) != nil || readID != id {
		return cloudreveShareHardeningInvalid("Cloudreve group readback changed identity")
	}
	permissions, ok := decodeCloudreveGroupPermissions(group["permissions"])
	if !ok {
		return cloudreveShareHardeningInvalid("Cloudreve group permissions were unreadable")
	}
	if cloudrevePermissionEnabled(permissions, cloudreveGroupPermissionIsAdmin) ||
		!cloudrevePermissionEnabled(permissions, cloudreveGroupPermissionShareDownload) {
		return nil
	}
	permissions[cloudreveGroupPermissionShareDownload/8] &^= 1 << (cloudreveGroupPermissionShareDownload % 8)
	encoded, _ := json.Marshal(base64.StdEncoding.EncodeToString(permissions))
	group["permissions"] = encoded
	delete(group, "total_users")

	updated, apiErr := CloudreveJSON(ctx, client, http.MethodPut, baseURL, path, token, map[string]any{"group": group})
	if apiErr != nil {
		return cloudreveOwnerDependencyError(
			"cloudreve_share_hardening_group_update_failed", "failed to restrict Cloudreve share-link access", "PUT /admin/group/:id", apiErr,
		)
	}
	var readback cloudreveAdminGroup
	if json.Unmarshal(updated, &readback) != nil || readback.ID != id {
		return cloudreveShareHardeningInvalid("Cloudreve group update readback was invalid")
	}
	after, ok := decodeCloudreveGroupPermissions(readback.Permissions)
	if !ok || !bytes.Equal(bytes.TrimRight(after, "\x00"), bytes.TrimRight(permissions, "\x00")) {
		return cloudreveShareHardeningInvalid("Cloudreve group update did not converge to the restricted permissions")
	}
	return nil
}

func decodeCloudreveGroupPermissions(raw json.RawMessage) ([]byte, bool) {
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil {
		return nil, false
	}
	permissions, err := base64.StdEncoding.DecodeString(encoded)
	return permissions, err == nil
}

func cloudrevePermissionEnabled(permissions []byte, flag int) bool {
	return flag/8 < len(permissions) && permissions[flag/8]&(1<<(flag%8)) != 0
}

func cloudreveShareHardeningInvalid(message string) *skerrors.StackKitError {
	return skerrors.NewDependencyError("cloudreve_share_hardening_invalid", message)
}
