package upgradelifecycle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localbackupruntime"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	ExecutorStateSnapshotAPIVersion  = "stackkit.executor-state-snapshot/v1"
	executorStateOperationAPIVersion = "stackkit.executor-state-operation/v1"
	executorStateRoot                = ".stackkit/upgrades/executor-state"
	basementCoreComposeArtifactPath  = "platform/basement-core/compose.yaml"
	basementCoreRuntimeComposePath   = ".stackkit/runtime/basement-core/compose.yaml"
)

var (
	executorStateOperationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	executorStateIDPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	executorStateModePattern      = regexp.MustCompile(`^0[0-7]{3}$`)
	executorStateVersionPattern   = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)
)

type ExecutorStateRelease struct {
	Kit                    string                `json:"kit"`
	Version                string                `json:"version"`
	Channel                releaseindex.Channel  `json:"channel"`
	Platform               releaseindex.Platform `json:"platform"`
	ArchiveSHA256          string                `json:"archiveSha256"`
	SBOMSHA256             string                `json:"sbomSha256"`
	AttestationSHA256      string                `json:"attestationSha256"`
	TrustedRootSHA256      string                `json:"trustedRootSha256"`
	IndexSHA256            string                `json:"indexSha256"`
	IndexAttestationSHA256 string                `json:"indexAttestationSha256"`
	AttestationIssuer      string                `json:"attestationIssuer"`
	CertificateIdentity    string                `json:"certificateIdentity"`
	AttestationSubject     string                `json:"attestationSubject"`
	PredicateType          string                `json:"predicateType"`
}

type ExecutorStateBlobInput struct {
	ID   string
	Path string
	Mode string
	Data []byte
}

type ExecutorStateBlob struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
}

type ExecutorStateExecutableInput struct {
	Blob ExecutorStateBlobInput
}

type ExecutorStateExecutable struct {
	Version string            `json:"version"`
	Blob    ExecutorStateBlob `json:"blob"`
}

type ExecutorStateCaptureInput struct {
	OperationID         string
	GenerationTarget    string
	Release             releaseindex.VerifiedInstallation
	Executable          ExecutorStateExecutableInput
	Lineage             backuplifecycle.AuthorityLineage
	StackSpec           ExecutorStateBlobInput
	Inventory           *ExecutorStateBlobInput
	Artifacts           []ExecutorStateBlobInput
	RuntimeCompose      ExecutorStateBlobInput
	KopiaSnapshotAnchor backuplifecycle.SnapshotAnchor
}

type executorStateCaptureInput = ExecutorStateCaptureInput

type verifiedExecutorStateCaptureToken struct{}

// VerifiedExecutorStateCapture is an immutable authority handle created only
// after re-verifying the exact current Plan/Generation/Apply/Owner/Backup
// closure. No package outside upgradelifecycle can invoke state persistence
// from caller-assembled inputs.
type VerifiedExecutorStateCapture struct {
	token   *verifiedExecutorStateCaptureToken
	input   executorStateCaptureInput
	release ExecutorStateRelease
}

type ExecutorStateSnapshot struct {
	APIVersion          string                                    `json:"apiVersion"`
	ID                  string                                    `json:"id"`
	RequestHash         string                                    `json:"requestHash"`
	OwnerRef            string                                    `json:"ownerRef"`
	OperationID         string                                    `json:"operationId"`
	GenerationTarget    string                                    `json:"generationTarget"`
	Release             ExecutorStateRelease                      `json:"release"`
	Executable          ExecutorStateExecutable                   `json:"executable"`
	Lineage             backuplifecycle.AuthorityLineage          `json:"lineage"`
	StackSpec           ExecutorStateBlob                         `json:"stackSpec"`
	Inventory           *ExecutorStateBlob                        `json:"inventory,omitempty"`
	Artifacts           []ExecutorStateBlob                       `json:"artifacts"`
	RuntimeCompose      ExecutorStateBlob                         `json:"runtimeCompose"`
	KopiaSnapshotAnchor backuplifecycle.SnapshotAnchor            `json:"kopiaSnapshotAnchor"`
	CapturedAt          time.Time                                 `json:"capturedAt"`
	Signature           localevidence.OwnerExecutorStateSignature `json:"signature"`
}

type executorStateOperation struct {
	APIVersion  string `json:"apiVersion"`
	OperationID string `json:"operationId"`
	RequestHash string `json:"requestHash"`
	SnapshotID  string `json:"snapshotId"`
}

type ExecutorStateStore struct {
	Now func() time.Time
}

