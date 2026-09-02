// Package localbackupruntime adapts the owner-bound local backup lifecycle to
// the fixed Kopia container runtime without retaining repository secrets.
package localbackupruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	RepositoryID = "kopia:local:basement"
	Backend      = "filesystem"
)

type engineAPI interface {
	EnsureFilesystemRepository(context.Context, string, []byte) (backupexec.RepositoryStatus, error)
	ConfigureSourcePolicy(context.Context, string, []string, []byte) error
	SourcePolicy(context.Context, string, []string, []byte) (backupexec.SourcePolicy, error)
	RepositoryStatus(context.Context, []byte) (backupexec.RepositoryStatus, error)
	FindSnapshot(context.Context, backupexec.SnapshotRequest, []byte) (backupexec.Snapshot, bool, error)
	CreateSnapshot(context.Context, backupexec.SnapshotRequest, []byte) (backupexec.Snapshot, error)
	RestoreSnapshot(context.Context, backupexec.RestoreRequest, []byte) (backupexec.RestoreResult, error)
}

type engineFactory func() engineAPI

// Runtime is the production RepositoryRuntime for the CUE-owned local Kopia
// service. It retains only a confined workspace name and an engine factory.
type Runtime struct {
	workspaceRoot   string
	newEngine       engineFactory
	policy          *localbackuppolicy.Policy
	quiescer        backupexec.ContainerQuiescer
	snapshotSettler func(context.Context) error
}

// New binds all current repository operations to the exact policy
// already verified through the Plan, artifact and Apply authority chain.
// Copy through the canonical codec so caller-owned slices cannot change the
// source selection after the runtime is created.
func New(workspaceRoot string, policy localbackuppolicy.Policy, custody backupexec.ApplicationContainerCustodyVerifier) (*Runtime, error) {
	raw, err := localbackuppolicy.ArtifactBytes(policy)
	if err != nil {
		return nil, fmt.Errorf("localbackupruntime: validate held policy: %w", err)
	}
	held, err := localbackuppolicy.Decode(raw)
	if err != nil {
		return nil, err
	}
	engine, err := backupexec.NewDockerV2EngineForPolicy(held)
	if err != nil {
		return nil, err
	}
	runtime, err := newWithEngine(workspaceRoot, func() engineAPI { return engine })
	if err != nil {
		return nil, err
	}
	runtime.policy = &held
	runtime.quiescer = backupexec.NewDockerV2QuiescerWithApplicationCustody(held.SourceProjection(), custody)
	runtime.snapshotSettler = backupexec.NewDockerV2SnapshotSettler(held.SourceProjection())
	return runtime, nil
}

func (r *Runtime) sourceProjection() localbackuppolicy.Source {
	if r.policy != nil {
		return r.policy.SourceProjection()
	}
	return localbackuppolicy.GovernedSource()
}

func newWithEngine(workspaceRoot string, factory engineFactory) (*Runtime, error) {
	if factory == nil {
		return nil, errors.New("localbackupruntime: engine factory is required")
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("localbackupruntime: open workspace: %w", err)
	}
	absolute := root.Name()
	if err := root.Close(); err != nil {
		return nil, fmt.Errorf("localbackupruntime: close workspace validation root: %w", err)
	}
	return &Runtime{workspaceRoot: absolute, newEngine: factory}, nil
}

func (r *Runtime) Configure(ctx context.Context, configuration backuplifecycle.RepositoryConfiguration) (backuplifecycle.RepositoryReceipt, error) {
	if err := r.ready(ctx); err != nil {
		return backuplifecycle.RepositoryReceipt{}, err
	}
	if err := r.validateAuthority(configuration.OwnerRef, configuration.AuthorityRef, configuration.Lineage); err != nil {
		return backuplifecycle.RepositoryReceipt{}, err
	}
	if _, err := localbackuppolicy.ArtifactBytes(configuration.Policy); err != nil {
		return backuplifecycle.RepositoryReceipt{}, fmt.Errorf("localbackupruntime: reject non-governed policy: %w", err)
	}
	if r.policy != nil && !reflect.DeepEqual(configuration.Policy, *r.policy) {
		return backuplifecycle.RepositoryReceipt{}, errors.New("localbackupruntime: repository configuration differs from the held Plan policy")
	}

	if err := r.validateAuthority(configuration.OwnerRef, configuration.AuthorityRef, configuration.Lineage); err != nil {
		return backuplifecycle.RepositoryReceipt{}, err
	}
	custody, establishedSecret, err := backupcustody.Establish(r.workspaceRoot)
	if err != nil {
		return backuplifecycle.RepositoryReceipt{}, fmt.Errorf("localbackupruntime: establish backup custody: %w", err)
	}
	backupcustody.Clear(establishedSecret)
	if custody.OwnerRef != configuration.OwnerRef {
		return backuplifecycle.RepositoryReceipt{}, errors.New("localbackupruntime: backup custody owner differs from repository configuration")
	}

	secret, err := r.loadAuthorizedSecret(configuration.OwnerRef, configuration.AuthorityRef, configuration.Lineage)
	if err != nil {
		return backuplifecycle.RepositoryReceipt{}, fmt.Errorf("localbackupruntime: load backup custody for repository configuration: %w", err)
	}
	status, engineErr := r.newEngine().EnsureFilesystemRepository(ctx, backupexec.DefaultRepositoryPath, secret)
	backupcustody.Clear(secret)
	if engineErr != nil {
		if diagnostic, safe := backupexec.SafeDiagnostic(engineErr); safe {
			return backuplifecycle.RepositoryReceipt{}, fmt.Errorf(
				"localbackupruntime: configure filesystem repository failed: %s",
				diagnostic,
			)
		}
		return backuplifecycle.RepositoryReceipt{}, errors.New("localbackupruntime: configure filesystem repository failed")
	}
	if !repositoryReady(status) {
		return backuplifecycle.RepositoryReceipt{}, errors.New("localbackupruntime: configured filesystem repository did not report its exact runtime identity")
	}

	source := configuration.Policy.SourceProjection()
	secret, err = r.loadAuthorizedSecret(configuration.OwnerRef, configuration.AuthorityRef, configuration.Lineage)
	if err != nil {
		return backuplifecycle.RepositoryReceipt{}, fmt.Errorf("localbackupruntime: load backup custody for source policy: %w", err)
	}
	engineErr = r.newEngine().ConfigureSourcePolicy(ctx, source.ContainerPath, source.ExcludePaths, secret)
	backupcustody.Clear(secret)
	if engineErr != nil {
		if diagnostic, ok := backupexec.SafeDiagnostic(engineErr); ok {
			return backuplifecycle.RepositoryReceipt{}, fmt.Errorf("localbackupruntime: configure source policy failed: %s", diagnostic)
		}
		return backuplifecycle.RepositoryReceipt{}, errors.New("localbackupruntime: configure source policy failed")
	}
	policyStatus, err := r.sourcePolicy(ctx, configuration.OwnerRef, configuration.AuthorityRef, configuration.Lineage, source)
	if err != nil {
		return backuplifecycle.RepositoryReceipt{}, err
	}
	if !policyStatus.Exact {
		return backuplifecycle.RepositoryReceipt{}, errors.New("localbackupruntime: configured source policy did not read back as the exact governed policy")
	}
	return backuplifecycle.RepositoryReceipt{
		APIVersion:          "stackkit.local-backup-repository-receipt/v1",
		RepositoryID:        RepositoryID,
		Backend:             Backend,
		ConfigurationDigest: backuplifecycle.RepositoryConfigurationDigest(configuration),
	}, nil
}

