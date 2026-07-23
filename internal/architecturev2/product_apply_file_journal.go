package architecturev2

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeapplyv2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	productApplyJournalAPIVersion = "stackkits.product-runtime-apply-journal/v1alpha1"
	productApplyJournalRoot       = ".stackkits-control/runtime-apply-journal"
	productApplyJournalLockRoot   = "runtime-apply-journal-lock"
)

// ProductApplyFileJournal is a provider-free durable runtimeapply Journal for
// one held workspace. It owns persistence, atomic fencing, and crash resume;
// it does not own executor selection, transport, credentials, provider
// lifecycle, leases, generation, or compensation execution.
type ProductApplyFileJournal struct {
	mu   sync.RWMutex
	root *confinedfs.Root
	view confinedfs.View
}

type productApplyJournalRecord struct {
	APIVersion      string                 `json:"api_version"`
	OperationDigest string                 `json:"operation_digest"`
	Operation       runtimeapply.Operation `json:"operation"`
	FenceToken      string                 `json:"fence_token,omitempty"`
	Snapshot        runtimeapply.Snapshot  `json:"snapshot"`
}

// NewProductApplyFileJournal opens an existing workspace without mutating it.
// Private control directories are created lazily by the first Journal or
// recovery operation, after Apply has crossed resolution and verification.
// The caller owns Close and may inject this Journal into
// NewProductEmbeddedServiceWithRuntimeOwners.
func NewProductApplyFileJournal(workspaceRoot string) (*ProductApplyFileJournal, error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("open Product Apply journal workspace: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = root.Close()
		}
	}()
	view, err := root.View(".")
	if err != nil {
		return nil, fmt.Errorf("open Product Apply journal view: %w", err)
	}
	if err := validateExistingProductApplyJournalDirectories(root); err != nil {
		return nil, err
	}
	keep = true
	return &ProductApplyFileJournal{root: root, view: view}, nil
}

