package opentofu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// GenericRestoreAdmission holds the destination project locks until generic
// recovery has finished its writes and engine calls. It is local to one
// operation and must be closed before invoking a governed recovery child.
type GenericRestoreAdmission struct {
	fs          *confinedfs.Root
	transaction *confinedfs.Transaction
	locks       []*confinedfs.OutputLock
	roots       map[string]bool
	closeErr    error
}

// A context borrows a live operation lease; only its acquiring caller closes it.
// It is never transferred to a governed executable or to a detached process.
type genericAdmissionContextKey struct{}

func (a *GenericRestoreAdmission) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, genericAdmissionContextKey{}, a)
}

// WithoutGenericRestoreAdmission keeps cancellation and lifecycle authority,
// but requires a governed child to acquire its normal executor admission.
func WithoutGenericRestoreAdmission(ctx context.Context) context.Context {
	return context.WithValue(ctx, genericAdmissionContextKey{}, struct{}{})
}

// AdmitGenericOperation acquires the complete current/destination batch at a
// public execution entry, or checks a nested call against its caller's lease.
// The returned release is a no-op for borrowed admission.
func AdmitGenericOperation(ctx context.Context, workspace string, roots []string) (context.Context, func() error, error) {
	return admitGenericOperation(ctx, workspace, roots, true)
}

// AdmitSelectedGenericOperation admits a sealed operation's explicit ordinary
// destinations without discovering unrelated managers. A containing lease is
// still borrowed and rechecked in full. This does not select or recover owners.
func AdmitSelectedGenericOperation(ctx context.Context, workspace string, roots []string) (context.Context, func() error, error) {
	return admitGenericOperation(ctx, workspace, roots, false)
}

// A single workload owns only its explicit project. Inventory, recovery and
// workspace execution use discovery instead. Either scope borrows a containing
// lease in full: CheckRoots rechecks all its members and rejects extensions.
func admitGenericOperation(ctx context.Context, workspace string, roots []string, discover bool) (context.Context, func() error, error) {
	if ctx == nil {
		return ctx, nil, errors.New("generic operation requires a context")
	}
	if a, ok := ctx.Value(genericAdmissionContextKey{}).(*GenericRestoreAdmission); ok {
		if err := a.CheckRoots(workspace, roots); err != nil {
			return ctx, nil, err
		}
		return ctx, func() error { return nil }, nil
	}
	a, err := acquireGenericRestoreAdmission(workspace, roots, discover)
	if err != nil {
		return ctx, nil, err
	}
	return a.Context(ctx), a.Close, nil
}

// Close releases all project locks in reverse order, then their held workspace.
func (a *GenericRestoreAdmission) Close() error {
	if a == nil {
		return nil
	}
	for index := len(a.locks) - 1; index >= 0; index-- {
		a.closeErr = errors.Join(a.closeErr, a.locks[index].Release())
	}
	a.locks = nil
	if a.transaction != nil {
		a.closeErr = errors.Join(a.closeErr, a.transaction.Close())
		a.transaction = nil
	}
	if a.fs != nil {
		a.closeErr = errors.Join(a.closeErr, a.fs.Close())
		a.fs = nil
	}
	return a.closeErr
}

// AcquireGenericRestoreAdmission takes the same per-project OS locks as
// provider Apply/adopt before reading destination markers. The fixed set is
// the current runtime directories plus every explicit checkpoint destination,
// including absent roots. A new unrelated project is not a restore destination.
func AcquireGenericRestoreAdmission(workspaceRoot string, runtimeRoots []string) (_ *GenericRestoreAdmission, returnErr error) {
	return acquireGenericRestoreAdmission(workspaceRoot, runtimeRoots, true)
}

