package architecturev2renderer

import "strings"

func applicationAudienceGroups(access rawPublicRouteAccessV2, service string) (string, bool) {
	if access.Audience == nil {
		return tinyAuthRouteACLGroups(access.Privilege, service)
	}
	if access.PolicyExposure != "local-only" || access.Privilege != "user" || (access.Authentication != "human" && access.Authentication != "human+device") {
		return "", false
	}
	groups, err := access.Audience.Groups()
	return strings.Join(groups, ","), err == nil
}
