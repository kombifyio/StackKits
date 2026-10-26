package upgradelifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
	"github.com/kombifyio/stackkits/internal/terramatehost"
	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
	"github.com/kombifyio/stackkits/internal/tofu"
	"gopkg.in/yaml.v3"
)

// ExecutorStateRecoveryResult is the secret-free projection of one verified
// executor-state recovery handoff. Kopia data remains an owner-signed anchor;
// Recover never promotes staged backup data into live volumes.
type ExecutorStateRecoveryResult struct {
	SnapshotID          string                         `json:"snapshotId"`
	OperationID         string                         `json:"operationId"`
	Release             ExecutorStateRelease           `json:"release"`
	KopiaSnapshotAnchor backuplifecycle.SnapshotAnchor `json:"kopiaSnapshotAnchor"`
	RestoredPaths       []string                       `json:"restoredPaths"`
}

// RecoveryCommand receives the exact captured executable in a private,
// process-owned temporary directory after the captured StackSpec and optional
// Inventory have been restored atomically.
type RecoveryCommand func(
	context.Context,
	string,
	ExecutorStateSnapshot,
) error

// Recover verifies a committed executor-state snapshot and every retained
// blob before restoring its StackSpec, optional Inventory, and, for an
// OpenTofu generation target, every captured OpenTofu root. It invokes
// the caller with the exact captured executable and removes that temporary
// executable when the callback returns.
func (store ExecutorStateStore) Recover(
	ctx context.Context,
	workspaceRoot string,
	snapshotID string,
	invoke RecoveryCommand,
) (ExecutorStateRecoveryResult, error) {
	return store.RecoverWith(ctx, workspaceRoot, snapshotID, RecoveryOptions{}, invoke)
}

// RecoveryOptions narrows Recover for the Advanced coordinated rollback
// (docs/ARCHITECTURE.md "Coordinated rollback across stacks (Stage 1)").
type RecoveryOptions struct {
	// Admission transfers an already-held complete preparation lease. Recovery
	// checks its destinations and closes it before invoking the governed child.
	// This joins lifecycle preparation to authority restoration without a gap.
	Admission *opentofu.GenericRestoreAdmission
	// ReplaceAuthority restores the captured StackSpec and Inventory even
	// when they changed after the checkpoint. Only an Owner-approved
	// rollback to this exact checkpoint sets it; a change set promotes its
	// candidate StackSpec before its target runs, so the current StackSpec
	// is expected to differ.
	ReplaceAuthority bool
	// SkipOpenTofuRoots leaves the OpenTofu roots to the caller. The
	// coordinated rollback restores and converges them per stack, and a
	// second restore here would overwrite the state its forced apply wrote.
	SkipOpenTofuRoots bool
	// Attestations verifies the prior release's archive in workspace release
	// custody. An OpenTofu generation captured from a verified release
	// recovers on the OpenTofu and provider mirror of that archive, and
	// fails closed without a verifier or a verified archive.
	Attestations releaseindex.AttestationVerifier
}