func (r *Runtime) Status(ctx context.Context, scope backuplifecycle.RepositoryScope) (backuplifecycle.RepositoryStatus, error) {
	if err := r.ready(ctx); err != nil {
		return backuplifecycle.RepositoryStatus{}, err
	}
	if scope.RepositoryID != RepositoryID {
		return backuplifecycle.RepositoryStatus{}, errors.New("localbackupruntime: repository scope differs from the fixed local repository")
	}
	if err := r.validateAuthority(scope.OwnerRef, scope.AuthorityRef, scope.Lineage); err != nil {
		return backuplifecycle.RepositoryStatus{}, err
	}
	secret, err := r.loadAuthorizedSecret(scope.OwnerRef, scope.AuthorityRef, scope.Lineage)
	if err != nil {
		return backuplifecycle.RepositoryStatus{}, fmt.Errorf("localbackupruntime: load backup custody for status: %w", err)
	}
	status, engineErr := r.newEngine().RepositoryStatus(ctx, secret)
	backupcustody.Clear(secret)
	if engineErr != nil {
		return backuplifecycle.RepositoryStatus{}, errors.New("localbackupruntime: repository status failed")
	}
	sourcePolicy, err := r.sourcePolicy(ctx, scope.OwnerRef, scope.AuthorityRef, scope.Lineage, r.sourceProjection())
	if err != nil {
		return backuplifecycle.RepositoryStatus{}, err
	}
	return backuplifecycle.RepositoryStatus{
		RepositoryID: RepositoryID,
		Ready:        repositoryReady(status) && sourcePolicy.Exact,
		Consistency:  backuplifecycle.ConsistencyCrashConsistent,
	}, nil
}

func (r *Runtime) LookupSnapshot(ctx context.Context, request backuplifecycle.RepositorySnapshotRequest) (backuplifecycle.RepositorySnapshotReceipt, bool, error) {
	if err := r.ready(ctx); err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, false, err
	}
	engineRequest, err := r.snapshotRequest(request)
	if err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, false, err
	}
	policy, err := r.sourcePolicy(ctx, request.OwnerRef, request.AuthorityRef, request.Lineage, r.sourceProjection())
	if err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, false, err
	}
	if !policy.Exact {
		return backuplifecycle.RepositorySnapshotReceipt{}, false, errors.New("localbackupruntime: snapshot denied because the effective Kopia policy has drifted")
	}
	secret, err := r.loadAuthorizedSecret(request.OwnerRef, request.AuthorityRef, request.Lineage)
	if err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, false, fmt.Errorf("localbackupruntime: load backup custody for snapshot lookup: %w", err)
	}
	snapshot, found, engineErr := r.newEngine().FindSnapshot(ctx, engineRequest, secret)
	backupcustody.Clear(secret)
	if engineErr != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, false, errors.New("localbackupruntime: snapshot lookup failed")
	}
	if !found {
		return backuplifecycle.RepositorySnapshotReceipt{}, false, nil
	}
	receipt, err := snapshotReceipt(request, engineRequest, snapshot)
	if err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, false, err
	}
	return receipt, true, nil
}

func (r *Runtime) CreateSnapshot(ctx context.Context, request backuplifecycle.RepositorySnapshotRequest) (backuplifecycle.RepositorySnapshotReceipt, error) {
	if err := r.ready(ctx); err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, err
	}
	engineRequest, err := r.snapshotRequest(request)
	if err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, err
	}
	policy, err := r.sourcePolicy(ctx, request.OwnerRef, request.AuthorityRef, request.Lineage, r.sourceProjection())
	if err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, err
	}
	if !policy.Exact {
		return backuplifecycle.RepositorySnapshotReceipt{}, errors.New("localbackupruntime: snapshot denied because the effective Kopia policy has drifted")
	}
	secret, err := r.loadAuthorizedSecret(request.OwnerRef, request.AuthorityRef, request.Lineage)
	if err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, fmt.Errorf("localbackupruntime: load backup custody for snapshot creation: %w", err)
	}
	snapshot, engineErr := r.newEngine().CreateSnapshot(ctx, engineRequest, secret)
	backupcustody.Clear(secret)
	if engineErr != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, errors.New("localbackupruntime: snapshot creation failed")
	}
	return snapshotReceipt(request, engineRequest, snapshot)
}

