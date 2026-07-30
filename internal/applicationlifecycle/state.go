// Package applicationlifecycle persists the resumable state of every selected
// Application Kit. The state is bound to the exact ResolvedPlan and lifecycle
// contract; StackSpec and caller-supplied package identities are never read.
package applicationlifecycle

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	APIVersion = "stackkit.application-lifecycle-state/v1"
	stateRoot  = ".stackkit/lifecycle/applications"

	StatusRunning          = "running"
	StatusSucceeded        = "succeeded"
	StatusFailed           = "failed"
	StatusRecoveryRequired = "recovery-required"
	StatusRecovered        = "recovered"
)

var (
	contractIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	operationIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{7,127}$`)
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	allowedStages      = map[string]struct{}{
		"install": {}, "manage": {}, "backup": {}, "upgrade": {},
		"restore": {}, "drift": {}, "remove": {},
	}
)

type StageContract struct {
	Name          string
	Operations    []string
	Phases        []string
	Evidence      []string
	Mutation      bool
	Destructive   bool
	OwnerApproval bool
}

type Contract struct {
	WorkloadRef  string
	PackageRef   string
	Version      string
	ContractHash string
	PlanHash     string
	Stages       map[string]StageContract
}

type Authority struct {
	PlanHash              string `json:"planHash"`
	LifecycleContractHash string `json:"lifecycleContractHash"`
	LifecycleVersion      string `json:"lifecycleVersion"`
	PackageRef            string `json:"packageRef"`
}

// Evidence is an immutable content-addressed proof produced by the operation
// named in the ResolvedPlan lifecycle contract. Ref is a local owner-custody
// path or another stable identifier; Digest always identifies its exact bytes.
type Evidence struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

type Operation struct {
	ID             string     `json:"id"`
	Stage          string     `json:"stage"`
	OperationRef   string     `json:"operationRef"`
	Status         string     `json:"status"`
	Attempt        uint64     `json:"attempt"`
	StartedAt      time.Time  `json:"startedAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	CompletedAt    time.Time  `json:"completedAt,omitempty"`
	Authority      Authority  `json:"authority"`
	Evidence       []Evidence `json:"evidence"`
	LastError      string     `json:"lastError,omitempty"`
	RecoveryRef    string     `json:"recoveryRef,omitempty"`
	PreviousDigest string     `json:"previousDigest,omitempty"`
	Digest         string     `json:"digest"`
}

type State struct {
	APIVersion       string      `json:"apiVersion"`
	WorkloadRef      string      `json:"workloadRef"`
	Authority        Authority   `json:"authority"`
	CurrentOperation string      `json:"currentOperation,omitempty"`
	Operations       []Operation `json:"operations"`
	UpdatedAt        time.Time   `json:"updatedAt"`
	StateDigest      string      `json:"stateDigest"`
}

type BeginRequest struct {
	ID           string
	Stage        string
	OperationRef string
	Now          time.Time
}

type TransitionRequest struct {
	ID          string
	Status      string
	Evidence    []Evidence
	LastError   string
	RecoveryRef string
	Now         time.Time
}

type Store struct {
	Workspace string
}

// ContractFromResolvedPlan returns the only lifecycle contract runtimes may
// consume. The selected application and package are read from the compiled,
// authority-bound projection.
func ContractFromResolvedPlan(plan resolvedplan.ResolvedPlan, workloadRef string) (Contract, error) {
	if !contractIDPattern.MatchString(workloadRef) {
		return Contract{}, errors.New("application lifecycle workload reference is invalid")
	}
	contracts, err := ContractsFromResolvedPlan(plan)
	if err != nil {
		return Contract{}, err
	}
	for _, contract := range contracts {
		if contract.WorkloadRef == workloadRef {
			return contract, nil
		}
	}
	return Contract{}, fmt.Errorf("ResolvedPlan has no lifecycle for application workload %q", workloadRef)
}