func validateExistingProductApplyJournalDirectories(root *confinedfs.Root) (returnErr error) {
	if root == nil {
		return errors.New("Product Apply journal workspace is required")
	}
	transaction, err := root.BeginTransaction()
	if err != nil {
		return fmt.Errorf("begin Product Apply journal workspace inspection: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()
	for _, directory := range []string{".stackkits-control", productApplyJournalRoot} {
		exists, info, err := transaction.Exists(directory)
		if err != nil {
			return fmt.Errorf("inspect Product Apply journal directory %q: %w", directory, err)
		}
		if !exists {
			continue
		}
		if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
			return fmt.Errorf("Product Apply journal directory %q is not private", directory)
		}
	}
	return nil
}

// Close releases the held workspace root. It is safe to call more than once.
func (j *ProductApplyFileJournal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.root == nil {
		return nil
	}
	err := j.root.Close()
	j.root = nil
	j.view = confinedfs.View{}
	return err
}

func (j *ProductApplyFileJournal) Begin(ctx context.Context, operation runtimeapply.Operation) (runtimeapply.Reservation, error) {
	if err := validateProductApplyJournalContext(ctx); err != nil {
		return runtimeapply.Reservation{}, err
	}
	operationCanonical, err := canonicalProductApplyOperation(operation)
	if err != nil {
		return runtimeapply.Reservation{}, err
	}
	var reservation runtimeapply.Reservation
	err = j.withProductApplyOperation(ctx, operation.OperationID, func(transaction *confinedfs.Transaction, recordPath string) error {
		record, found, err := j.readProductApplyJournalRecord(transaction, recordPath)
		if err != nil {
			return err
		}
		if !found {
			token, err := newProductApplyFenceToken()
			if err != nil {
				return err
			}
			snapshot := runtimeapply.Snapshot{OperationID: operation.OperationID, State: runtimeapply.OperationRunning}
			for _, step := range operation.Steps {
				snapshot.Steps = append(snapshot.Steps, runtimeapply.StepSnapshot{
					StepID: step.ID, RequestDigest: step.RequestDigest, State: runtimeapply.StepPending,
				})
			}
			record = productApplyJournalRecord{
				APIVersion: productApplyJournalAPIVersion, OperationDigest: productApplyDigest(operationCanonical),
				Operation: operation, FenceToken: token, Snapshot: snapshot,
			}
			if err := j.writeProductApplyJournalRecord(recordPath, record); err != nil {
				return err
			}
			reservation = runtimeapply.Reservation{Disposition: runtimeapply.DispositionAcquired, FenceToken: token}
			return runtimeapply.ValidateReservation(operation, reservation)
		}
		storedCanonical, err := canonicalProductApplyOperation(record.Operation)
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(storedCanonical, operationCanonical) != 1 {
			reservation = runtimeapply.Reservation{Disposition: runtimeapply.DispositionConflict}
			return runtimeapply.ValidateReservation(operation, reservation)
		}
		snapshot := cloneProductApplyFileSnapshot(record.Snapshot)
		if snapshot.State == runtimeapply.OperationCompleted || snapshot.State == runtimeapply.OperationCompensated {
			reservation = runtimeapply.Reservation{Disposition: runtimeapply.DispositionReplay, Snapshot: &snapshot}
			return runtimeapply.ValidateReservation(operation, reservation)
		}
		token, err := newProductApplyFenceToken()
		if err != nil {
			return err
		}
		record.FenceToken = token
		if err := j.writeProductApplyJournalRecord(recordPath, record); err != nil {
			return err
		}
		reservation = runtimeapply.Reservation{Disposition: runtimeapply.DispositionResume, FenceToken: token, Snapshot: &snapshot}
		return runtimeapply.ValidateReservation(operation, reservation)
	})
	if err != nil {
		return runtimeapply.Reservation{}, err
	}
	return cloneProductApplyFileReservation(reservation), nil
}

func (j *ProductApplyFileJournal) CommitStep(ctx context.Context, commit runtimeapply.StepCommit) (runtimeapply.Snapshot, error) {
	if err := validateProductApplyJournalContext(ctx); err != nil {
		return runtimeapply.Snapshot{}, err
	}
	commit.Result = cloneProductApplyFileResult(commit.Result)
	var result runtimeapply.Snapshot
	err := j.withProductApplyOperation(ctx, commit.OperationID, func(transaction *confinedfs.Transaction, recordPath string) error {
		record, found, err := j.readProductApplyJournalRecord(transaction, recordPath)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("Product Apply journal operation does not exist")
		}
		if subtle.ConstantTimeCompare([]byte(record.FenceToken), []byte(commit.FenceToken)) != 1 {
			return errors.New("Product Apply journal rejected a stale fence token")
		}
		if err := runtimeapply.ValidateStepCommit(record.Operation, commit); err != nil {
			return err
		}
		index := sort.Search(len(record.Snapshot.Steps), func(index int) bool {
			return record.Snapshot.Steps[index].StepID >= commit.StepID
		})
		if index == len(record.Snapshot.Steps) || record.Snapshot.Steps[index].StepID != commit.StepID {
			return errors.New("Product Apply journal snapshot does not contain the committed step")
		}
		if record.Snapshot.Steps[index].State != commit.ExpectedState {
			return errors.New("Product Apply journal step CAS state does not match")
		}
		record.Snapshot.Steps[index] = runtimeapply.StepSnapshot{
			StepID: commit.StepID, RequestDigest: commit.RequestDigest, State: commit.State,
			Result: cloneProductApplyFileResult(commit.Result), FailureCode: commit.FailureCode,
			CompensationReceiptDigest: commit.CompensationReceiptDigest,
		}
		record.Snapshot.State = productApplyOperationStateForSteps(record.Snapshot.Steps)
		if err := runtimeapply.ValidateSnapshot(record.Operation, record.Snapshot); err != nil {
			return err
		}
		if err := j.writeProductApplyJournalRecord(recordPath, record); err != nil {
			return err
		}
		result = cloneProductApplyFileSnapshot(record.Snapshot)
		return nil
	})
	return result, err
}