// CreateSnapshotWithQuiescence creates or resumes one native-v2 snapshot while
// all current writers of governed managed volumes are stopped. The lifecycle
// callback atomically records the exact identities before the first stop and
// after each resumable phase in the existing snapshot operation journal.
func (r *Runtime) CreateSnapshotWithQuiescence(
	ctx context.Context,
	request backuplifecycle.RepositorySnapshotRequest,
	journal backuplifecycle.SnapshotQuiescence,
	persist func(backuplifecycle.SnapshotQuiescence) error,
) (receipt backuplifecycle.RepositorySnapshotReceipt, resultErr error) {
	hasJournal := journal.Phase != "" || len(journal.Containers) > 0
	if hasJournal && persist == nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, errors.New("localbackupruntime: snapshot quiescence journal callback is required for recovery")
	}
	// A supplied journal represents a prior stop boundary. Arm cleanup before
	// readiness and policy gates so a failed resume check cannot leave writers
	// stopped with cleanup silently skipped. restoreQuiescence revalidates the
	// persisted graph and exact identities before it can start anything.
	cleanupNeeded := hasJournal
	resumeSettlementRequired := hasJournal && quiescenceNeedsSettlement(journal.Phase)
	settlementFailed := false
	settlementComplete := false
	snapshotAttempted := false
	defer func() {
		if !cleanupNeeded || r == nil || r.quiescer == nil || persist == nil {
			return
		}
		if settlementFailed {
			return
		}
		if (resumeSettlementRequired && !settlementComplete) || (snapshotAttempted && resultErr != nil && !settlementComplete) {
			if r.snapshotSettler == nil {
				settlementFailed = true
				resultErr = combineQuiescenceErrors(resultErr, errors.New("localbackupruntime: recovery-required: Kopia runtime settlement is required before writer resume"))
				return
			}
			if err := r.authorizeSnapshotSettlement(request); err != nil {
				settlementFailed = true
				resultErr = combineQuiescenceErrors(resultErr, err)
				return
			}
			if err := r.settleSnapshotRuntime(); err != nil {
				settlementFailed = true
				resultErr = combineQuiescenceErrors(resultErr, fmt.Errorf("localbackupruntime: settle Kopia runtime before writer resume: %w", err))
				return
			}
			settlementComplete = true
		}
		cleanupErr := r.restoreQuiescence(journal, persist)
		if resultErr != nil {
			resultErr = combineQuiescenceErrors(resultErr, cleanupErr)
			return
		}
		if cleanupErr != nil {
			receipt = backuplifecycle.RepositorySnapshotReceipt{}
			resultErr = cleanupErr
		}
	}()
	if err := r.ready(ctx); err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, err
	}
	if r.quiescer == nil {
		if hasJournal {
			return backuplifecycle.RepositorySnapshotReceipt{}, errors.New("localbackupruntime: snapshot quiescer is required to recover a journaled operation")
		}
		return r.CreateSnapshot(ctx, request)
	}
	if persist == nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, errors.New("localbackupruntime: snapshot quiescence journal callback is required")
	}
	engineRequest, err := r.snapshotRequest(request)
	if err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, err
	}
	if hasJournal {
		if err := r.validateQuiescence(ctx, journal); err != nil {
			return backuplifecycle.RepositorySnapshotReceipt{}, err
		}
		if journal.Phase == "stopped" || journal.Phase == "snapshot-created" {
			if r.snapshotSettler == nil {
				settlementFailed = true
				return backuplifecycle.RepositorySnapshotReceipt{}, errors.New("localbackupruntime: resumed snapshot requires Kopia runtime settlement")
			}
			if err := r.authorizeSnapshotSettlement(request); err != nil {
				settlementFailed = true
				return backuplifecycle.RepositorySnapshotReceipt{}, err
			}
			if err := r.settleSnapshotRuntime(); err != nil {
				settlementFailed = true
				return backuplifecycle.RepositorySnapshotReceipt{}, fmt.Errorf("localbackupruntime: settle resumed Kopia runtime: %w", err)
			}
			settlementComplete = true
		}
	}
	policy, err := r.sourcePolicy(ctx, request.OwnerRef, request.AuthorityRef, request.Lineage, r.sourceProjection())
	if err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, err
	}
	if !policy.Exact {
		return backuplifecycle.RepositorySnapshotReceipt{}, errors.New("localbackupruntime: snapshot denied because the effective Kopia policy has drifted")
	}
	secret, err := r.loadAuthorizedSecret(request.OwnerRef, request.AuthorityRef, request.Lineage)
	if err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, fmt.Errorf("localbackupruntime: load backup custody for snapshot lookup: %w", err)
	}
	existing, found, engineErr := r.newEngine().FindSnapshot(ctx, engineRequest, secret)
	backupcustody.Clear(secret)
	if engineErr != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, errors.New("localbackupruntime: snapshot lookup failed")
	}
	if found {
		return snapshotReceipt(request, engineRequest, existing)
	}
	if journal.Phase == "" {
		journal, err = r.prepareQuiescence(ctx)
		if err != nil {
			return backuplifecycle.RepositorySnapshotReceipt{}, err
		}
		if err := persist(journal); err != nil {
			return backuplifecycle.RepositorySnapshotReceipt{}, err
		}
		cleanupNeeded = true
	}

	if journal.Phase != "prepared" {
		journal.Phase = "prepared"
		if err := persist(journal); err != nil {
			return backuplifecycle.RepositorySnapshotReceipt{}, err
		}
	}

	if err := r.stopQuiescence(ctx, journal); err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, err
	}
	journal.Phase = "stopped"
	if err := persist(journal); err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, err
	}

	secret, err = r.loadAuthorizedSecret(request.OwnerRef, request.AuthorityRef, request.Lineage)
	if err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, err
	}
	// A resumed stopped journal may already have settled the previous Kopia
	// attempt. A fresh create below opens a new mutation window and therefore
	// needs its own settlement if that attempt fails before cleanup.
	settlementComplete = false
	snapshotAttempted = true
	snapshot, engineErr := r.newEngine().CreateSnapshot(ctx, engineRequest, secret)
	backupcustody.Clear(secret)
	if engineErr != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, errors.New("localbackupruntime: snapshot creation failed")
	}

	journal.Phase = "snapshot-created"
	if err := persist(journal); err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, err
	}
	return snapshotReceipt(request, engineRequest, snapshot)
}