func (store ExecutorStateStore) Capture(
	workspaceRoot string,
	verified VerifiedExecutorStateCapture,
) (ExecutorStateSnapshot, error) {
	if verified.token == nil {
		return ExecutorStateSnapshot{}, errors.New("executor state: verified current-state authority is required")
	}
	input := verified.input
	owner, err := localevidence.LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return ExecutorStateSnapshot{}, fmt.Errorf("executor state: verify local Owner custody: %w", err)
	}
	if err := verifyExecutorStateSnapshotAnchor(
		workspaceRoot, owner.OwnerRef, input.Lineage, input.KopiaSnapshotAnchor,
	); err != nil {
		return ExecutorStateSnapshot{}, err
	}
	release := verified.release
	if err := validateExecutorStateRelease(release); err != nil {
		return ExecutorStateSnapshot{}, errors.New("executor state: verified current-state release authority is required")
	}
	snapshot, payloads, err := prepareExecutorStateSnapshot(owner.OwnerRef, release, input)
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	snapshot.RequestHash, err = executorStateRequestHash(snapshot)
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}

	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	defer func() { _ = transaction.Close() }()
	lock, err := transaction.TryAcquireOutputLock(executorStateRoot)
	if err != nil {
		return ExecutorStateSnapshot{}, fmt.Errorf("executor state: acquire store lock: %w", err)
	}
	defer func() { _ = lock.Release() }()
	view, err := root.View(".")
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}

	existing, exists, err := readExecutorStateOperation(transaction, input.OperationID)
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	if exists {
		if existing.RequestHash != snapshot.RequestHash {
			return ExecutorStateSnapshot{}, errors.New("executor state: operation ID is already bound to a different recovery request")
		}
		loaded, err := store.loadWithTransaction(workspaceRoot, transaction, existing.SnapshotID)
		if err != nil {
			return ExecutorStateSnapshot{}, err
		}
		if loaded.RequestHash != existing.RequestHash || loaded.OperationID != existing.OperationID {
			return ExecutorStateSnapshot{}, errors.New("executor state: operation journal differs from its snapshot")
		}
		return loaded, nil
	}

	now := store.Now
	if now == nil {
		now = time.Now
	}
	snapshot.CapturedAt = now().UTC()
	if snapshot.CapturedAt.IsZero() {
		return ExecutorStateSnapshot{}, errors.New("executor state: capture time is required")
	}
	snapshot.ID, err = executorStateSnapshotID(snapshot)
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	signingBytes, err := executorStateSigningBytes(snapshot)
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	snapshot.Signature, err = localevidence.SignOwnerExecutorState(workspaceRoot, signingBytes)
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	if err := store.verifySnapshot(workspaceRoot, transaction, snapshot, false); err != nil {
		return ExecutorStateSnapshot{}, err
	}

	for _, payload := range payloads {
		if err := persistExecutorStateBlob(transaction, view, payload.identity, payload.data); err != nil {
			return ExecutorStateSnapshot{}, err
		}
	}
	canonical, err := resolvedplan.CanonicalJSON(snapshot)
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	snapshotPath, err := executorStateSnapshotPath(snapshot.ID)
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	if err := persistExecutorStateCAS(transaction, view, snapshotPath, canonical); err != nil {
		return ExecutorStateSnapshot{}, err
	}
	if err := store.verifySnapshot(workspaceRoot, transaction, snapshot, true); err != nil {
		return ExecutorStateSnapshot{}, err
	}
	operation := executorStateOperation{
		APIVersion:  executorStateOperationAPIVersion,
		OperationID: input.OperationID,
		RequestHash: snapshot.RequestHash,
		SnapshotID:  snapshot.ID,
	}
	canonicalOperation, err := resolvedplan.CanonicalJSON(operation)
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	if err := persistExecutorStateCAS(transaction, view, executorStateOperationPath(input.OperationID), canonicalOperation); err != nil {
		return ExecutorStateSnapshot{}, err
	}
	committed, exists, err := readExecutorStateOperation(transaction, input.OperationID)
	if err != nil || !exists || committed != operation {
		return ExecutorStateSnapshot{}, errors.New("executor state: committed operation marker does not verify")
	}
	if err := store.verifySnapshot(workspaceRoot, transaction, snapshot, true); err != nil {
		return ExecutorStateSnapshot{}, err
	}
	return snapshot, nil
}

func (store ExecutorStateStore) Load(workspaceRoot, snapshotID string) (ExecutorStateSnapshot, error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	defer func() { _ = transaction.Close() }()
	lock, err := transaction.TryAcquireOutputLock(executorStateRoot)
	if err != nil {
		return ExecutorStateSnapshot{}, fmt.Errorf("executor state: acquire store lock: %w", err)
	}
	defer func() { _ = lock.Release() }()
	return store.loadWithTransaction(workspaceRoot, transaction, snapshotID)
}

