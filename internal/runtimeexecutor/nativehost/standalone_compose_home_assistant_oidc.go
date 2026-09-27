package nativehost

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/hassoidc"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

func applyHomeAssistantOIDC(workspace string, extension *architecturev2renderer.HomeAssistantOIDCDescriptor, service *standaloneComposeService, files map[string][]byte) error {
	if extension == nil {
		return nil
	}
	if extension.Version != hassoidc.Version || extension.SHA256 != hassoidc.SHA256 || extension.Patch != hassoidc.PatchVersion || extension.PatchSHA256 != hassoidc.PatchSHA256 || extension.AiofilesSHA256 != hassoidc.AiofilesSHA256 || extension.JoseRFCSHA256 != hassoidc.JoseRFCSHA256 {
		return errors.New("Home Assistant OIDC extension differs from governed source")
	}
	assets, err := hassoidc.Files()
	if err != nil {
		return err
	}
	binding, err := localevidence.LoadOwnerRuntimeBinding(workspace)
	if err != nil {
		return err
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(workspace)
	if err != nil {
		return err
	}
	policy, err := json.Marshal(map[string]string{"issuer": address.PocketIDOrigin(), "subject": binding.PocketIDSubject})
	if err != nil {
		return err
	}
	targets := map[string][]byte{"/etc/stackkit/home-assistant-identity.json": policy}
	for name, body := range assets {
		targets[hassoidc.TargetDirectory+"/"+name] = body
	}
	for _, target := range slices.Sorted(maps.Keys(targets)) {
		rel := architecturev2renderer.StandaloneComposeConfigRelPath(target)
		if _, exists := files[rel]; exists {
			return errors.New("Home Assistant OIDC shadows a governed runtime file")
		}
		files[rel] = targets[target]
		service.Volumes = append(service.Volumes, "./"+rel+":"+target+":ro")
	}
	return nil
}
