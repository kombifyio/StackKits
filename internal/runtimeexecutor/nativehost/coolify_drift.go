package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"gopkg.in/yaml.v3"
)

// CoolifyDriftError reports that the recipe Coolify stores for a workload no
// longer matches the authorized node recipe, for example after an edit in the
// Coolify UI. The running containers may still look healthy until Coolify
// redeploys; re-applying the workload restores the authorized recipe.
type CoolifyDriftError struct{ projectRef string }

func (err *CoolifyDriftError) Error() string {
	return "Coolify service recipe differs from the authorized workload"
}
func (err *CoolifyDriftError) DriftSubject() string    { return "runtime-configuration" }
func (err *CoolifyDriftError) DriftCode() string       { return "coolify-service-recipe-changed" }
func (err *CoolifyDriftError) DriftProjectRef() string { return err.projectRef }

// coolifyRecipeDecorations are the only service fields left out of the
// comparison: env_file carries the node's private derived secret file, which a
// read-only observation must not write. Every other field, and the top-level
// volumes and networks, must match the authorized recipe, so a Coolify edit to
// ports, networks, privileges, labels or volume drivers is drift too.
var coolifyRecipeDecorations = map[string]bool{"env_file": true}

// requireCoolifyServiceRecipe compares the stored Coolify recipe with the
// authorized node recipe, read-only: no derived secret file is written. The
// API withholds stored recipes from the node token, so the Marketplace's own
// application container reads it, as Marketplace observation already does.
func requireCoolifyServiceRecipe(ctx context.Context, marketplace *osMarketplaceOperations, ref platformdeploy.DeploymentRef, project standaloneComposeProject) error {
	expected, err := platformNodeCompose(NativeWorkloadCompose{ProjectName: project.name, Directory: project.directory, Compose: project.compose, EnvFile: standaloneComposeEnvFile, project: project})
	if err != nil {
		return err
	}
	if expected, err = coolifyOneShotRestart(expected); err != nil {
		return err
	}
	stored, err := marketplace.serviceComposeRaw(ctx, ref.ExternalID)
	if err != nil {
		return err
	}
	want, err := coolifyRecipeProjection(expected)
	if err != nil {
		return err
	}
	got, err := coolifyRecipeProjection([]byte(stored))
	if err != nil {
		return &CoolifyDriftError{projectRef: project.name}
	}
	if !reflect.DeepEqual(want, got) {
		return &CoolifyDriftError{projectRef: project.name}
	}
	return nil
}

func (m *osMarketplaceOperations) serviceComposeRaw(ctx context.Context, uuid string) (string, error) {
	if m == nil || !validStandaloneComposeVolumeName(uuid) {
		return "", errors.New("Coolify recipe observation requires an exact service id")
	}
	php := `require '/var/www/html/vendor/autoload.php';$app=require '/var/www/html/bootstrap/app.php';$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();$s=App\Models\Service::where('uuid',getenv('STACKKIT_SERVICE'))->first();if(!$s){throw new Exception('Coolify service is absent');}echo json_encode(['raw'=>$s->docker_compose_raw]);`
	directory := filepath.Join(m.workspace, filepath.FromSlash(marketplaceDirectory))
	raw, err := m.compose(ctx, directory, nil, "exec", "-T", "-e", "STACKKIT_SERVICE="+uuid, "coolify", "php", "-r", php)
	if err != nil {
		return "", fmt.Errorf("read stored Coolify recipe: %w", err)
	}
	var result struct {
		Raw string `json:"raw"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Raw == "" {
		return "", errors.New("stored Coolify recipe is unreadable")
	}
	return result.Raw, nil
}

func coolifyRecipeProjection(raw []byte) (map[string]any, error) {
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	services, _ := document["services"].(map[string]any)
	if len(services) == 0 {
		return nil, errors.New("Coolify recipe declares no services")
	}
	projected := map[string]any{}
	for name, value := range services {
		service, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("Coolify recipe service is not a mapping")
		}
		fields := map[string]any{}
		for key, field := range service {
			if !coolifyRecipeDecorations[key] {
				fields[key] = normalizeCoolifyRecipeValue(key, field)
			}
		}
		projected[name] = fields
	}
	return map[string]any{"services": projected, "volumes": document["volumes"], "networks": document["networks"]}, nil
}

// normalizeCoolifyRecipeValue makes list and map forms of environment and
// unordered lists comparable without changing their meaning.
func normalizeCoolifyRecipeValue(key string, value any) any {
	switch typed := value.(type) {
	case []any:
		if key == "command" || key == "entrypoint" {
			return typed
		}
		if key == "environment" {
			environment := map[string]any{}
			for _, item := range typed {
				if text, ok := item.(string); ok {
					name, val, _ := strings.Cut(text, "=")
					environment[name] = val
				}
			}
			return environment
		}
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text, _ := yaml.Marshal(item)
			items = append(items, string(text))
		}
		sort.Strings(items)
		return items
	case map[string]any:
		if key == "environment" {
			environment := map[string]any{}
			for name, val := range typed {
				if val == nil {
					environment[name] = ""
					continue
				}
				if text, ok := val.(string); ok {
					environment[name] = text
					continue
				}
				environment[name] = fmt.Sprint(val)
			}
			return environment
		}
	}
	return value
}
