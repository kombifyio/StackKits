package runtimeexecutorlocal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// CloudOffsiteBackupSourceResolver reloads the current verified Plan and its
// signed local owner/source binding. Endpoint and credentials never enter it.
type CloudOffsiteBackupSourceResolver func(context.Context) (localbackuppolicy.Policy, backupcustody.S3TargetAuthority, error)

type osCloudOffsiteBackupOperations struct {
	workspace string
	resolve   CloudOffsiteBackupSourceResolver
	newEngine func(localbackuppolicy.Policy) (backupexec.V2Engine, error)
	cleanup   func(context.Context, localbackuppolicy.Policy, string) error
	settle    func(context.Context, localbackuppolicy.Policy) error
	mu        sync.Mutex
	verified  map[string]CloudOffsiteBackupObservation
}

func NewOSCloudOffsiteBackupOperations(workspace string, resolve CloudOffsiteBackupSourceResolver) (CloudOffsiteBackupOperations, error) {
	if resolve == nil {
		return nil, errors.New("Cloud offsite backup requires current owner/source resolution")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return &osCloudOffsiteBackupOperations{workspace: root.Name(), resolve: resolve, newEngine: backupexec.NewDockerV2OffsiteEngineForPolicy, cleanup: backupexec.CleanupCloudVerificationRestore, settle: func(ctx context.Context, p localbackuppolicy.Policy) error {
		return backupexec.NewDockerV2SnapshotSettler(p.SourceProjection())(ctx)
	}, verified: map[string]CloudOffsiteBackupObservation{}}, nil
}

func (o *osCloudOffsiteBackupOperations) target(ctx context.Context, expected CloudOffsiteBackupExpectation) (localbackuppolicy.Policy, backupexec.V2Engine, backupcustody.S3TargetMaterial, error) {
	var zero backupexec.V2Engine
	var empty backupcustody.S3TargetMaterial
	policy, authority, err := o.resolve(ctx)
	if err != nil {
		return policy, zero, empty, errors.New("Cloud offsite source authority does not verify")
	}
	b := authority.Binding
	if policy.Source.CoreModuleRef != localbackuppolicy.CloudCoreModuleRef || b.StackID != expected.StackID || b.SiteRef != expected.SiteRef ||
		!reflect.DeepEqual(b.TargetNodeRefs, []string{expected.NodeRef}) || b.BindingRef != expected.BindingRef || b.BindingHash != expected.BindingHash ||
		b.BackupTargetRef != expected.BackupTargetRef || b.CustodyAttestationRef != expected.CustodyAttestationRef || b.ValidUntil != expected.ValidUntil {
		return policy, zero, empty, errors.New("Cloud offsite request differs from current owner target")
	}
	digest, err := localbackuppolicy.SourceDigest(policy.SourceProjection())
	if err != nil || digest != authority.SourceDigest {
		return policy, zero, empty, errors.New("Cloud offsite source custody differs from current policy")
	}
	material, err := backupcustody.LoadS3Target(o.workspace, authority)
	if err != nil {
		return policy, zero, empty, errors.New("Cloud offsite target custody does not verify")
	}
	engine, err := o.newEngine(policy)
	if err != nil {
		backupcustody.Clear(material.Passphrase)
		return policy, zero, empty, err
	}
	return policy, engine, material, nil
}

func cloudOffsiteRepository(material backupcustody.S3TargetMaterial) backupexec.S3Repository {
	return backupexec.S3Repository{Endpoint: material.Endpoint, Bucket: material.Bucket, Prefix: material.Prefix, Region: material.Region, AccessKeyID: material.AccessKeyID, SecretAccessKey: material.SecretAccessKey}
}

func cloudOffsiteOSObservation(e CloudOffsiteBackupExpectation, operation, status string) CloudOffsiteBackupObservation {
	return CloudOffsiteBackupObservation{Operation: operation, Status: status, PolicyDigest: e.PolicyDigest, RequestDigest: e.RequestDigest, ArtifactDigest: e.ArtifactDigest, StateDigest: e.StateDigest, EvaluatedAt: e.EvaluatedAt, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), StackID: e.StackID, SiteRef: e.SiteRef, NodeRef: e.NodeRef, ExecutionChannelRef: e.ExecutionChannelRef, BindingRef: e.BindingRef, BindingHash: e.BindingHash, BackupTargetRef: e.BackupTargetRef, CustodyAttestationRef: e.CustodyAttestationRef}
}

