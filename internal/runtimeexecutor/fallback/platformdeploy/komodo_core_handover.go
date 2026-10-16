package platformdeploy

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
)

var handoverIDPattern = regexp.MustCompile(`^handover-[0-9a-f]{32}$`)

func handoverMarker(operation string) (string, error) {
	if !handoverIDPattern.MatchString(operation) {
		return "", errors.New("Core handover requires its signed operation identity")
	}
	return "stackkit://manager-handover/" + operation, nil
}

func requireHandoverMarker(stack map[string]any, operation string) error {
	marker, err := handoverMarker(operation)
	if err != nil {
		return err
	}
	config, _ := stack["config"].(map[string]any)
	links, _ := config["links"].([]any)
	for _, link := range links {
		if link == marker {
			return nil
		}
	}
	return errors.New("target resource does not carry this signed handover's creation marker")
}

// HostStackNameAvailable observes the target Core before a signed transfer
// starts. A pre-existing resource cannot be adopted by a new handover.
func (a *KomodoAdapter) HostStackNameAvailable(ctx context.Context, project string) error {
	if err := a.validateConfig(); err != nil {
		return err
	}
	var stacks []map[string]any
	if _, _, err := a.client.postJSON(ctx, "/read/ListStacks", map[string]any{"query": map[string]any{"names": []string{project}}, "limit": 0}, &stacks); err != nil {
		return err
	}
	if stacks == nil {
		return errors.New("target Core did not return a complete stack inventory")
	}
	for _, stack := range stacks {
		if firstString(stack, "name") == project {
			return errors.New("target Core already has this stack; inspect its custody before transferring")
		}
	}
	return nil
}

// CreateHostStackForHandover is attempted once after the source is detached.
// It configures an existing host recipe without executing or copying it.
// An uncertain response is recovered with FindHostStackForHandover, never
// another CreateStack or an implicit UpdateStack.
func (a *KomodoAdapter) CreateHostStackForHandover(ctx context.Context, project, directory, operation string) (DeploymentRef, error) {
	if err := a.validateConfig(); err != nil {
		return DeploymentRef{}, err
	}
	if !hostProjectPattern.MatchString(project) || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || filepath.Base(directory) != project || a.cfg.ServerID == "" {
		return DeploymentRef{}, errors.New("Core handover requires the exact host project and target server")
	}
	manifest := AppManifest{Name: project, HostDirectory: directory, ComposePath: "compose.yaml"}
	marker, err := handoverMarker(operation)
	if err != nil {
		return DeploymentRef{}, err
	}
	config := komodoStackConfig(manifest, a.cfg)
	// This request can never attach the workload, even if it commits after
	// cancellation. Attachment is a separate journaled UpdateStack operation.
	config["server_id"] = ""
	config["links"] = []string{marker}
	var created map[string]any
	if _, _, err := a.client.postJSON(ctx, "/write/CreateStack", map[string]any{"name": project, "config": config}, &created); err != nil {
		return DeploymentRef{}, errors.New("target stack creation is uncertain; inspect and resume this handover without replay")
	}
	ref := DeploymentRef{Platform: "komodo", AppName: project, ExternalID: firstKomodoID(created)}
	if ref.ExternalID == "" {
		return DeploymentRef{}, errors.New("target stack creation returned no identity; inspect and resume without replay")
	}
	attachment, err := a.HostStackHandoverAttachment(ctx, ref, directory, operation)
	if err != nil {
		return DeploymentRef{}, err
	}
	if attachment != "" {
		return DeploymentRef{}, errors.New("new handover resource is unexpectedly attached")
	}
	return ref, nil
}

// FindHostStackForHandover is read-only recovery of the exact private recipe
// after a journaled creation. It does not create, update, deploy or delete.
func (a *KomodoAdapter) FindHostStackForHandover(ctx context.Context, project, directory, operation string) (DeploymentRef, error) {
	if err := a.validateConfig(); err != nil {
		return DeploymentRef{}, err
	}
	var stack map[string]any
	if _, _, err := a.client.postJSON(ctx, "/read/GetStack", map[string]any{"stack": project}, &stack); err != nil {
		return DeploymentRef{}, errors.New("journaled target creation has no verified outcome; inspect existing Core resources without replay")
	}
	ref := DeploymentRef{Platform: "komodo", AppName: project, ExternalID: firstKomodoID(stack)}
	attachment, err := a.HostStackHandoverAttachment(ctx, ref, directory, operation)
	if err != nil || attachment != "" {
		return DeploymentRef{}, errors.New("journaled target stack differs from the exact native server and recipe")
	}
	return ref, nil
}

func (a *KomodoAdapter) HostStackHandoverAttachment(ctx context.Context, ref DeploymentRef, directory, operation string) (string, error) {
	if err := a.validateConfig(); err != nil {
		return "", err
	}
	stack, err := a.readBoundHostStack(ctx, ref, directory)
	if err != nil {
		return "", err
	}
	if err := requireHandoverMarker(stack, operation); err != nil {
		return "", err
	}
	config, _ := stack["config"].(map[string]any)
	return firstString(config, "server_id"), nil
}

// RequireUnattachedHandoverTarget proves absence, or the exact operation's
// detached resource, before source restoration. Full resources cover aliases
// that refer to the same physical project under a different resource name.
func (a *KomodoAdapter) RequireUnattachedHandoverTarget(ctx context.Context, project, directory, operation, externalID string) error {
	if err := a.validateConfig(); err != nil {
		return err
	}
	if _, err := handoverMarker(operation); err != nil {
		return err
	}
	var stacks []map[string]any
	if _, _, err := a.client.postJSON(ctx, "/read/ListFullStacks", map[string]any{"query": map[string]any{}, "limit": 0}, &stacks); err != nil {
		return err
	}
	if stacks == nil {
		return errors.New("cancellation requires a complete authoritative target inventory")
	}
	for _, stack := range stacks {
		config, _ := stack["config"].(map[string]any)
		if firstString(stack, "name") != project && firstString(config, "project_name") != project && firstString(config, "run_directory") != directory {
			continue
		}
		id := firstKomodoID(stack)
		if id == "" || (externalID != "" && id != externalID) {
			return errors.New("cancellation found a different target identity")
		}
		attachment, err := a.HostStackHandoverAttachment(ctx, DeploymentRef{Platform: "komodo", AppName: project, ExternalID: id}, directory, operation)
		if err != nil {
			return err
		}
		if attachment != "" {
			return errors.New("target is attached; pending transfer cannot restore source control")
		}
	}
	return nil
}
