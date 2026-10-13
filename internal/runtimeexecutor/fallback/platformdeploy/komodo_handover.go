package platformdeploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

func (a *KomodoAdapter) HostStackAttachment(ctx context.Context, ref DeploymentRef, directory string) (string, error) {
	if err := a.validateConfig(); err != nil {
		return "", err
	}
	stack, err := a.readBoundHostStack(ctx, ref, directory)
	if err != nil {
		return "", err
	}
	config, _ := stack["config"].(map[string]any)
	return firstString(config, "server_id"), nil
}

// SetHostStackAttachment changes only the server attachment of an existing
// exact host project. Komodo 2.3.3 UpdateStack does not execute Compose; unlike
// DeleteStack this retains the running project and its API identity.
func (a *KomodoAdapter) SetHostStackAttachment(ctx context.Context, ref DeploymentRef, directory string, attached bool) error {
	if err := a.validateConfig(); err != nil {
		return err
	}
	if a.cfg.ServerID == "" || ref.Platform != "komodo" || ref.ExternalID == "" || ref.AppName == "" ||
		!filepath.IsAbs(directory) || filepath.Clean(directory) != directory || filepath.Base(directory) != ref.AppName {
		return errors.New("manager handover requires the exact node, stack and host directory")
	}
	stack, err := a.readBoundHostStack(ctx, ref, directory)
	if err != nil {
		return err
	}
	config, _ := stack["config"].(map[string]any)
	server := firstString(config, "server_id")
	if server != "" && server != a.cfg.ServerID {
		return errors.New("manager handover refuses another Komodo server attachment")
	}
	wanted := ""
	if attached {
		wanted = a.cfg.ServerID
	}
	if server != wanted {
		var updated map[string]any
		if _, _, err := a.client.postJSON(ctx, "/write/UpdateStack", map[string]any{
			"id": ref.ExternalID, "config": map[string]any{"server_id": wanted},
		}, &updated); err != nil {
			return fmt.Errorf("Komodo manager attachment update is uncertain; resume the same handover: %w", err)
		}
	}
	stack, err = a.readBoundHostStack(ctx, ref, directory)
	if err != nil {
		return err
	}
	config, _ = stack["config"].(map[string]any)
	if firstString(config, "server_id") != wanted {
		return errors.New("Komodo manager attachment readback differs from the handover")
	}
	return nil
}

func (a *KomodoAdapter) readBoundHostStack(ctx context.Context, ref DeploymentRef, directory string) (map[string]any, error) {
	var stack map[string]any
	if _, _, err := a.client.postJSON(ctx, "/read/GetStack", map[string]any{"stack": ref.ExternalID}, &stack); err != nil {
		return nil, err
	}
	config, ok := stack["config"].(map[string]any)
	paths, _ := config["file_paths"].([]any)
	filesOnHost, _ := config["files_on_host"].(bool)
	project := firstString(config, "project_name")
	if project == "" {
		project = firstString(stack, "name")
	}
	if !ok || firstKomodoID(stack) != ref.ExternalID || firstString(stack, "name") != ref.AppName || project != ref.AppName ||
		!filesOnHost || firstString(config, "run_directory") != directory || firstString(config, "swarm_id") != "" ||
		len(paths) != 1 || paths[0] != "compose.yaml" || firstString(config, "env_file_path") != ".env" {
		return nil, errors.New("Komodo stack differs from the node-owned host recipe")
	}
	for _, field := range []string{"environment", "repo", "linked_repo", "file_contents", "extra_args"} {
		if !emptyHostRecipeValue(config[field]) {
			return nil, errors.New("manager handover refuses a competing environment or remote recipe")
		}
	}
	for _, field := range []string{"auto_update", "auto_update_all_services", "auto_pull", "poll_for_updates", "destroy_before_deploy"} {
		if value := config[field]; value != nil && value != false {
			return nil, errors.New("manager handover refuses automatic deployment or destructive reconciliation")
		}
	}
	return stack, nil
}

func emptyHostRecipeValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case []any:
		return len(typed) == 0
	default:
		return false
	}
}
