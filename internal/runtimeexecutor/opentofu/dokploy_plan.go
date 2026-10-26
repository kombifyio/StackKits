package opentofu

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/tofu"
)

// DokployPlanAdmission admits only in-place convergence of the independently
// held imported service. A missing remote object must never become a create,
// even if the provider removed it from refreshed state after API preflight.
func DokployPlanAdmission(held platformdeploy.HostDeployment, payload []byte, description string) func(context.Context, *tofu.Executor, string) error {
	expectedDigest := nativehost.DokployProviderRecipeDigest(string(payload), description)
	return func(ctx context.Context, runner *tofu.Executor, plan string) error {
		result, err := runner.Show(ctx, plan)
		if err := requireTofuStep("Dokploy plan admission", result, err); err != nil {
			discardTofuOutput(result)
			return errors.New("Dokploy saved plan could not be admitted")
		}
		defer discardTofuOutput(result)
		return admitDokployPlan([]byte(result.Stdout), held, expectedDigest)
	}
}

func admitDokployPlan(raw []byte, held platformdeploy.HostDeployment, desiredDigest string) error {
	deny := errors.New("Dokploy plan refused: only in-place update of the signed held ID, node, environment, physical project and secret-file recipe is admitted")
	if len(raw) > 4<<20 {
		return deny
	}
	type value struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		AppName        string `json:"app_name"`
		EnvironmentID  string `json:"environment_id"`
		ServerID       string `json:"server_id"`
		Env            string `json:"env"`
		Description    string `json:"description"`
		ComposeType    string `json:"compose_type"`
		DeleteVolumes  bool   `json:"delete_volumes"`
		FreshVolumes   bool   `json:"fresh_volumes"`
		CreateEnvFile  bool   `json:"create_env_file"`
		DeployOnChange bool   `json:"deploy_on_change"`
		AutoDeploy     bool   `json:"auto_deploy"`
		Randomize      bool   `json:"randomize"`
		Raw            *struct {
			ComposeFile string `json:"compose_file"`
		} `json:"raw"`
	}
	var document struct {
		ResourceChanges []struct {
			Address         string `json:"address"`
			PreviousAddress string `json:"previous_address"`
			Mode            string `json:"mode"`
			Type            string `json:"type"`
			Provider        string `json:"provider_name"`
			Change          struct {
				Actions []string                   `json:"actions"`
				Before  *value                     `json:"before"`
				After   *value                     `json:"after"`
				Unknown map[string]json.RawMessage `json:"after_unknown"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if json.Unmarshal(raw, &document) != nil || len(document.ResourceChanges) != 1 {
		return deny
	}
	resource := document.ResourceChanges[0]
	if resource.Address != "dokploy_compose.workload" || resource.PreviousAddress != "" || resource.Mode != "managed" ||
		resource.Type != "dokploy_compose" || resource.Provider != "registry.opentofu.org/vanillauys/dokploy" || len(resource.Change.Actions) != 1 ||
		(resource.Change.Actions[0] != "no-op" && resource.Change.Actions[0] != "update") {
		return deny
	}
	for _, key := range []string{"id", "name", "app_name", "environment_id", "server_id", "raw", "env", "description", "compose_type", "delete_volumes", "fresh_volumes", "create_env_file", "deploy_on_change", "auto_deploy", "randomize"} {
		if unknown, exists := resource.Change.Unknown[key]; exists && string(unknown) != "false" && string(unknown) != "{}" {
			return deny
		}
	}
	for _, values := range []*value{resource.Change.Before, resource.Change.After} {
		if values == nil || values.ID != held.Deployment.ExternalID || values.Name != held.Deployment.AppName ||
			values.AppName != held.RuntimeProject || values.EnvironmentID != held.EnvironmentID || values.ServerID != held.ServerID ||
			values.Env != "" || values.ComposeType != "docker-compose" || values.Raw == nil {
			return deny
		}
	}
	beforeDigest := nativehost.DokployProviderRecipeDigest(resource.Change.Before.Raw.ComposeFile, resource.Change.Before.Description)
	if beforeDigest != held.RecipeDigest && beforeDigest != desiredDigest {
		return deny
	}
	if nativehost.DokployProviderRecipeDigest(resource.Change.After.Raw.ComposeFile, resource.Change.After.Description) != desiredDigest ||
		resource.Change.After.DeleteVolumes || resource.Change.After.FreshVolumes || resource.Change.After.CreateEnvFile ||
		!resource.Change.After.DeployOnChange || resource.Change.After.AutoDeploy || resource.Change.After.Randomize {
		return deny
	}
	return nil
}
