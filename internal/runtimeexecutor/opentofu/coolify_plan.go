package opentofu

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/kombifyio/stackkits/internal/tofu"
)

// Import/update-only service admission is checked on the saved plan, after
// refresh. A remote delete racing the independent API preflight cannot turn
// Apply into an implicit create with a new service ID.
func admitCoolifyProviderPlan(ctx context.Context, runner *tofu.Executor, planFile, heldID string, pending bool) (bool, error) {
	result, err := runner.Show(ctx, planFile)
	if stepErr := requireTofuStep("Coolify saved plan inspection", result, err); stepErr != nil {
		discardTofuOutput(result)
		return false, stepErr
	}
	defer discardTofuOutput(result)
	if len(result.Stdout) == 0 || len(result.Stdout) > maxOpenTofuStateBytes {
		return false, errors.New("Coolify saved plan is not a bounded provider plan")
	}
	var plan struct {
		Changes []struct {
			Address string `json:"address"`
			Mode    string `json:"mode"`
			Change  struct {
				Actions       []string `json:"actions"`
				Before, After map[string]any
				Unknown       map[string]any `json:"after_unknown"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if json.Unmarshal([]byte(result.Stdout), &plan) != nil {
		return false, errors.New("Coolify saved plan could not be inspected")
	}
	service, activation := 0, 0
	plannedActivation := false
	for _, entry := range plan.Changes {
		if entry.Mode != "managed" {
			return false, errors.New("Coolify provider plan contains an unowned data source")
		}
		change := entry.Change
		switch entry.Address {
		case "coolify_service.workload":
			service++
			if !(slices.Equal(change.Actions, []string{"no-op"}) || slices.Equal(change.Actions, []string{"update"})) ||
				change.Before["uuid"] != heldID || change.After["uuid"] != heldID || change.Unknown["uuid"] == true {
				return false, errors.New("Coolify provider plan would create, delete, replace or lose the exact held service identity")
			}
		case "coolify_resource_action.activate":
			activation++
			plannedActivation = !slices.Equal(change.Actions, []string{"no-op"})
			if pending && plannedActivation {
				return false, errors.New("Coolify pending activation requires an action no-op; reconcile its provider state before requesting another restart")
			}
			if !(slices.Equal(change.Actions, []string{"no-op"}) || slices.Equal(change.Actions, []string{"create"}) || slices.Equal(change.Actions, []string{"delete", "create"})) ||
				change.After["resource_uuid"] != heldID || change.Unknown["resource_uuid"] == true {
				return false, errors.New("Coolify activation plan does not target the exact held service")
			}
		default:
			return false, errors.New("Coolify provider plan contains a resource outside the owned workload")
		}
	}
	if service != 1 || activation != 1 {
		return false, errors.New("Coolify provider plan does not bind one held service and its activation")
	}
	return plannedActivation, nil
}
