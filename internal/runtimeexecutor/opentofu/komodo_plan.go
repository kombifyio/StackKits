package opentofu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/tofu"
)

// Inspect the saved plan after provider refresh, immediately before Apply.
// An independently held ID can converge in place, never create or replace.
func KomodoPlanAdmission(heldID, project, server, directory string) func(context.Context, *tofu.Executor, string) error {
	return func(ctx context.Context, runner *tofu.Executor, plan string) error {
		return admitKomodoProviderPlan(ctx, runner, plan, heldID, project, server, directory)
	}
}

func admitKomodoProviderPlan(ctx context.Context, runner *tofu.Executor, planFile, heldID, project, server, directory string) error {
	result, err := runner.Show(ctx, planFile)
	if stepErr := requireTofuStep("Komodo saved plan inspection", result, err); stepErr != nil {
		discardTofuOutput(result)
		return stepErr
	}
	defer discardTofuOutput(result)
	return admitKomodoPlan([]byte(result.Stdout), heldID, project, server, directory)
}

func admitKomodoPlan(raw []byte, heldID, project, server, directory string) error {
	deny := errors.New("Komodo saved plan refused: the held stack may only update in place; recovery requires explicit exact-ID admission before planning")
	if len(raw) == 0 || len(raw) > 4<<20 {
		return deny
	}
	var plan struct {
		Changes []struct {
			Address  string `json:"address"`
			Previous string `json:"previous_address"`
			Mode     string `json:"mode"`
			Type     string `json:"type"`
			Provider string `json:"provider_name"`
			Change   struct {
				Actions       []string `json:"actions"`
				Before, After map[string]any
				Unknown       map[string]any `json:"after_unknown"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if json.Unmarshal(raw, &plan) != nil || len(plan.Changes) != 3 {
		return deny
	}
	seen := map[string]bool{}
	for _, entry := range plan.Changes {
		if entry.Mode != "managed" || entry.Previous != "" || seen[entry.Address] {
			return deny
		}
		seen[entry.Address] = true
		c := entry.Change
		switch entry.Address {
		case "komodo_stack.workload":
			if entry.Type != "komodo_stack" || entry.Provider != "registry.opentofu.org/sebastianfs82/komodo" {
				return deny
			}
			if heldID == "" {
				if !slices.Equal(c.Actions, []string{"create"}) || c.Before != nil {
					return deny
				}
			} else {
				if !(slices.Equal(c.Actions, []string{"no-op"}) || slices.Equal(c.Actions, []string{"update"})) || c.Before["id"] != heldID || c.After["id"] != heldID || komodoUnknown(c.Unknown["id"]) {
					return deny
				}
			}
			if c.After["name"] != project || c.After["project_name"] != project || c.After["server_id"] != server || !komodoEmpty(c.After["swarm_id"]) {
				return deny
			}
			for _, key := range []string{"name", "project_name", "server_id", "swarm_id", "destroy_mode_enabled", "auto_pull_enabled", "poll_updates_enabled"} {
				if komodoUnknown(c.Unknown[key]) {
					return deny
				}
			}
			for _, key := range []string{"destroy_mode_enabled", "auto_pull_enabled", "poll_updates_enabled"} {
				if c.After[key] != false {
					return deny
				}
			}
			source, ok := c.After["source"].(map[string]any)
			if !ok || source["directory"] != filepath.Clean(directory) || source["on_host_enabled"] != true {
				return deny
			}
			paths, ok := source["file_paths"].([]any)
			if !ok || len(paths) != 1 || paths[0] != "compose.yaml" {
				return deny
			}
			for _, key := range []string{"contents", "path", "repo_id", "account_id", "url"} {
				if !komodoEmpty(source[key]) {
					return deny
				}
			}
			environment, ok := c.After["environment"].(map[string]any)
			if !ok || environment["file_path"] != ".env" || !komodoEmpty(environment["variables"]) {
				return deny
			}
			// Unknown optional computed values are allowed only on first create;
			// a held resource must expose the entire safe recipe before mutation.
			if heldID != "" && (komodoUnknown(c.Unknown["source"]) || komodoUnknown(c.Unknown["environment"])) {
				return deny
			}
		case "terraform_data.komodo_deploy", "terraform_data.komodo_lifecycle":
			if entry.Type != "terraform_data" || entry.Provider != "terraform.io/builtin/terraform" {
				return deny
			}
			allowed := slices.Equal(c.Actions, []string{"no-op"}) || slices.Equal(c.Actions, []string{"update"}) || slices.Equal(c.Actions, []string{"create"})
			if entry.Address == "terraform_data.komodo_deploy" {
				allowed = allowed || slices.Equal(c.Actions, []string{"delete", "create"})
			}
			if !allowed {
				return deny
			}
			input, ok := c.After["input"].(map[string]any)
			if !ok || input["project"] != project || (heldID != "" && input["stack_id"] != heldID) {
				return deny
			}
			if heldID != "" && komodoUnknown(c.Unknown["input"]) {
				return deny
			}
		default:
			return deny
		}
	}
	if !seen["komodo_stack.workload"] || !seen["terraform_data.komodo_deploy"] || !seen["terraform_data.komodo_lifecycle"] {
		return deny
	}
	return nil
}

func komodoUnknown(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case map[string]any:
		for _, item := range value {
			if komodoUnknown(item) {
				return true
			}
		}
		return false
	case []any:
		for _, item := range value {
			if komodoUnknown(item) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func komodoEmpty(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case string:
		return value == ""
	case []any:
		return len(value) == 0
	case map[string]any:
		return len(value) == 0
	default:
		return false
	}
}

// KomodoDestroyPlanAdmission is the deletion boundary for the internal root
// workflow. It does not admit public remove. Only lifecycle coverage already
// present in untainted prior state can authorize deleting configuration; a
// planned create, imported stack alone, or a receipt alone is insufficient.
func KomodoDestroyPlanAdmission(workspace, root string) func(context.Context, *tofu.Executor, string) error {
	return func(ctx context.Context, runner *tofu.Executor, planFile string) error {
		deny := errors.New("Komodo configuration deletion refused: verified untainted teardown coverage is missing; complete an unchanged verified Apply before destroying this root")
		marker, err := ReadRootMarker(root)
		if err != nil || !isKomodoProviderRoot(marker) || marker.RuntimeDir != marker.ComposeProject || marker.Kind != RootKindWorkload {
			return deny
		}
		held, err := platformdeploy.LoadOwnerHostDeployment(workspace, marker.ComposeProject)
		if err != nil || held.Signature == nil || !komodoProviderRootMatches(marker, held) || platformdeploy.RequireHostDeploymentManager(held, "komodo") != nil || marker.InstanceRef != held.InstanceRef {
			return deny
		}
		expected, err := architecturev2renderer.RenderKomodoWorkloadOpenTofu(architecturev2renderer.KomodoWorkloadSpec{ProjectName: marker.ComposeProject, Directory: filepath.Dir(root), ServerID: held.ServerID, HeldID: held.Deployment.ExternalID})
		if err != nil {
			return deny
		}
		actual, exists, err := readBoundedRegularFile(filepath.Join(root, ConfigFile), 1<<20)
		if err != nil || !exists || !bytes.Equal(actual, expected) {
			return deny
		}
		result, err := runner.Show(ctx, planFile)
		if stepErr := requireTofuStep("Komodo destroy saved plan inspection", result, err); stepErr != nil {
			discardTofuOutput(result)
			return stepErr
		}
		defer discardTofuOutput(result)
		if len(result.Stdout) == 0 || len(result.Stdout) > 4<<20 {
			return deny
		}
		type stateResource struct {
			Address  string         `json:"address"`
			Mode     string         `json:"mode"`
			Type     string         `json:"type"`
			Provider string         `json:"provider_name"`
			Tainted  bool           `json:"tainted"`
			Deposed  string         `json:"deposed_key"`
			Values   map[string]any `json:"values"`
		}
		var plan struct {
			Prior struct {
				Values struct {
					Root struct {
						Resources []stateResource `json:"resources"`
						Children  []any           `json:"child_modules"`
					} `json:"root_module"`
				} `json:"values"`
			} `json:"prior_state"`
			Changes []struct {
				Address  string `json:"address"`
				Previous string `json:"previous_address"`
				Deposed  string `json:"deposed"`
				Change   struct {
					Actions []string       `json:"actions"`
					Before  map[string]any `json:"before"`
				} `json:"change"`
			} `json:"resource_changes"`
		}
		if json.Unmarshal([]byte(result.Stdout), &plan) != nil || len(plan.Prior.Values.Root.Children) != 0 {
			return deny
		}
		prior := map[string]stateResource{}
		for _, r := range plan.Prior.Values.Root.Resources {
			if _, duplicate := prior[r.Address]; duplicate || r.Mode != "managed" || r.Deposed != "" {
				return deny
			}
			prior[r.Address] = r
		}
		stack, hasStack := prior["komodo_stack.workload"]
		if !hasStack {
			if len(plan.Changes) != 0 {
				return deny
			}
			return nil
		} // No configuration is held/deleted by this plan.
		lifecycle, covered := prior["terraform_data.komodo_lifecycle"]
		input, _ := lifecycle.Values["input"].(map[string]any)
		lifecycleID, isID := lifecycle.Values["id"].(string)
		if stack.Type != "komodo_stack" || stack.Provider != "registry.opentofu.org/sebastianfs82/komodo" || stack.Tainted || stack.Values["id"] != held.Deployment.ExternalID || !covered || lifecycle.Type != "terraform_data" || lifecycle.Provider != "terraform.io/builtin/terraform" || lifecycle.Tainted || !isID || lifecycleID == "" || !sameKomodoRecord(lifecycle.Values["input"], lifecycle.Values["output"]) || input["project"] != marker.ComposeProject || input["stack_id"] != held.Deployment.ExternalID {
			return deny
		}
		deleting := map[string]bool{}
		for _, change := range plan.Changes {
			if change.Previous != "" || change.Deposed != "" || deleting[change.Address] || !slices.Equal(change.Change.Actions, []string{"delete"}) {
				return deny
			}
			if change.Address != "komodo_stack.workload" && change.Address != "terraform_data.komodo_lifecycle" && change.Address != "terraform_data.komodo_deploy" {
				return deny
			}
			deleting[change.Address] = true
			if change.Address == "komodo_stack.workload" && change.Change.Before["id"] != held.Deployment.ExternalID {
				return deny
			}
			if change.Address == "terraform_data.komodo_lifecycle" && change.Change.Before["id"] != lifecycle.Values["id"] {
				return deny
			}
		}
		if !deleting["komodo_stack.workload"] || !deleting["terraform_data.komodo_lifecycle"] {
			return deny
		}
		return nil
	}
}