func (store ExecutorStateStore) loadWithTransaction(
	workspaceRoot string,
	transaction *confinedfs.Transaction,
	snapshotID string,
) (ExecutorStateSnapshot, error) {
	snapshotPath, err := executorStateSnapshotPath(snapshotID)
	if err != nil {
		return ExecutorStateSnapshot{}, err
	}
	raw, info, err := transaction.ReadStable(snapshotPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return ExecutorStateSnapshot{}, fmt.Errorf("executor state: read bounded snapshot: %w", err)
	}
	var snapshot ExecutorStateSnapshot
	if err := decodeExactJSON(raw, &snapshot); err != nil {
		return ExecutorStateSnapshot{}, fmt.Errorf("executor state: decode snapshot: %w", err)
	}
	if snapshot.ID != snapshotID {
		return ExecutorStateSnapshot{}, errors.New("executor state: snapshot content address differs from requested ID")
	}
	operation, exists, err := readExecutorStateOperation(transaction, snapshot.OperationID)
	if err != nil || !exists {
		return ExecutorStateSnapshot{}, errors.New("executor state: snapshot is not committed by an operation marker")
	}
	if operation.OperationID != snapshot.OperationID ||
		operation.RequestHash != snapshot.RequestHash ||
		operation.SnapshotID != snapshot.ID {
		return ExecutorStateSnapshot{}, errors.New("executor state: operation marker differs from snapshot")
	}
	if err := store.verifySnapshot(workspaceRoot, transaction, snapshot, true); err != nil {
		return ExecutorStateSnapshot{}, err
	}
	canonical, err := resolvedplan.CanonicalJSON(snapshot)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ExecutorStateSnapshot{}, errors.New("executor state: snapshot is not canonical")
	}
	return snapshot, nil
}

func (store ExecutorStateStore) Verify(workspaceRoot string, snapshot ExecutorStateSnapshot) error {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Close() }()
	lock, err := transaction.TryAcquireOutputLock(executorStateRoot)
	if err != nil {
		return fmt.Errorf("executor state: acquire store lock: %w", err)
	}
	defer func() { _ = lock.Release() }()
	operation, exists, err := readExecutorStateOperation(transaction, snapshot.OperationID)
	if err != nil || !exists ||
		operation.OperationID != snapshot.OperationID ||
		operation.RequestHash != snapshot.RequestHash ||
		operation.SnapshotID != snapshot.ID {
		return errors.New("executor state: snapshot is not committed by its operation marker")
	}
	return store.verifySnapshot(workspaceRoot, transaction, snapshot, true)
}

type executorStatePayload struct {
	identity ExecutorStateBlob
	data     []byte
}

