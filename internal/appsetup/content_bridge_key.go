package appsetup

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kombifyio/stackkits/internal/contentbridge"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// IssueContentBridgeImmichKey issues the kombify Content Bridge's Immich key
// and custodies it on this node. The key carries only the three statistics
// permissions of contentbridge.ImmichPermissions, so it can count assets,
// albums and memories and can neither list, open nor change anything. Issuing
// again replaces the earlier key, which keeps one bridge key per Immich.
//
// The key is custodied as an owner-signed issued secret under
// contentbridge.ImmichKeySecretRef and reaches the bridge container as a file
// on the next apply. It never reaches Techstack, the Gateway, Cloud or a
// client, and the returned result holds no secret.
func IssueContentBridgeImmichKey(ctx context.Context, client *http.Client, baseURL, email, password, workspace string) (ImmichAPIKeyResult, error) {
	issued, issueErr := IssueImmichAddOnAPIKey(ctx, client, baseURL, email, password, contentbridge.ImmichKeyName, contentbridge.ImmichPermissions())
	if issueErr != nil {
		return ImmichAPIKeyResult{}, issueErr
	}
	secret := []byte(issued.Secret)
	defer clear(secret)
	if err := localevidence.StoreLocalIssuedSecret(workspace, contentbridge.ImmichKeySecretRef(), secret); err != nil {
		return ImmichAPIKeyResult{}, fmt.Errorf("custody the content bridge Immich key: %w", err)
	}
	issued.Secret = ""
	return issued, nil
}