func (r *Runtime) settleSnapshotRuntime() error {
	if r == nil || r.snapshotSettler == nil {
		return errors.New("localbackupruntime: Kopia runtime settlement is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), backupexec.QuickOperationTimeout)
	defer cancel()
	return r.snapshotSettler(ctx)
}

func quiescenceNeedsSettlement(phase string) bool {
	return phase == "stopped" || phase == "snapshot-created"
}

func (r *Runtime) authorizeSnapshotSettlement(request backuplifecycle.RepositorySnapshotRequest) error {
	secret, err := r.loadAuthorizedSecret(request.OwnerRef, request.AuthorityRef, request.Lineage)
	backupcustody.Clear(secret)
	if err != nil {
		return fmt.Errorf("localbackupruntime: authorize Kopia runtime settlement: %w", err)
	}
	return nil
}

func (r *Runtime) prepareQuiescence(ctx context.Context) (backuplifecycle.SnapshotQuiescence, error) {
	observed, err := r.quiescer.ManagedContainers(ctx)
	if err != nil {
		return backuplifecycle.SnapshotQuiescence{}, err
	}
	graphDigest, err := localbackuppolicy.ApplicationGraphDigest(r.sourceProjection())
	if err != nil {
		return backuplifecycle.SnapshotQuiescence{}, fmt.Errorf("localbackupruntime: bind application graph to quiescence journal: %w", err)
	}
	// Only a fresh journal receives a capture start. Retries retain their
	// original value, including nil in legacy journals; they cannot refresh age.
	startedAt := time.Now().UTC()
	journal := backuplifecycle.SnapshotQuiescence{Phase: "prepared", GraphDigest: graphDigest, CaptureStartedAt: &startedAt}
	for _, container := range observed {
		if container.Paused || container.Restarting {
			return backuplifecycle.SnapshotQuiescence{}, fmt.Errorf("localbackupruntime: managed Docker container %q is paused or restarting", container.ID)
		}
		if !container.Running {
			continue
		}
		if container.Lifecycle == "one-shot" {
			return backuplifecycle.SnapshotQuiescence{}, fmt.Errorf("localbackupruntime: one-shot Docker component %q must complete before snapshot quiescence", container.ComponentRef)
		}
		journal.Containers = append(journal.Containers, backuplifecycle.SnapshotQuiescedContainer{
			ID: container.ID, Name: container.Name, WasRunning: true,
			WorkloadRef: container.WorkloadRef, SiteRef: container.SiteRef, NodeRef: container.NodeRef,
			ComposeProject: container.ComposeProject, ComposeService: container.ComposeService,
			ComponentRef: container.ComponentRef, Image: container.Image, StopOrder: container.StopOrder,
			Mounts: snapshotQuiesceMounts(container.Mounts),
		})
	}
	return journal, nil
}

func (r *Runtime) validateQuiescence(ctx context.Context, journal backuplifecycle.SnapshotQuiescence) error {
	if err := validateQuiescencePhase(journal.Phase); err != nil {
		return err
	}
	if journal.Phase == "" && len(journal.Containers) > 0 {
		return errors.New("localbackupruntime: snapshot quiescence journal has containers but no recovery phase")
	}
	if err := r.validateQuiescenceGraph(journal); err != nil {
		return err
	}
	byID := make(map[string]backuplifecycle.SnapshotQuiescedContainer, len(journal.Containers))
	for _, expected := range quiescenceStopOrder(journal) {
		if !expected.WasRunning || expected.ID == "" || expected.Name == "" {
			return errors.New("localbackupruntime: snapshot quiescence journal has incomplete container identity")
		}
		if _, exists := byID[expected.ID]; exists {
			return fmt.Errorf("localbackupruntime: snapshot quiescence journal repeats container %q", expected.ID)
		}
		byID[expected.ID] = expected
		current, err := r.quiescer.InspectContainer(ctx, expected.ID)
		if err != nil {
			return err
		}
		if err := exactQuiescedContainer(expected, current); err != nil {
			return err
		}
		if current.Paused || current.Restarting {
			return fmt.Errorf("localbackupruntime: managed Docker container %q is paused or restarting", current.ID)
		}
	}
	managed, err := r.quiescer.ManagedContainers(ctx)
	if err != nil {
		return err
	}
	managedByID := make(map[string]struct{}, len(managed))
	for _, current := range managed {
		managedByID[current.ID] = struct{}{}
		if current.Paused || current.Restarting {
			return fmt.Errorf("localbackupruntime: managed Docker container %q is paused or restarting", current.ID)
		}
		if current.Running {
			if _, expected := byID[current.ID]; !expected {
				return fmt.Errorf("localbackupruntime: running managed Docker container %q was not in the persisted quiescence set", current.ID)
			}
		}
	}
	for expectedID := range byID {
		if _, present := managedByID[expectedID]; !present {
			return fmt.Errorf("localbackupruntime: persisted container %q no longer has its governed writable volume identity", expectedID)
		}
	}
	return nil
}

func (r *Runtime) stopQuiescence(ctx context.Context, journal backuplifecycle.SnapshotQuiescence) error {
	if err := r.validateQuiescence(ctx, journal); err != nil {
		return err
	}
	for _, expected := range quiescenceStopOrder(journal) {
		current, err := r.quiescer.InspectContainer(ctx, expected.ID)
		if err != nil {
			return err
		}
		if current.Running {
			if err := r.quiescer.StopContainer(ctx, current.ID); err != nil {
				return err
			}
		} else if err := backupexec.ValidateCleanDockerStop(current); err != nil {
			return fmt.Errorf("localbackupruntime: journaled writer %q is not cleanly stopped: %w", current.ID, err)
		}
	}
	managed, err := r.quiescer.ManagedContainers(ctx)
	if err != nil {
		return err
	}
	for _, current := range managed {
		if current.Paused || current.Restarting {
			return fmt.Errorf("localbackupruntime: managed Docker container %q is paused or restarting", current.ID)
		}
		if current.Running {
			return fmt.Errorf("localbackupruntime: managed Docker container %q remains running during snapshot", current.ID)
		}
	}
	return nil
}

func (r *Runtime) restoreQuiescence(journal backuplifecycle.SnapshotQuiescence, persist func(backuplifecycle.SnapshotQuiescence) error) error {
	if err := validateQuiescencePhase(journal.Phase); err != nil {
		return err
	}
	if journal.Phase == "" {
		if len(journal.Containers) > 0 {
			return errors.New("localbackupruntime: snapshot quiescence journal has containers but no recovery phase")
		}
		return nil
	}
	if r == nil || r.quiescer == nil {
		return errors.New("localbackupruntime: snapshot quiescer is unavailable for cleanup")
	}
	if persist == nil {
		return errors.New("localbackupruntime: snapshot quiescence journal callback is required for cleanup")
	}
	if err := r.validateQuiescenceGraph(journal); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), backupexec.QuickOperationTimeout)
	defer cancel()
	expectedByID := make(map[string]backuplifecycle.SnapshotQuiescedContainer, len(journal.Containers))
	for _, expected := range journal.Containers {
		expectedByID[expected.ID] = expected
	}
	managed, err := r.quiescer.ManagedContainers(ctx)
	if err != nil {
		return err
	}
	managedByID := make(map[string]backupexec.QuiesceContainer, len(managed))
	for _, current := range managed {
		if current.Paused || current.Restarting {
			return fmt.Errorf("localbackupruntime: managed Docker container %q is paused or restarting", current.ID)
		}
		managedByID[current.ID] = current
		if current.Running {
			if _, expected := expectedByID[current.ID]; !expected {
				return fmt.Errorf("localbackupruntime: running managed Docker container %q was not in the persisted quiescence set", current.ID)
			}
		}
	}
	for expectedID := range expectedByID {
		if _, present := managedByID[expectedID]; !present {
			return fmt.Errorf("localbackupruntime: persisted container %q no longer has its governed writable volume identity", expectedID)
		}
	}
	currentByID := make(map[string]backupexec.QuiesceContainer, len(journal.Containers))
	for _, expected := range journal.Containers {
		current, err := r.quiescer.InspectContainer(ctx, expected.ID)
		if err != nil {
			return err
		}
		if err := exactQuiescedContainer(expected, current); err != nil {
			return err
		}
		if current.Paused || current.Restarting {
			return fmt.Errorf("localbackupruntime: managed Docker container %q is paused or restarting", current.ID)
		}
		currentByID[expected.ID] = current
	}
	if journal.Phase == "prepared" {
		for _, expected := range quiescenceStopOrder(journal) {
			current := currentByID[expected.ID]
			if !current.Running {
				continue
			}
			settled, err := r.settleQuiescenceStop(ctx, expected)
			if err != nil {
				return err
			}
			currentByID[expected.ID] = settled
		}
	}
	for _, expected := range quiescenceRestoreOrder(journal) {
		current := currentByID[expected.ID]
		if expected.WasRunning && !current.Running {
			if backupexec.IsSuccessfulOneShotCompletion(current) {
				continue
			}
			if current.Lifecycle == "one-shot" {
				return fmt.Errorf("localbackupruntime: recovery-required: one-shot Docker component %q cannot be restarted from the quiescence journal", current.ComponentRef)
			}
			if err := r.quiescer.StartContainer(ctx, current.ID); err != nil {
				return err
			}
		}
	}
	journal.Phase = "restored"
	if err := persist(journal); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) settleQuiescenceStop(ctx context.Context, expected backuplifecycle.SnapshotQuiescedContainer) (backupexec.QuiesceContainer, error) {
	var current backupexec.QuiesceContainer
	var lastStopErr error
	for attempt := 0; attempt < 2; attempt++ {
		var err error
		current, err = r.quiescer.InspectContainer(ctx, expected.ID)
		if err != nil {
			return backupexec.QuiesceContainer{}, err
		}
		if err := exactQuiescedContainer(expected, current); err != nil {
			return backupexec.QuiesceContainer{}, err
		}
		if current.Paused || current.Restarting {
			return backupexec.QuiesceContainer{}, fmt.Errorf("localbackupruntime: managed Docker container %q is paused or restarting", current.ID)
		}
		if !current.Running {
			return current, nil
		}
		stopErr := r.quiescer.StopContainer(ctx, current.ID)
		if stopErr != nil {
			lastStopErr = stopErr
		}
		current, err = r.quiescer.InspectContainer(ctx, expected.ID)
		if err != nil {
			return backupexec.QuiesceContainer{}, err
		}
		if err := exactQuiescedContainer(expected, current); err != nil {
			return backupexec.QuiesceContainer{}, err
		}
		if current.Paused || current.Restarting {
			return backupexec.QuiesceContainer{}, fmt.Errorf("localbackupruntime: managed Docker container %q is paused or restarting", current.ID)
		}
		if !current.Running {
			return current, nil
		}
	}
	if lastStopErr != nil {
		return backupexec.QuiesceContainer{}, lastStopErr
	}
	return backupexec.QuiesceContainer{}, fmt.Errorf("localbackupruntime: managed Docker container %q remains running during cleanup", expected.ID)
}