func acquireGenericRestoreAdmission(workspaceRoot string, runtimeRoots []string, discover bool) (_ *GenericRestoreAdmission, returnErr error) {
	fs, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return nil, err
	}
	admission := &GenericRestoreAdmission{fs: fs}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, admission.Close())
		}
	}()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return nil, err
	}
	admission.transaction = tx
	roots, err := genericRestoreDestinations(tx, runtimeRoots, discover)
	if err != nil {
		return nil, err
	}
	admission.roots = make(map[string]bool, len(roots))
	for _, root := range roots {
		admission.roots[root] = true
	}
	// Sort the lock identities themselves, not roots whose path suffix could
	// change the order of project names that share a prefix.
	projects := make([]string, 0, len(roots))
	for _, root := range roots {
		projects = append(projects, path.Dir(root))
	}
	sort.Strings(projects)
	// Use the parent lock even if the destination directory does not exist.
	for _, project := range projects {
		duplicate := false
		for _, held := range admission.locks {
			same, err := confinedfs.OutputLockRootsEqual(held.OutputRoot(), project)
			if err != nil {
				return nil, err
			}
			duplicate = duplicate || same
		}
		if duplicate {
			continue
		}
		lock, err := tx.TryAcquireOutputLock(project)
		if err != nil {
			return nil, fmt.Errorf("lock generic restore destination %s: %w", project, err)
		}
		admission.locks = append(admission.locks, lock)
	}
	if err := requireGenericRestoreDestinations(tx, roots); err != nil {
		return nil, err
	}
	// Admit every staging domain before any earlier destination can be written.
	// Unselected absent current roots need no preparation publication.
	explicit := make(map[string]bool, len(runtimeRoots))
	for _, root := range runtimeRoots {
		explicit[root] = true
	}
	for _, root := range roots {
		exists, _, err := tx.Exists(root)
		if err != nil {
			return nil, err
		}
		if !exists && !explicit[root] {
			continue
		}
		marker, _, err := tx.Exists(path.Join(root, MarkerFile))
		if err != nil {
			return nil, err
		}
		prepared, _, err := tx.Exists(path.Join(root, RootPreparationFile))
		if err != nil {
			return nil, err
		}
		if err := tx.CheckSiblingStaging(root, !marker || prepared); err != nil {
			return nil, err
		}
	}
	return admission, nil
}

// requireRoots prevents a nested writer from extending the lease's fixed set.
func (a *GenericRestoreAdmission) requireRoots(roots []string) error {
	if a == nil || a.transaction == nil {
		return errors.New("generic restore admission is closed")
	}
	for _, root := range roots {
		if !a.roots[root] {
			return fmt.Errorf("generic restore destination %s is outside the held admission", root)
		}
	}
	return requireGenericRestoreDestinations(a.transaction, roots)
}

// CheckRoots validates reuse by a nested lifecycle reader/writer. Reuse is
// workspace-bound and cannot extend the already locked destination set.
func (a *GenericRestoreAdmission) CheckRoots(workspace string, roots []string) error {
	if a == nil || a.transaction == nil {
		return errors.New("generic restore admission is closed")
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	if absolute != a.transaction.Name() {
		return errors.New("generic restore admission belongs to another workspace")
	}
	if err := a.requireRoots(roots); err != nil {
		return err
	}
	all := make([]string, 0, len(a.roots))
	for root := range a.roots {
		all = append(all, root)
	}
	sort.Strings(all)
	return requireGenericRestoreDestinations(a.transaction, all)
}

// RequireGenericRestoreAdmission is a point check for nested writers whose
// entry point already holds AcquireGenericRestoreAdmission. It checks only
// the supplied destinations and never reacquires their project locks.
func RequireGenericRestoreAdmission(workspaceRoot string, runtimeRoots []string) (returnErr error) {
	fs, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, fs.Close()) }()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, tx.Close()) }()
	roots, err := genericRestoreDestinations(tx, runtimeRoots, false)
	if err != nil {
		return err
	}
	return requireGenericRestoreDestinations(tx, roots)
}

