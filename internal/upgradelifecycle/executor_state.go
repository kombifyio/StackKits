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
	ExecutorStateSnapshotAPIVersion            = "stackkit.executor-state-snapshot/v1"
	executorStateOperationAPIVersion           = "stackkit.executor-state-operation/v1"
	executorStateRoot                          = ".stackkit/upgrades/executor-state"
	executorStateMaxSnapshotBytes              = 1 << 20
	executorStateMaxBlobBytes                  = 512 << 20
	basementCoreComposeArtifactPath            = "platform/basement-core/compose.yaml"
	basementCoreRuntimeComposePath             = ".stackkit/runtime/basement-core/compose.yaml"
	executorStateStandaloneComposeIDPrefix     = "standalone-compose-"
	executorStateStandaloneEnvironmentIDPrefix = "standalone-env-"
	executorStateStandaloneConfigIDPrefix      = "standalone-config-"
)

var (
	errExecutorStateSnapshotMetadataLimit = errors.New("executor state: snapshot metadata exceeds its bounded read contract")
	executorStateOperationPattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	executorStateIDPattern                = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	executorStateModePattern              = regexp.MustCompile(`^0[0-7]{3}$`)
	executorStateVersionPattern           = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)
)

type ExecutorStateRelease struct {
	// Authority is empty for a verified installed release and
	// ExecutorStateReleaseRunningExecutable for the running executable.
	Authority              string                `json:"authority,omitempty"`
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
	// Server is the release's stackkit-server. The v2 Core stages it from
	// beside the executing stackkit, so a recovery Apply needs it too.
	// Releases that predate the Core server ship none.
	Server *ExecutorStateBlobInput
}

type ExecutorStateExecutable struct {
	Version string             `json:"version"`
	Blob    ExecutorStateBlob  `json:"blob"`
	Server  *ExecutorStateBlob `json:"server,omitempty"`
}

