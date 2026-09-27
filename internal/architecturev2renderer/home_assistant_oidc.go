package architecturev2renderer

import (
	"reflect"

	"github.com/kombifyio/stackkits/internal/hassoidc"
)

type HomeAssistantOIDCDescriptor struct {
	PatchSHA256    string `json:"patchSHA256"`
	Version        string `json:"version"`
	SHA256         string `json:"sha256"`
	Patch          string `json:"patch"`
	AiofilesSHA256 string `json:"aiofilesSHA256"`
	JoseRFCSHA256  string `json:"joserfcSHA256"`
}

const homeAssistantOIDCConfiguration = `client_id: "{{clientId}}"
discovery_url: "{{issuer}}/.well-known/openid-configuration"
roles:
  admin: admins
  user: household
claims:
  username: sub
  groups: groups
features:
  automatic_user_linking: false
  force_https: true
  default_redirect: false
network:
  tls_verify: true
  tls_ca_path: /etc/stackkit/ca-bundle.pem`

func validateHomeAssistantOIDC(module string, component selectedPaaSRuntimeComponent, path string) error {
	if module != homeAssistantWorkloadModuleID {
		if component.HomeAssistantOIDC != nil || (component.PocketIDClient != nil && component.PocketIDClient.Public) {
			return fail(ErrInvalidPlan, path, "public OIDC and Home Assistant extension are admitted only for Smart Home")
		}
		return nil
	}
	want := &HomeAssistantOIDCDescriptor{Version: hassoidc.Version, SHA256: hassoidc.SHA256, Patch: hassoidc.PatchVersion, PatchSHA256: hassoidc.PatchSHA256, AiofilesSHA256: hassoidc.AiofilesSHA256, JoseRFCSHA256: hassoidc.JoseRFCSHA256}
	access := &selectedPaaSHomeIdentityAccess{CABundleTarget: "/etc/stackkit/ca-bundle.pem", CABundleEnvironment: []string{"SSL_CERT_FILE"}}
	client := &selectedPaaSPocketIDClient{Public: true, CallbackPath: "/auth/oidc/callback", Environment: map[string]string{}, Configuration: &selectedPaaSPocketIDClientConfiguration{Target: "/config/stackkit-oidc.yaml", Body: homeAssistantOIDCConfiguration}}
	if component.ID != "home-assistant" || !reflect.DeepEqual(component.HomeAssistantOIDC, want) || !reflect.DeepEqual(component.HomeIdentityAccess, access) || !reflect.DeepEqual(component.PocketIDClient, client) {
		return fail(ErrInvalidPlan, path, "Smart Home requires the exact packaged OIDC extension and identity policy")
	}
	return nil
}