func prepareExecutorStateSnapshot(
	ownerRef string,
	release ExecutorStateRelease,
	input executorStateCaptureInput,
) (ExecutorStateSnapshot, []executorStatePayload, error) {
	if !executorStateOperationPattern.MatchString(input.OperationID) {
		return ExecutorStateSnapshot{}, nil, errors.New("executor state: operation ID must be 1-128 portable characters")
	}
	if input.GenerationTarget != "compose" {
		return ExecutorStateSnapshot{}, nil, errors.New("executor state: unsupported_state_snapshot: only the active Compose executor is supported")
	}
	if err := validateExecutorStateRelease(release); err != nil {
		return ExecutorStateSnapshot{}, nil, err
	}
	if err := validateExecutorStateLineage(input.Lineage); err != nil {
		return ExecutorStateSnapshot{}, nil, err
	}
	payloadInputs := []ExecutorStateBlobInput{input.Executable.Blob, input.StackSpec}
	if input.Inventory != nil {
		payloadInputs = append(payloadInputs, *input.Inventory)
	}
	payloadInputs = append(payloadInputs, input.Artifacts...)
	payloadInputs = append(payloadInputs, input.RuntimeCompose)
	if len(payloadInputs) > defaultMaxFiles {
		return ExecutorStateSnapshot{}, nil, errors.New("executor state: recovery closure contains too many blobs")
	}
	payloads := make([]executorStatePayload, 0, len(payloadInputs))
	seenIDs := make(map[string]struct{}, len(payloadInputs))
	seenPaths := make(map[string]struct{}, len(payloadInputs))
	var totalBytes int64
	for _, candidate := range payloadInputs {
		payload, err := prepareExecutorStateBlob(candidate)
		if err != nil {
			return ExecutorStateSnapshot{}, nil, err
		}
		if _, duplicate := seenIDs[payload.identity.ID]; duplicate {
			return ExecutorStateSnapshot{}, nil, errors.New("executor state: duplicate blob ID")
		}
		seenIDs[payload.identity.ID] = struct{}{}
		pathKey := strings.ToLower(payload.identity.Path)
		for existing := range seenPaths {
			if executorStatePathsCollide(existing, pathKey) {
				return ExecutorStateSnapshot{}, nil, errors.New("executor state: case-folded blob path hierarchy collision")
			}
		}
		seenPaths[pathKey] = struct{}{}
		totalBytes += int64(len(payload.data))
		if totalBytes > defaultMaxExtractBytes {
			return ExecutorStateSnapshot{}, nil, errors.New("executor state: recovery closure exceeds the bounded byte budget")
		}
		payloads = append(payloads, payload)
	}
	executable := payloads[0].identity
	if executable.Path != executorStateExecutablePath(release.Platform) || executable.Mode != "0755" {
		return ExecutorStateSnapshot{}, nil, errors.New("executor state: recovery executable path or mode differs from its release platform")
	}
	stackSpec := payloads[1].identity
	index := 2
	var inventory *ExecutorStateBlob
	if input.Inventory != nil {
		value := payloads[index].identity
		inventory = &value
		index++
	}
	artifacts := make([]ExecutorStateBlob, len(input.Artifacts))
	for artifactIndex := range input.Artifacts {
		artifacts[artifactIndex] = payloads[index+artifactIndex].identity
	}
	runtimeCompose := payloads[len(payloads)-1].identity
	if runtimeCompose.Path != basementCoreRuntimeComposePath {
		return ExecutorStateSnapshot{}, nil, errors.New("executor state: runtime Compose path is not the governed Basement runtime path")
	}
	sourceMatches := 0
	for _, artifact := range artifacts {
		if artifact.Path == basementCoreComposeArtifactPath {
			sourceMatches++
			if artifact.SHA256 != runtimeCompose.SHA256 {
				return ExecutorStateSnapshot{}, nil, errors.New("executor state: runtime Compose differs from the governed generation artifact")
			}
		}
	}
	if sourceMatches != 1 {
		return ExecutorStateSnapshot{}, nil, errors.New("executor state: runtime Compose requires exactly one governed source artifact")
	}
	sort.Slice(artifacts, func(left, right int) bool {
		if artifacts[left].ID == artifacts[right].ID {
			return artifacts[left].Path < artifacts[right].Path
		}
		return artifacts[left].ID < artifacts[right].ID
	})
	return ExecutorStateSnapshot{
		APIVersion: ExecutorStateSnapshotAPIVersion,
		OwnerRef:   ownerRef, OperationID: input.OperationID,
		GenerationTarget: input.GenerationTarget, Release: release,
		Executable: ExecutorStateExecutable{
			Version: release.Version, Blob: executable,
		},
		Lineage: input.Lineage, StackSpec: stackSpec, Inventory: inventory,
		Artifacts: artifacts, RuntimeCompose: runtimeCompose,
		KopiaSnapshotAnchor: input.KopiaSnapshotAnchor,
	}, payloads, nil
}

func prepareExecutorStateBlob(input ExecutorStateBlobInput) (executorStatePayload, error) {
	if !executorStateIDPattern.MatchString(input.ID) {
		return executorStatePayload{}, errors.New("executor state: blob ID must be portable")
	}
	canonicalPath, err := confinedfs.ValidatePortablePath(filepathToSlash(input.Path))
	if err != nil {
		return executorStatePayload{}, fmt.Errorf("executor state: blob path: %w", err)
	}
	if !executorStateModePattern.MatchString(input.Mode) {
		return executorStatePayload{}, errors.New("executor state: blob mode must be a four-digit portable mode")
	}
	if len(input.Data) == 0 || len(input.Data) > 512<<20 {
		return executorStatePayload{}, errors.New("executor state: blob bytes must be non-empty and bounded")
	}
	sum := sha256.Sum256(input.Data)
	return executorStatePayload{
		identity: ExecutorStateBlob{
			ID: input.ID, Path: canonicalPath, Mode: input.Mode,
			SHA256: "sha256:" + hex.EncodeToString(sum[:]),
		},
		data: append([]byte(nil), input.Data...),
	}, nil
}

func executorStatePathsCollide(left, right string) bool {
	return left == right ||
		strings.HasPrefix(left, right+"/") ||
		strings.HasPrefix(right, left+"/")
}