type ExecutorStateCaptureInput struct {
	OperationID           string
	GenerationTarget      string
	CoreModuleRef         string
	CoreComposeArtifactID string
	CorePolicyArtifactID  string
	Release               releaseindex.VerifiedInstallation
	// RunningRelease replaces Release when the prior release is the running
	// executable and no workspace release cache holds it.
	RunningRelease *RunningExecutableRelease
	Executable     ExecutorStateExecutableInput
	Lineage        backuplifecycle.AuthorityLineage
	StackSpec      ExecutorStateBlobInput
	Inventory      *ExecutorStateBlobInput
	Artifacts      []ExecutorStateBlobInput
	RuntimeCompose ExecutorStateBlobInput
	// RuntimeOpenTofu replaces RuntimeCompose when the generation target
	// executes OpenTofu (opentofu or terramate).
	RuntimeOpenTofu     []ExecutorStateOpenTofuRootInput
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
	APIVersion            string                                    `json:"apiVersion"`
	ID                    string                                    `json:"id"`
	RequestHash           string                                    `json:"requestHash"`
	OwnerRef              string                                    `json:"ownerRef"`
	OperationID           string                                    `json:"operationId"`
	GenerationTarget      string                                    `json:"generationTarget"`
	CoreModuleRef         string                                    `json:"coreModuleRef,omitempty"`
	CoreComposeArtifactID string                                    `json:"coreComposeArtifactId,omitempty"`
	CorePolicyArtifactID  string                                    `json:"corePolicyArtifactId,omitempty"`
	Release               ExecutorStateRelease                      `json:"release"`
	Executable            ExecutorStateExecutable                   `json:"executable"`
	Lineage               backuplifecycle.AuthorityLineage          `json:"lineage"`
	StackSpec             ExecutorStateBlob                         `json:"stackSpec"`
	Inventory             *ExecutorStateBlob                        `json:"inventory,omitempty"`
	Artifacts             []ExecutorStateBlob                       `json:"artifacts"`
	RuntimeCompose        ExecutorStateBlob                         `json:"runtimeCompose,omitzero"`
	RuntimeOpenTofu       []ExecutorStateOpenTofuRoot               `json:"runtimeOpenTofu,omitempty"`
	KopiaSnapshotAnchor   backuplifecycle.SnapshotAnchor            `json:"kopiaSnapshotAnchor"`
	CapturedAt            time.Time                                 `json:"capturedAt"`
	Signature             localevidence.OwnerExecutorStateSignature `json:"signature"`
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
		workspaceRoot, owner.OwnerRef, input.CoreModuleRef, input.Lineage, input.KopiaSnapshotAnchor,
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
	raw, info, err := transaction.ReadStableBounded(snapshotPath, executorStateMaxSnapshotBytes)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
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
	openTofu := executorStateTargetExecutesOpenTofu(input.GenerationTarget)
	if input.GenerationTarget != executorStateTargetCompose && !openTofu {
		return ExecutorStateSnapshot{}, nil, errors.New("executor state: unsupported_state_snapshot: only the Compose and OpenTofu executors are supported")
	}
	if openTofu != (len(input.RuntimeOpenTofu) > 0) ||
		(openTofu && (input.RuntimeCompose.ID != "" || input.RuntimeCompose.Path != "" || len(input.RuntimeCompose.Data) != 0)) {
		return ExecutorStateSnapshot{}, nil, errors.New("executor state: runtime custody differs from the generation target executor")
	}
	if err := validateExecutorStateRelease(release); err != nil {
		return ExecutorStateSnapshot{}, nil, err
	}
	if err := validateExecutorStateLineage(input.Lineage); err != nil {
		return ExecutorStateSnapshot{}, nil, err
	}
	profile, err := currentStateCoreProfileForCapture(input)
	if err != nil {
		return ExecutorStateSnapshot{}, nil, err
	}
	payloadInputs := []ExecutorStateBlobInput{input.Executable.Blob, input.StackSpec}
	if input.Executable.Server != nil {
		payloadInputs = append(payloadInputs, *input.Executable.Server)
	}
	if input.Inventory != nil {
		payloadInputs = append(payloadInputs, *input.Inventory)
	}
	payloadInputs = append(payloadInputs, input.Artifacts...)
	if openTofu {
		payloadInputs = append(payloadInputs, executorStateOpenTofuBlobInputs(input.RuntimeOpenTofu)...)
	} else {
		payloadInputs = append(payloadInputs, input.RuntimeCompose)
	}
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
	var server *ExecutorStateBlob
	if input.Executable.Server != nil {
		value := payloads[index].identity
		if value.Path != executorStateServerExecutablePath(release.Platform) || value.Mode != "0755" {
			return ExecutorStateSnapshot{}, nil, errors.New("executor state: recovery stackkit-server path or mode differs from its release platform")
		}
		server = &value
		index++
	}
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
	if openTofu {
		roots, err := executorStateOpenTofuRootsFromPayloads(input.RuntimeOpenTofu, payloads, index+len(input.Artifacts))
		if err != nil {
			return ExecutorStateSnapshot{}, nil, err
		}
		if err := validateExecutorStateOpenTofuRoots(roots, artifacts, profile); err != nil {
			return ExecutorStateSnapshot{}, nil, err
		}
		if err := requireExecutorStatePolicyArtifact(artifacts, profile); err != nil {
			return ExecutorStateSnapshot{}, nil, err
		}
		sortExecutorStateArtifacts(artifacts)
		sort.Slice(roots, func(left, right int) bool { return roots[left].Root < roots[right].Root })
		return ExecutorStateSnapshot{
			APIVersion: ExecutorStateSnapshotAPIVersion,
			OwnerRef:   ownerRef, OperationID: input.OperationID,
			GenerationTarget: input.GenerationTarget, Release: release,
			CoreModuleRef:         strings.TrimSpace(input.CoreModuleRef),
			CoreComposeArtifactID: strings.TrimSpace(input.CoreComposeArtifactID),
			CorePolicyArtifactID:  strings.TrimSpace(input.CorePolicyArtifactID),
			Executable: ExecutorStateExecutable{
				Version: release.Version, Blob: executable, Server: server,
			},
			Lineage: input.Lineage, StackSpec: stackSpec, Inventory: inventory,
			Artifacts: artifacts, RuntimeOpenTofu: roots,
			KopiaSnapshotAnchor: input.KopiaSnapshotAnchor,
		}, payloads, nil
	}
	runtimeCompose := payloads[len(payloads)-1].identity
	if runtimeCompose.Path != profile.RuntimeComposePath {
		return ExecutorStateSnapshot{}, nil, errors.New("executor state: runtime Compose path is not the governed Core runtime path")
	}
	sourceMatches := 0
	policyMatches := 0
	for _, artifact := range artifacts {
		if artifact.Path == profile.ComposeOutputRef &&
			(profile.ComposeArtifactID == "" || artifact.ID == profile.ComposeArtifactID) {
			sourceMatches++
			if artifact.SHA256 != runtimeCompose.SHA256 {
				return ExecutorStateSnapshot{}, nil, errors.New("executor state: runtime Compose differs from the governed generation artifact")
			}
		}
		if profile.PolicyArtifactID != "" && artifact.ID == profile.PolicyArtifactID {
			policyMatches++
		}
	}
	if sourceMatches != 1 {
		return ExecutorStateSnapshot{}, nil, errors.New("executor state: runtime Compose requires exactly one governed source artifact")
	}
	if profile.PolicyArtifactID != "" && policyMatches != 1 {
		return ExecutorStateSnapshot{}, nil, errors.New("executor state: selected Core source-policy artifact is missing or ambiguous")
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
		CoreModuleRef:         strings.TrimSpace(input.CoreModuleRef),
		CoreComposeArtifactID: strings.TrimSpace(input.CoreComposeArtifactID),
		CorePolicyArtifactID:  strings.TrimSpace(input.CorePolicyArtifactID),
		Executable: ExecutorStateExecutable{
			Version: release.Version, Blob: executable, Server: server,
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
	if (len(input.Data) == 0 && !executorStateStandaloneEnvironmentBlob(input.ID, canonicalPath, input.Mode)) || len(input.Data) > executorStateMaxBlobBytes {
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
	canonical, err := resolvedplan.CanonicalJSON(snapshot)
	if err != nil {
		return err
	}
	if len(canonical) > executorStateMaxSnapshotBytes {
		return errExecutorStateSnapshotMetadataLimit
	}
	blobs := executorStateBlobs(snapshot)
	if len(blobs) > defaultMaxFiles {
		return errors.New("executor state: recovery closure contains too many blobs")
	}
	if snapshot.APIVersion != ExecutorStateSnapshotAPIVersion ||
		!executorStateOperationPattern.MatchString(snapshot.OperationID) ||
		(snapshot.GenerationTarget != executorStateTargetCompose && !executorStateTargetExecutesOpenTofu(snapshot.GenerationTarget)) ||
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
		snapshot.Executable.Blob.Mode != "0755" ||
		(snapshot.Executable.Server != nil &&
			(snapshot.Executable.Server.Path != executorStateServerExecutablePath(snapshot.Release.Platform) ||
				snapshot.Executable.Server.Mode != "0755")) {
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
		workspaceRoot, snapshot.OwnerRef, snapshot.CoreModuleRef, snapshot.Lineage, snapshot.KopiaSnapshotAnchor,
	); err != nil {
		return err
	}
	profile, err := currentStateCoreProfileForCapture(ExecutorStateCaptureInput{
		CoreModuleRef:         snapshot.CoreModuleRef,
		CoreComposeArtifactID: snapshot.CoreComposeArtifactID,
		CorePolicyArtifactID:  snapshot.CorePolicyArtifactID,
	})
	if err != nil {
		return err
	}
	openTofu := executorStateTargetExecutesOpenTofu(snapshot.GenerationTarget)
	if openTofu {
		if snapshot.RuntimeCompose != (ExecutorStateBlob{}) {
			return errors.New("executor state: an OpenTofu snapshot must not carry native runtime Compose")
		}
		if err := validateExecutorStateOpenTofuRoots(snapshot.RuntimeOpenTofu, snapshot.Artifacts, profile); err != nil {
			return err
		}
		if err := requireExecutorStatePolicyArtifact(snapshot.Artifacts, profile); err != nil {
			return err
		}
	} else if snapshot.RuntimeCompose.Path != profile.RuntimeComposePath || len(snapshot.RuntimeOpenTofu) != 0 {
		return errors.New("executor state: runtime Compose path is invalid")
	}
	sourceMatches := 0
	policyMatches := 0
	seenIDs := make(map[string]struct{})
	seenPaths := make(map[string]struct{})
	for _, blob := range blobs {
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
		if openTofu {
			break
		}
		if artifact.Path == profile.ComposeOutputRef &&
			(profile.ComposeArtifactID == "" || artifact.ID == profile.ComposeArtifactID) {
			sourceMatches++
			if artifact.SHA256 != snapshot.RuntimeCompose.SHA256 {
				return errors.New("executor state: runtime Compose differs from source artifact")
			}
		}
		if profile.PolicyArtifactID != "" && artifact.ID == profile.PolicyArtifactID {
			policyMatches++
		}
	}
	if !openTofu && sourceMatches != 1 {
		return errors.New("executor state: governed Compose source is missing or ambiguous")
	}
	if !openTofu && profile.PolicyArtifactID != "" && policyMatches != 1 {
		return errors.New("executor state: selected Core source-policy artifact is missing or ambiguous")
	}
	if !verifyBlobs {
		return nil
	}
	remaining := defaultMaxExtractBytes
	for _, blob := range blobs {
		// A zero remaining budget may still contain an empty environment file.
		// Read at most one byte in that case and reject any non-empty result.
		size, err := verifyExecutorStateBlob(transaction, blob, min(executorStateMaxBlobBytes, max(remaining, 1)))
		if err != nil {
			return err
		}
		if size > remaining {
			return errors.New("executor state: recovery closure exceeds the bounded byte budget")
		}
		remaining -= size
	}
	return nil
}

func executorStateBlobs(snapshot ExecutorStateSnapshot) []ExecutorStateBlob {
	result := []ExecutorStateBlob{snapshot.Executable.Blob, snapshot.StackSpec}
	if snapshot.Executable.Server != nil {
		result = append(result, *snapshot.Executable.Server)
	}
	if snapshot.Inventory != nil {
		result = append(result, *snapshot.Inventory)
	}
	result = append(result, snapshot.Artifacts...)
	if executorStateTargetExecutesOpenTofu(snapshot.GenerationTarget) {
		return append(result, executorStateOpenTofuBlobs(snapshot.RuntimeOpenTofu)...)
	}
	result = append(result, snapshot.RuntimeCompose)
	return result
}

func requireExecutorStatePolicyArtifact(artifacts []ExecutorStateBlob, profile CurrentStateCoreProfile) error {
	if profile.PolicyArtifactID == "" {
		return nil
	}
	matches := 0
	for _, artifact := range artifacts {
		if artifact.ID == profile.PolicyArtifactID {
			matches++
		}
	}
	if matches != 1 {
		return errors.New("executor state: selected Core source-policy artifact is missing or ambiguous")
	}
	return nil
}

func sortExecutorStateArtifacts(artifacts []ExecutorStateBlob) {
	sort.Slice(artifacts, func(left, right int) bool {
		if artifacts[left].ID == artifacts[right].ID {
			return artifacts[left].Path < artifacts[right].Path
		}
		return artifacts[left].ID < artifacts[right].ID
	})
}

func validateExecutorStateRelease(release ExecutorStateRelease) error {
	if release.Authority == ExecutorStateReleaseRunningExecutable {
		// The running executable has no archive, SBOM, index or attestation
		// identity; its captured executable blob digest is its identity.
		if strings.TrimSpace(release.Kit) == "" || !executorStateVersionPattern.MatchString(release.Version) ||
			strings.TrimSpace(release.Platform.OS) == "" || strings.TrimSpace(release.Platform.Arch) == "" ||
			release != (ExecutorStateRelease{
				Authority: release.Authority, Kit: release.Kit, Version: release.Version,
				Channel: release.Channel, Platform: release.Platform,
			}) {
			return errors.New("executor state: running executable release identity is incomplete or carries archive identity")
		}
		return nil
	}
	if release.Authority != "" || strings.TrimSpace(release.Kit) == "" || !executorStateVersionPattern.MatchString(release.Version) ||
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

// verifyExecutorStateReleaseProof binds the captured executables to the
// verified installed release. A release that ships stackkit-server must be
// captured with exactly that server, so its recovery Apply can run the Core.
func verifyExecutorStateReleaseProof(
	proof releaseindex.VerifiedInstallation,
	executable ExecutorStateExecutableInput,
) (ExecutorStateRelease, error) {
	return verifyExecutorStateCaptureRelease(proof, nil, executable)
}

// verifyExecutorStateCaptureRelease selects the release proof of a capture:
// the running executable when the capture carries one, else the verified
// installed release.
func verifyExecutorStateCaptureRelease(
	proof releaseindex.VerifiedInstallation,
	running *RunningExecutableRelease,
	executable ExecutorStateExecutableInput,
) (ExecutorStateRelease, error) {
	if running != nil {
		return verifyRunningExecutableReleaseProof(running, executable)
	}
	executableBytes := executable.Blob.Data
	if len(executableBytes) == 0 || len(executableBytes) > executorStateMaxBlobBytes {
		return ExecutorStateRelease{}, errors.New("executor state: exact recovery executable bytes are required")
	}
	release, archiveExecutable, archiveServer, err := inspectVerifiedReleaseExecutables(proof)
	if err != nil {
		return ExecutorStateRelease{}, err
	}
	if !bytes.Equal(archiveExecutable, executableBytes) {
		return ExecutorStateRelease{}, errors.New(
			"executor state: recovery executable differs from verified installed release",
		)
	}
	var capturedServer []byte
	if executable.Server != nil {
		capturedServer = executable.Server.Data
	}
	if (archiveServer == nil) != (executable.Server == nil) || !bytes.Equal(archiveServer, capturedServer) {
		return ExecutorStateRelease{}, errors.New(
			"executor state: recovery stackkit-server differs from verified installed release",
		)
	}
	return release, nil
}

// ReleaseExecutablesFromVerifiedRelease returns the exact stackkit and
// stackkit-server executables of an offline-verified installed release. The
// server is nil for a release that does not ship one. The v2 Core runs that
// server, so an upgrade stages it beside the target stackkit executable.
func ReleaseExecutablesFromVerifiedRelease(
	proof releaseindex.VerifiedInstallation,
) (cli, server []byte, err error) {
	_, cli, server, err = inspectVerifiedReleaseExecutables(proof)
	return cli, server, err
}

func inspectExecutorStateReleaseProof(
	proof releaseindex.VerifiedInstallation,
) (ExecutorStateRelease, []byte, error) {
	release, executable, _, err := inspectVerifiedReleaseExecutables(proof)
	return release, executable, err
}

func inspectVerifiedReleaseExecutables(
	proof releaseindex.VerifiedInstallation,
) (ExecutorStateRelease, []byte, []byte, error) {
	var verifiedRelease ExecutorStateRelease
	var verifiedExecutable, verifiedServer []byte
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
		server, err := os.ReadFile(filepath.Join(extractRoot, executorStateServerExecutablePath(release.Platform)))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("executor state: read stackkit-server from verified release: %w", err)
		}
		verifiedRelease = release
		verifiedExecutable = append([]byte(nil), archiveExecutable...)
		verifiedServer = server
		return nil
	})
	if err != nil {
		return ExecutorStateRelease{}, nil, nil, fmt.Errorf(
			"executor state: verified installed release proof: %w", err,
		)
	}
	return verifiedRelease, verifiedExecutable, verifiedServer, nil
}

func executorStateReleaseDigest(raw string) string {
	return "sha256:" + raw
}

func executorStateServerExecutablePath(platform releaseindex.Platform) string {
	if platform.OS == "windows" {
		return "stackkit-server.exe"
	}
	return "stackkit-server"
}

func executorStateExecutablePath(platform releaseindex.Platform) string {
	if platform.OS == "windows" {
		return "stackkit.exe"
	}
	return "stackkit"
}

func verifyExecutorStateSnapshotAnchor(
	workspaceRoot, ownerRef, coreModuleRef string,
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
		workspaceRoot, ownerRef, coreModuleRef, lineage, anchor, owner, runtimeBinding,
	)
}