func exactQuiescedContainer(expected backuplifecycle.SnapshotQuiescedContainer, current backupexec.QuiesceContainer) error {
	if current.ID != expected.ID || current.Name != expected.Name ||
		!reflect.DeepEqual(snapshotQuiesceMounts(current.Mounts), expected.Mounts) {
		return fmt.Errorf("localbackupruntime: Docker container %q identity or mounts differ from the persisted quiescence set", expected.ID)
	}
	if expected.ComposeProject != "" || current.ComposeProject != "" {
		if current.WorkloadRef != expected.WorkloadRef || current.SiteRef != expected.SiteRef || current.NodeRef != expected.NodeRef ||
			current.ComposeProject != expected.ComposeProject || current.ComposeService != expected.ComposeService ||
			current.ComponentRef != expected.ComponentRef || current.Image != expected.Image || current.StopOrder != expected.StopOrder {
			return fmt.Errorf("localbackupruntime: Docker container %q Compose graph identity differs from the persisted quiescence set", expected.ID)
		}
	}
	return nil
}

func (r *Runtime) validateQuiescenceGraph(journal backuplifecycle.SnapshotQuiescence) error {
	graphDigest, err := localbackuppolicy.ApplicationGraphDigest(r.sourceProjection())
	if err != nil {
		return fmt.Errorf("localbackupruntime: validate held application graph for quiescence: %w", err)
	}
	if journal.GraphDigest != graphDigest {
		return fmt.Errorf("localbackupruntime: persisted quiescence graph does not match the held application graph")
	}
	return nil
}

