package architecturev2renderer

import (
	"maps"
	"regexp"
	"slices"
	"strings"
)

// governedHomeIdentityComponents lists the only application components that
// may reach the home identity provider server-side and hold a Pocket ID
// client. Both rights exist for native OIDC sign-in; they are never generic.
var governedHomeIdentityComponents = map[string]string{
	paperlessWorkloadModuleID:      "paperless",
	nextcloudWorkloadModuleID:      "nextcloud",
	forgejoWorkloadModuleID:        "forgejo",
	audiobookshelfWorkloadModuleID: "audiobookshelf",
}

var pocketIDTemplatePlaceholder = regexp.MustCompile(`\{\{[^}]*\}\}`)

// PocketIDTemplatePlaceholders are the only substitutions a Pocket ID client
// environment template may use.
var PocketIDTemplatePlaceholders = []string{"{{issuer}}", "{{clientId}}", "{{clientSecret}}", "{{origin}}"}

// validateHomeIdentityRights admits server-side identity reach and a Pocket
// ID client only for governed components, and a client only together with
// that reach (the application talks to Pocket ID itself).
func validateHomeIdentityRights(moduleRef string, component selectedPaaSRuntimeComponent, path string) error {
	access, client := component.HomeIdentityAccess, component.PocketIDClient
	if access == nil && client == nil {
		return nil
	}
	if governedHomeIdentityComponents[moduleRef] != component.ID {
		return fail(ErrInvalidPlan, path, "home identity access is admitted only for governed native OIDC applications")
	}
	if access == nil || !strings.HasPrefix(access.CABundleTarget, "/") || len(access.CABundleEnvironment) == 0 {
		return fail(ErrInvalidPlan, path+".homeIdentityAccess", "requires a CA bundle target and its trust variables")
	}
	for _, name := range access.CABundleEnvironment {
		if _, clash := component.Environment[name]; clash {
			return fail(ErrInvalidPlan, path+".homeIdentityAccess", "trust variable %q is also a declared variable", name)
		}
	}
	if client == nil {
		return nil
	}
	if !strings.HasPrefix(client.CallbackPath, "/") || len(client.Environment) == 0 {
		return fail(ErrInvalidPlan, path+".pocketIDClient", "requires a callback path and its sign-in settings")
	}
	for _, name := range slices.Sorted(maps.Keys(client.Environment)) {
		_, public := component.Environment[name]
		_, secret := component.SecretEnvironment[name]
		if public || secret || slices.Contains(access.CABundleEnvironment, name) {
			return fail(ErrInvalidPlan, path+".pocketIDClient", "sign-in variable %q is also declared elsewhere", name)
		}
		for _, placeholder := range pocketIDTemplatePlaceholder.FindAllString(client.Environment[name], -1) {
			if !slices.Contains(PocketIDTemplatePlaceholders, placeholder) {
				return fail(ErrInvalidPlan, path+".pocketIDClient", "sign-in variable %q uses an unknown placeholder", name)
			}
		}
	}
	return nil
}

// ApplicationDeliveryHomeIdentityAccess is the governed server-side identity
// reach of one component.
type ApplicationDeliveryHomeIdentityAccess struct {
	CABundleTarget      string
	CABundleEnvironment []string
}

// ApplicationDeliveryPocketIDClient is the governed Pocket ID client of one
// component; Environment values are unsubstituted templates.
type ApplicationDeliveryPocketIDClient struct {
	CallbackPath string
	Environment  map[string]string
}

func homeIdentityDescriptors(component selectedPaaSRuntimeComponent) (*ApplicationDeliveryHomeIdentityAccess, *ApplicationDeliveryPocketIDClient) {
	var access *ApplicationDeliveryHomeIdentityAccess
	if component.HomeIdentityAccess != nil {
		access = &ApplicationDeliveryHomeIdentityAccess{
			CABundleTarget:      component.HomeIdentityAccess.CABundleTarget,
			CABundleEnvironment: append([]string(nil), component.HomeIdentityAccess.CABundleEnvironment...),
		}
	}
	var client *ApplicationDeliveryPocketIDClient
	if component.PocketIDClient != nil {
		client = &ApplicationDeliveryPocketIDClient{
			CallbackPath: component.PocketIDClient.CallbackPath,
			Environment:  maps.Clone(component.PocketIDClient.Environment),
		}
	}
	return access, client
}