// ContractsFromResolvedPlan returns the complete, deterministically ordered
// application lifecycle projection from one verified canonical ResolvedPlan.
func ContractsFromResolvedPlan(plan resolvedplan.ResolvedPlan) ([]Contract, error) {
	planHash, ok := plan["planHash"].(string)
	if !ok || !digestPattern.MatchString(planHash) {
		return nil, errors.New("application lifecycle requires a canonical ResolvedPlan hash")
	}
	raw, ok := plan["applicationLifecycles"].([]any)
	if !ok {
		return nil, errors.New("ResolvedPlan applicationLifecycles is missing")
	}
	contracts := make([]Contract, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for index, item := range raw {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("ResolvedPlan applicationLifecycles[%d] is not an object", index)
		}
		contract, err := decodeContract(object, planHash)
		if err != nil {
			return nil, fmt.Errorf("decode ResolvedPlan applicationLifecycles[%d]: %w", index, err)
		}
		if _, duplicate := seen[contract.WorkloadRef]; duplicate {
			return nil, fmt.Errorf("ResolvedPlan has duplicate lifecycle for application workload %q", contract.WorkloadRef)
		}
		seen[contract.WorkloadRef] = struct{}{}
		contracts = append(contracts, contract)
	}
	sort.Slice(contracts, func(i, j int) bool {
		return contracts[i].WorkloadRef < contracts[j].WorkloadRef
	})
	return contracts, nil
}

func decodeContract(object map[string]any, planHash string) (Contract, error) {
	contract := Contract{PlanHash: planHash}
	var ok bool
	if contract.WorkloadRef, ok = object["workloadRef"].(string); !ok || !contractIDPattern.MatchString(contract.WorkloadRef) {
		return Contract{}, errors.New("resolved application lifecycle workloadRef is invalid")
	}
	if id, _ := object["id"].(string); id != contract.WorkloadRef {
		return Contract{}, errors.New("resolved application lifecycle identity differs from workloadRef")
	}
	if contract.PackageRef, ok = object["packageRef"].(string); !ok || !contractIDPattern.MatchString(contract.PackageRef) {
		return Contract{}, errors.New("resolved application lifecycle packageRef is invalid")
	}
	if contract.Version, ok = object["version"].(string); !ok || strings.TrimSpace(contract.Version) == "" {
		return Contract{}, errors.New("resolved application lifecycle version is invalid")
	}
	if contract.ContractHash, ok = object["contractHash"].(string); !ok || !digestPattern.MatchString(contract.ContractHash) {
		return Contract{}, errors.New("resolved application lifecycle contractHash is invalid")
	}
	lifecycle, ok := object["lifecycle"].(map[string]any)
	if !ok {
		return Contract{}, errors.New("resolved application lifecycle body is invalid")
	}
	stages, ok := lifecycle["stages"].(map[string]any)
	if !ok || len(stages) != len(allowedStages) {
		return Contract{}, errors.New("resolved application lifecycle must contain the seven standard stages")
	}
	contract.Stages = make(map[string]StageContract, len(stages))
	for name := range allowedStages {
		rawStage, ok := stages[name].(map[string]any)
		if !ok {
			return Contract{}, fmt.Errorf("resolved application lifecycle stage %q is missing", name)
		}
		stage, err := decodeStage(name, rawStage)
		if err != nil {
			return Contract{}, err
		}
		contract.Stages[name] = stage
	}
	return contract, nil
}

func decodeStage(name string, object map[string]any) (StageContract, error) {
	stage := StageContract{Name: name}
	if actual, _ := object["name"].(string); actual != name {
		return StageContract{}, fmt.Errorf("resolved application lifecycle stage %q has a mismatched name", name)
	}
	var err error
	if stage.Operations, err = stringList(object["operations"]); err != nil || len(stage.Operations) == 0 {
		return StageContract{}, fmt.Errorf("resolved application lifecycle stage %q operations are invalid", name)
	}
	if stage.Phases, err = stringList(object["phases"]); err != nil || len(stage.Phases) == 0 {
		return StageContract{}, fmt.Errorf("resolved application lifecycle stage %q phases are invalid", name)
	}
	if stage.Evidence, err = stringList(object["evidence"]); err != nil || len(stage.Evidence) == 0 {
		return StageContract{}, fmt.Errorf("resolved application lifecycle stage %q evidence is invalid", name)
	}
	var ok bool
	if stage.Mutation, ok = object["mutation"].(bool); !ok {
		return StageContract{}, fmt.Errorf("resolved application lifecycle stage %q mutation is invalid", name)
	}
	if stage.Destructive, ok = object["destructive"].(bool); !ok {
		return StageContract{}, fmt.Errorf("resolved application lifecycle stage %q destructive is invalid", name)
	}
	if stage.OwnerApproval, ok = object["ownerApproval"].(bool); !ok {
		return StageContract{}, fmt.Errorf("resolved application lifecycle stage %q ownerApproval is invalid", name)
	}
	return stage, nil
}