func (o *osCloudOffsiteBackupOperations) BindOffsiteBackupTarget(ctx context.Context, p CloudOffsiteBackupApplyPolicy) (CloudOffsiteBackupObservation, error) {
	e := CloudOffsiteBackupExpectation{PolicyDigest: p.PolicyDigest, RequestDigest: p.RequestDigest, ArtifactDigest: p.ArtifactDigest, StateDigest: p.StateDigest, EvaluatedAt: p.EvaluatedAt, StackID: p.StackID, SiteRef: p.SiteRef, NodeRef: p.NodeRef, ExecutionChannelRef: p.ExecutionChannelRef, BindingRef: p.BindingRef, BindingHash: p.BindingHash, BackupTargetRef: p.BackupTargetRef, CustodyAttestationRef: p.CustodyAttestationRef, ValidUntil: p.ValidUntil}
	policy, engine, material, err := o.target(ctx, e)
	if err != nil {
		return CloudOffsiteBackupObservation{}, err
	}
	defer backupcustody.Clear(material.Passphrase)
	if _, err := engine.ConnectS3Repository(ctx, cloudOffsiteRepository(material), material.Passphrase); err != nil {
		return CloudOffsiteBackupObservation{}, errors.New("Cloud offsite repository connection failed")
	}
	if err := engine.ConfigureSourcePolicy(ctx, policy.Source.ContainerPath, policy.Source.ExcludePaths, material.Passphrase); err != nil {
		return CloudOffsiteBackupObservation{}, errors.New("Cloud offsite source policy did not configure")
	}
	return cloudOffsiteOSObservation(e, "bind-offsite-backup-target", "bound"), nil
}

func (o *osCloudOffsiteBackupOperations) RemoveObsoleteOffsiteBackupBindings(ctx context.Context, e CloudOffsiteBackupExpectation) (CloudOffsiteBackupObservation, error) {
	_, engine, material, err := o.target(ctx, e)
	if err != nil {
		return CloudOffsiteBackupObservation{}, err
	}
	defer backupcustody.Clear(material.Passphrase)
	// One fixed offsite configuration is admitted. A foreign repository is
	// rejected by ConnectS3Repository; it is never disconnected or replaced.
	if _, err := engine.ConnectS3Repository(ctx, cloudOffsiteRepository(material), material.Passphrase); err != nil {
		return CloudOffsiteBackupObservation{}, errors.New("Cloud offsite singleton binding did not reconcile")
	}
	return cloudOffsiteOSObservation(e, "remove-obsolete-offsite-backup-binding", "reconciled"), nil
}