func genericRestoreDestinations(tx *confinedfs.Transaction, runtimeRoots []string, discover bool) ([]string, error) {
	if err := tx.VerifyPathIdentity(); err != nil {
		return nil, err
	}
	roots := make(map[string]bool, len(runtimeRoots))
	for _, root := range runtimeRoots {
		// Do not clean untrusted input into a different destination.
		if root != path.Clean(root) || !strings.HasPrefix(root, ".stackkit/runtime/") || path.Base(root) != RootDirName || strings.Contains(root, "\\") {
			return nil, fmt.Errorf("generic restore destination %q is not an OpenTofu runtime root", root)
		}
		roots[root] = true
	}
	if discover {
		// Recovery may restore a pre-OpenTofu checkpoint, or skip root writes while
		// invoking a captured manager. Include current roots even in those cases.
		for _, parent := range []string{".stackkit/runtime", ".stackkit/runtime/" + ApplicationsDir, ".stackkit/runtime/" + ModulesDir} {
			exists, info, err := tx.Exists(parent)
			if err != nil {
				return nil, fmt.Errorf("inspect restore destination %s: %w", parent, err)
			}
			if !exists {
				continue
			}
			if !info.IsDir() {
				return nil, fmt.Errorf("restore destination %s must be a plain directory", parent)
			}
			entries, err := os.ReadDir(filepath.Join(tx.Name(), filepath.FromSlash(parent)))
			if err != nil {
				return nil, fmt.Errorf("list restore destination %s: %w", parent, err)
			}
			for _, entry := range entries {
				if parent == ".stackkit/runtime" && (entry.Name() == ApplicationsDir || entry.Name() == ModulesDir) {
					continue
				}
				// Only runtime directories can own a root. Other runtime files are not
				// destinations of this restore. Symlinks are checked by Exists below.
				if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
					roots[path.Join(parent, entry.Name(), RootDirName)] = true
				}
			}
		}
	}
	ordered := make([]string, 0, len(roots))
	for root := range roots {
		ordered = append(ordered, root)
	}
	sort.Strings(ordered)
	return ordered, nil
}

func requireGenericRestoreDestinations(tx *confinedfs.Transaction, roots []string) error {
	if err := tx.VerifyPathIdentity(); err != nil {
		return err
	}
	for _, root := range roots {
		exists, info, err := tx.Exists(root)
		if err != nil {
			return fmt.Errorf("inspect restore destination %s: %w", root, err)
		}
		// Missing roots may be recreated from a historical checkpoint. An existing
		// root without a readable, valid marker has unknown custody and is refused.
		if !exists {
			continue
		}
		if !info.IsDir() {
			return fmt.Errorf("restore destination %s must be a plain directory", root)
		}
		if _, _, err := tx.ReadStableBounded(path.Join(root, MarkerFile), 64<<10); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if _, proofErr := readRootPreparation(tx, root); proofErr == nil {
					continue
				} else {
					return fmt.Errorf("generic restore cannot establish destination custody for %s: %w", root, proofErr)
				}
			}
			return fmt.Errorf("generic restore cannot establish destination custody for %s: %w", root, err)
		}
		marker, err := ReadRootMarker(filepath.Join(tx.Name(), filepath.FromSlash(root)))
		if err != nil {
			return fmt.Errorf("generic restore cannot establish destination custody for %s: %w", root, err)
		}
		if IsPlatformProviderExecution(marker.Execution) {
			return fmt.Errorf("%s destination %s requires explicit signed provider recovery; generic recovery is unavailable", marker.Execution, root)
		}
	}
	return tx.VerifyPathIdentity()
}

// RootPreparationFile authenticates a closed file set while no executor marker
// exists. Ordinary Apply validates its exact deployment before handing that
// preparation to a current executor marker. The receipt then remains an origin
// record and keeps durability required on retries; it is no longer a live file
// allowlist. It contains digests only, never a second secret backup.
const RootPreparationFile = "stackkit-root-preparation.json"
const RootPreparationScaffold = "terramate-scaffold"
const RootPreparationRecovery = "checkpoint-restore"

// An owned-origin receipt requires the executor marker; it never authorizes
// markerless recovery or arbitrary additions after ownership handoff.
const rootPreparationOwned = "executor-owned"

type preparedRootFile struct {
	SHA256   []string `json:"sha256"`
	Optional bool     `json:"optional,omitempty"`
}
type rootPreparation struct {
	SchemaVersion string                                    `json:"schemaVersion"`
	Root          string                                    `json:"root"`
	Purpose       string                                    `json:"purpose"`
	Authority     string                                    `json:"authority"`
	Files         map[string]preparedRootFile               `json:"files"`
	Signature     localevidence.OwnerExecutorStateSignature `json:"signature"`
}

func preparationBytes(proof rootPreparation) ([]byte, error) {
	proof.Signature = localevidence.OwnerExecutorStateSignature{}
	return json.Marshal(proof)
}

func allowedPreparationFile(root, file string) bool {
	for _, name := range []string{ConfigFile, StateFile, LockFile, StackFile, CLIConfigFile} {
		if file == path.Join(root, name) {
			return true
		}
	}
	return file == path.Join(path.Dir(root), ComposeFile) || file == path.Join(path.Dir(root), EnvFile)
}