func (store Store) Load(contract Contract) (State, error) {
	if err := validateContract(contract); err != nil {
		return State{}, err
	}
	state, exists, err := store.load(contract.WorkloadRef)
	if err != nil {
		return State{}, err
	}
	if !exists {
		return newState(contract), nil
	}
	if err := validateState(state, contract); err != nil {
		return State{}, err
	}
	return state, nil
}

func (store Store) Begin(contract Contract, request BeginRequest) (State, error) {
	return store.mutate(contract, func(state *State) error {
		if !operationIDPattern.MatchString(request.ID) {
			return errors.New("application lifecycle operation ID is invalid")
		}
		stage, exists := contract.Stages[request.Stage]
		if !exists || !contains(stage.Operations, request.OperationRef) {
			return errors.New("operation is not admitted by the resolved application lifecycle stage")
		}
		for _, operation := range state.Operations {
			if operation.ID == request.ID {
				return errors.New("application lifecycle operation ID was already used")
			}
		}
		if current := currentOperation(*state); current != nil &&
			(current.Status == StatusRunning || current.Status == StatusRecoveryRequired) {
			return fmt.Errorf("application lifecycle operation %s requires completion or recovery", current.ID)
		}
		now := normalizedNow(request.Now)
		previous := ""
		if len(state.Operations) > 0 {
			previous = state.Operations[len(state.Operations)-1].Digest
		}
		operation := Operation{
			ID: request.ID, Stage: request.Stage, OperationRef: request.OperationRef,
			Status: StatusRunning, Attempt: 1, StartedAt: now, UpdatedAt: now,
			Authority: authorityFromContract(contract), Evidence: []Evidence{},
			PreviousDigest: previous,
		}
		operation.Digest = operationDigest(operation)
		state.Operations = append(state.Operations, operation)
		state.Authority = operation.Authority
		state.CurrentOperation = operation.ID
		return nil
	})
}

func (store Store) Transition(contract Contract, request TransitionRequest) (State, error) {
	return store.mutate(contract, func(state *State) error {
		index := operationIndex(*state, request.ID)
		if index < 0 {
			return errors.New("application lifecycle operation was not found")
		}
		operation := state.Operations[index]
		if index != len(state.Operations)-1 || state.CurrentOperation != operation.ID {
			return errors.New("only the current application lifecycle operation may transition")
		}
		if operation.Authority != authorityFromContract(contract) || state.Authority != operation.Authority {
			return errors.New("application lifecycle operation belongs to different ResolvedPlan authority")
		}
		if err := validateTransition(operation.Status, request.Status); err != nil {
			return err
		}
		now := normalizedNow(request.Now)
		operation.Status = request.Status
		operation.UpdatedAt = now
		operation.Evidence = sortedUniqueEvidence(request.Evidence)
		operation.LastError = strings.TrimSpace(request.LastError)
		operation.RecoveryRef = strings.TrimSpace(request.RecoveryRef)
		switch request.Status {
		case StatusRunning:
			operation.Attempt++
			operation.CompletedAt = time.Time{}
			operation.Evidence = []Evidence{}
			operation.LastError = ""
			operation.RecoveryRef = ""
		case StatusSucceeded, StatusRecovered:
			if err := validateCompletedEvidence(contract.Stages[operation.Stage], operation.Evidence); err != nil {
				return err
			}
			operation.CompletedAt = now
		case StatusFailed:
			if operation.LastError == "" {
				return errors.New("failed application lifecycle operation requires a diagnostic")
			}
			operation.CompletedAt = now
		case StatusRecoveryRequired:
			if operation.LastError == "" || operation.RecoveryRef == "" {
				return errors.New("recovery-required operation needs a diagnostic and recovery reference")
			}
		}
		if request.Status != StatusSucceeded && request.Status != StatusRecovered {
			if err := validatePartialEvidence(contract.Stages[operation.Stage], operation.Evidence); err != nil {
				return err
			}
		}
		operation.Digest = operationDigest(operation)
		state.Operations[index] = operation
		return nil
	})
}