// RecoverWith is Recover with explicit options.
func (store ExecutorStateStore) RecoverWith(
	ctx context.Context,
	workspaceRoot string,
	snapshotID string,
	options RecoveryOptions,
	invoke RecoveryCommand,
) (ExecutorStateRecoveryResult, error) {
	if options.Admission != nil {
		defer options.Admission.Close()
	}
	if ctx == nil {
		return ExecutorStateRecoveryResult{}, errors.New("executor state: recovery requires a context")
	}
	if invoke == nil {
		return ExecutorStateRecoveryResult{}, errors.New("executor state: recovery command is required")
	}
	snapshot, err := store.Load(workspaceRoot, snapshotID)
	if err != nil {
		return ExecutorStateRecoveryResult{}, fmt.Errorf(
			"executor state: load verified recovery snapshot: %w", err,
		)
	}
	if err := ctx.Err(); err != nil {
		return ExecutorStateRecoveryResult{}, err
	}

	tempRoot, err := os.MkdirTemp("", "stackkit-executor-recovery-")
	if err != nil {
		return ExecutorStateRecoveryResult{}, fmt.Errorf(
			"executor state: create private recovery directory: %w", err,
		)
	}
	defer func() { _ = os.RemoveAll(tempRoot) }()
	if err := os.Chmod(tempRoot, 0o700); err != nil {
		return ExecutorStateRecoveryResult{}, fmt.Errorf(
			"executor state: protect private recovery directory: %w", err,
		)
	}
	// The snapshot retains stackkit and stackkit-server only, and the
	// recovered release resolves tofu beside its executable. An OpenTofu
	// generation captured from a verified release therefore stages the tofu
	// and provider mirror record of that release's own verified archive, as
	// an upgrade target does (ReleaseExecutablesWithOpenTofu). A running
	// executable capture has no archive and recovers on this installation's
	// packaged OpenTofu and a mirror carrying its manifest (ExecRunner).
	openTofu := executorStateTargetExecutesOpenTofu(snapshot.GenerationTarget)
	fromArchive := openTofu && snapshot.Release.Authority != ExecutorStateReleaseRunningExecutable
	if fromArchive {
		archiveExecutable, err := stagePriorReleaseOpenTofu(ctx, workspaceRoot, snapshot.Release, options.Attestations, tempRoot)
		if err != nil {
			return ExecutorStateRecoveryResult{}, err
		}
		if executorStateDigest(archiveExecutable) != snapshot.Executable.Blob.SHA256 {
			return ExecutorStateRecoveryResult{}, errors.New(
				"executor state: recovery executable differs from its verified release archive",
			)
		}
	} else if openTofu {
		if packaged, ok := tofu.PackagedBinaryPath(); ok {
			if err := copyPrivateExecutable(packaged, filepath.Join(tempRoot, tofu.BinaryName())); err != nil {
				return ExecutorStateRecoveryResult{}, fmt.Errorf(
					"executor state: stage packaged OpenTofu for recovery: %w", err,
				)
			}
		}
	}

	executablePath, restoredPaths, err := store.prepareExecutorStateRecovery(
		ctx, workspaceRoot, snapshot, tempRoot, options,
	)
	if err != nil {
		return ExecutorStateRecoveryResult{}, err
	}
	// The recovered release accepts only a provider mirror that carries its
	// own manifest. The archive staging recorded one already; otherwise it
	// runs on the installation mirror or the mirror kept when this release
	// was staged for the upgrade (ExecRunner).
	if openTofu && !fromArchive {
		executable, readErr := os.ReadFile(executablePath)
		if readErr != nil {
			return ExecutorStateRecoveryResult{}, fmt.Errorf(
				"executor state: read recovery executable: %w", readErr,
			)
		}
		if mirror, ok := recoveredReleaseProviderMirror(workspaceRoot, executable); ok {
			if err := writeReleaseProviderMirrorFile(tempRoot, mirror); err != nil {
				return ExecutorStateRecoveryResult{}, err
			}
		}
	}
	result := ExecutorStateRecoveryResult{
		SnapshotID: snapshot.ID, OperationID: snapshot.OperationID,
		Release: snapshot.Release, KopiaSnapshotAnchor: snapshot.KopiaSnapshotAnchor,
		RestoredPaths: append([]string(nil), restoredPaths...),
	}
	if err := invoke(opentofu.WithoutGenericRestoreAdmission(ctx), executablePath, snapshot); err != nil {
		return result, fmt.Errorf(
			"executor state: invoke verified recovery executable: %w", err,
		)
	}
	return result, nil
}