func readRootPreparation(tx *confinedfs.Transaction, root string) (rootPreparation, error) {
	raw, _, err := tx.ReadStableBounded(path.Join(root, RootPreparationFile), 64<<10)
	if err != nil {
		return rootPreparation{}, err
	}
	var proof rootPreparation
	if err := json.Unmarshal(raw, &proof); err != nil {
		return proof, err
	}
	if proof.SchemaVersion != "stackkit.opentofu-root-preparation/v1" || proof.Root != root || proof.Authority == "" || len(proof.Files) == 0 ||
		(proof.Purpose != RootPreparationScaffold && proof.Purpose != RootPreparationRecovery) {
		return proof, errors.New("root preparation does not identify this destination")
	}
	payload, err := preparationBytes(proof)
	if err != nil {
		return proof, err
	}
	if err := localevidence.VerifyOwnerExecutorState(tx.Name(), payload, proof.Signature); err != nil {
		return proof, fmt.Errorf("authenticate root preparation: %w", err)
	}
	entries, err := tx.Walk(root)
	if err != nil {
		return proof, err
	}
	for _, entry := range entries {
		if entry.Path == root || entry.Path == path.Join(root, RootPreparationFile) {
			continue
		}
		if _, ok := proof.Files[entry.Path]; !ok || !entry.Info.Mode().IsRegular() {
			return proof, fmt.Errorf("unowned file in prepared root %s", entry.Path)
		}
	}
	for file, identity := range proof.Files {
		if !allowedPreparationFile(root, file) || len(identity.SHA256) == 0 {
			return proof, errors.New("root preparation has an invalid file closure")
		}
		data, _, err := tx.ReadStableBounded(file, maxOpenTofuStateBytes)
		if errors.Is(err, os.ErrNotExist) && identity.Optional {
			continue
		}
		if err != nil {
			return proof, err
		}
		digest := digestBytes(data)
		clear(data)
		matched := false
		for _, expected := range identity.SHA256 {
			matched = matched || digest == expected
		}
		if !matched {
			return proof, fmt.Errorf("prepared root file %s differs from authenticated ownership", file)
		}
	}
	return proof, tx.VerifyPathIdentity()
}

// Stage outside runtime roots: failed atomic writes deliberately retain their
// temporary entries, which must never become unsigned members of a root's
// authenticated closure. Only this operation's exclusive private tree is removed.
func writePreparedRootFile(tx *confinedfs.Transaction, view confinedfs.View, stagingParent, destination string, data []byte, durable bool) (returnErr error) {
	stage, err := tx.CreatePrivateDirectoryAt(stagingParent, "stackkit-root-write-")
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, tx.RemoveTree(stage)) }()
	file := path.Join(stage, "payload")
	staged, err := view.WriteAtomic0600(file, data)
	if err != nil {
		return err
	}
	if durable && !staged.DirectorySynced {
		return fmt.Errorf("authenticated root staging requires directory durability: %w", errors.ErrUnsupported)
	}
	installed, err := tx.ReplaceRegularFile(file, destination)
	if err != nil {
		return err
	}
	if durable && !installed.DirectorySynced {
		return fmt.Errorf("authenticated root publication requires directory durability: %w", errors.ErrUnsupported)
	}
	return nil
}

func writeRootPreparation(tx *confinedfs.Transaction, view confinedfs.View, where string, proof rootPreparation) error {
	payload, err := preparationBytes(proof)
	if err != nil {
		return err
	}
	proof.Signature, err = localevidence.SignOwnerExecutorState(tx.Name(), payload)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(proof)
	if err != nil {
		return err
	}
	return writePreparedRootFile(tx, view, path.Dir(where), path.Join(where, RootPreparationFile), encoded, true)
}