func (j *ProductApplyFileJournal) Finalize(ctx context.Context, finalization runtimeapply.Finalization) (runtimeapply.Snapshot, error) {
	if err := validateProductApplyJournalContext(ctx); err != nil {
		return runtimeapply.Snapshot{}, err
	}
	var result runtimeapply.Snapshot
	err := j.withProductApplyOperation(ctx, finalization.OperationID, func(transaction *confinedfs.Transaction, recordPath string) error {
		record, found, err := j.readProductApplyJournalRecord(transaction, recordPath)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("Product Apply journal operation does not exist")
		}
		if subtle.ConstantTimeCompare([]byte(record.FenceToken), []byte(finalization.FenceToken)) != 1 {
			return errors.New("Product Apply journal rejected a stale fence token")
		}
		if record.Snapshot.State != finalization.ExpectedState {
			return errors.New("Product Apply journal operation CAS state does not match")
		}
		if err := runtimeapply.ValidateFinalization(record.Operation, finalization); err != nil {
			return err
		}
		record.Snapshot.State = finalization.State
		if err := runtimeapply.ValidateSnapshot(record.Operation, record.Snapshot); err != nil {
			return err
		}
		if finalization.State == runtimeapply.OperationCompleted || finalization.State == runtimeapply.OperationCompensated {
			record.FenceToken = ""
		}
		if err := j.writeProductApplyJournalRecord(recordPath, record); err != nil {
			return err
		}
		result = cloneProductApplyFileSnapshot(record.Snapshot)
		return nil
	})
	return result, err
}

func (j *ProductApplyFileJournal) SaveApplyRecovery(ctx context.Context, requestDigest string, canonical []byte) error {
	if err := validateProductApplyJournalContext(ctx); err != nil {
		return err
	}
	capsule, err := parseProductApplyRecoveryCapsule(canonical)
	if err != nil {
		return err
	}
	if capsule.Shared.RequestDigest != requestDigest {
		return errors.New("Product Apply recovery capsule has a foreign request digest")
	}
	return j.withProductApplyOperation(ctx, requestDigest, func(transaction *confinedfs.Transaction, _ string) error {
		recoveryPath, err := productApplyRecoveryPath(requestDigest)
		if err != nil {
			return err
		}
		exists, _, err := transaction.Exists(recoveryPath)
		if err != nil {
			return err
		}
		if exists {
			stored, _, err := transaction.ReadStable(recoveryPath)
			if err != nil {
				return err
			}
			if subtle.ConstantTimeCompare(stored, canonical) != 1 {
				return errors.New("Product Apply recovery custody conflicts with an existing capsule")
			}
			return nil
		}
		return j.writeProductApplyCanonical(recoveryPath, canonical)
	})
}

func (j *ProductApplyFileJournal) LoadApplyRecovery(ctx context.Context, requestDigest string) ([]byte, error) {
	if err := validateProductApplyJournalContext(ctx); err != nil {
		return nil, err
	}
	var result []byte
	err := j.withProductApplyOperation(ctx, requestDigest, func(transaction *confinedfs.Transaction, _ string) error {
		recoveryPath, err := productApplyRecoveryPath(requestDigest)
		if err != nil {
			return err
		}
		exists, info, err := transaction.Exists(recoveryPath)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("Product Apply recovery capsule does not exist")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			return errors.New("Product Apply recovery capsule is not private")
		}
		data, _, err := transaction.ReadStable(recoveryPath)
		if err != nil {
			return err
		}
		capsule, err := parseProductApplyRecoveryCapsule(data)
		if err != nil {
			return err
		}
		if capsule.Shared.RequestDigest != requestDigest {
			return errors.New("Product Apply recovery capsule lookup is substituted")
		}
		result = append([]byte(nil), data...)
		return nil
	})
	return result, err
}

