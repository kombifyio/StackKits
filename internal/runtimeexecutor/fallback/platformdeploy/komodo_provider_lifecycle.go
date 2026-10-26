package platformdeploy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
)

// RequireProviderHostStack checks the ID independently of provider Read, which
// in v0.12.0 can adopt a replacement by name. It also refuses remote material
// that the provider would otherwise copy into state on refresh or import.
func (a *KomodoAdapter) RequireProviderHostStack(ctx context.Context, ref DeploymentRef, directory string) error {
	if err := a.validateConfig(); err != nil {
		return err
	}
	var identity map[string]any
	status, _, err := a.client.postJSON(ctx, "/read/GetStack", map[string]any{"stack": ref.ExternalID}, &identity)
	if err != nil {
		if status == http.StatusNotFound {
			return errors.New("Komodo held ID is absent; explicit exact-ID recovery is required before import/apply")
		}
		return fmt.Errorf("Komodo held-ID lookup failed (HTTP %d); preserve custody and repair API access before recovery", status)
	}
	if firstKomodoID(identity) != ref.ExternalID {
		return errors.New("Komodo returned a different ID; implicit name adoption is refused")
	}
	stack, err := a.readBoundHostStack(ctx, ref, directory)
	if err != nil {
		return errors.New("Komodo exact host-recipe readback could not be confirmed; preserve custody and inspect API access and recipe binding before recovery")
	}
	config, _ := stack["config"].(map[string]any)
	if firstString(config, "server_id") != a.cfg.ServerID {
		return errors.New("Komodo stack is attached to another server")
	}
	for _, field := range []string{"pre_deploy", "post_deploy"} {
		command, _ := config[field].(map[string]any)
		if !emptyHostRecipeValue(command["command"]) || !emptyHostRecipeValue(command["path"]) {
			return errors.New("Komodo provider refuses unmanaged lifecycle hooks")
		}
	}
	for _, field := range []string{"webhook_secret", "compose_cmd_wrapper", "registry_account", "registry_provider"} {
		if !emptyHostRecipeValue(config[field]) {
			return errors.New("Komodo provider refuses remote credentials or command wrappers")
		}
	}
	if enabled, _ := config["webhook_enabled"].(bool); enabled {
		return errors.New("Komodo provider refuses automatic webhook deployment")
	}
	return nil
}

// RequireProviderStackIDAbsent admits a replacement only after a successful
// exact-ID request positively returns 404. Transport/authorization failures
// and name lookups never establish absence.
func (a *KomodoAdapter) RequireProviderStackIDAbsent(ctx context.Context, id string) error {
	if err := a.validateConfig(); err != nil {
		return err
	}
	var stack map[string]any
	status, _, err := a.client.postJSON(ctx, "/read/GetStack", map[string]any{"stack": id}, &stack)
	if status == http.StatusNotFound && err != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Komodo previous-ID absence is unproven (HTTP %d); preserve the original custody", status)
	}
	return errors.New("Komodo previous held ID still exists; recovery cannot create competing stack ownership")
}

// RequireAbsentProviderStack is read-only. New provider ownership never adopts
// a pre-existing name. Even a secret-bearing stack response stays in memory.
func (a *KomodoAdapter) RequireAbsentProviderStack(ctx context.Context, project string) error {
	if err := a.validateConfig(); err != nil {
		return err
	}
	var listed []map[string]any
	if _, _, err := a.client.postJSON(ctx, "/read/ListStacks", map[string]any{"query": map[string]any{"names": []string{project}}}, &listed); err != nil {
		return errors.New("cannot establish absence of the Komodo project")
	}
	for _, item := range listed {
		if firstString(item, "name") == project {
			return errors.New("Komodo project exists; explicit ID import/transition is required")
		}
	}
	return nil
}

// DestroyProviderHostStack removes containers through the same Komodo API.
// Komodo v2.3.3 DestroyStack invokes compose down without --volumes. It has no
// volume deletion option. Provider DeleteStack then removes the configuration.
func (a *KomodoAdapter) DestroyProviderHostStack(ctx context.Context, ref DeploymentRef, directory string) error {
	if !filepath.IsAbs(directory) {
		return errors.New("Komodo destroy requires the exact host directory")
	}
	if err := a.RequireProviderHostStack(ctx, ref, directory); err != nil {
		return err
	}
	var update map[string]any
	if _, _, err := a.client.postJSON(ctx, "/execute/DestroyStack", map[string]any{
		"stack": ref.ExternalID, "services": []string{}, "remove_orphans": false, "stop_time": 60,
	}, &update); err != nil {
		return errors.New("Komodo destroy outcome is uncertain; inspect the held stack before retrying")
	}
	if komodoOperationError(update) != nil {
		return errors.New("Komodo destroy failed")
	}
	if isKomodoOperationPending(update) && firstKomodoID(update) != "" {
		if _, err := a.waitForUpdateResult(ctx, ref.AppName, firstKomodoID(update)); err != nil {
			return errors.New("Komodo destroy did not complete")
		}
	} else if !isKomodoOperationComplete(update) {
		return errors.New("Komodo destroy returned no completed operation")
	}
	status, err := a.readStackObservedStatus(ctx, ref)
	if err != nil || (status != "down" && status != "stopped" && status != "Down" && status != "Stopped") {
		return errors.New("Komodo destroy did not observe the held stack down")
	}
	return nil
}

// RequireProviderHostStackDown is read-only and shares the existing API client.
// A signed old completion cannot authorize deleting a restarted configuration.
func (a *KomodoAdapter) RequireProviderHostStackDown(ctx context.Context, ref DeploymentRef, directory string) error {
	if err := a.RequireProviderHostStack(ctx, ref, directory); err != nil {
		return err
	}
	status, err := a.readStackObservedStatus(ctx, ref)
	if err != nil || (status != "down" && status != "stopped" && status != "Down" && status != "Stopped") {
		return errors.New("Komodo configuration deletion refused: exact held stack is not observed down")
	}
	return nil
}
