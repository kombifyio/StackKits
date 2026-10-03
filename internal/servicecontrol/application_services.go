package servicecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/logging"
)

// applicationComposeAdapter controls one selected application workload that
// the standalone-compose runtime adapter materialized under
// .stackkit/runtime/applications/stackkit-<workload>-<node>.
const applicationComposeAdapter = "application-compose"

const standaloneComposeRuntimeAdapter = "standalone-compose"

// servicePlanProjection is the bounded ResolvedPlan view service control
// needs: declared core controls plus the selected application workloads.
type servicePlanProjection struct {
	PlanHash string `json:"planHash"`
	Modules  []struct {
		ID              string              `json:"id"`
		ServiceControls []serviceDefinition `json:"serviceControls"`
		Runtime         struct {
			Components []struct {
				ID               string `json:"id"`
				Lifecycle        string `json:"lifecycle"`
				EnabledBySetting string `json:"enabledBySetting"`
			} `json:"components"`
		} `json:"runtime"`
		RenderUnits []struct {
			Values map[string]json.RawMessage `json:"values"`
		} `json:"renderUnits"`
	} `json:"modules"`
	Workloads []struct {
		ID          string   `json:"id"`
		NodeRefs    []string `json:"nodeRefs"`
		Alternative struct {
			ModuleRef string `json:"moduleRef"`
			Route     struct {
				ServiceRef string `json:"serviceRef"`
			} `json:"route"`
			Runtime struct {
				Adapter struct {
					ID string `json:"id"`
				} `json:"adapter"`
			} `json:"runtime"`
		} `json:"alternative"`
	} `json:"workloads"`
}

// applicationServiceControls derives one owner-controllable service per
// selected standalone-compose application workload, keyed by the workload
// (for example "ai"). The service covers every daemon component of the
// workload's CUE runtime graph, so stopping it quiesces all writers of the
// workload's volumes. Workload services are never critical control-plane
// services. A key or endpoint already declared by a module keeps its declared
// control.
func (p servicePlanProjection) applicationServiceControls(declared []serviceDefinition) []serviceDefinition {
	taken := map[string]struct{}{}
	for _, definition := range declared {
		taken["key\x00"+definition.Key] = struct{}{}
		taken["ref\x00"+definition.ServiceRef] = struct{}{}
	}
	result := make([]serviceDefinition, 0)
	for _, workload := range p.Workloads {
		if workload.Alternative.Runtime.Adapter.ID != standaloneComposeRuntimeAdapter || len(workload.NodeRefs) != 1 {
			continue
		}
		serviceRef := workload.Alternative.Route.ServiceRef
		if serviceRef == "" {
			serviceRef = workload.ID
		}
		if _, exists := taken["key\x00"+workload.ID]; exists {
			continue
		}
		if _, exists := taken["ref\x00"+serviceRef]; exists {
			continue
		}
		components := []string{}
		for _, module := range p.Modules {
			if module.ID != workload.Alternative.ModuleRef {
				continue
			}
			for _, component := range module.Runtime.Components {
				if component.Lifecycle != "daemon" {
					continue
				}
				// An optional component exists only while its setting is on.
				if component.EnabledBySetting != "" && !moduleSettingOn(module.RenderUnits, component.EnabledBySetting) {
					continue
				}
				components = append(components, component.ID)
			}
		}
		if len(components) == 0 {
			continue
		}
		taken["key\x00"+workload.ID] = struct{}{}
		taken["ref\x00"+serviceRef] = struct{}{}
		result = append(result, serviceDefinition{
			ModuleID: workload.Alternative.ModuleRef,
			Key:      workload.ID, ServiceRef: serviceRef, Adapter: applicationComposeAdapter,
			RuntimeRef: workload.ID + "-" + workload.NodeRefs[0], ComponentRefs: components,
			AllowedActions: []string{ActionLogs, ActionRestart, ActionStart, ActionStop},
		})
	}
	return result
}

func moduleSettingOn(units []struct {
	Values map[string]json.RawMessage `json:"values"`
}, setting string) bool {
	for _, unit := range units {
		var on bool
		if raw, set := unit.Values[setting]; set && json.Unmarshal(raw, &on) == nil && on {
			return true
		}
	}
	return false
}

func applicationComposeDirectory(workspace, runtimeRef string) string {
	return filepath.Join(workspace, ".stackkit", "runtime", "applications", "stackkit-"+runtimeRef)
}

func applicationComposePath(workspace, runtimeRef string) string {
	return filepath.Join(applicationComposeDirectory(workspace, runtimeRef), "compose.yaml")
}

// runApplicationCompose runs one bounded Compose command against the
// adapter-owned application project, with the project's own env file.
func runApplicationCompose(ctx context.Context, request runtimeCommandRequest) (runtimeCommandOutput, error) {
	directory := filepath.Dir(request.ComposePath)
	if filepath.Base(request.ComposePath) != "compose.yaml" || filepath.Base(directory) != "stackkit-"+request.RuntimeRef ||
		filepath.Base(filepath.Dir(directory)) != "applications" {
		return runtimeCommandOutput{}, errors.New("service control rejected an unbounded application runtime")
	}
	prefix := []string{"compose", "--project-name", "stackkit-" + request.RuntimeRef, "--env-file", filepath.Join(directory, ".env"), "-f", request.ComposePath}
	command := exec.CommandContext(ctx, "docker", append(prefix, request.Args...)...) //nolint:gosec // finite arguments validated by the caller
	command.Dir = directory
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	output, err := command.CombinedOutput()
	if err != nil {
		return runtimeCommandOutput{}, fmt.Errorf("docker compose: %s", logging.RedactText(string(output)))
	}
	return runtimeCommandOutput{Bytes: output}, nil
}
