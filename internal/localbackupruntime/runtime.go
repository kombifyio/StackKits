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
	workspaceRoot string
	newEngine     engineFactory
}

// New creates the production Docker-backed local backup runtime.
func New(workspaceRoot string) (*Runtime, error) {
	return newWithEngine(workspaceRoot, func() engineAPI {
		return backupexec.NewDockerV2Engine()
	})
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
	sourcePolicy, err := r.sourcePolicy(ctx, scope.OwnerRef, scope.AuthorityRef, scope.Lineage, localbackuppolicy.GovernedSource())
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
	policy, err := r.sourcePolicy(ctx, request.OwnerRef, request.AuthorityRef, request.Lineage, localbackuppolicy.GovernedSource())
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
	policy, err := r.sourcePolicy(ctx, request.OwnerRef, request.AuthorityRef, request.Lineage, localbackuppolicy.GovernedSource())
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
		localbackuppolicy.GovernedSource(),
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
	source := localbackuppolicy.GovernedSource()
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
		Source:      source.ContainerPath,
		Description: "stackkit-local-backup:" + request.OperationID,
		OperationID: request.OperationID,
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
