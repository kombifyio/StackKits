package localowner

import (
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

// This error carries only fixed public field names. Neither API errors nor
// actual/expected values may enter its CLI, MCP or support-export contract.
type tinyAuthBindingError struct {
	detail actionableerror.Contract
}

func (e *tinyAuthBindingError) Error() string                             { return e.detail.Message }
func (e *tinyAuthBindingError) ActionableError() actionableerror.Contract { return e.detail }
func (e *tinyAuthBindingError) ActionableStatus() string                  { return "failed" }

func tinyAuthBindingDiagnostic(client *pocketid.OIDCClient, readErr error,
	binding localevidence.TinyAuthPocketIDBinding, owner localevidence.OwnerCustody,
	callback string, groupIDs []string, requirePKCE bool,
) error {
	if readErr != nil || client == nil {
		detail, _ := actionableerror.Diagnostic("pocketid_tinyauth_client_unreadable")
		return &tinyAuthBindingError{detail: detail}
	}
	var fields []string
	for _, check := range []struct {
		field   string
		matches bool
	}{
		{"client.id", client.ID == localevidence.TinyAuthPocketIDClientID},
		{"client.name", client.Name == tinyAuthClientName},
		{"client.callbackURLs", slices.Equal(client.CallbackURLs, []string{callback})},
		{"client.isPublic", !client.IsPublic},
		{"client.pkceEnabled", !requirePKCE || client.PkceEnabled},
		{"client.isGroupRestricted", client.IsGroupRestricted},
		{"client.allowedUserGroups", samePocketIDGroupIDs(client.AllowedUserGroups, groupIDs)},
		{"binding.ownerRef", binding.OwnerRef == owner.OwnerRef},
		{"binding.groupIds", slices.Equal(binding.GroupIDs, sortedCopy(groupIDs))},
	} {
		if !check.matches {
			fields = append(fields, check.field)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	detail, _ := actionableerror.Diagnostic("pocketid_tinyauth_binding_mismatch")
	detail.Message += " Mismatching fields: " + strings.Join(fields, ", ") + "."
	return &tinyAuthBindingError{detail: detail}
}