func (store Store) mutate(contract Contract, change func(*State) error) (State, error) {
	if err := validateContract(contract); err != nil {
		return State{}, err
	}
	root, err := confinedfs.Open(filepath.Clean(store.Workspace))
	if err != nil {
		return State{}, fmt.Errorf("open application lifecycle workspace: %w", err)
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return State{}, fmt.Errorf("begin application lifecycle transaction: %w", err)
	}
	defer transaction.Close()
	if err := transaction.MkdirAll(stateRoot, 0o700); err != nil {
		return State{}, fmt.Errorf("create application lifecycle state root: %w", err)
	}
	lock, err := transaction.TryAcquireOutputLock(stateRoot)
	if err != nil {
		return State{}, fmt.Errorf("acquire application lifecycle state lock: %w", err)
	}
	defer lock.Release()
	state, exists, err := loadWithTransaction(transaction, contract.WorkloadRef)
	if err != nil {
		return State{}, err
	}
	if !exists {
		state = newState(contract)
	} else if err := validateState(state, contract); err != nil {
		return State{}, err
	}
	if err := change(&state); err != nil {
		return State{}, err
	}
	state.UpdatedAt = operationUpdatedAt(state)
	state.StateDigest = stateDigest(state)
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return State{}, fmt.Errorf("encode application lifecycle state: %w", err)
	}
	raw = append(raw, '\n')
	view, err := root.View(stateRoot)
	if err != nil {
		return State{}, fmt.Errorf("open application lifecycle state view: %w", err)
	}
	fileName := contract.WorkloadRef + ".json"
	result, err := view.WriteAtomic0600(fileName, raw)
	if err != nil || !result.Installed || !result.FileSynced {
		if err == nil {
			err = errors.New("atomic state write was not installed and synced")
		}
		return State{}, fmt.Errorf("persist application lifecycle state: %w", err)
	}
	absoluteRoot := filepath.Join(root.Name(), filepath.FromSlash(stateRoot))
	if err := backupcustody.ProtectPrivatePath(absoluteRoot, true); err != nil {
		return State{}, fmt.Errorf("protect application lifecycle state root: %w", err)
	}
	if err := backupcustody.ProtectPrivatePath(filepath.Join(absoluteRoot, fileName), false); err != nil {
		return State{}, fmt.Errorf("protect application lifecycle state: %w", err)
	}
	return state, nil
}

func (store Store) load(workloadRef string) (State, bool, error) {
	root, err := confinedfs.Open(filepath.Clean(store.Workspace))
	if err != nil {
		return State{}, false, fmt.Errorf("open application lifecycle workspace: %w", err)
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return State{}, false, fmt.Errorf("begin application lifecycle read: %w", err)
	}
	defer transaction.Close()
	return loadWithTransaction(transaction, workloadRef)
}

func loadWithTransaction(transaction *confinedfs.Transaction, workloadRef string) (State, bool, error) {
	relative := stateRoot + "/" + workloadRef + ".json"
	exists, info, err := transaction.Exists(relative)
	if err != nil {
		return State{}, false, fmt.Errorf("inspect application lifecycle state: %w", err)
	}
	if !exists {
		return State{}, false, nil
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return State{}, false, errors.New("application lifecycle state file is invalid")
	}
	raw, _, err := transaction.ReadStable(relative)
	if err != nil {
		return State{}, false, fmt.Errorf("read application lifecycle state: %w", err)
	}
	var state State
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, false, fmt.Errorf("decode application lifecycle state: %w", err)
	}
	return state, true, nil
}

