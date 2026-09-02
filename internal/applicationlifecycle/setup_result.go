package applicationlifecycle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	SetupResultAPIVersion = "stackkit.application-setup-result/v1"

	// VaultOwnerInviteActionRef is the sole action allowed to persist the
	// preparation facts below. They describe server-side invitation metadata;
	// neither fact proves personal login or client-side decryption.
	VaultOwnerInviteActionRef       = "vault-owner-invite"
	VaultOwnerPreparationInvited    = "owner-invited"
	VaultOwnerPreparationRegistered = "owner-registered"
)

// SetupResult is a secret-free observation of the application API. It is
// evidence for the existing application lifecycle, not a second setup state.
type SetupResult struct {
	APIVersion         string    `json:"apiVersion"`
	Authority          Authority `json:"authority"`
	WorkloadRef        string    `json:"workloadRef"`
	OperationID        string    `json:"operationId"`
	ActionRef          string    `json:"actionRef"`
	ApplyResultHash    string    `json:"applyResultHash"`
	ArtifactDigest     string    `json:"artifactDigest"`
	InstanceRef        string    `json:"instanceRef"`
	ApplicationVersion string    `json:"applicationVersion"`
	AccountRef         string    `json:"accountRef"`
	Initialized        bool      `json:"initialized"`
	AdminLoginVerified bool      `json:"adminLoginVerified"`
	OnboardingComplete bool      `json:"onboardingComplete"`
	Preparation        string    `json:"preparation,omitempty"`
	VerifiedAt         time.Time `json:"verifiedAt"`
}

type signedSetupResult struct {
	Result    SetupResult                                   `json:"result"`
	Signature localevidence.OwnerLifecycleMutationSignature `json:"signature"`
}

// SaveSetupResult seals one result before the existing lifecycle operation
// becomes terminal. A crash may leave an unreferenced immutable receipt; a
// retry re-observes the app and can safely finalize the same operation.
func (store Store) SaveSetupResult(contract Contract, result SetupResult) (Evidence, error) {
	if err := validateSetupResult(contract, result); err != nil {
		return Evidence{}, err
	}
	state, err := store.Load(contract)
	if err != nil {
		return Evidence{}, err
	}
	operation := currentOperation(state)
	if operation == nil || operation.ID != result.OperationID || operation.Stage != "setup" || operation.OperationRef != "stackkit.setup" || operation.Authority != result.Authority || operation.Status != StatusRunning {
		return Evidence{}, errors.New("setup result has no matching running application lifecycle operation")
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return Evidence{}, err
	}
	signature, err := localevidence.SignOwnerLifecycleMutation(store.Workspace, payload)
	if err != nil {
		return Evidence{}, err
	}
	raw, err := json.Marshal(signedSetupResult{Result: result, Signature: signature})
	if err != nil {
		return Evidence{}, err
	}
	digest := setupDigest(raw)
	relative := setupResultPath(contract.WorkloadRef, digest)
	root, err := confinedfs.Open(store.Workspace)
	if err != nil {
		return Evidence{}, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return Evidence{}, err
	}
	defer transaction.Close()
	directory := filepath.ToSlash(filepath.Dir(relative))
	if err := transaction.MkdirAll(directory, 0700); err != nil {
		return Evidence{}, err
	}
	if err := backupcustody.ProtectPrivatePath(filepath.Join(root.Name(), filepath.FromSlash(directory)), true); err != nil {
		return Evidence{}, err
	}
	view, err := root.View(".")
	if err != nil {
		return Evidence{}, err
	}
	written, err := view.WriteAtomic0600NoReplace(relative, raw)
	if err != nil || !written.Installed || !written.FileSynced {
		return Evidence{}, fmt.Errorf("persist immutable setup result: %w", errors.Join(err, errors.New("setup result is not durably installed")))
	}
	if err := backupcustody.ProtectPrivatePath(filepath.Join(root.Name(), filepath.FromSlash(relative)), false); err != nil {
		return Evidence{}, err
	}
	return Evidence{Kind: "setup-result", Ref: relative, Digest: digest}, nil
}