// verifyExecutorStateSnapshotAnchorWithAuthority binds the anchor to the
// exact local Kopia repository of the sealed kit core (the Cloud standalone
// core writes kopia:local:cloud, every Basement core kopia:local:basement);
// an empty core module is a pre-profile Full-Core snapshot.
func verifyExecutorStateSnapshotAnchorWithAuthority(
	workspaceRoot, ownerRef, coreModuleRef string,
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
		anchor.Repository.RepositoryID != localbackupruntime.RepositoryIDForCoreModule(strings.TrimSpace(coreModuleRef)) ||
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

func executorStateStandaloneComposeID(project string) string {
	return executorStateStandaloneComposeIDPrefix + project
}

func executorStateStandaloneEnvironmentID(project string) string {
	return executorStateStandaloneEnvironmentIDPrefix + project
}

func executorStateStandaloneConfigID(project, relativePath string) string {
	sum := sha256.Sum256([]byte(relativePath))
	return executorStateStandaloneConfigIDPrefix + project + "-" + hex.EncodeToString(sum[:8])
}

func executorStateStandaloneEnvironmentBlob(id, relativePath, mode string) bool {
	if !strings.HasPrefix(id, executorStateStandaloneEnvironmentIDPrefix) {
		return false
	}
	project := strings.TrimPrefix(id, executorStateStandaloneEnvironmentIDPrefix)
	if !executorStateIDPattern.MatchString(project) || strings.Contains(project, "/") || mode != "0600" || !executorStateIDPattern.MatchString(id) {
		return false
	}
	wantPath := path.Join(".stackkit", "runtime", "applications", project, ".env")
	return filepathToSlash(relativePath) == wantPath
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

// SnapshotInventoryBlobPath returns the content-addressed Inventory captured
// by a verified executor-state snapshot. Callers must load or verify the
// snapshot before handing this path to a release CLI.
func SnapshotInventoryBlobPath(snapshot ExecutorStateSnapshot) (string, error) {
	if snapshot.Inventory == nil {
		return "", nil
	}
	return executorStateBlobPath(snapshot.Inventory.SHA256)
}

// SnapshotRuntimeComposeBlobPath returns the original, signed runtime Compose
// definition. During an upgrade, target Generate may replace the active file
// before Apply checks whether the old runtime owns its published ports.
func SnapshotRuntimeComposeBlobPath(snapshot ExecutorStateSnapshot) (string, error) {
	profile, err := currentStateCoreProfileForCapture(ExecutorStateCaptureInput{
		CoreModuleRef:         snapshot.CoreModuleRef,
		CoreComposeArtifactID: snapshot.CoreComposeArtifactID,
		CorePolicyArtifactID:  snapshot.CorePolicyArtifactID,
	})
	if err != nil {
		return "", err
	}
	if executorStateTargetExecutesOpenTofu(snapshot.GenerationTarget) {
		for _, root := range snapshot.RuntimeOpenTofu {
			if root.ModuleRef == profile.ModuleRef {
				return executorStateBlobPath(root.Compose.SHA256)
			}
		}
		return "", errors.New("executor state: prior Core OpenTofu root is not governed")
	}
	if snapshot.RuntimeCompose.Path != profile.RuntimeComposePath {
		return "", errors.New("executor state: prior runtime Compose path is not governed")
	}
	return executorStateBlobPath(snapshot.RuntimeCompose.SHA256)
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

func verifyExecutorStateBlob(transaction *confinedfs.Transaction, blob ExecutorStateBlob, maxBytes int64) (int64, error) {
	if !executorStateIDPattern.MatchString(blob.ID) ||
		!executorStateModePattern.MatchString(blob.Mode) {
		return 0, errors.New("executor state: blob identity is invalid")
	}
	if _, err := confinedfs.ValidatePortablePath(blob.Path); err != nil {
		return 0, fmt.Errorf("executor state: blob path: %w", err)
	}
	blobPath, err := executorStateBlobPath(blob.SHA256)
	if err != nil {
		return 0, err
	}
	raw, info, err := transaction.ReadStableBounded(blobPath, maxBytes)
	if err != nil || !info.Mode().IsRegular() ||
		(info.Size() == 0 && !executorStateStandaloneEnvironmentBlob(blob.ID, blob.Path, blob.Mode)) {
		return 0, fmt.Errorf("executor state: read bounded blob: %w", err)
	}
	if executorStateDigest(raw) != blob.SHA256 {
		return 0, errors.New("executor state: blob digest does not verify")
	}
	return int64(len(raw)), nil
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
