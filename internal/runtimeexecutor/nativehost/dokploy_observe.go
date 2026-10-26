package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"gopkg.in/yaml.v3"
)

// inspect only non-secret fields: Docker's full inspect includes Env values.
const dokployContainerBindingFormat = `{"id":{{json .Id}},"image":{{json .Config.Image}},"labels":{{json .Config.Labels}},"mounts":{{json .Mounts}},"networks":{{json .NetworkSettings.Networks}}}`

// verifyRuntimeBindings checks the actual daemon rather than accepting API
// Compose text as evidence that the retained data/network layout is in use.
func (o *DokployWorkloadPreparation) verifyRuntimeBindings(ctx context.Context, project standaloneComposeProject) error {
	deny := errors.New("Dokploy actual containers differ from the held physical project, pinned image, retained mount or existing network recipe")
	if !dokployPhysicalProjectPattern.MatchString(project.runtimeProject) {
		return deny
	}
	payload, err := o.ProviderCompose(preparedWorkloadCompose(project))
	if err != nil {
		return deny
	}
	var recipe architecturev2renderer.WorkloadComposeDocument
	if yaml.Unmarshal(payload, &recipe) != nil {
		return deny
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := runClosedDockerCLI(ctx, []string{"ps", "--all", "--filter", "label=com.docker.compose.project=" + project.runtimeProject, "--format", "{{.ID}}", "--no-trunc"})
	if err != nil {
		return deny
	}
	ids := strings.Fields(string(raw))
	if len(ids) != len(recipe.Services) {
		return deny
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !dokployContainerID.MatchString(id) {
			return deny
		}
		raw, err = runClosedDockerCLI(ctx, []string{"inspect", "--format", dokployContainerBindingFormat, id})
		if err != nil {
			return deny
		}
		var container struct {
			ID, Image string
			Labels    map[string]string
			Mounts    []dockerContainerMount
			Networks  map[string]json.RawMessage
		}
		if json.Unmarshal(raw, &container) != nil || container.ID != id || container.Labels["com.docker.compose.project"] != project.runtimeProject {
			return deny
		}
		name := container.Labels["com.docker.compose.service"]
		service, ok := recipe.Services[name]
		if !ok || seen[name] || container.Image != service.Image || len(container.Mounts) != len(service.Volumes) || len(container.Networks) != len(service.Networks) {
			return deny
		}
		seen[name] = true
		for _, network := range service.Networks {
			definition, ok := recipe.Networks[network]
			if !ok || definition.Name == "" {
				return deny
			}
			if _, ok := container.Networks[definition.Name]; !ok {
				return deny
			}
		}
		used := map[string]bool{}
		for _, rawMount := range service.Volumes {
			mount, ok := rawMount.(string)
			if !ok {
				return deny
			} // Vault's closed contract uses only short mounts.
			parts := strings.Split(mount, ":")
			if len(parts) < 2 || len(parts) > 3 {
				return deny
			}
			source, target := parts[0], parts[1]
			readOnly := len(parts) == 3 && parts[2] == "ro"
			if len(parts) == 3 && parts[2] != "ro" {
				return deny
			}
			kind := "bind"
			if !filepath.IsAbs(source) {
				volume, ok := recipe.Volumes[source]
				if !ok || volume["external"] != true {
					return deny
				}
				source, ok = volume["name"].(string)
				if !ok || source == "" {
					return deny
				}
				kind = "volume"
			}
			matched := false
			for _, observed := range container.Mounts {
				actualSource := observed.Source
				if kind == "volume" {
					actualSource = observed.Name
				}
				if observed.Destination == target && !used[target] && observed.Type == kind && actualSource == source && observed.RW != readOnly {
					used[target], matched = true, true
					break
				}
			}
			if !matched {
				return deny
			}
		}
	}
	return nil
}
