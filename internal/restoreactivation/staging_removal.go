package restoreactivation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const stagingRemovalScript = `root=$1
leaf=$2
test -d "$root"
rm -rf -- "$root/$leaf"
test ! -e "$root/$leaf"`

var stagedRestoreLeafPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// StagedRestoreRemover removes one operation's staged restore tree from the
// governed staging volume. It is implemented by the Docker runtime and is
// used by the Advanced restore drill, which must leave no staged data behind
// and must never mount or modify a live application volume.
type StagedRestoreRemover interface {
	RemoveStagedRestore(ctx context.Context, graph RuntimeRecoveryGraph, stagingPath string) error
}

// RemoveStagedRestore deletes exactly /restore-staging/<operation digest>
// inside the CUE-derived staging volume. Only the staging volume is mounted.
func (runtime *dockerRuntime) RemoveStagedRestore(
	ctx context.Context,
	graph RuntimeRecoveryGraph,
	stagingPath string,
) error {
	leaf, err := stagedRestoreLeaf(graph, stagingPath)
	if err != nil {
		return err
	}
	if err := runtime.inspectStagingVolume(ctx, graph); err != nil {
		return err
	}
	helper := helperName(Authority{OperationID: graph.OperationID}, "staging-removal")
	args := []string{
		"run", "--name", helper,
		"--label", "io.stackkit.restore.operation=" + graph.OperationID,
		"--label", "io.stackkit.restore.plan=" + graph.PlanHash,
		"--rm", "--network", "none", "--read-only",
		"--user", "0:0",
		"--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER",
		"--security-opt", "no-new-privileges",
		"--mount", "type=volume,src=" + graph.StagingVolume + ",dst=/staging",
		"--entrypoint", "/bin/sh",
		graph.KopiaHelperImage,
		"-ceu", stagingRemovalScript, "--", "/staging", leaf,
	}
	if _, err := runtime.docker(ctx, args...); err != nil {
		return wrapDocker("remove staged restore", err)
	}
	return nil
}

func stagedRestoreLeaf(graph RuntimeRecoveryGraph, stagingPath string) (string, error) {
	if graph.OperationID == "" || graph.StagingVolume == "" || graph.KopiaHelperImage == "" {
		return "", errors.New("restoreactivation: staged restore removal requires a derived recovery graph")
	}
	if !strings.HasPrefix(graph.StagingVolume, graph.ComposeProject+"_") {
		return "", errors.New("restoreactivation: staging volume is outside the Compose project")
	}
	leaf := path.Base(stagingPath)
	if path.Clean(stagingPath) != stagingPath || path.Dir(stagingPath) != "/restore-staging" ||
		!stagedRestoreLeafPattern.MatchString(leaf) {
		return "", errors.New("restoreactivation: staged restore path is not governed")
	}
	return leaf, nil
}

func (runtime *dockerRuntime) inspectStagingVolume(ctx context.Context, graph RuntimeRecoveryGraph) error {
	output, err := runtime.docker(ctx, "volume", "inspect", graph.StagingVolume)
	if err != nil {
		return fmt.Errorf("restoreactivation: inspect staging volume: %w", err)
	}
	var inspected []struct {
		Name   string            `json:"Name"`
		Driver string            `json:"Driver"`
		Labels map[string]string `json:"Labels"`
	}
	if err := json.Unmarshal(output, &inspected); err != nil || len(inspected) != 1 {
		return errors.New("restoreactivation: staging volume inspection is invalid")
	}
	volume := inspected[0]
	logical := strings.TrimPrefix(graph.StagingVolume, graph.ComposeProject+"_")
	if volume.Name != graph.StagingVolume || volume.Driver != "local" ||
		volume.Labels["com.docker.compose.project"] != graph.ComposeProject ||
		volume.Labels["com.docker.compose.volume"] != logical {
		return errors.New("restoreactivation: staging volume is not owned by the verified Compose project")
	}
	return nil
}
