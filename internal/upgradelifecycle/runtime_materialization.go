package upgradelifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
)

// MaterializedRuntimeCustody identifies exact checkpoint files within a private
// workspace directory. It is valid only during WithRuntimeCustody's callback.
// It neither starts services nor authorizes a live data cutover.
type MaterializedRuntimeCustody struct {
	snapshotID  string
	operationID string
	graph       restoreactivation.RuntimeRecoveryGraph
	paths       map[string]string
}

func (custody MaterializedRuntimeCustody) SnapshotID() string  { return custody.snapshotID }
func (custody MaterializedRuntimeCustody) OperationID() string { return custody.operationID }

// Graph returns historical, defensively copied data, never a fresh CUE plan.
func (custody MaterializedRuntimeCustody) Graph() restoreactivation.RuntimeRecoveryGraph {
	graph := custody.graph
	graph.ComposeRuntimes = append([]restoreactivation.ComposeRuntime(nil), graph.ComposeRuntimes...)
	graph.Volumes = append([]string(nil), graph.Volumes...)
	graph.VolumeDetails = append([]restoreactivation.Volume(nil), graph.VolumeDetails...)
	return graph
}

// Path maps one recorded workspace-relative path to its private materialized
// copy. Callers retain the original workspace as the owner-custody root.
func (custody MaterializedRuntimeCustody) Path(original string) (string, error) {
	materialized, ok := custody.paths[original]
	if !ok {
		return "", errors.New("executor state: path is outside materialized runtime custody")
	}
	return materialized, nil
}

// WithRuntimeCustody verifies a committed owner-signed checkpoint and all its
// blobs, then copies the recorded runtime closure without generating artifacts
// or overwriting workspace files. The callback sees only a complete private
// view. That view is removed on success, failure, or context cancellation.
func (store ExecutorStateStore) WithRuntimeCustody(
	ctx context.Context,
	workspaceRoot, snapshotID string,
	use func(context.Context, MaterializedRuntimeCustody) error,
) (returnErr error) {
	if ctx == nil || use == nil {
		return errors.New("executor state: runtime materialization requires a context and callback")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()
	// Committed snapshots and blobs are immutable CAS entries. Reading them
	// does not acquire a write lock or create store directories.
	snapshot, err := store.loadWithTransaction(workspaceRoot, transaction, snapshotID)
	if err != nil {
		return fmt.Errorf("executor state: verify runtime checkpoint: %w", err)
	}
	graph, files, err := runtimeMaterializationFiles(transaction, snapshot)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Allocate directly beneath the held workspace root. No existing operator
	// directory is reused or chmodded, and every file is created exclusively.
	directory, err := transaction.CreatePrivateDirectory(".stackkit-upgrade-runtime-")
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.RemoveTree(directory)) }()
	custody := MaterializedRuntimeCustody{
		snapshotID: snapshot.ID, operationID: snapshot.OperationID, graph: graph,
		paths: make(map[string]string, len(files)),
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := readExecutorStateRecoveryBlob(transaction, file.blob)
		if err != nil {
			return err
		}
		target := path.Join(directory, file.path)
		if err := transaction.MkdirAll(path.Dir(target), 0o700); err != nil {
			return err
		}
		mode, err := strconv.ParseUint(file.blob.Mode, 8, 32)
		if err != nil {
			return err
		}
		if err := transaction.WriteFileExclusive(target, data, os.FileMode(mode)); err != nil {
			return fmt.Errorf("executor state: materialize runtime artifact %s: %w", file.blob.ID, err)
		}
		custody.paths[file.path] = target
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := transaction.VerifyPathIdentity(); err != nil {
		return err
	}
	err = use(ctx, custody)
	return errors.Join(err, ctx.Err())
}

type runtimeMaterializationFile struct {
	blob ExecutorStateBlob
	path string
}