func (store ExecutorStateStore) verifySnapshot(
	workspaceRoot string,
	transaction *confinedfs.Transaction,
	snapshot ExecutorStateSnapshot,
	verifyBlobs bool,
) error {
	if snapshot.APIVersion != ExecutorStateSnapshotAPIVersion ||
		!executorStateOperationPattern.MatchString(snapshot.OperationID) ||
		snapshot.GenerationTarget != "compose" ||
		snapshot.OwnerRef == "" || snapshot.CapturedAt.IsZero() ||
		snapshot.KopiaSnapshotAnchor.ID == "" {
		return errors.New("executor state: snapshot contract is incomplete")
	}
	if err := validateExecutorStateRelease(snapshot.Release); err != nil {
		return err
	}
	if err := validateExecutorStateLineage(snapshot.Lineage); err != nil {
		return err
	}
	if !executorStateVersionPattern.MatchString(snapshot.Executable.Version) ||
		snapshot.Executable.Version != snapshot.Release.Version ||
		snapshot.Executable.Blob.Path != executorStateExecutablePath(snapshot.Release.Platform) ||
		snapshot.Executable.Blob.Mode != "0755" {
		return errors.New("executor state: executor or Owner lineage is invalid")
	}
	requestHash, err := executorStateRequestHash(snapshot)
	if err != nil || requestHash != snapshot.RequestHash {
		return errors.New("executor state: request hash does not verify")
	}
	identity, err := executorStateSnapshotID(snapshot)
	if err != nil || identity != snapshot.ID {
		return errors.New("executor state: snapshot identity does not verify")
	}
	signingBytes, err := executorStateSigningBytes(snapshot)
	if err != nil {
		return err
	}
	if err := localevidence.VerifyOwnerExecutorState(workspaceRoot, signingBytes, snapshot.Signature); err != nil {
		return fmt.Errorf("executor state: verify Owner signature: %w", err)
	}
	if snapshot.Signature.OwnerRef != snapshot.OwnerRef {
		return errors.New("executor state: signature Owner differs from snapshot")
	}
	if err := verifyExecutorStateSnapshotAnchor(
		workspaceRoot, snapshot.OwnerRef, snapshot.Lineage, snapshot.KopiaSnapshotAnchor,
	); err != nil {
		return err
	}
	if snapshot.RuntimeCompose.Path != basementCoreRuntimeComposePath {
		return errors.New("executor state: runtime Compose path is invalid")
	}
	sourceMatches := 0
	seenIDs := make(map[string]struct{})
	seenPaths := make(map[string]struct{})
	for _, blob := range executorStateBlobs(snapshot) {
		if _, duplicate := seenIDs[blob.ID]; duplicate {
			return errors.New("executor state: duplicate blob ID")
		}
		seenIDs[blob.ID] = struct{}{}
		pathKey := strings.ToLower(blob.Path)
		for existing := range seenPaths {
			if executorStatePathsCollide(existing, pathKey) {
				return errors.New("executor state: case-folded blob path hierarchy collision")
			}
		}
		seenPaths[pathKey] = struct{}{}
	}
	for _, artifact := range snapshot.Artifacts {
		if artifact.Path == basementCoreComposeArtifactPath {
			sourceMatches++
			if artifact.SHA256 != snapshot.RuntimeCompose.SHA256 {
				return errors.New("executor state: runtime Compose differs from source artifact")
			}
		}
	}
	if sourceMatches != 1 {
		return errors.New("executor state: governed Compose source is missing or ambiguous")
	}
	if !verifyBlobs {
		return nil
	}
	for _, blob := range executorStateBlobs(snapshot) {
		if err := verifyExecutorStateBlob(transaction, blob); err != nil {
			return err
		}
	}
	return nil
}

func executorStateBlobs(snapshot ExecutorStateSnapshot) []ExecutorStateBlob {
	result := []ExecutorStateBlob{snapshot.Executable.Blob, snapshot.StackSpec}
	if snapshot.Inventory != nil {
		result = append(result, *snapshot.Inventory)
	}
	result = append(result, snapshot.Artifacts...)
	result = append(result, snapshot.RuntimeCompose)
	return result
}

func validateExecutorStateRelease(release ExecutorStateRelease) error {
	if strings.TrimSpace(release.Kit) == "" || !executorStateVersionPattern.MatchString(release.Version) ||
		strings.TrimSpace(string(release.Channel)) == "" ||
		strings.TrimSpace(release.Platform.OS) == "" || strings.TrimSpace(release.Platform.Arch) == "" ||
		!validExecutorStateDigest(release.ArchiveSHA256) ||
		!validExecutorStateDigest(release.SBOMSHA256) ||
		!validExecutorStateDigest(release.IndexSHA256) ||
		!validExecutorStateDigest(release.IndexAttestationSHA256) ||
		!validExecutorStateDigest(release.AttestationSHA256) ||
		!validExecutorStateDigest(release.TrustedRootSHA256) ||
		strings.TrimSpace(release.AttestationIssuer) == "" ||
		strings.TrimSpace(release.CertificateIdentity) == "" ||
		strings.TrimSpace(release.AttestationSubject) == "" ||
		strings.TrimSpace(release.PredicateType) == "" {
		return errors.New("executor state: verified source release identity is incomplete")
	}
	return nil
}