// WriteGenericRootFiles runs only under the destination project lock and an
// already verified plan/checkpoint. New roots are staged with signed ownership
// and published by a no-replace directory rename. Existing prepared roots get
// a signed old/new digest transition before their first write. These transitions
// require supported directory flushes; unsupported durability fails closed.
// Successful flushes support retry at file boundaries, without claiming hardware
// or filesystem crash guarantees beyond the operating system's durability API.
// Recovery never synthesizes a historical executor marker.
func WriteGenericRootFiles(tx *confinedfs.Transaction, view confinedfs.View, root, purpose, authority string, files map[string][]byte) (written []string, returnErr error) {
	if _, err := genericRestoreDestinations(tx, []string{root}, false); err != nil {
		return nil, err
	}
	if purpose != RootPreparationScaffold && purpose != RootPreparationRecovery || authority == "" || len(files) == 0 {
		return nil, errors.New("generic root preparation needs verified authority and files")
	}
	for file := range files {
		if !allowedPreparationFile(root, file) {
			return nil, fmt.Errorf("file %s is outside the prepared root closure", file)
		}
	}
	exists, _, err := tx.Exists(root)
	if err != nil {
		return nil, err
	}
	var old rootPreparation
	hasMarker := false
	if exists {
		markerExists, _, err := tx.Exists(path.Join(root, MarkerFile))
		if err != nil {
			return nil, err
		}
		if markerExists {
			marker, err := ReadRootMarker(filepath.Join(tx.Name(), filepath.FromSlash(root)))
			if err != nil {
				return nil, err
			}
			if IsPlatformProviderExecution(marker.Execution) {
				return nil, errors.New("explicit signed provider recovery is required")
			}
			hasMarker = true
		} else {
			old, err = readRootPreparation(tx, root)
			if err != nil {
				return nil, err
			}
		}
	}
	prepared, _, err := tx.Exists(path.Join(root, RootPreparationFile))
	if err != nil {
		return nil, err
	}
	requireDurability := !hasMarker || prepared
	if err := tx.CheckSiblingStaging(root, requireDurability); err != nil {
		return nil, err
	}
	ordered := make([]string, 0, len(files))
	for file := range files {
		ordered = append(ordered, file)
	}
	sort.Slice(ordered, func(i, j int) bool {
		state := path.Join(root, StateFile)
		if ordered[i] == state {
			return false
		}
		if ordered[j] == state {
			return true
		}
		return ordered[i] < ordered[j]
	})
	if hasMarker {
		for _, file := range ordered {
			if err := writePreparedRootFile(tx, view, path.Dir(root), file, files[file], requireDurability); err != nil {
				return nil, err
			}
			written = append(written, file)
		}
		return written, nil
	}
	proof := rootPreparation{SchemaVersion: "stackkit.opentofu-root-preparation/v1", Root: root, Purpose: purpose, Authority: authority, Files: make(map[string]preparedRootFile)}
	for file, identity := range old.Files {
		proof.Files[file] = identity
	}
	for file, data := range files {
		proof.Files[file] = preparedRootFile{SHA256: []string{digestBytes(data)}}
	}
	if exists {
		previous, err := preparationBytes(old)
		if err != nil {
			return nil, err
		}
		next, err := preparationBytes(proof)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(previous, next) {
			return nil, nil
		}
	}
	if !exists {
		if err := tx.CheckSiblingStaging(root, true); err != nil {
			return nil, err
		}
		if err := tx.MkdirAll(path.Dir(root), 0o700); err != nil {
			return nil, err
		}
		stage, err := tx.CreatePrivateDirectoryAt(path.Dir(root), "stackkit-root-recovery-")
		if err != nil {
			return nil, err
		}
		defer func() {
			if stage != "" {
				returnErr = errors.Join(returnErr, tx.RemoveTree(stage))
			}
		}()
		if err := tx.MkdirAll(path.Dir(root), 0o700); err != nil {
			return nil, err
		}
		for _, file := range ordered {
			destination := file
			if path.Dir(file) == root {
				destination = path.Join(stage, path.Base(file))
			}
			if path.Dir(file) != root {
				if err := writePreparedRootFile(tx, view, path.Dir(root), destination, files[file], true); err != nil {
					return nil, err
				}
			} else {
				result, err := view.WriteAtomic0600(destination, files[file])
				if err != nil {
					return nil, err
				}
				if !result.DirectorySynced {
					return nil, fmt.Errorf("authenticated root staging requires directory durability: %w", errors.ErrUnsupported)
				}
			}
			written = append(written, file)
		}
		if err := writeRootPreparation(tx, view, stage, proof); err != nil {
			return nil, err
		}
		installed, err := tx.Rename(stage, root)
		if installed {
			stage = ""
		}
		if err != nil {
			return nil, err
		}
		supported, err := tx.SyncDirectory(path.Dir(root))
		if err != nil {
			return nil, err
		}
		if !supported {
			return nil, fmt.Errorf("authenticated root publication requires directory durability: %w", errors.ErrUnsupported)
		}
		return append(written, path.Join(root, RootPreparationFile)), nil
	}
	transition := proof
	transition.Files = make(map[string]preparedRootFile, len(proof.Files))
	for file, identity := range proof.Files {
		transition.Files[file] = identity
	}
	for file, oldIdentity := range old.Files {
		next := transition.Files[file]
		next.SHA256 = append([]string{}, next.SHA256...)
		for _, digest := range oldIdentity.SHA256 {
			found := false
			for _, present := range next.SHA256 {
				found = found || present == digest
			}
			if !found {
				next.SHA256 = append(next.SHA256, digest)
			}
		}
		next.Optional = oldIdentity.Optional
		transition.Files[file] = next
	}
	for file := range files {
		if _, ok := old.Files[file]; !ok {
			identity := transition.Files[file]
			identity.Optional = true
			transition.Files[file] = identity
		}
	}
	if err := writeRootPreparation(tx, view, root, transition); err != nil {
		return nil, err
	}
	for _, file := range ordered {
		if err := writePreparedRootFile(tx, view, path.Dir(root), file, files[file], true); err != nil {
			return nil, err
		}
		written = append(written, file)
	}
	if err := writeRootPreparation(tx, view, root, proof); err != nil {
		return nil, err
	}
	return append(written, path.Join(root, RootPreparationFile)), nil
}