func validateQuiescencePhase(phase string) error {
	switch phase {
	case "", "prepared", "stopped", "snapshot-created", "restored":
		return nil
	default:
		return fmt.Errorf("localbackupruntime: snapshot quiescence journal phase %q cannot be recovered", phase)
	}
}

func quiescenceRestoreOrder(journal backuplifecycle.SnapshotQuiescence) []backuplifecycle.SnapshotQuiescedContainer {
	ordered := quiescenceStopOrder(journal)
	if journal.GraphDigest == "" {
		return ordered
	}
	for left, right := 0, len(ordered)-1; left < right; left, right = left+1, right-1 {
		ordered[left], ordered[right] = ordered[right], ordered[left]
	}
	return ordered
}

// quiescenceStopOrder derives the mutation order from the held graph-bound
// component metadata. A persisted journal is an identity record and may be
// replayed after a crash; its incidental slice order must not become a second
// ordering authority. Core-only journals retain their historical order.
func quiescenceStopOrder(journal backuplifecycle.SnapshotQuiescence) []backuplifecycle.SnapshotQuiescedContainer {
	ordered := append([]backuplifecycle.SnapshotQuiescedContainer(nil), journal.Containers...)
	if journal.GraphDigest == "" {
		return ordered
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		leftGraph := ordered[left].ComposeProject != ""
		rightGraph := ordered[right].ComposeProject != ""
		if leftGraph != rightGraph {
			return leftGraph
		}
		if !leftGraph {
			return false
		}
		if ordered[left].ComposeProject != ordered[right].ComposeProject {
			return ordered[left].ComposeProject < ordered[right].ComposeProject
		}
		if ordered[left].StopOrder != ordered[right].StopOrder {
			return ordered[left].StopOrder < ordered[right].StopOrder
		}
		if ordered[left].ComponentRef != ordered[right].ComponentRef {
			return ordered[left].ComponentRef < ordered[right].ComponentRef
		}
		return ordered[left].ID < ordered[right].ID
	})
	return ordered
}

func snapshotQuiesceMounts(mounts []backupexec.QuiesceMount) []backuplifecycle.SnapshotQuiesceMount {
	converted := make([]backuplifecycle.SnapshotQuiesceMount, len(mounts))
	for index, mount := range mounts {
		converted[index] = backuplifecycle.SnapshotQuiesceMount{
			Type: mount.Type, Name: mount.Name, Source: mount.Source,
			Destination: mount.Destination, RW: mount.RW, Propagation: mount.Propagation,
		}
	}
	return converted
}

func combineQuiescenceErrors(primary, cleanup error) error {
	if primary == nil {
		return cleanup
	}
	if cleanup == nil {
		return primary
	}
	return fmt.Errorf("%v; cleanup failed: %w", primary, cleanup)
}