func verifyExecutorStateReleaseProof(
	proof releaseindex.VerifiedInstallation,
	executableBytes []byte,
) (ExecutorStateRelease, error) {
	if len(executableBytes) == 0 || len(executableBytes) > 512<<20 {
		return ExecutorStateRelease{}, errors.New("executor state: exact recovery executable bytes are required")
	}
	var verifiedRelease ExecutorStateRelease
	err := proof.Inspect(func(
		receipt releaseindex.Receipt,
		asset releaseindex.Asset,
		archiveReader io.Reader,
	) error {
		if asset.Kit != receipt.Kit ||
			asset.Version != receipt.Version ||
			asset.Channel != receipt.Channel ||
			asset.Platform != receipt.Platform ||
			asset.Archive.SHA256 != receipt.ArchiveSHA256 ||
			asset.SBOM.SHA256 != receipt.SBOMSHA256 ||
			asset.Attestation.SHA256 != receipt.AttestationSHA256 {
			return errors.New("executor state: verified installed release proof is internally inconsistent")
		}
		release := ExecutorStateRelease{
			Kit: receipt.Kit, Version: receipt.Version, Channel: receipt.Channel, Platform: receipt.Platform,
			ArchiveSHA256:          executorStateReleaseDigest(asset.Archive.SHA256),
			SBOMSHA256:             executorStateReleaseDigest(asset.SBOM.SHA256),
			AttestationSHA256:      executorStateReleaseDigest(asset.Attestation.SHA256),
			TrustedRootSHA256:      executorStateReleaseDigest(receipt.TrustedRootSHA256),
			IndexSHA256:            executorStateReleaseDigest(receipt.IndexSHA256),
			IndexAttestationSHA256: executorStateReleaseDigest(receipt.IndexAttestationSHA256),
			AttestationIssuer:      asset.Attestation.Issuer,
			CertificateIdentity:    asset.Attestation.CertificateIdentity,
			AttestationSubject:     asset.Attestation.Subject, PredicateType: asset.Attestation.PredicateType,
		}
		if err := validateExecutorStateRelease(release); err != nil {
			return err
		}
		tempRoot, err := os.MkdirTemp("", "stackkit-executor-state-release-")
		if err != nil {
			return fmt.Errorf("executor state: create release verification directory: %w", err)
		}
		defer os.RemoveAll(tempRoot)
		archivePath := filepath.Join(tempRoot, filepath.Base(asset.Archive.Name))
		archiveFile, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("executor state: stage retained verified archive: %w", err)
		}
		digest := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(archiveFile, digest), archiveReader)
		syncErr := archiveFile.Sync()
		closeErr := archiveFile.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil {
			return fmt.Errorf(
				"executor state: stage retained verified archive: %w",
				errors.Join(copyErr, syncErr, closeErr),
			)
		}
		if written <= 0 || hex.EncodeToString(digest.Sum(nil)) != asset.Archive.SHA256 {
			return errors.New("executor state: retained verified archive digest changed")
		}
		extractRoot := filepath.Join(tempRoot, "extract")
		if err := os.Mkdir(extractRoot, 0o700); err != nil {
			return err
		}
		if err := extractArchive(
			archivePath, asset.Archive.Name, extractRoot, defaultMaxFiles, defaultMaxExtractBytes,
		); err != nil {
			return fmt.Errorf("executor state: extract verified installed release: %w", err)
		}
		archiveExecutable, err := os.ReadFile(filepath.Join(
			extractRoot, executorStateExecutablePath(release.Platform),
		))
		if err != nil {
			return fmt.Errorf("executor state: read exact executable from verified release: %w", err)
		}
		if !bytes.Equal(archiveExecutable, executableBytes) {
			return errors.New("executor state: recovery executable differs from verified installed release")
		}
		verifiedRelease = release
		return nil
	})
	if err != nil {
		return ExecutorStateRelease{}, fmt.Errorf("executor state: verified installed release proof: %w", err)
	}
	return verifiedRelease, nil
}

func executorStateReleaseDigest(raw string) string {
	return "sha256:" + raw
}

func executorStateExecutablePath(platform releaseindex.Platform) string {
	if platform.OS == "windows" {
		return "stackkit.exe"
	}
	return "stackkit"
}

