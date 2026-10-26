package engine

import (
	"context"
	"fmt"
	"github.com/mtalavi/coolify-mirror/internal/coolify"
)

func relocationAutomationBlockers(ctx context.Context, in *coolify.Instance, resources []coolify.Resource) ([]string, error) {
	var selected []map[string]string
	var blockers []string
	for _, r := range resources {
		if r.Local() && r.IsDatabase() {
			blockers = append(blockers, fmt.Sprintf("standalone database %s cannot be migrated: external/shared writers and public access are not fenced by the selected application drain", r.UUID))
		}
		if r.Local() && r.Table == "applications" {
			selected = append(selected, map[string]string{"uuid": r.UUID, "table": r.Table})
		}
	}
	if len(selected) == 0 {
		return blockers, nil
	}
	var result struct {
		Blockers []string `json:"blockers"`
	}
	if err := in.PHP(ctx, "relocation_automation", selected, &result); err != nil {
		return nil, err
	}
	return append(blockers, result.Blockers...), nil
}
