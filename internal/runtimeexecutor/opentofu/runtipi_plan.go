package opentofu

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/kombifyio/stackkits/internal/tofu"
)

func admitRuntipiAPIPlan(ctx context.Context, runner *tofu.Executor, planFile, project, digest string) (bool, error) {
	result, err := runner.Show(ctx, planFile)
	if err := requireTofuStep("Runtipi saved plan inspection", result, err); err != nil {
		discardTofuOutput(result)
		return false, err
	}
	defer discardTofuOutput(result)
	if len(result.Stdout) == 0 || len(result.Stdout) > maxOpenTofuStateBytes {
		return false, errors.New("Runtipi plan is not bounded")
	}
	var plan struct {
		Changes []struct {
			Address  string `json:"address"`
			Mode     string `json:"mode"`
			Type     string `json:"type"`
			Previous string `json:"previous_address"`
			Change   struct {
				Actions []string       `json:"actions"`
				After   map[string]any `json:"after"`
				Unknown map[string]any `json:"after_unknown"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	deny := errors.New("Runtipi plan may only converge its exact API action; removal and foreign resource changes are refused")
	if json.Unmarshal([]byte(result.Stdout), &plan) != nil || len(plan.Changes) != 1 {
		return false, deny
	}
	entry := plan.Changes[0]
	change := entry.Change
	if entry.Address != "terraform_data.runtipi_workload" || entry.Mode != "managed" || entry.Type != "terraform_data" || entry.Previous != "" ||
		!(slices.Equal(change.Actions, []string{"no-op"}) || slices.Equal(change.Actions, []string{"create"}) || slices.Equal(change.Actions, []string{"delete", "create"})) {
		return false, deny
	}
	input, ok := change.After["input"].(map[string]any)
	if !ok || len(input) != 2 || input["project"] != project || input["request_digest"] != digest {
		return false, deny
	}
	triggers, ok := change.After["triggers_replace"].([]any)
	if !ok || len(triggers) != 1 || triggers[0] != digest || change.Unknown["input"] == true || change.Unknown["triggers_replace"] == true {
		return false, deny
	}
	return !slices.Equal(change.Actions, []string{"no-op"}), nil
}
