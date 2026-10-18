package nativehost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"gopkg.in/yaml.v3"
)

// normalizeKopiaApplicationHostPaths admits an alias only when replaying the
// same node-custody translation reproduces every byte of the installed recipe.
func normalizeKopiaApplicationHostPaths(ctx context.Context, workspace, site, node string, content []byte) ([]byte, error) {
	var doc struct {
		Services map[string]struct {
			Volumes []any `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, err
	}
	canonical := string(content)
	for _, value := range doc.Services[localbackuppolicy.ServiceRef].Volumes {
		mount, ok := value.(string)
		if !ok {
			continue
		}
		parts := strings.Split(mount, ":")
		if len(parts) != 3 || parts[2] != "ro" || !strings.HasPrefix(parts[0], "/var/lib/docker/volumes/") || !strings.HasPrefix(parts[1], localbackuppolicy.SourcePath+"/") {
			continue
		}
		logical := strings.TrimSuffix(strings.TrimPrefix(parts[1], localbackuppolicy.SourcePath+"/"), "/_data")
		if !localbackuppolicy.ValidComposeVolumeName(logical) {
			return nil, errors.New("Kopia logical source path is invalid")
		}
		canonical = strings.ReplaceAll(canonical, mount, "/var/lib/docker/volumes/"+logical+"/_data:"+parts[1]+":ro")
	}
	bound, err := bindKopiaApplicationHostPaths(ctx, workspace, site, node, []byte(canonical))
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(bound, content) {
		return nil, errors.New("installed Kopia source differs from its node-owned binding")
	}
	return []byte(canonical), nil
}

// bindKopiaApplicationHostPaths realizes only the selected read-only source
// mounts from existing signed manager custody. Snapshot paths remain canonical;
// final backup admission additionally requires the current signed Apply binding.
func bindKopiaApplicationHostPaths(ctx context.Context, workspace, site, node string, definition []byte) ([]byte, error) {
	var document struct {
		Services map[string]struct {
			Volumes []any `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(definition, &document); err != nil {
		return nil, err
	}
	service, exists := document.Services[localbackuppolicy.ServiceRef]
	if !exists {
		return definition, nil
	}
	bound := string(definition)
	client := docker.NewLocalClient()
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, value := range service.Volumes {
		mount, shortForm := value.(string)
		if !shortForm {
			continue
		}
		parts := strings.Split(mount, ":")
		if len(parts) != 3 || parts[2] != "ro" || !strings.HasPrefix(parts[0], "/var/lib/docker/volumes/") || !strings.HasPrefix(parts[1], localbackuppolicy.SourcePath+"/") {
			continue
		}
		logical := strings.TrimSuffix(strings.TrimPrefix(parts[0], "/var/lib/docker/volumes/"), "/_data")
		if !localbackuppolicy.ValidComposeVolumeName(logical) || parts[1] != localbackuppolicy.SourcePath+"/"+logical+"/_data" {
			return nil, errors.New("Kopia source mount differs from its declared logical volume")
		}
		project, volume, application := strings.Cut(logical, "_")
		// Only curated StackKit workloads have logical-to-physical aliases.
		// Store volumes already carry their selected native physical identity.
		if !application || !strings.HasPrefix(project, "stackkit-") {
			continue
		}
		present, err := platformdeploy.HasOwnerHostDeployment(workspace, project)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		state, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
		if err != nil {
			return nil, err
		}
		if platformdeploy.HostDeploymentManager(state) != "coolify" {
			continue
		}
		if state.SiteRef != site || state.NodeRef != node {
			return nil, errors.New("Kopia source manager belongs to another node")
		}
		view, err := root.View(filepath.ToSlash(filepath.Join(".stackkit/runtime/applications", project)))
		if err != nil {
			return nil, err
		}
		file, err := view.Open("compose.yaml")
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(file, 131073))
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(raw) > 131072 {
			return nil, errors.New("Kopia source recipe exceeds its bound")
		}
		var recipe struct {
			Services map[string]struct {
				Image   string   `yaml:"image"`
				Volumes []string `yaml:"volumes"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(raw, &recipe); err != nil {
			return nil, err
		}
		physical := ""
		containers, err := client.ListContainers(ctx, true)
		if err != nil {
			return nil, err
		}
		for _, candidate := range containers {
			container, err := client.InspectContainer(ctx, candidate.ID)
			if err != nil {
				return nil, err
			}
			if container.Config.Labels["com.docker.compose.project"] != state.Deployment.ExternalID {
				continue
			}
			component := container.Config.Labels["com.docker.compose.service"]
			service, selected := recipe.Services[component]
			if !selected || container.Config.Image != service.Image {
				return nil, errors.New("Kopia source differs from the manager's installed component")
			}
			for _, declared := range service.Volumes {
				name, target, ok := strings.Cut(declared, ":")
				if !ok || name != volume {
					continue
				}
				for _, observed := range container.Mounts {
					if observed.Type != "volume" || !observed.RW || observed.Destination != target {
						continue
					}
					if observed.Name != logical && observed.Name != state.Deployment.ExternalID+"_"+volume {
						return nil, errors.New("Kopia source physical volume is outside the exact manager namespace")
					}
					if observed.Source != "/var/lib/docker/volumes/"+observed.Name+"/_data" || physical != "" {
						return nil, errors.New("Kopia source has ambiguous physical custody")
					}
					physical = observed.Name
				}
			}
		}
		if physical == "" {
			return nil, errors.New("Kopia source has no exact manager-owned mounted volume")
		}
		actual := "/var/lib/docker/volumes/" + physical + "/_data:" + parts[1] + ":ro"
		bound = strings.ReplaceAll(bound, mount, actual)
	}
	return []byte(bound), nil
}