func (o *osCloudOffsiteBackupOperations) VerifyOffsiteBackupTarget(ctx context.Context, e CloudOffsiteBackupExpectation) (result CloudOffsiteBackupObservation, operationErr error) {
	policy, engine, material, err := o.target(ctx, e)
	if err != nil {
		return CloudOffsiteBackupObservation{}, err
	}
	defer backupcustody.Clear(material.Passphrase)
	if _, err := engine.ConnectS3Repository(ctx, cloudOffsiteRepository(material), material.Passphrase); err != nil {
		return CloudOffsiteBackupObservation{}, errors.New("Cloud offsite target changed before backup")
	}
	if status, err := engine.SourcePolicy(ctx, policy.Source.ContainerPath, policy.Source.ExcludePaths, material.Passphrase); err != nil || !status.Exact {
		return CloudOffsiteBackupObservation{}, errors.New("Cloud offsite source policy has drifted")
	}
	operation := "cloud-offsite-" + strings.TrimPrefix(e.RequestDigest, "sha256:")
	snapshot, err := engine.CreateSnapshot(ctx, backupexec.SnapshotRequest{Source: policy.Source.ContainerPath, Description: "StackKits Cloud offsite verification", OperationID: operation}, material.Passphrase)
	if err != nil {
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backupexec.QuickOperationTimeout)
		defer cancel()
		return CloudOffsiteBackupObservation{}, errors.Join(errors.New("Cloud offsite snapshot did not complete"), o.settle(settleCtx, policy))
	}
	restoreCompleted := false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backupexec.QuickOperationTimeout)
		defer cancel()
		if !restoreCompleted {
			if err := o.settle(cleanupCtx, policy); err != nil {
				operationErr = errors.Join(operationErr, errors.New("Cloud offsite interrupted restore did not settle"))
				result = CloudOffsiteBackupObservation{}
				return
			}
		}
		if err := o.cleanup(cleanupCtx, policy, operation); err != nil {
			operationErr = errors.Join(operationErr, err)
			result = CloudOffsiteBackupObservation{}
			o.mu.Lock()
			delete(o.verified, e.RequestDigest)
			o.mu.Unlock()
		}
	}()
	restore, err := engine.RestoreSnapshot(ctx, backupexec.RestoreRequest{SnapshotID: snapshot.ID, OperationID: operation, StagingPath: localbackuppolicy.RestorePathForOperation(operation)}, material.Passphrase)
	if err != nil || !restore.RepositoryContentVerified {
		return CloudOffsiteBackupObservation{}, errors.New("Cloud offsite staged restore did not verify")
	}
	restoreCompleted = true
	backupDigest, err := o.persistProof("backup-observation", snapshot)
	if err != nil {
		return CloudOffsiteBackupObservation{}, err
	}
	restoreDigest, err := o.persistProof("restore-readback", restore)
	if err != nil {
		return CloudOffsiteBackupObservation{}, err
	}
	observation := cloudOffsiteOSObservation(e, "verify-offsite-backup-target", "ready")
	observation.BackupObservationDigest, observation.RestoreReadbackDigest = backupDigest, restoreDigest
	observation.BackupObservationRef = "backup-observation://sha256/" + strings.TrimPrefix(backupDigest, "sha256:")
	observation.RestoreReadbackRef = "restore-readback://sha256/" + strings.TrimPrefix(restoreDigest, "sha256:")
	o.mu.Lock()
	o.verified[e.RequestDigest] = observation
	o.mu.Unlock()
	return observation, nil
}

func (o *osCloudOffsiteBackupOperations) CommitCloudOffsiteBackupEvidence(ctx context.Context, evidence CloudOffsiteBackupEvidence) (CloudOffsiteBackupEvidenceReceipt, error) {
	if err := ctx.Err(); err != nil {
		return CloudOffsiteBackupEvidenceReceipt{}, err
	}
	o.mu.Lock()
	verified, ok := o.verified[evidence.RequestDigest]
	o.mu.Unlock()
	if !ok || !reflect.DeepEqual(verified, evidence.Verify) {
		return CloudOffsiteBackupEvidenceReceipt{}, errors.New("Cloud offsite evidence lacks actual local backup and restore")
	}
	digest, err := o.persistProof("evidence", evidence)
	if err != nil {
		return CloudOffsiteBackupEvidenceReceipt{}, err
	}
	return CloudOffsiteBackupEvidenceReceipt{EvidenceDigest: digest, CommittedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}

func (o *osCloudOffsiteBackupOperations) persistProof(kind string, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest, err := digestCloudOffsiteBackup(value)
	if err != nil {
		return "", err
	}
	signature, err := localevidence.SignOwnerPolicyState(o.workspace, raw)
	if err != nil {
		return "", err
	}
	record, err := json.Marshal(struct {
		Payload   json.RawMessage                         `json:"payload"`
		Signature localevidence.OwnerPolicyStateSignature `json:"signature"`
	}{raw, signature})
	if err != nil {
		return "", err
	}
	root, err := confinedfs.Open(o.workspace)
	if err != nil {
		return "", err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return "", err
	}
	defer tx.Close()
	directory := ".stackkit/evidence/cloud-offsite-backup/" + kind
	if err := tx.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	path := directory + "/" + strings.TrimPrefix(digest, "sha256:") + ".json"
	if err := tx.WriteFileExclusive(path, record, 0600); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	if err := backupcustody.ProtectPrivatePath(filepath.Join(o.workspace, filepath.FromSlash(path)), false); err != nil {
		return "", err
	}
	stored, _, err := tx.ReadStable(path)
	if err != nil || !reflect.DeepEqual(stored, record) {
		return "", errors.New("Cloud offsite proof did not read back")
	}
	return digest, nil
}

var _ CloudOffsiteBackupOperations = (*osCloudOffsiteBackupOperations)(nil)