// handoffRestoredRoot installs the current ordinary executor identity derived
// from verified checkpoint generation metadata, never an alleged old marker.
// A markerless root must first authenticate its exact restored file closure.
// The caller holds the complete destination lease and verified checkpoint.
func (a *GenericRestoreAdmission) handoffRestoredRoot(root, checkpoint string, marker RootMarker) error {
	if err := a.requireRoots([]string{root}); err != nil {
		return err
	}
	if err := a.CheckRoots(a.transaction.Name(), []string{root}); err != nil {
		return err
	}
	if IsPlatformProviderExecution(marker.Execution) {
		return errors.New("platform-manager recreation requires explicit recovery")
	}
	if err := validateRootMarker(filepath.Join(a.transaction.Name(), filepath.FromSlash(root)), marker); err != nil {
		return err
	}
	exists, _, err := a.transaction.Exists(path.Join(root, MarkerFile))
	if err != nil {
		return err
	}
	if !exists {
		proof, err := readRootPreparation(a.transaction, root)
		if err != nil {
			return err
		}
		if proof.Purpose != RootPreparationRecovery || proof.Authority != checkpoint {
			return errors.New("restored root handoff differs from the verified checkpoint")
		}
	}
	if err := a.transaction.CheckSiblingStaging(root, true); err != nil {
		return err
	}
	encoded, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	view, err := a.fs.View(".")
	if err != nil {
		return err
	}
	return writePreparedRootFile(a.transaction, view, path.Dir(root), path.Join(root, MarkerFile), append(encoded, '\n'), true)
}

// OrdinaryRootRecreation is a verified checkpoint payload and the identity of
// its current ordinary executor. The receipt authenticates file preparation;
// the handoff marker identifies the executor that may now mutate those files.
type OrdinaryRootRecreation struct {
	RuntimeRoot string
	Marker      RootMarker
	Files       map[string][]byte
}

// RecreateOrdinaryRoots retains the admission's exact held workspace handles.
// Every destination and publication domain passes before the first write.
func (a *GenericRestoreAdmission) RecreateOrdinaryRoots(ctx context.Context, checkpoint string, batch []OrdinaryRootRecreation) error {
	var roots []string
	for _, item := range batch {
		roots = append(roots, item.RuntimeRoot)
	}
	if err := a.requireRoots(roots); err != nil {
		return err
	}
	if err := a.CheckRoots(a.transaction.Name(), roots); err != nil {
		return err
	}
	for _, item := range batch {
		if IsPlatformProviderExecution(item.Marker.Execution) {
			return errors.New("platform-manager recreation requires explicit recovery")
		}
		if err := validateRootMarker(filepath.Join(a.transaction.Name(), filepath.FromSlash(item.RuntimeRoot)), item.Marker); err != nil {
			return err
		}
		if err := a.transaction.CheckSiblingStaging(item.RuntimeRoot, true); err != nil {
			return err
		}
	}
	view, err := a.fs.View(".")
	if err != nil {
		return err
	}
	for _, item := range batch {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := WriteGenericRootFiles(a.transaction, view, item.RuntimeRoot, RootPreparationRecovery, checkpoint, item.Files); err != nil {
			return err
		}
		if err := a.handoffRestoredRoot(item.RuntimeRoot, checkpoint, item.Marker); err != nil {
			return err
		}
	}
	return nil
}

