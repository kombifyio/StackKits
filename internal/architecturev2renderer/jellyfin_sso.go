package architecturev2renderer

import (
	"reflect"

	"github.com/kombifyio/stackkits/internal/jellyfinsso"
)

// JellyfinSSOPluginDescriptor is an exact bundled artifact, never a fetch URL.
type JellyfinSSOPluginDescriptor struct {
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	SourceURL string `json:"sourceURL"`
}

func validateJellyfinSSOPlugin(module string, component selectedPaaSRuntimeComponent, path string) error {
	if module != jellyfinWorkloadModuleID {
		if component.JellyfinSSOPlugin != nil {
			return fail(ErrInvalidPlan, path, "Jellyfin SSO is admitted only for Media")
		}
		return nil
	}
	want := &JellyfinSSOPluginDescriptor{Version: jellyfinsso.Version, SHA256: jellyfinsso.SHA256, SourceURL: jellyfinsso.SourceURL}
	access := &selectedPaaSHomeIdentityAccess{CABundleTarget: "/etc/stackkit/ca-bundle.pem", CABundleEnvironment: []string{"SSL_CERT_FILE"}}
	client := &selectedPaaSPocketIDClient{CallbackURLs: []string{"{{origin}}/sso/OID/redirect/pocketid", "{{origin}}/sso/OID/r/pocketid"}, Environment: map[string]string{"JELLYFIN_PublishedServerUrl": "{{origin}}"}}
	if component.ID != "jellyfin" || !reflect.DeepEqual(component.JellyfinSSOPlugin, want) || !reflect.DeepEqual(component.HomeIdentityAccess, access) || !reflect.DeepEqual(component.PocketIDClient, client) {
		return fail(ErrInvalidPlan, path, "Jellyfin requires the exact bundled SSO artifact and home identity settings")
	}
	return nil
}