func verifyExecutorStateSnapshotAnchor(
	workspaceRoot, ownerRef string,
	lineage backuplifecycle.AuthorityLineage,
	anchor backuplifecycle.SnapshotAnchor,
) error {
	owner, err := localevidence.LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return fmt.Errorf("executor state: load Owner authority for snapshot anchor: %w", err)
	}
	runtimeBinding, err := localevidence.LoadOwnerRuntimeBinding(workspaceRoot)
	if err != nil {
		return fmt.Errorf("executor state: load current Owner runtime binding: %w", err)
	}
	return verifyExecutorStateSnapshotAnchorWithAuthority(
		workspaceRoot, ownerRef, lineage, anchor, owner, runtimeBinding,
	)
}

func verifyExecutorStateSnapshotAnchorWithAuthority(
	workspaceRoot, ownerRef string,
	lineage backuplifecycle.AuthorityLineage,
	anchor backuplifecycle.SnapshotAnchor,
	owner localevidence.OwnerCustody,
	runtimeBinding localevidence.OwnerRuntimeBinding,
) error {
	if err := backuplifecycle.VerifySnapshotAnchor(workspaceRoot, anchor); err != nil {
		return fmt.Errorf("executor state: verify Kopia snapshot anchor: %w", err)
	}
	persistedAnchor, err := backuplifecycle.LoadSnapshotAnchor(workspaceRoot, anchor.ID)
	if err != nil {
		return fmt.Errorf("executor state: load persisted Kopia snapshot anchor: %w", err)
	}
	if !reflect.DeepEqual(persistedAnchor, anchor) {
		return errors.New("executor state: embedded Kopia snapshot anchor differs from persisted restore anchor")
	}
	if anchor.OwnerRef != ownerRef ||
		owner.OwnerRef != ownerRef ||
		anchor.AuthorityRef != owner.Trust.HumanAuthorityRef ||
		anchor.Repository.RepositoryID != localbackupruntime.RepositoryID ||
		runtimeBinding.OwnerRef != ownerRef ||
		runtimeBinding.PocketIDSubject != lineage.PocketIDSubject ||
		localevidence.OwnerRuntimeBindingDigest(runtimeBinding) != lineage.OwnerBindingDigest ||
		!reflect.DeepEqual(anchor.Lineage, lineage) {
		return errors.New("executor state: Kopia snapshot anchor differs from Owner, authority, repository, or lineage")
	}
	return nil
}

func validateExecutorStateLineage(lineage backuplifecycle.AuthorityLineage) error {
	for _, digest := range []string{
		lineage.Binding.PlanHash, lineage.Binding.SpecHash, lineage.Binding.InventoryHash,
		lineage.Binding.DefinitionHash, lineage.Binding.Authority.CatalogHash,
		lineage.ManifestHash, lineage.GenerationReceiptHash, lineage.ApplyResultHash,
		lineage.ApplyReceiptHash, lineage.OwnerBindingDigest,
	} {
		if !validExecutorStateDigest(digest) {
			return errors.New("executor state: authority lineage contains an invalid digest")
		}
	}
	if strings.TrimSpace(lineage.PocketIDSubject) == "" ||
		strings.TrimSpace(lineage.Binding.CompilerVersion) == "" ||
		strings.TrimSpace(lineage.Binding.Renderer.ID) == "" ||
		strings.TrimSpace(lineage.Binding.Renderer.Version) == "" ||
		strings.TrimSpace(lineage.Binding.Authority.Class) == "" ||
		strings.TrimSpace(lineage.Binding.Authority.Document) == "" ||
		strings.TrimSpace(lineage.Binding.Authority.Issuer) == "" {
		return errors.New("executor state: authority lineage metadata is incomplete")
	}
	return nil
}

func executorStateRequestHash(snapshot ExecutorStateSnapshot) (string, error) {
	unsigned := snapshot
	unsigned.ID = ""
	unsigned.RequestHash = ""
	unsigned.CapturedAt = time.Time{}
	unsigned.Signature = localevidence.OwnerExecutorStateSignature{}
	canonical, err := resolvedplan.CanonicalJSON(unsigned)
	if err != nil {
		return "", err
	}
	return executorStateDigest(canonical), nil
}

func executorStateSnapshotID(snapshot ExecutorStateSnapshot) (string, error) {
	unsigned := snapshot
	unsigned.ID = ""
	unsigned.Signature = localevidence.OwnerExecutorStateSignature{}
	canonical, err := resolvedplan.CanonicalJSON(unsigned)
	if err != nil {
		return "", err
	}
	return executorStateDigest(canonical), nil
}

