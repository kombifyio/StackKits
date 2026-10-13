package upgradelifecycle

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// Recover restores the historical native recipe before the StackSpec commit.
// This grants no data activation or old-runtime start; the mutation journal
// continues to deny pre-Apply rollback after target Apply may have run.
func restoreExecutorStateMarketplaceRuntime(transaction *confinedfs.Transaction, view confinedfs.View, snapshot ExecutorStateSnapshot) ([]string, error) {
	selected := false
	for _, artifact := range snapshot.Artifacts {
		selected = selected || strings.HasPrefix(artifact.ID, "marketplace-runtime-")
	}
	if !selected {
		return nil, nil
	}
	graph, files, err := runtimeMaterializationFiles(transaction, snapshot)
	if err != nil {
		return nil, err
	}
	selected = false
	for _, runtime := range graph.ComposeRuntimes {
		if runtime.Project == localbackuppolicy.MarketplaceComposeProject {
			if selected || runtime.Path != path.Join(localbackuppolicy.MarketplaceRuntimeDirectory, "compose.yaml") ||
				runtime.EnvironmentPath != path.Join(localbackuppolicy.MarketplaceRuntimeDirectory, "coolify.env") {
				return nil, errors.New("executor state: Marketplace recovery graph has a different native owner")
			}
			selected = true
		}
	}
	if !selected {
		return nil, errors.New("executor state: Marketplace private files have no selected recovery runtime")
	}
	byPath := make(map[string]ExecutorStateBlob, len(files))
	for _, file := range files {
		byPath[file.path] = file.blob
	}
	// Validate and read the entire fixed closure before the first write.
	values := make(map[string][]byte)
	defer func() {
		for _, data := range values {
			clear(data)
		}
	}()
	for _, name := range localbackuppolicy.MarketplaceRuntimeFiles() {
		file := path.Join(localbackuppolicy.MarketplaceRuntimeDirectory, name)
		blob, ok := byPath[file]
		if !ok || blob.ID != "marketplace-runtime-"+name || blob.Mode != "0600" {
			return nil, fmt.Errorf("executor state: Marketplace private %s is outside the signed recovery closure", name)
		}
		data, err := readExecutorStateRecoveryBlob(transaction, blob)
		if err != nil {
			return nil, err
		}
		values[file] = data
	}
	restored := make([]string, 0)
	for _, name := range localbackuppolicy.MarketplaceRuntimeFiles() {
		file := path.Join(localbackuppolicy.MarketplaceRuntimeDirectory, name)
		if _, err := view.WriteAtomic0600(file, values[file]); err != nil {
			return nil, fmt.Errorf("executor state: restore Marketplace private %s: %w", name, err)
		}
		restored = append(restored, file)
	}
	return restored, nil
}