func (r *Runtime) RestoreSnapshot(
	ctx context.Context,
	request backuplifecycle.RepositoryRestoreRequest,
) (backuplifecycle.RepositoryRestoreReceipt, error) {
	if err := r.ready(ctx); err != nil {
		return backuplifecycle.RepositoryRestoreReceipt{}, err
	}
	if err := r.validateAuthority(request.OwnerRef, request.AuthorityRef, request.AuthorizationLineage); err != nil {
		return backuplifecycle.RepositoryRestoreReceipt{}, err
	}
	currentSourceDigest, err := localbackuppolicy.SourceDigest(r.sourceProjection())
	if err != nil {
		return backuplifecycle.RepositoryRestoreReceipt{}, fmt.Errorf("localbackupruntime: digest held restore source: %w", err)
	}
	if request.SnapshotSourceDigest != currentSourceDigest {
		return backuplifecycle.RepositoryRestoreReceipt{}, errors.New("localbackupruntime: snapshot volume selection differs from the held Plan policy")
	}
	if request.RepositoryID != RepositoryID ||
		request.SnapshotAnchorID == "" ||
		request.SnapshotRequest.RepositoryID != RepositoryID ||
		request.SnapshotReceipt.RepositoryID != RepositoryID ||
		request.SnapshotReceipt.SnapshotID == "" ||
		request.SnapshotReceipt.RequestDigest != backuplifecycle.RepositorySnapshotRequestDigest(request.SnapshotRequest) ||
		request.OperationID == "" ||
		request.StagingPath != backuplifecycle.RestoreStagingPath(request.OperationID) {
		return backuplifecycle.RepositoryRestoreReceipt{}, errors.New("localbackupruntime: restore request differs from the fixed owner-authorized staging contract")
	}
	engineSnapshotRequest, err := r.historicalSnapshotRequest(request.SnapshotRequest)
	if err != nil {
		return backuplifecycle.RepositoryRestoreReceipt{}, err
	}
	policy, err := r.sourcePolicy(
		ctx,
		request.OwnerRef,
		request.AuthorityRef,
		request.AuthorizationLineage,
		r.sourceProjection(),
	)
	if err != nil {
		return backuplifecycle.RepositoryRestoreReceipt{}, err
	}
	if !policy.Exact {
		return backuplifecycle.RepositoryRestoreReceipt{}, errors.New("localbackupruntime: restore denied because the effective Kopia policy has drifted")
	}

	secret, err := r.loadAuthorizedSecret(request.OwnerRef, request.AuthorityRef, request.AuthorizationLineage)
	if err != nil {
		return backuplifecycle.RepositoryRestoreReceipt{}, fmt.Errorf("localbackupruntime: load backup custody for restore lookup: %w", err)
	}
	snapshot, found, engineErr := r.newEngine().FindSnapshot(ctx, engineSnapshotRequest, secret)
	backupcustody.Clear(secret)
	if engineErr != nil {
		return backuplifecycle.RepositoryRestoreReceipt{}, errors.New("localbackupruntime: exact restore snapshot lookup failed")
	}
	if !found {
		return backuplifecycle.RepositoryRestoreReceipt{}, errors.New("localbackupruntime: selected restore snapshot is absent from the governed repository")
	}
	observedReceipt, err := snapshotReceipt(request.SnapshotRequest, engineSnapshotRequest, snapshot)
	if err != nil {
		return backuplifecycle.RepositoryRestoreReceipt{}, err
	}
	if !reflect.DeepEqual(observedReceipt, request.SnapshotReceipt) {
		return backuplifecycle.RepositoryRestoreReceipt{}, errors.New("localbackupruntime: selected repository snapshot differs from its owner-signed anchor receipt")
	}

	engineRestore := backupexec.RestoreRequest{
		SnapshotID:  request.SnapshotReceipt.SnapshotID,
		OperationID: request.OperationID,
		StagingPath: request.StagingPath,
	}
	secret, err = r.loadAuthorizedSecret(request.OwnerRef, request.AuthorityRef, request.AuthorizationLineage)
	if err != nil {
		return backuplifecycle.RepositoryRestoreReceipt{}, fmt.Errorf("localbackupruntime: load backup custody for restore staging: %w", err)
	}
	result, engineErr := r.newEngine().RestoreSnapshot(ctx, engineRestore, secret)
	backupcustody.Clear(secret)
	if engineErr != nil {
		return backuplifecycle.RepositoryRestoreReceipt{}, errors.New("localbackupruntime: verified snapshot staging failed")
	}
	if result.SnapshotID != engineRestore.SnapshotID ||
		result.OperationID != engineRestore.OperationID ||
		result.StagingPath != engineRestore.StagingPath ||
		!result.RepositoryContentVerified {
		return backuplifecycle.RepositoryRestoreReceipt{}, errors.New("localbackupruntime: restore engine result differs from the exact typed request")
	}
	return backuplifecycle.RepositoryRestoreReceipt{
		APIVersion:                "stackkit.local-backup-repository-restore/v1",
		RepositoryID:              RepositoryID,
		SnapshotID:                request.SnapshotReceipt.SnapshotID,
		OperationID:               request.OperationID,
		RequestDigest:             backuplifecycle.RepositoryRestoreRequestDigest(request),
		StagingPath:               request.StagingPath,
		SnapshotContentDigest:     request.SnapshotReceipt.ContentDigest,
		RepositoryContentVerified: true,
		CompletedAt:               time.Now().UTC(),
	}, nil
}

func (r *Runtime) snapshotRequest(request backuplifecycle.RepositorySnapshotRequest) (backupexec.SnapshotRequest, error) {
	if err := r.validateAuthority(request.OwnerRef, request.AuthorityRef, request.Lineage); err != nil {
		return backupexec.SnapshotRequest{}, err
	}
	source := r.sourceProjection()
	if !reflect.DeepEqual(request.Excludes, source.ExcludePaths) {
		return backupexec.SnapshotRequest{}, errors.New("localbackupruntime: current snapshot request excludes differ from the governed policy")
	}
	return snapshotRequestShape(request)
}

func (r *Runtime) historicalSnapshotRequest(
	request backuplifecycle.RepositorySnapshotRequest,
) (backupexec.SnapshotRequest, error) {
	if !localbackuppolicy.IsRecognizedSnapshotSelection(request.Source, request.Excludes) {
		return backupexec.SnapshotRequest{}, errors.New("localbackupruntime: historical snapshot selection is not recognized")
	}
	return snapshotRequestShape(request)
}