func executorStateSigningBytes(snapshot ExecutorStateSnapshot) ([]byte, error) {
	unsigned := snapshot
	unsigned.Signature = localevidence.OwnerExecutorStateSignature{}
	return resolvedplan.CanonicalJSON(unsigned)
}

func executorStateDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validExecutorStateDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func executorStateBlobPath(digest string) (string, error) {
	if !validExecutorStateDigest(digest) {
		return "", errors.New("executor state: invalid blob digest")
	}
	return path.Join(executorStateRoot, "blobs", strings.TrimPrefix(digest, "sha256:")), nil
}

func executorStateSnapshotPath(snapshotID string) (string, error) {
	if !validExecutorStateDigest(snapshotID) {
		return "", errors.New("executor state: invalid snapshot ID")
	}
	return path.Join(executorStateRoot, "snapshots", strings.TrimPrefix(snapshotID, "sha256:")+".json"), nil
}

func executorStateOperationPath(operationID string) string {
	return path.Join(executorStateRoot, "operations", operationID+".json")
}

func persistExecutorStateBlob(
	transaction *confinedfs.Transaction,
	view confinedfs.View,
	identity ExecutorStateBlob,
	data []byte,
) error {
	if executorStateDigest(data) != identity.SHA256 {
		return errors.New("executor state: blob bytes differ from their identity")
	}
	blobPath, err := executorStateBlobPath(identity.SHA256)
	if err != nil {
		return err
	}
	return persistExecutorStateCAS(transaction, view, blobPath, data)
}

func persistExecutorStateCAS(
	transaction *confinedfs.Transaction,
	view confinedfs.View,
	target string,
	data []byte,
) error {
	if err := transaction.MkdirAll(path.Dir(target), 0o700); err != nil {
		return err
	}
	if err := syncExecutorStateHierarchy(transaction, path.Dir(target)); err != nil {
		return err
	}
	if _, err := view.WriteAtomic0600NoReplace(target, data); err != nil {
		existing, info, readErr := transaction.ReadStable(target)
		if readErr != nil || !info.Mode().IsRegular() || !bytes.Equal(existing, data) {
			return fmt.Errorf("executor state: atomically persist immutable CAS object: %w", err)
		}
	}
	return nil
}

func syncExecutorStateHierarchy(transaction *confinedfs.Transaction, directory string) error {
	for current := directory; ; current = path.Dir(current) {
		if _, err := transaction.SyncDirectory(current); err != nil {
			return fmt.Errorf("executor state: sync immutable store hierarchy %s: %w", current, err)
		}
		if current == "." {
			return nil
		}
	}
}

func verifyExecutorStateBlob(transaction *confinedfs.Transaction, blob ExecutorStateBlob) error {
	if !executorStateIDPattern.MatchString(blob.ID) ||
		!executorStateModePattern.MatchString(blob.Mode) {
		return errors.New("executor state: blob identity is invalid")
	}
	if _, err := confinedfs.ValidatePortablePath(blob.Path); err != nil {
		return fmt.Errorf("executor state: blob path: %w", err)
	}
	blobPath, err := executorStateBlobPath(blob.SHA256)
	if err != nil {
		return err
	}
	raw, info, err := transaction.ReadStable(blobPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 512<<20 {
		return fmt.Errorf("executor state: read bounded blob: %w", err)
	}
	if executorStateDigest(raw) != blob.SHA256 {
		return errors.New("executor state: blob digest does not verify")
	}
	return nil
}

func readExecutorStateOperation(
	transaction *confinedfs.Transaction,
	operationID string,
) (executorStateOperation, bool, error) {
	raw, _, err := transaction.ReadStable(executorStateOperationPath(operationID))
	if errors.Is(err, os.ErrNotExist) {
		return executorStateOperation{}, false, nil
	}
	if err != nil {
		return executorStateOperation{}, false, err
	}
	var operation executorStateOperation
	if err := decodeExactJSON(raw, &operation); err != nil {
		return executorStateOperation{}, false, fmt.Errorf("executor state: decode operation: %w", err)
	}
	canonical, err := resolvedplan.CanonicalJSON(operation)
	if err != nil || !bytes.Equal(raw, canonical) ||
		operation.APIVersion != executorStateOperationAPIVersion ||
		operation.OperationID != operationID ||
		!validExecutorStateDigest(operation.RequestHash) ||
		!validExecutorStateDigest(operation.SnapshotID) {
		return executorStateOperation{}, false, errors.New("executor state: operation journal is invalid")
	}
	return operation, true, nil
}

func filepathToSlash(value string) string {
	return strings.ReplaceAll(value, `\`, "/")
}
