package docker

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const composeProjectLabel = "com.docker.compose.project"

// CanonicalComposeProjects are the native core Compose project names created
// by Basement and Cloud Kit Apply. Whole-deployment remove always targets them.
var CanonicalComposeProjects = []string{
	"stackkit-basement-core",
	"stackkit-cloud-core",
	"stackkit-cloud-core-standalone",
}

// ListComposeProjects returns unique Compose project names from containers
// that carry com.docker.compose.project.
func (c *Client) ListComposeProjects(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmd := c.command(ctx, "ps", "-a",
		"--filter", "label="+composeProjectLabel,
		"--format", `{{.Label "`+composeProjectLabel+`"}}`,
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list compose projects: %w", err)
	}
	seen := map[string]bool{}
	var names []string
	for _, line := range strings.Split(string(output), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || seen[name] {
			continue
		}
		if err := validateName(name); err != nil {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, nil
}

// StackKitsComposeProjectsToRemove unions discovered compose projects that use
// the stackkit- prefix with the canonical core project names.
func StackKitsComposeProjectsToRemove(discovered []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] || !strings.HasPrefix(name, "stackkit-") {
			return
		}
		if err := validateName(name); err != nil {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, name := range discovered {
		if isCanonicalComposeProject(name) {
			continue
		}
		add(name)
	}
	for _, name := range CanonicalComposeProjects {
		add(name)
	}
	return out
}

func isCanonicalComposeProject(name string) bool {
	for _, canonical := range CanonicalComposeProjects {
		if name == canonical {
			return true
		}
	}
	return false
}

// RemoveComposeProject deletes containers, networks, and volumes owned by one
// Compose project. Missing resources are success. Empty lists never invoke rm.
func (c *Client) RemoveComposeProject(ctx context.Context, project string, deleteVolumes bool) error {
	if err := validateName(project); err != nil {
		return fmt.Errorf("invalid compose project: %w", err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	label := composeProjectLabel + "=" + project
	containers, err := c.listResourceIDs(ctx, []string{"ps", "-aq", "--filter", "label=" + label})
	if err != nil {
		return fmt.Errorf("list compose containers for %s: %w", project, err)
	}
	if len(containers) > 0 {
		args := append([]string{"rm", "-f"}, containers...)
		if err := c.runIgnoreMissing(ctx, args); err != nil {
			return fmt.Errorf("remove compose containers for %s: %w", project, err)
		}
	}
	networks, err := c.listResourceIDs(ctx, []string{"network", "ls", "-q", "--filter", "label=" + label})
	if err != nil {
		return fmt.Errorf("list compose networks for %s: %w", project, err)
	}
	for _, network := range networks {
		if err := c.runIgnoreMissing(ctx, []string{"network", "rm", network}); err != nil {
			return fmt.Errorf("remove compose network %s: %w", network, err)
		}
	}
	if !deleteVolumes {
		return nil
	}
	volumes, err := c.listResourceIDs(ctx, []string{"volume", "ls", "-q", "--filter", "label=" + label})
	if err != nil {
		return fmt.Errorf("list compose volumes for %s: %w", project, err)
	}
	for _, volume := range volumes {
		if err := c.runIgnoreMissing(ctx, []string{"volume", "rm", volume}); err != nil {
			return fmt.Errorf("remove compose volume %s: %w", volume, err)
		}
	}
	return nil
}

func (c *Client) listResourceIDs(ctx context.Context, args []string) ([]string, error) {
	cmd := c.command(ctx, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Split(string(output), "\n") {
		id := strings.TrimSpace(line)
		if id == "" {
			continue
		}
		if err := validateNameOrID(id); err != nil {
			if err := validateName(id); err != nil {
				continue
			}
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (c *Client) runIgnoreMissing(ctx context.Context, args []string) error {
	cmd := c.command(ctx, args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	out := strings.TrimSpace(string(output))
	if strings.Contains(out, "not found") || strings.Contains(out, "No such") {
		return nil
	}
	return fmt.Errorf("%s", out)
}