func snapshotRequestShape(request backuplifecycle.RepositorySnapshotRequest) (backupexec.SnapshotRequest, error) {
	source := localbackuppolicy.GovernedSource()
	if request.RepositoryID != RepositoryID ||
		request.Consistency != backuplifecycle.ConsistencyCrashConsistent ||
		request.Source != source.ContainerPath ||
		strings.TrimSpace(request.OperationID) == "" {
		return backupexec.SnapshotRequest{}, errors.New("localbackupruntime: snapshot request differs from the fixed crash-consistent local policy")
	}
	return backupexec.SnapshotRequest{
		Source:          source.ContainerPath,
		Description:     "stackkit-local-backup:" + request.OperationID,
		OperationID:     request.OperationID,
		ProtectRecovery: request.ProtectRecovery,
	}, nil
}

func (r *Runtime) sourcePolicy(
	ctx context.Context,
	ownerRef string,
	authorityRef string,
	lineage backuplifecycle.AuthorityLineage,
	source localbackuppolicy.Source,
) (backupexec.SourcePolicy, error) {
	secret, err := r.loadAuthorizedSecret(ownerRef, authorityRef, lineage)
	if err != nil {
		return backupexec.SourcePolicy{}, fmt.Errorf("localbackupruntime: load backup custody for source policy verification: %w", err)
	}
	policy, engineErr := r.newEngine().SourcePolicy(ctx, source.ContainerPath, source.ExcludePaths, secret)
	backupcustody.Clear(secret)
	if engineErr != nil {
		return backupexec.SourcePolicy{}, errors.New("localbackupruntime: effective source policy verification failed")
	}
	return policy, nil
}

func (r *Runtime) validateAuthority(
	ownerRef string,
	authorityRef string,
	lineage backuplifecycle.AuthorityLineage,
) error {
	owner, err := localevidence.LoadOwnerCustody(r.workspaceRoot)
	if err != nil {
		return err
	}
	if ownerRef != owner.OwnerRef || authorityRef != owner.Trust.HumanAuthorityRef {
		return errors.New("localbackupruntime: request differs from established owner authority")
	}
	binding, err := localevidence.LoadOwnerRuntimeBinding(r.workspaceRoot)
	if err != nil {
		return errors.New("localbackupruntime: owner runtime binding is unavailable or invalid")
	}
	if binding.OwnerRef != ownerRef ||
		binding.PocketIDSubject != lineage.PocketIDSubject ||
		localevidence.OwnerRuntimeBindingDigest(binding) != lineage.OwnerBindingDigest {
		return errors.New("localbackupruntime: request differs from the signed PocketID owner binding")
	}
	return nil
}

func (r *Runtime) loadAuthorizedSecret(
	ownerRef string,
	authorityRef string,
	lineage backuplifecycle.AuthorityLineage,
) ([]byte, error) {
	if err := r.validateAuthority(ownerRef, authorityRef, lineage); err != nil {
		return nil, err
	}
	_, secret, err := backupcustody.Load(r.workspaceRoot)
	return secret, err
}

func (r *Runtime) ready(ctx context.Context) error {
	if ctx == nil {
		return errors.New("localbackupruntime: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.workspaceRoot == "" || r.newEngine == nil {
		return errors.New("localbackupruntime: runtime is not initialized")
	}
	return nil
}

func repositoryReady(status backupexec.RepositoryStatus) bool {
	return status.Configured &&
		status.ConfigFile == backupexec.DefaultConfigFile &&
		status.Storage == Backend &&
		status.StoragePath == backupexec.DefaultRepositoryPath
}

func snapshotReceipt(
	request backuplifecycle.RepositorySnapshotRequest,
	engineRequest backupexec.SnapshotRequest,
	snapshot backupexec.Snapshot,
) (backuplifecycle.RepositorySnapshotReceipt, error) {
	if snapshot.ID == "" || snapshot.SourceHost == "" ||
		snapshot.SourceHost != localbackuppolicy.Hostname ||
		snapshot.SourcePath != engineRequest.Source ||
		snapshot.Description != engineRequest.Description ||
		snapshot.OperationID != engineRequest.OperationID ||
		snapshot.StartTime.IsZero() || snapshot.EndTime.IsZero() ||
		snapshot.EndTime.Before(snapshot.StartTime) || snapshot.TotalSize < 0 {
		return backuplifecycle.RepositorySnapshotReceipt{}, errors.New("localbackupruntime: Kopia snapshot differs from the exact typed request")
	}
	snapshot.StartTime = snapshot.StartTime.UTC()
	snapshot.EndTime = snapshot.EndTime.UTC()
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return backuplifecycle.RepositorySnapshotReceipt{}, errors.New("localbackupruntime: encode typed Kopia snapshot")
	}
	sum := sha256.Sum256(encoded)
	return backuplifecycle.RepositorySnapshotReceipt{
		APIVersion:    "stackkit.local-backup-repository-snapshot/v1",
		RepositoryID:  RepositoryID,
		SnapshotID:    snapshot.ID,
		OperationID:   request.OperationID,
		RequestDigest: backuplifecycle.RepositorySnapshotRequestDigest(request),
		ContentDigest: "sha256:" + hex.EncodeToString(sum[:]),
		Consistency:   backuplifecycle.ConsistencyCrashConsistent,
		CreatedAt:     snapshot.EndTime,
	}, nil
}

var _ backuplifecycle.RepositoryRuntime = (*Runtime)(nil)
var _ backuplifecycle.SnapshotQuiesceRuntime = (*Runtime)(nil)