func validateContract(contract Contract) error {
	if !contractIDPattern.MatchString(contract.WorkloadRef) || !contractIDPattern.MatchString(contract.PackageRef) ||
		!digestPattern.MatchString(contract.PlanHash) || !digestPattern.MatchString(contract.ContractHash) ||
		strings.TrimSpace(contract.Version) == "" || len(contract.Stages) != len(allowedStages) {
		return errors.New("application lifecycle contract is incomplete")
	}
	for name := range allowedStages {
		if stage, ok := contract.Stages[name]; !ok || stage.Name != name ||
			len(stage.Operations) == 0 || len(stage.Phases) == 0 {
			return fmt.Errorf("application lifecycle contract stage %q is incomplete", name)
		}
	}
	return nil
}

func validateState(state State, contract Contract) error {
	if state.APIVersion != APIVersion || state.WorkloadRef != contract.WorkloadRef {
		return errors.New("persisted application lifecycle state belongs to a different workload")
	}
	if err := validateAuthority(state.Authority); err != nil || state.Authority.PackageRef != contract.PackageRef {
		return errors.New("persisted application lifecycle state belongs to a different package authority")
	}
	previous := ""
	for index, operation := range state.Operations {
		if err := validateAuthority(operation.Authority); err != nil ||
			operation.Authority.PackageRef != contract.PackageRef {
			return fmt.Errorf("application lifecycle operation authority is invalid at index %d", index)
		}
		if operation.PreviousDigest != previous || operation.Digest != operationDigest(operation) {
			return fmt.Errorf("application lifecycle operation digest chain is invalid at index %d", index)
		}
		previous = operation.Digest
	}
	if state.StateDigest != stateDigest(state) {
		return errors.New("application lifecycle state digest is invalid")
	}
	if len(state.Operations) == 0 && state.CurrentOperation != "" {
		return errors.New("application lifecycle current operation has no history")
	}
	if len(state.Operations) > 0 && state.CurrentOperation != state.Operations[len(state.Operations)-1].ID {
		return errors.New("application lifecycle current operation differs from history")
	}
	if len(state.Operations) > 0 && state.Authority != state.Operations[len(state.Operations)-1].Authority {
		return errors.New("application lifecycle current authority differs from history")
	}
	return nil
}

func validateTransition(from, to string) error {
	allowed := false
	switch from {
	case StatusRunning:
		allowed = to == StatusSucceeded || to == StatusFailed || to == StatusRecoveryRequired
	case StatusFailed:
		allowed = to == StatusRunning || to == StatusRecoveryRequired
	case StatusRecoveryRequired:
		allowed = to == StatusRecovered
	case StatusRecovered:
		allowed = to == StatusRunning
	}
	if !allowed {
		return fmt.Errorf("application lifecycle transition %s -> %s is not allowed", from, to)
	}
	return nil
}

func newState(contract Contract) State {
	return State{
		APIVersion: APIVersion, WorkloadRef: contract.WorkloadRef,
		Authority:  authorityFromContract(contract),
		Operations: []Operation{},
	}
}

func authorityFromContract(contract Contract) Authority {
	return Authority{
		PlanHash: contract.PlanHash, LifecycleContractHash: contract.ContractHash,
		LifecycleVersion: contract.Version, PackageRef: contract.PackageRef,
	}
}

func validateAuthority(authority Authority) error {
	if !digestPattern.MatchString(authority.PlanHash) ||
		!digestPattern.MatchString(authority.LifecycleContractHash) ||
		!contractIDPattern.MatchString(authority.PackageRef) ||
		strings.TrimSpace(authority.LifecycleVersion) == "" {
		return errors.New("application lifecycle authority is invalid")
	}
	return nil
}

