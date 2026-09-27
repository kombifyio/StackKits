package nativehost

import (
	"errors"
	"maps"
	"slices"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/jellyfinsso"
)

func applyJellyfinSSOPlugin(plugin *architecturev2renderer.JellyfinSSOPluginDescriptor, service *standaloneComposeService, configFiles map[string][]byte) error {
	if plugin == nil {
		return nil
	}
	files, err := jellyfinsso.Files(plugin.Version, plugin.SHA256, plugin.SourceURL)
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		target := jellyfinsso.TargetDirectory + "/" + name
		rel := architecturev2renderer.StandaloneComposeConfigRelPath(target)
		if _, exists := configFiles[rel]; exists {
			return errors.New("Jellyfin SSO assembly shadows another runtime file")
		}
		configFiles[rel] = files[name]
		service.Volumes = append(service.Volumes, "./"+rel+":"+target+":ro")
	}
	return nil
}