func runtimeMaterializationFiles(
	transaction *confinedfs.Transaction,
	snapshot ExecutorStateSnapshot,
) (restoreactivation.RuntimeRecoveryGraph, []runtimeMaterializationFile, error) {
	var empty restoreactivation.RuntimeRecoveryGraph
	byID := make(map[string]ExecutorStateBlob, len(snapshot.Artifacts))
	for _, blob := range snapshot.Artifacts {
		byID[blob.ID] = blob
	}
	for _, id := range []string{runtimeRecoveryGraphArtifactID, "generation-manifest", "applied-runtime-custody"} {
		if blob, ok := byID[id]; !ok || blob.Mode != "0600" {
			return empty, nil, errors.New("executor state: checkpoint lacks current runtime recovery custody")
		}
	}
	graphData, err := readExecutorStateRecoveryBlob(transaction, byID[runtimeRecoveryGraphArtifactID])
	if err != nil {
		return empty, nil, err
	}
	graph, err := restoreactivation.ParseRuntimeRecoveryGraph(graphData)
	if err != nil {
		return empty, nil, err
	}
	manifestData, err := readExecutorStateRecoveryBlob(transaction, byID["generation-manifest"])
	if err != nil {
		return empty, nil, err
	}
	manifest, err := generationartifact.ParseManifest(manifestData)
	if err != nil {
		return empty, nil, err
	}
	manifestHash, err := manifest.Hash()
	if err != nil || manifest.Binding != snapshot.Lineage.Binding ||
		manifestHash != snapshot.Lineage.ManifestHash || graph.PlanBinding != snapshot.Lineage.Binding ||
		graph.ManifestHash != manifestHash || graph.OperationID != snapshot.OperationID ||
		graph.CorePolicyArtifactID != snapshot.CorePolicyArtifactID ||
		graph.CorePolicyDigest != snapshot.KopiaSnapshotAnchor.PolicyArtifactDigest {
		return empty, nil, errors.New("executor state: runtime graph differs from signed checkpoint lineage")
	}
	manifestPaths := make(map[string]string, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		blob, ok := byID[artifact.ID]
		if !ok || blob.SHA256 != artifact.SHA256 || blob.Mode != artifact.Mode {
			return empty, nil, errors.New("executor state: runtime manifest differs from captured artifacts")
		}
		manifestPaths[artifact.ID] = artifact.Path
	}
	// Preserve paths relative to every Compose file, including application
	// ./files mounts. The manifest owns generated workspace paths; historical
	// Core snapshots stored the Core Compose file at its output-relative path.
	blobs := append([]ExecutorStateBlob{snapshot.StackSpec}, snapshot.Artifacts...)
	if snapshot.Inventory != nil {
		blobs = append(blobs, *snapshot.Inventory)
	}
	blobs = append(blobs, snapshot.RuntimeCompose)
	files := make([]runtimeMaterializationFile, 0, len(blobs))
	paths := make(map[string]ExecutorStateBlob, len(blobs))
	foldedPaths := make(map[string]struct{}, len(blobs))
	for _, blob := range blobs {
		target := blob.Path
		if generatedPath, ok := manifestPaths[blob.ID]; ok {
			target = generatedPath
		}
		if _, err := confinedfs.ValidatePortablePath(target); err != nil {
			return empty, nil, err
		}
		folded := strings.ToLower(target)
		for previous := range foldedPaths {
			if executorStatePathsCollide(previous, folded) {
				return empty, nil, errors.New("executor state: materialized runtime paths collide")
			}
		}
		foldedPaths[folded] = struct{}{}
		paths[target] = blob
		files = append(files, runtimeMaterializationFile{blob: blob, path: target})
	}
	if policy := paths[graph.CorePolicyPath]; policy.ID != graph.CorePolicyArtifactID || policy.SHA256 != graph.CorePolicyDigest {
		return empty, nil, errors.New("executor state: runtime policy is missing from materialization")
	}
	for _, runtime := range graph.ComposeRuntimes {
		if paths[runtime.Path].SHA256 != runtime.Digest ||
			(runtime.EnvironmentPath != "" && paths[runtime.EnvironmentPath].SHA256 != runtime.EnvironmentDigest) {
			return empty, nil, errors.New("executor state: runtime files differ from signed recovery graph")
		}
	}
	return graph, files, nil
}