// handoffPreparedExecutor establishes current ordinary ownership before Core or
// contract native side effects. The exact artifact identity was validated by
// the creator; this is not a historical checkpoint marker.
func (a *GenericRestoreAdmission) handoffPreparedExecutor(root string, marker RootMarker, authority string) error {
	if err := a.requireRoots([]string{root}); err != nil {
		return err
	}
	if err := a.CheckRoots(a.transaction.Name(), []string{root}); err != nil {
		return err
	}
	if IsPlatformProviderExecution(marker.Execution) || !validDigest(authority) {
		return errors.New("ordinary executor handoff requires its exact artifact authority")
	}
	if err := validateRootMarker(filepath.Join(a.transaction.Name(), filepath.FromSlash(root)), marker); err != nil {
		return err
	}
	exists, _, err := a.transaction.Exists(root)
	if err != nil {
		return err
	}
	if !exists {
		return publishOrdinaryRoot(a.fs, a.transaction, root, marker, authority)
	}
	marked, _, err := a.transaction.Exists(path.Join(root, MarkerFile))
	if err != nil {
		return err
	}
	if marked {
		prepared, _, err := a.transaction.Exists(path.Join(root, RootPreparationFile))
		if err != nil {
			return err
		}
		if prepared {
			return a.transaction.CheckSiblingStaging(root, true)
		}
		return nil
	}
	if _, err := readRootPreparation(a.transaction, root); err != nil {
		return err
	}
	if err := a.transaction.CheckSiblingStaging(root, true); err != nil {
		return err
	}
	encoded, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	view, err := a.fs.View(".")
	if err != nil {
		return err
	}
	return writePreparedRootFile(a.transaction, view, path.Dir(root), path.Join(root, MarkerFile), append(encoded, '\n'), true)
}

// publishOrdinaryRoot publishes the current marker and signed digest-only
// ownership receipt together, under the caller's held destination lease. Staging
// stays on the destination filesystem, outside the authenticated root closure.
func publishOrdinaryRoot(fs *confinedfs.Root, tx *confinedfs.Transaction, root string, marker RootMarker, authority string) error {
	if IsPlatformProviderExecution(marker.Execution) || !validDigest(authority) {
		return errors.New("ordinary root publication requires exact executor authority")
	}
	if err := validateRootMarker(filepath.Join(tx.Name(), filepath.FromSlash(root)), marker); err != nil {
		return err
	}
	if err := tx.CheckSiblingStaging(root, true); err != nil {
		return err
	}
	encoded, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	view, err := fs.View(".")
	if err != nil {
		return err
	}
	// Publish the first root and its truthful current executor identity together.
	// Staging is on the project filesystem, outside the authenticated closure.
	if err := tx.MkdirAll(path.Dir(root), 0700); err != nil {
		return err
	}
	stage, err := tx.CreatePrivateDirectoryAt(path.Dir(root), "stackkit-executor-root-")
	if err != nil {
		return err
	}
	defer func() {
		if stage != "" {
			_ = tx.RemoveTree(stage)
		}
	}()
	result, err := view.WriteAtomic0600(path.Join(stage, MarkerFile), append(encoded, '\n'))
	if err != nil {
		return err
	}
	if !result.DirectorySynced {
		return fmt.Errorf("ordinary ownership publication requires directory durability: %w", errors.ErrUnsupported)
	}
	proof := rootPreparation{SchemaVersion: "stackkit.opentofu-root-preparation/v1", Root: root,
		Purpose: rootPreparationOwned, Authority: authority,
		Files: map[string]preparedRootFile{path.Join(root, MarkerFile): {SHA256: []string{digestBytes(append(encoded, '\n'))}}}}
	if err := writeRootPreparation(tx, view, stage, proof); err != nil {
		return err
	}
	installed, err := tx.Rename(stage, root)
	if installed {
		stage = ""
	}
	if err != nil {
		return err
	}
	supported, err := tx.SyncDirectory(path.Dir(root))
	if err != nil {
		return err
	}
	if !supported {
		return fmt.Errorf("ordinary ownership publication requires directory durability: %w", errors.ErrUnsupported)
	}
	return nil
}