// SetupRuns projects the existing journal. Only operations with the exact
// current lifecycle Authority enter the current setup axis. An operation may
// be current and failed/running without a terminal receipt; only a matching
// signed Apply-bound receipt gains a verified setup result.
func (store Store) SetupRuns(contract Contract, applyResultHash, actionRef string) ([]SetupRun, error) {
	state, err := store.Load(contract)
	if err != nil {
		return nil, err
	}
	var runs []SetupRun
	currentAuthority := authorityFromContract(contract)
	for _, operation := range state.Operations {
		if operation.Stage != "setup" || operation.OperationRef != "stackkit.setup" {
			continue
		}
		if operation.Authority != currentAuthority {
			// The lifecycle journal intentionally retains prior Plan history, but
			// it is not current setup evidence and must not block this Plan.
			continue
		}
		run := SetupRun{WorkloadRef: contract.WorkloadRef, DropName: actionRef, RunID: operation.ID,
			PlanHash: contract.PlanHash, Policy: "on-demand", Status: operation.Status,
			LastStarted: operation.StartedAt, LastFinished: operation.CompletedAt,
			Error: operation.LastError}
		if operation.Status == StatusSucceeded {
			run.Status, run.Phase = "completed", "verified"
			for _, evidence := range operation.Evidence {
				if evidence.Kind != "setup-result" {
					continue
				}
				result, err := store.readSetupResult(contract, operation, evidence)
				if err != nil {
					return nil, err
				}
				if result.ActionRef != actionRef {
					return nil, errors.New("setup result action differs from the current Plan")
				}
				run.DropName = result.ActionRef
				run.Evidence = append(run.Evidence, ExperienceEvidence{Kind: evidence.Kind, Ref: evidence.Ref, Digest: evidence.Digest})
				if result.ApplyResultHash == applyResultHash && result.Authority == authorityFromContract(contract) {
					run.PlanHash = contract.PlanHash
					run.authenticated = true
				} else {
					run.Message = "setup receipt belongs to an earlier Plan or Apply"
				}
				if run.authenticated && !result.OnboardingComplete {
					run.Status, run.Phase = "waiting", "configured"
					if result.ActionRef == VaultOwnerInviteActionRef {
						run.Message = "Vaultwarden owner invitation is prepared; complete encrypted account setup in the official client"
					} else {
						run.Message = "owner login is verified; app onboarding remains open"
					}
				}
			}
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (store Store) readSetupResult(contract Contract, operation Operation, evidence Evidence) (SetupResult, error) {
	if !digestPattern.MatchString(evidence.Digest) || evidence.Ref != setupResultPath(contract.WorkloadRef, evidence.Digest) {
		return SetupResult{}, errors.New("setup evidence has an invalid immutable reference")
	}
	root, err := confinedfs.Open(store.Workspace)
	if err != nil {
		return SetupResult{}, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return SetupResult{}, err
	}
	defer transaction.Close()
	if err := backupcustody.RequirePrivatePath(filepath.Join(root.Name(), filepath.FromSlash(evidence.Ref)), false); err != nil {
		return SetupResult{}, err
	}
	raw, _, err := transaction.ReadStable(evidence.Ref)
	if err != nil {
		return SetupResult{}, err
	}
	if setupDigest(raw) != evidence.Digest {
		return SetupResult{}, errors.New("setup evidence digest differs from its receipt")
	}
	var envelope signedSetupResult
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return SetupResult{}, err
	}
	canonical, err := json.Marshal(envelope)
	if err != nil || !bytes.Equal(canonical, raw) {
		return SetupResult{}, errors.New("setup evidence is not canonical")
	}
	payload, err := json.Marshal(envelope.Result)
	if err != nil {
		return SetupResult{}, err
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(store.Workspace, payload, envelope.Signature); err != nil {
		return SetupResult{}, err
	}
	result := envelope.Result
	// Historical receipts stay readable as historical evidence; they never
	// inherit the current contract authority merely from a matching workload.
	historical := contract
	historical.PlanHash, historical.ContractHash, historical.Version, historical.PackageRef = result.Authority.PlanHash, result.Authority.LifecycleContractHash, result.Authority.LifecycleVersion, result.Authority.PackageRef
	if err := validateSetupResult(historical, result); err != nil {
		return SetupResult{}, err
	}
	if result.OperationID != operation.ID || result.Authority != operation.Authority {
		return SetupResult{}, errors.New("setup receipt differs from its lifecycle operation")
	}
	return result, nil
}

func validateSetupResult(contract Contract, result SetupResult) error {
	if result.APIVersion != SetupResultAPIVersion || result.Authority != authorityFromContract(contract) || result.WorkloadRef != contract.WorkloadRef ||
		!operationIDPattern.MatchString(result.OperationID) || !contractIDPattern.MatchString(result.ActionRef) || !digestPattern.MatchString(result.ApplyResultHash) || !digestPattern.MatchString(result.ArtifactDigest) ||
		result.InstanceRef == "" || result.ApplicationVersion == "" || result.AccountRef == "" || result.VerifiedAt.IsZero() || !result.AdminLoginVerified {
		return errors.New("application setup result is incomplete or differs from the admitted authority")
	}
	if result.ActionRef == VaultOwnerInviteActionRef {
		if result.Initialized || result.OnboardingComplete || (result.Preparation != VaultOwnerPreparationInvited && result.Preparation != VaultOwnerPreparationRegistered) {
			return errors.New("Vaultwarden invitation setup result has invalid personal-account state")
		}
	} else if result.Preparation != "" || !result.Initialized {
		return errors.New("application setup result has an invalid preparation state")
	}
	return nil
}

func setupDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}
func setupResultPath(workload, digest string) string {
	return stateRoot + "/setup-results/" + workload + "/" + strings.TrimPrefix(digest, "sha256:") + ".json"
}