func (j *ProductApplyFileJournal) withProductApplyOperation(ctx context.Context, operationID string, action func(*confinedfs.Transaction, string) error) (returnErr error) {
	recordPath, lockRoot, err := productApplyJournalPaths(operationID)
	if err != nil {
		return err
	}
	if action == nil {
		return errors.New("Product Apply journal action is required")
	}
	if j == nil {
		return errors.New("Product Apply journal is not initialized")
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.root == nil {
		return errors.New("Product Apply journal is closed")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("Product Apply journal context: %w", err)
	}
	transaction, err := j.root.BeginTransaction()
	if err != nil {
		return fmt.Errorf("begin Product Apply journal transaction: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()
	if err := ensureProductApplyJournalDirectories(transaction); err != nil {
		return err
	}
	lock, err := transaction.TryAcquireOutputLock(lockRoot)
	if err != nil {
		if errors.Is(err, confinedfs.ErrOutputLockBusy) {
			return fmt.Errorf("Product Apply journal operation is busy: %w", err)
		}
		return fmt.Errorf("lock Product Apply journal operation: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, lock.Release()) }()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("Product Apply journal context: %w", err)
	}
	if err := action(transaction, recordPath); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("Product Apply journal context after mutation: %w", err)
	}
	return nil
}

func ensureProductApplyJournalDirectories(transaction *confinedfs.Transaction) error {
	if transaction == nil {
		return errors.New("Product Apply journal workspace transaction is required")
	}
	if err := transaction.MkdirAll(productApplyJournalRoot, 0o700); err != nil {
		return fmt.Errorf("create Product Apply journal root: %w", err)
	}
	for _, directory := range []string{".stackkits-control", productApplyJournalRoot} {
		info, err := transaction.Lstat(directory)
		if err != nil {
			return fmt.Errorf("inspect Product Apply journal directory %q: %w", directory, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("Product Apply journal directory %q is not private", directory)
		}
	}
	return nil
}

func (j *ProductApplyFileJournal) readProductApplyJournalRecord(transaction *confinedfs.Transaction, recordPath string) (productApplyJournalRecord, bool, error) {
	exists, info, err := transaction.Exists(recordPath)
	if err != nil {
		return productApplyJournalRecord{}, false, fmt.Errorf("inspect Product Apply journal record: %w", err)
	}
	if !exists {
		return productApplyJournalRecord{}, false, nil
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		return productApplyJournalRecord{}, false, errors.New("Product Apply journal record is not private")
	}
	data, _, err := transaction.ReadStable(recordPath)
	if err != nil {
		return productApplyJournalRecord{}, false, fmt.Errorf("read Product Apply journal record: %w", err)
	}
	record, err := parseProductApplyJournalRecord(data)
	if err != nil {
		return productApplyJournalRecord{}, false, err
	}
	return record, true, nil
}

func (j *ProductApplyFileJournal) writeProductApplyJournalRecord(recordPath string, record productApplyJournalRecord) error {
	canonical, err := canonicalProductApplyJournalRecord(record)
	if err != nil {
		return err
	}
	return j.writeProductApplyCanonical(recordPath, canonical)
}

func (j *ProductApplyFileJournal) writeProductApplyCanonical(recordPath string, canonical []byte) error {
	result, err := j.view.WriteAtomic0600(recordPath, canonical)
	if err != nil {
		return fmt.Errorf("write Product Apply journal record: %w", err)
	}
	if !result.Installed || !result.FileSynced || (runtime.GOOS != "windows" && !result.PermissionsVerified) {
		return errors.New("Product Apply journal record did not reach the required atomic private write boundary")
	}
	return nil
}

func productApplyRecoveryPath(requestDigest string) (string, error) {
	recordPath, _, err := productApplyJournalPaths(requestDigest)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(recordPath, ".json") + ".recovery.json", nil
}

func canonicalProductApplyJournalRecord(record productApplyJournalRecord) ([]byte, error) {
	if err := validateProductApplyJournalRecord(record); err != nil {
		return nil, err
	}
	// runtimeexecutor result slices are normatively ordered by their typed
	// identity fields. The generic ResolvedPlan canonicalizer treats a field
	// named "health" as set-semantic and would reorder these already-validated
	// results by their complete JSON body. encoding/json is deterministic for
	// struct fields and string-keyed maps while preserving required slice order.
	canonical, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("canonicalize Product Apply journal record: %w", err)
	}
	return canonical, nil
}

func parseProductApplyJournalRecord(data []byte) (productApplyJournalRecord, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var record productApplyJournalRecord
	if err := decoder.Decode(&record); err != nil {
		return productApplyJournalRecord{}, fmt.Errorf("decode Product Apply journal record: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return productApplyJournalRecord{}, errors.New("Product Apply journal record contains multiple JSON values")
		}
		return productApplyJournalRecord{}, fmt.Errorf("decode trailing Product Apply journal data: %w", err)
	}
	canonical, err := canonicalProductApplyJournalRecord(record)
	if err != nil {
		return productApplyJournalRecord{}, err
	}
	if subtle.ConstantTimeCompare(data, canonical) != 1 {
		return productApplyJournalRecord{}, errors.New("Product Apply journal record is not canonical JSON")
	}
	return record, nil
}

func validateProductApplyJournalRecord(record productApplyJournalRecord) error {
	if record.APIVersion != productApplyJournalAPIVersion {
		return errors.New("Product Apply journal record has an unsupported API version")
	}
	operationCanonical, err := canonicalProductApplyOperation(record.Operation)
	if err != nil {
		return err
	}
	if record.OperationDigest != productApplyDigest(operationCanonical) {
		return errors.New("Product Apply journal operation digest does not match")
	}
	if err := runtimeapply.ValidateSnapshot(record.Operation, record.Snapshot); err != nil {
		return fmt.Errorf("validate Product Apply journal snapshot: %w", err)
	}
	snapshot := cloneProductApplyFileSnapshot(record.Snapshot)
	if snapshot.State == runtimeapply.OperationCompleted || snapshot.State == runtimeapply.OperationCompensated {
		if record.FenceToken != "" {
			return errors.New("final Product Apply journal record retains mutable fence authority")
		}
		return runtimeapply.ValidateReservation(record.Operation, runtimeapply.Reservation{
			Disposition: runtimeapply.DispositionReplay, Snapshot: &snapshot,
		})
	}
	return runtimeapply.ValidateReservation(record.Operation, runtimeapply.Reservation{
		Disposition: runtimeapply.DispositionResume, FenceToken: record.FenceToken, Snapshot: &snapshot,
	})
}

func canonicalProductApplyOperation(operation runtimeapply.Operation) ([]byte, error) {
	if err := validateProductApplyOperationShape(operation); err != nil {
		return nil, err
	}
	canonical, err := resolvedplan.CanonicalJSON(operation)
	if err != nil {
		return nil, fmt.Errorf("canonicalize Product Apply operation: %w", err)
	}
	return canonical, nil
}

func validateProductApplyOperationShape(operation runtimeapply.Operation) error {
	if operation.APIVersion != runtimeapply.APIVersion || operation.OperationID != operation.ParentRequestDigest ||
		!validProductApplyDigest(operation.OperationID) || !validProductApplyDigest(operation.PlanHash) ||
		!validProductApplyDigest(operation.ManifestHash) || !validProductApplyDigest(operation.GenerationReceiptHash) ||
		!validProductApplyDigest(operation.RequirementsHash) || !validProductApplyDigest(operation.EvidenceBundleHash) ||
		operation.Executor.ID == "" || operation.Executor.Version == "" || !validProductApplyDigest(operation.Executor.Digest) ||
		len(operation.Steps) == 0 {
		return errors.New("Product Apply journal operation shape is invalid")
	}
	seenRuntime := make(map[string]struct{})
	seenHealth := make(map[string]struct{})
	for index, step := range operation.Steps {
		if index > 0 && operation.Steps[index-1].ID >= step.ID {
			return errors.New("Product Apply journal operation steps are not sorted and unique")
		}
		if step.ID != step.RequestDigest || !validProductApplyDigest(step.ID) || !validProductApplyDigest(step.ArtifactSetHash) ||
			step.PlanHash != operation.PlanHash || step.ManifestHash != operation.ManifestHash ||
			step.GenerationReceiptHash != operation.GenerationReceiptHash || step.RequirementsHash != operation.RequirementsHash ||
			step.EvidenceBundleHash != operation.EvidenceBundleHash || step.Executor.ID == "" || step.Executor.Version == "" ||
			!validProductApplyDigest(step.Executor.Digest) || len(step.Runtime) == 0 || len(step.Health) == 0 ||
			(step.Compensation != runtimeapply.CompensationNone && step.Compensation != runtimeapply.CompensationExplicit) {
			return errors.New("Product Apply journal step shape is invalid")
		}
		for runtimeIndex, expectation := range step.Runtime {
			if expectation.RequirementID == "" || expectation.InstanceRef == "" ||
				(runtimeIndex > 0 && step.Runtime[runtimeIndex-1].RequirementID >= expectation.RequirementID) {
				return errors.New("Product Apply journal runtime expectations are invalid")
			}
			if _, duplicate := seenRuntime[expectation.RequirementID]; duplicate {
				return errors.New("Product Apply journal operation contains duplicate runtime authority")
			}
			seenRuntime[expectation.RequirementID] = struct{}{}
		}
		for healthIndex, expectation := range step.Health {
			if expectation.RequirementID == "" || expectation.TargetRef == "" ||
				(healthIndex > 0 && step.Health[healthIndex-1].RequirementID >= expectation.RequirementID) {
				return errors.New("Product Apply journal Health expectations are invalid")
			}
			key := expectation.RequirementID + "\x00" + expectation.TargetRef
			if _, duplicate := seenHealth[key]; duplicate {
				return errors.New("Product Apply journal operation contains duplicate Health authority")
			}
			seenHealth[key] = struct{}{}
		}
	}
	return nil
}

func productApplyJournalPaths(operationID string) (recordPath, lockRoot string, err error) {
	if !validProductApplyDigest(operationID) {
		return "", "", errors.New("Product Apply journal operation ID is invalid")
	}
	hexID := strings.TrimPrefix(operationID, "sha256:")
	return path.Join(productApplyJournalRoot, hexID+".json"), path.Join(productApplyJournalLockRoot, hexID), nil
}

func newProductApplyFenceToken() (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate Product Apply journal fence token: %w", err)
	}
	return "stackkits-runtime-apply-fence/" + hex.EncodeToString(random[:]), nil
}

func productApplyOperationStateForSteps(steps []runtimeapply.StepSnapshot) runtimeapply.OperationState {
	for _, step := range steps {
		if step.State == runtimeapply.StepFailed {
			return runtimeapply.OperationReconcileRequired
		}
	}
	return runtimeapply.OperationRunning
}

func cloneProductApplyFileReservation(reservation runtimeapply.Reservation) runtimeapply.Reservation {
	clone := reservation
	if reservation.Snapshot != nil {
		snapshot := cloneProductApplyFileSnapshot(*reservation.Snapshot)
		clone.Snapshot = &snapshot
	}
	return clone
}

func cloneProductApplyFileSnapshot(snapshot runtimeapply.Snapshot) runtimeapply.Snapshot {
	clone := runtimeapply.Snapshot{OperationID: snapshot.OperationID, State: snapshot.State}
	for _, step := range snapshot.Steps {
		stepClone := step
		stepClone.Result = cloneProductApplyFileResult(step.Result)
		clone.Steps = append(clone.Steps, stepClone)
	}
	return clone
}

func cloneProductApplyFileResult(result *runtimeexecutor.ExecutionResult) *runtimeexecutor.ExecutionResult {
	if result == nil {
		return nil
	}
	clone := runtimeexecutor.CloneExecutionResult(*result)
	return &clone
}

func productApplyDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validProductApplyDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	hexValue := strings.TrimPrefix(value, "sha256:")
	if hexValue != strings.ToLower(hexValue) {
		return false
	}
	_, err := hex.DecodeString(hexValue)
	return err == nil
}

func validateProductApplyJournalContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("Product Apply journal requires a context")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("Product Apply journal context: %w", err)
	}
	return nil
}

var _ runtimeapply.Journal = (*ProductApplyFileJournal)(nil)
var _ ProductApplyRecoveryStore = (*ProductApplyFileJournal)(nil)