// stagePriorReleaseOpenTofu re-verifies the captured release in workspace
// release custody, binds it to the checkpoint's release identity, and stages
// its packaged OpenTofu and provider mirror record in dir. It returns the
// archive's stackkit executable.
func stagePriorReleaseOpenTofu(
	ctx context.Context,
	workspaceRoot string,
	release ExecutorStateRelease,
	attestations releaseindex.AttestationVerifier,
	dir string,
) ([]byte, error) {
	if attestations == nil {
		return nil, errors.New(
			"executor state: recovering an OpenTofu checkpoint requires verifying its release archive",
		)
	}
	workspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, err
	}
	installDir, err := releaseindex.CacheInstallationDirectory(
		filepath.Join(workspace, ".stackkit", "releases"), release.Kit, release.Version, release.Platform,
	)
	if err != nil {
		return nil, fmt.Errorf("executor state: locate prior release %s: %w", release.Version, err)
	}
	var executable []byte
	err = (releaseindex.Installer{Attestations: attestations}).InspectInstalled(
		ctx, installDir, func(proof releaseindex.VerifiedInstallation) error {
			verified, cli, _, inspectErr := inspectVerifiedReleaseExecutables(proof, dir)
			if inspectErr != nil {
				return inspectErr
			}
			if verified != release {
				return errors.New("verified release archive differs from the checkpoint release")
			}
			executable = cli
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"executor state: stage OpenTofu from verified prior release %s: %w", release.Version, err,
		)
	}
	return executable, nil
}