func operationDigest(operation Operation) string {
	operation.Digest = ""
	raw, _ := json.Marshal(operation)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func stateDigest(state State) string {
	state.StateDigest = ""
	raw, _ := json.Marshal(state)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func stringList(value any) ([]string, error) {
	raw, ok := value.([]any)
	if !ok {
		return nil, errors.New("value is not a list")
	}
	result := make([]string, len(raw))
	for index, item := range raw {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, errors.New("list contains an invalid string")
		}
		result[index] = text
	}
	return result, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sortedUniqueEvidence(values []Evidence) []Evidence {
	seen := make(map[string]struct{}, len(values))
	result := make([]Evidence, 0, len(values))
	for _, value := range values {
		value.Kind = strings.TrimSpace(value.Kind)
		value.Ref = strings.TrimSpace(value.Ref)
		value.Digest = strings.TrimSpace(value.Digest)
		key := value.Kind + "\x00" + value.Ref + "\x00" + value.Digest
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		if result[i].Ref != result[j].Ref {
			return result[i].Ref < result[j].Ref
		}
		return result[i].Digest < result[j].Digest
	})
	return result
}

func validateCompletedEvidence(stage StageContract, evidence []Evidence) error {
	if err := validatePartialEvidence(stage, evidence); err != nil {
		return err
	}
	if len(evidence) != len(stage.Evidence) {
		return fmt.Errorf("completed application lifecycle stage %q requires its exact evidence set", stage.Name)
	}
	have := make(map[string]struct{}, len(evidence))
	for _, item := range evidence {
		have[item.Kind] = struct{}{}
	}
	for _, kind := range stage.Evidence {
		if _, ok := have[kind]; !ok {
			return fmt.Errorf("completed application lifecycle stage %q is missing %q evidence", stage.Name, kind)
		}
	}
	return nil
}

func validatePartialEvidence(stage StageContract, evidence []Evidence) error {
	allowed := make(map[string]struct{}, len(stage.Evidence))
	for _, kind := range stage.Evidence {
		allowed[kind] = struct{}{}
	}
	seen := make(map[string]struct{}, len(evidence))
	for _, item := range evidence {
		if _, ok := allowed[item.Kind]; !ok {
			return fmt.Errorf("application lifecycle stage %q does not admit %q evidence", stage.Name, item.Kind)
		}
		if _, duplicate := seen[item.Kind]; duplicate {
			return fmt.Errorf("application lifecycle stage %q contains duplicate %q evidence", stage.Name, item.Kind)
		}
		seen[item.Kind] = struct{}{}
		if item.Ref == "" || len(item.Ref) > 1024 || strings.ContainsAny(item.Ref, "\x00\r\n") {
			return fmt.Errorf("application lifecycle %q evidence reference is invalid", item.Kind)
		}
		if !digestPattern.MatchString(item.Digest) {
			return fmt.Errorf("application lifecycle %q evidence digest is invalid", item.Kind)
		}
	}
	return nil
}

// NewOperationID returns a collision-resistant local operation identity. The
// operationRef prefix keeps persisted state readable without making wall-clock
// time or a caller-provided identifier authoritative.
func NewOperationID(operationRef string) (string, error) {
	prefix := strings.TrimSpace(operationRef)
	if !contractIDPattern.MatchString(strings.ReplaceAll(prefix, ".", "-")) {
		return "", errors.New("application lifecycle operation reference is invalid")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("create application lifecycle operation identity: %w", err)
	}
	id := prefix + "-" + hex.EncodeToString(nonce)
	if !operationIDPattern.MatchString(id) {
		return "", errors.New("generated application lifecycle operation identity is invalid")
	}
	return id, nil
}

func normalizedNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

func operationIndex(state State, id string) int {
	for index := range state.Operations {
		if state.Operations[index].ID == id {
			return index
		}
	}
	return -1
}

func currentOperation(state State) *Operation {
	if len(state.Operations) == 0 {
		return nil
	}
	return &state.Operations[len(state.Operations)-1]
}

func operationUpdatedAt(state State) time.Time {
	if len(state.Operations) == 0 {
		return time.Now().UTC()
	}
	return state.Operations[len(state.Operations)-1].UpdatedAt
}
