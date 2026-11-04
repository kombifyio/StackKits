package accessgate

import "strings"

// isPocketIDAdministration classifies a request on the Pocket ID host as
// identity administration (user, group, client, configuration, key, claim,
// signup and audit management, and the admin pages) as opposed to the
// sign-in surface (passkey and one-time-code login, OIDC authorize and token,
// the signed-in person's own account, and the public assets the login and
// consent pages load). It is pinned to the Pocket ID API of the images the
// catalog pins. The path is the decoded request path; repeated slashes are
// collapsed first, so the classification only ever widens.
func isPocketIDAdministration(method, path string) bool {
	path = collapseSlashes(path)
	read := method == "GET" || method == "HEAD"
	switch {
	case under(path, "/settings/admin"):
		return true
	case path == "/api/users" || path == "/api/users/":
		return true
	case strings.HasPrefix(path, "/api/users/"):
		rest := strings.TrimPrefix(path, "/api/users/")
		if rest == "me" || strings.HasPrefix(rest, "me/") {
			return false
		}
		// Avatars are public; the consent page shows the signed-in person.
		if id, file, found := strings.Cut(rest, "/"); found && read && id != "" && file == "profile-picture.png" {
			return false
		}
		return true
	case under(path, "/api/user-groups"), under(path, "/api/api-keys"), under(path, "/api/custom-claims"),
		under(path, "/api/signup-tokens"), under(path, "/api/audit-logs/all"), under(path, "/api/audit-logs/filters"),
		under(path, "/api/membership-capabilities"), under(path, "/api/scim"), under(path, "/api/ldap"),
		// First-admin setup happens at home, never through a reach path.
		under(path, "/api/signup/setup"), under(path, "/api/one-time-access-token/setup"):
		return true
	case under(path, "/api/application-images"):
		// The login page reads the branding images; changing them is administration.
		return !read
	case under(path, "/api/oidc/clients"):
		// The consent page reads a client's public metadata and logo.
		rest := strings.TrimPrefix(strings.TrimPrefix(path, "/api/oidc/clients"), "/")
		if id, file, found := strings.Cut(rest, "/"); found && read && id != "" && (file == "meta" || file == "logo") {
			return false
		}
		return true
	case under(path, "/api/application-configuration"):
		// The login page reads the public configuration and branding.
		switch path {
		case "/api/application-configuration", "/api/application-configuration/logo",
			"/api/application-configuration/background-image", "/api/application-configuration/favicon":
			return !read
		}
		return true
	}
	return false
}

func under(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func collapseSlashes(path string) string {
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	return path
}