func (store ExecutorStateStore) prepareExecutorStateRecovery(
	ctx context.Context,
	workspaceRoot string,
	snapshot ExecutorStateSnapshot,
	tempRoot string,
	options RecoveryOptions,
) (executablePath string, restoredPaths []string, returnErr error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return "", nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return "", nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()

	// Serialize recovery authority replacement independently from the
	// executor-state CAS. Both locks are released before invoking a child
	// executable.
	outputLock, err := transaction.TryAcquireOutputLock(
		executorStateRoot + "/recovery-workspace",
	)
	if err != nil {
		return "", nil, fmt.Errorf(
			"executor state: acquire recovery workspace lock: %w", err,
		)
	}
	defer func() { returnErr = errors.Join(returnErr, outputLock.Release()) }()
	storeLock, err := transaction.TryAcquireOutputLock(executorStateRoot)
	if err != nil {
		return "", nil, fmt.Errorf(
			"executor state: acquire recovery store lock: %w", err,
		)
	}
	defer func() { returnErr = errors.Join(returnErr, storeLock.Release()) }()

	reloaded, err := store.loadWithTransaction(
		workspaceRoot, transaction, snapshot.ID,
	)
	if err != nil {
		return "", nil, fmt.Errorf(
			"executor state: reverify recovery snapshot under lock: %w", err,
		)
	}
	if !reflect.DeepEqual(reloaded, snapshot) {
		return "", nil, errors.New(
			"executor state: recovery snapshot changed after initial verification",
		)
	}
	executable, err := readExecutorStateRecoveryBlob(
		transaction, snapshot.Executable.Blob,
	)
	if err != nil {
		return "", nil, err
	}
	stackSpec, err := readExecutorStateRecoveryBlob(
		transaction, snapshot.StackSpec,
	)
	if err != nil {
		return "", nil, err
	}
	var inventory []byte
	if snapshot.Inventory != nil {
		inventory, err = readExecutorStateRecoveryBlob(
			transaction, *snapshot.Inventory,
		)
		if err != nil {
			return "", nil, err
		}
	}
	if snapshot.StackSpec.Mode != "0600" ||
		(snapshot.Inventory != nil && snapshot.Inventory.Mode != "0600") {
		return "", nil, errors.New(
			"executor state: recovery authority files must use mode 0600",
		)
	}
	if !options.ReplaceAuthority {
		if err := requireExecutorStateRecoveryInputUnchanged(
			transaction, snapshot.StackSpec, stackSpec, nil,
		); err != nil {
			return "", nil, err
		}
		if snapshot.Inventory != nil {
			if err := requireExecutorStateRecoveryInputUnchanged(
				transaction, *snapshot.Inventory, inventory,
				inventoryEquivalentIgnoringFreeSpace,
			); err != nil {
				return "", nil, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}

	executablePath, err = materializeExecutorStateRecoveryExecutable(
		tempRoot, snapshot.Executable.Blob, executable,
	)
	if err != nil {
		return "", nil, err
	}
	// The recovered stackkit stages the Core's stackkit-server from beside
	// itself, so the captured server is materialized next to it.
	if snapshot.Executable.Server != nil {
		server, err := readExecutorStateRecoveryBlob(transaction, *snapshot.Executable.Server)
		if err != nil {
			return "", nil, err
		}
		if _, err := materializeExecutorStateRecoveryExecutable(
			tempRoot, *snapshot.Executable.Server, server,
		); err != nil {
			return "", nil, err
		}
	}
	view, err := root.View(".")
	if err != nil {
		return "", nil, err
	}
	// A captured manager can rewrite the runtime even with SkipOpenTofuRoots,
	// or when the checkpoint predates OpenTofu. Admit the actual destination
	// before Inventory, runtime files, StackSpec or the callback can change it.
	admission := options.Admission
	if admission == nil {
		admission, err = opentofu.AcquireGenericRestoreAdmission(workspaceRoot, executorStateOpenTofuRestoreDestinations(snapshot.RuntimeOpenTofu))
	} else {
		err = admission.CheckRoots(workspaceRoot, executorStateOpenTofuRestoreDestinations(snapshot.RuntimeOpenTofu))
	}
	if err != nil {
		return "", nil, err
	}
	// Retain the provider project locks through every recovery write. This
	// function returns before RecoverWith invokes the governed child; that
	// child must acquire its normal manager admission rather than inherit locks.
	defer func() { returnErr = errors.Join(returnErr, admission.Close()) }()
	// Inventory is installed before the StackSpec, which remains the recovery
	// authority commit point for a workspace that uses both files.
	if snapshot.Inventory != nil {
		if _, err := view.WriteAtomic0600(snapshot.Inventory.Path, inventory); err != nil {
			return "", nil, fmt.Errorf(
				"executor state: atomically restore Inventory: %w", err,
			)
		}
		restoredPaths = append(restoredPaths, snapshot.Inventory.Path)
	}
	// OpenTofu state is the execution state owner (ADR-0045). The captured
	// roots are restored before the StackSpec commit point so the recovered
	// release plans against the checkpoint's state.
	if executorStateTargetExecutesOpenTofu(snapshot.GenerationTarget) && !options.SkipOpenTofuRoots {
		roots, err := restoreExecutorStateOpenTofuRoots(transaction, view, snapshot.RuntimeOpenTofu, snapshot.ID)
		if err != nil {
			return "", nil, err
		}
		restoredPaths = append(restoredPaths, roots...)
	}
	marketplacePaths, err := restoreExecutorStateMarketplaceRuntime(transaction, view, snapshot)
	if err != nil {
		return "", nil, err
	}
	restoredPaths = append(restoredPaths, marketplacePaths...)
	if _, err := view.WriteAtomic0600(snapshot.StackSpec.Path, stackSpec); err != nil {
		return "", nil, fmt.Errorf(
			"executor state: atomically restore StackSpec: %w", err,
		)
	}
	restoredPaths = append(restoredPaths, snapshot.StackSpec.Path)
	sort.Strings(restoredPaths)
	return executablePath, restoredPaths, nil
}

func readExecutorStateRecoveryBlob(
	transaction *confinedfs.Transaction,
	blob ExecutorStateBlob,
) ([]byte, error) {
	blobPath, err := executorStateBlobPath(blob.SHA256)
	if err != nil {
		return nil, err
	}
	data, info, err := transaction.ReadStableBounded(blobPath, executorStateMaxBlobBytes)
	if err != nil {
		return nil, fmt.Errorf(
			"executor state: read recovery blob %s: %w", blob.ID, err,
		)
	}
	if !info.Mode().IsRegular() ||
		(len(data) == 0 && !executorStateStandaloneEnvironmentBlob(blob.ID, blob.Path, blob.Mode)) ||
		executorStateDigest(data) != blob.SHA256 {
		return nil, fmt.Errorf(
			"executor state: recovery blob %s differs from its identity", blob.ID,
		)
	}
	return append([]byte(nil), data...), nil
}

func requireExecutorStateRecoveryInputUnchanged(
	transaction *confinedfs.Transaction,
	blob ExecutorStateBlob,
	captured []byte,
	equivalent func(current, captured []byte) bool,
) error {
	current, info, err := transaction.ReadStable(blob.Path)
	if err != nil {
		return fmt.Errorf(
			"executor state: re-read current recovery input %s: %w", blob.ID, err,
		)
	}
	unchanged := bytes.Equal(current, captured) ||
		(equivalent != nil && equivalent(current, captured))
	if !info.Mode().IsRegular() || !unchanged {
		return fmt.Errorf(
			"executor state: current recovery input %s changed after checkpoint; refusing to overwrite operator authority",
			blob.ID,
		)
	}
	return nil
}

func materializeExecutorStateRecoveryExecutable(
	tempRoot string,
	blob ExecutorStateBlob,
	data []byte,
) (path string, returnErr error) {
	name := executorStateExecutablePathFromBlob(blob)
	path = filepath.Join(tempRoot, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf(
			"executor state: create recovery executable: %w", err,
		)
	}
	defer func() {
		if file != nil {
			returnErr = errors.Join(returnErr, file.Close())
		}
	}()
	written, err := io.Copy(file, bytes.NewReader(data))
	if err != nil || written != int64(len(data)) {
		return "", fmt.Errorf(
			"executor state: write recovery executable: %w", err,
		)
	}
	if err := file.Chmod(0o700); err != nil {
		return "", fmt.Errorf(
			"executor state: protect recovery executable: %w", err,
		)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf(
			"executor state: sync recovery executable: %w", err,
		)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() ||
		executorStateDigest(data) != blob.SHA256 {
		return "", errors.New(
			"executor state: materialized recovery executable does not verify",
		)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf(
			"executor state: close recovery executable: %w", err,
		)
	}
	file = nil
	return path, nil
}

func executorStateExecutablePathFromBlob(blob ExecutorStateBlob) string {
	return filepath.Base(filepath.FromSlash(blob.Path))
}

// ExecutorStateRootPayload is the verified bytes of one captured OpenTofu
// root.
type ExecutorStateRootPayload struct {
	ModuleRef      string
	Root           string
	State          []byte
	Config         []byte
	Lock           []byte
	HasLock        bool
	Compose        []byte
	HasCompose     bool
	Environment    []byte
	HasEnvironment bool
}

// ExecutorStateRollbackCustody is the verified content of one checkpoint that
// the Advanced coordinated rollback restores per stack: every captured
// OpenTofu root and every captured artifact by ID (the Terramate stack graph
// and stack files among them).
type ExecutorStateRollbackCustody struct {
	Snapshot  ExecutorStateSnapshot
	Roots     []ExecutorStateRootPayload
	Artifacts map[string][]byte
}

// LoadRollbackCustody verifies a committed snapshot and returns its OpenTofu
// roots and artifacts. It writes nothing. Only OpenTofu-executing generation
// targets carry roots.
func (store ExecutorStateStore) LoadRollbackCustody(
	workspaceRoot, snapshotID string,
) (custody ExecutorStateRollbackCustody, returnErr error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return ExecutorStateRollbackCustody{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return ExecutorStateRollbackCustody{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()
	snapshot, err := store.loadWithTransaction(workspaceRoot, transaction, snapshotID)
	if err != nil {
		return ExecutorStateRollbackCustody{}, fmt.Errorf("executor state: verify rollback checkpoint: %w", err)
	}
	if !executorStateTargetExecutesOpenTofu(snapshot.GenerationTarget) {
		return ExecutorStateRollbackCustody{}, fmt.Errorf(
			"executor state: checkpoint generation target %q captured no OpenTofu roots", snapshot.GenerationTarget,
		)
	}
	custody = ExecutorStateRollbackCustody{
		Snapshot: snapshot, Roots: make([]ExecutorStateRootPayload, 0, len(snapshot.RuntimeOpenTofu)),
		Artifacts: make(map[string][]byte, len(snapshot.Artifacts)),
	}
	for _, artifact := range snapshot.Artifacts {
		data, err := readExecutorStateRecoveryBlob(transaction, artifact)
		if err != nil {
			return ExecutorStateRollbackCustody{}, err
		}
		custody.Artifacts[artifact.ID] = data
	}
	for _, captured := range snapshot.RuntimeOpenTofu {
		payload := ExecutorStateRootPayload{ModuleRef: captured.ModuleRef, Root: captured.Root}
		if payload.State, err = readExecutorStateRecoveryBlob(transaction, captured.State); err != nil {
			return ExecutorStateRollbackCustody{}, err
		}
		if payload.Config, err = readExecutorStateRecoveryBlob(transaction, captured.Config); err != nil {
			return ExecutorStateRollbackCustody{}, err
		}
		if captured.Lock != (ExecutorStateBlob{}) {
			if payload.Lock, err = readExecutorStateRecoveryBlob(transaction, captured.Lock); err != nil {
				return ExecutorStateRollbackCustody{}, err
			}
		} else {
			// A valid missing lock can only reach this point through an older
			// Owner-signed checkpoint. Supply the compiled closure before the
			// rollback's readonly initialization.
			if payload.Lock, err = tofu.CanonicalLockForConfiguration(payload.Config); err != nil {
				return ExecutorStateRollbackCustody{}, err
			}
		}
		payload.HasLock = true
		if captured.Compose != (ExecutorStateBlob{}) {
			if payload.Compose, err = readExecutorStateRecoveryBlob(transaction, captured.Compose); err != nil {
				return ExecutorStateRollbackCustody{}, err
			}
			payload.HasCompose = true
		}
		if captured.Environment != (ExecutorStateBlob{}) {
			if payload.Environment, err = readExecutorStateRecoveryBlob(transaction, captured.Environment); err != nil {
				return ExecutorStateRollbackCustody{}, err
			}
			payload.HasEnvironment = true
		}
		custody.Roots = append(custody.Roots, payload)
	}
	return custody, nil
}

// inventoryEquivalentIgnoringFreeSpace reports whether two Inventory
// documents carry the same operator authority. Every lifecycle command
// re-attests each node's measured storageCapacity.freeGiB into the workspace
// file (YAML), while the checkpoint seals the stable projection the current
// plan was generated from (JSON). Neither the encoding nor that measured
// sample is operator authority; every other field must be identical.
func inventoryEquivalentIgnoringFreeSpace(current, captured []byte) bool {
	normalize := func(raw []byte) (any, bool) {
		var decoded any
		if err := yaml.Unmarshal(raw, &decoded); err != nil {
			return nil, false
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			return nil, false
		}
		var document map[string]any
		if err := json.Unmarshal(encoded, &document); err != nil || document == nil {
			return nil, false
		}
		if nodes, ok := document["nodes"].(map[string]any); ok {
			for _, node := range nodes {
				if fields, ok := node.(map[string]any); ok {
					if capacity, ok := fields["storageCapacity"].(map[string]any); ok {
						delete(capacity, "freeGiB")
					}
				}
			}
		}
		return document, true
	}
	left, ok := normalize(current)
	if !ok {
		return false
	}
	right, ok := normalize(captured)
	return ok && reflect.DeepEqual(left, right)
}

// PrepareRollbackRecreatedRoots is the checkpoint-to-current-executor handoff
// after joined generation and before coordinated per-stack execution. It reads
// immutable signed custody again, validates the entire selected batch, restores
// authenticated files and stack definitions, and hands them to their ordinary
// executors under the caller's still-held project locks.
func (store ExecutorStateStore) PrepareRollbackRecreatedRoots(ctx context.Context, workspace, snapshotID string, roots []string, admission *opentofu.GenericRestoreAdmission) error {
	if err := admission.CheckRoots(workspace, roots); err != nil {
		return err
	}
	custody, err := store.LoadRollbackCustody(workspace, snapshotID)
	if err != nil {
		return err
	}
	if custody.Snapshot.GenerationTarget != executorStateTargetTerramate {
		return errors.New("coordinated recreation requires a Terramate checkpoint")
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return err
	}
	graph, err := terramatestackgraph.Parse(custody.Artifacts[terramatestackgraph.ArtifactID])
	if err != nil {
		return err
	}
	if graph.PlanHash != custody.Snapshot.Lineage.Binding.PlanHash {
		return errors.New("checkpoint stack graph differs from captured generation authority")
	}
	layout, err := terramatehost.Plan(graph, custody.Artifacts, owner.Binding.SiteRef, owner.Binding.NodeRef)
	if err != nil {
		return err
	}
	mirror, ok := tofu.PackagedProvidersDir()
	if !ok {
		return tofu.ErrProviderMirrorMissing
	}
	providers, err := tofu.LoadProviderClosure(mirror)
	if err != nil {
		return err
	}
	batch := make([]opentofu.OrdinaryRootRecreation, 0, len(roots))
	for _, root := range roots {
		var stack terramatestackgraph.Stack
		found := false
		for _, id := range layout.Host.RunOrder {
			candidate, ok := layout.Stack(id)
			if ok && candidate.RuntimeRoot == root && candidate.Role != terramatestackgraph.RoleHost {
				stack, found = candidate, true
				break
			}
		}
		if !found {
			return fmt.Errorf("checkpoint has no local executor stack for %s", root)
		}
		var payload ExecutorStateRootPayload
		found = false
		for _, candidate := range custody.Roots {
			if candidate.Root == root {
				payload, found = candidate, true
				break
			}
		}
		if !found || payload.ModuleRef != stack.ModuleRef || stack.InstanceRef == "" {
			return fmt.Errorf("checkpoint root %s differs from its executor generation identity", root)
		}
		marker := opentofu.RootMarker{SchemaVersion: opentofu.RootMarkerSchemaVersion, ModuleRef: stack.ModuleRef, InstanceRef: stack.InstanceRef, RuntimeDir: path.Base(path.Dir(root))}
		switch stack.Role {
		case terramatestackgraph.RoleCore:
			matched := false
			for _, binding := range opentofu.DefaultModuleBindings() {
				if binding.ModuleRef == stack.ModuleRef {
					expected, err := opentofu.RootRelativePath(binding.RuntimeDir)
					if err != nil {
						return err
					}
					if expected == root {
						marker.ComposeProject = binding.ComposeProject
						matched = true
					}
				}
			}
			if !matched || !bytes.Equal(payload.Config, custody.Artifacts[stack.Artifacts.OpenTofu]) {
				return errors.New("checkpoint Core root does not match its ordinary executor artifact")
			}
		case terramatestackgraph.RoleWorkload:
			matched := false
			for _, raw := range custody.Artifacts {
				bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(raw)
				if err != nil || bundle.ModuleRef != stack.ModuleRef || bundle.WorkloadRef != stack.WorkloadRef {
					continue
				}
				project := "stackkit-" + bundle.WorkloadRef + "-" + bundle.NodeRef
				expected, err := opentofu.WorkloadRootRelativePath(project)
				if err != nil {
					return err
				}
				if expected == root && bundle.WorkloadRef == stack.WorkloadRef && bundle.SiteRef == stack.SiteRef && bundle.NodeRef == stack.NodeRef {
					// The Terramate unit and delivery runtime unit have different
					// instance IDs. Ownership belongs to the verified delivery executor.
					if matched && marker.InstanceRef != bundle.InstanceRef {
						return errors.New("checkpoint workload delivery identity is ambiguous")
					}
					marker.Kind, marker.ComposeProject, marker.InstanceRef = opentofu.RootKindWorkload, project, bundle.InstanceRef
					matched = true
				}
			}
			if !matched {
				return errors.New("checkpoint workload has no matching ordinary delivery identity")
			}
			if !payload.HasCompose || !payload.HasEnvironment {
				return errors.New("checkpoint workload lacks its captured Compose/environment ownership")
			}
			marker.Execution, err = opentofu.ValidateOrdinaryWorkloadCheckpoint(marker.ModuleRef, marker.ComposeProject, payload.Config, payload.Compose)
			if err != nil {
				return err
			}
		case terramatestackgraph.RoleEdge, terramatestackgraph.RoleFederation:
			expected, err := opentofu.ModuleRootRelativePath(stack.ModuleRef)
			if err != nil {
				return err
			}
			if expected != root {
				return errors.New("checkpoint contract root differs from its ordinary executor")
			}
			marker.Kind = opentofu.RootKindModule
			marker.InstanceRef, err = checkpointRuntimeInstance(custody.Artifacts["resolved-plan"], stack)
			if err != nil {
				return err
			}
		default:
			return errors.New("checkpoint stack has no ordinary executor recreation")
		}
		stackFile, ok := custody.Artifacts[stack.Artifacts.Stack]
		if !ok || len(stackFile) == 0 {
			return errors.New("checkpoint executor has no runnable stack definition")
		}
		files := map[string][]byte{path.Join(root, opentofu.StateFile): payload.State, path.Join(root, opentofu.ConfigFile): payload.Config,
			path.Join(root, opentofu.StackFile): stackFile, path.Join(root, opentofu.CLIConfigFile): tofu.OfflineCLIConfig(providers.Directory())}
		if payload.HasLock {
			files[path.Join(root, opentofu.LockFile)] = payload.Lock
		} else {
			lock, err := providers.LockForConfiguration(payload.Config)
			if err != nil {
				return err
			}
			files[path.Join(root, opentofu.LockFile)] = lock
		}
		if payload.HasCompose {
			files[path.Join(path.Dir(root), opentofu.ComposeFile)] = payload.Compose
		}
		if payload.HasEnvironment {
			files[path.Join(path.Dir(root), opentofu.EnvFile)] = payload.Environment
		}
		batch = append(batch, opentofu.OrdinaryRootRecreation{RuntimeRoot: root, Marker: marker, Files: files})
	}
	// No earlier root is written until every selected identity and closure passed.
	return admission.RecreateOrdinaryRoots(ctx, snapshotID, batch)
}

// Contract roots belong to the runtime unit, not the separate Terramate unit.
// The captured canonical plan is already covered by the checkpoint signature
// and blob digests. Ambiguous runtime ownership is refused.
func checkpointRuntimeInstance(canonical []byte, stack terramatestackgraph.Stack) (string, error) {
	var plan struct {
		Modules []struct {
			ID          string `json:"id"`
			RenderUnits []struct {
				ApplyMode string `json:"applyMode"`
				Instances []struct {
					ID      string `json:"id"`
					SiteRef string `json:"siteRef"`
					NodeRef string `json:"nodeRef"`
				} `json:"instances"`
			} `json:"renderUnits"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(canonical, &plan); err != nil {
		return "", err
	}
	instance := ""
	for _, module := range plan.Modules {
		if module.ID != stack.ModuleRef {
			continue
		}
		for _, unit := range module.RenderUnits {
			if unit.ApplyMode != "" && unit.ApplyMode != "runtime" {
				continue
			}
			for _, candidate := range unit.Instances {
				if candidate.SiteRef != stack.SiteRef || candidate.NodeRef != stack.NodeRef {
					continue
				}
				if instance != "" && instance != candidate.ID {
					return "", errors.New("checkpoint contract runtime identity is ambiguous")
				}
				instance = candidate.ID
			}
		}
	}
	if instance == "" {
		return "", errors.New("checkpoint contract has no matching runtime executor")
	}
	return instance, nil
}
