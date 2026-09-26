package pocketid

import (
	"net/url"
	"strings"
)

// activationRedirect is where PocketID sends the holder after redeeming the
// one-time code: the account settings, where the passkey is enrolled.
const activationRedirect = "/settings/account"

// ActivationURL is the link a new owner or household member follows to enroll
// a passkey. PocketID v2 serves one-time codes at /lc/<code> (an alias for
// /login/alternative/code); it has no /setup-account route, which would drop
// the token and land on /login.
func ActivationURL(origin, token string) string {
	return strings.TrimRight(origin, "/") + "/lc/" + url.PathEscape(token) +
		"?redirect=" + url.QueryEscape(activationRedirect)
}

// ActivationToken returns the one-time code of an exact ActivationURL link and
// reports whether the link has exactly that shape (no user info, fragment or
// extra query). Callers still check the scheme and host.
func ActivationToken(link *url.URL) (string, bool) {
	if link == nil || link.User != nil || link.Fragment != "" || link.RawFragment != "" {
		return "", false
	}
	token, found := strings.CutPrefix(link.Path, "/lc/")
	query := link.Query()
	if !found || token == "" || strings.Contains(token, "/") ||
		len(query) != 1 || len(query["redirect"]) != 1 || query.Get("redirect") != activationRedirect {
		return "", false
	}
	return token, true
}
