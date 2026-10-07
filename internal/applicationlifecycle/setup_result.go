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

	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	SetupResultAPIVersion = "stackkit.application-setup-result/v1"

	// VaultOwnerInviteActionRef is the sole action allowed to persist the
	// preparation facts below. They describe server-side invitation metadata;
	// neither fact proves personal login or client-side decryption.
	VaultOwnerInviteActionRef = "vault-owner-invite"
	// ImmichAddOnAPIKeyActionRef issues an add-on API key through the verified
	// Immich administrator; its result records the issued key, not an account.
	ImmichAddOnAPIKeyActionRef   = "immich-add-on-api-key"
	ImmichAddOnAPIKeyPreparation = "immich-api-key-issued"
	// ComfyUIModelDownloadActionRef installs a reviewed model preset. ComfyUI
	// has no accounts, so no administrator login is verified; the result
	// records the preset (Preparation "model-preset-<id>").
	ComfyUIModelDownloadActionRef = "comfyui-model-download"
	ComfyUIModelPresetPreparation = "model-preset-"
	// KombifyAIConnectorEnabledPreparation and KombifyAIConnectorDisabled-
	// Preparation report `stackkit setup ai-connect`. It changes the owner's
	// intent (connector token custody and the kombify-connector setting) ahead
	// of the next apply, so it observes no application and seals no setup
	// receipt; the next apply records the connector like any component.
	KombifyAIConnectorEnabledPreparation  = "kombify-ai-connector-enabled"
	KombifyAIConnectorDisabledPreparation = "kombify-ai-connector-disabled"
	VaultOwnerPreparationInvited          = "owner-invited"
	VaultOwnerPreparationRegistered       = "owner-registered"
	OwnerEmailVerificationPending         = "pending"
	OwnerEmailVerificationVerified        = "verified"
)

// HomeAssistantConnectorDeployment contains the local facts observed by the node.
// External resource/revision remain correlation in Issuer.RequestedBinding.
type HomeAssistantConnectorDeployment struct {
	Node        localevidence.LocalBinding `json:"node"`
	InstanceRef string                     `json:"instanceRef"`
	ContainerID string                     `json:"containerId"`
	ImageDigest string                     `json:"imageDigest"`
}
type HomeAssistantConnectorObservation struct {
	Issuer                  appsetup.HomeAssistantConnectorEvidence `json:"issuer"`
	VerifiedLocalDeployment HomeAssistantConnectorDeployment        `json:"verifiedLocalDeployment"`
}

// SetupResult is a secret-free observation of the application API. It is
// evidence for the existing application lifecycle, not a second setup state.
type SetupResult struct {
	HomeAssistantConnector *HomeAssistantConnectorObservation `json:"homeAssistantConnector,omitempty"`
	APIVersion             string                             `json:"apiVersion"`
	Authority              Authority                          `json:"authority"`
	WorkloadRef            string                             `json:"workloadRef"`
	OperationID            string                             `json:"operationId"`
	ActionRef              string                             `json:"actionRef"`
	ApplyResultHash        string                             `json:"applyResultHash"`
	ArtifactDigest         string                             `json:"artifactDigest"`
	InstanceRef            string                             `json:"instanceRef"`
	ApplicationVersion     string                             `json:"applicationVersion"`
	AccountRef             string                             `json:"accountRef"`
	Initialized            bool                               `json:"initialized"`
	AdminLoginVerified     bool                               `json:"adminLoginVerified"`
	OnboardingComplete     bool                               `json:"onboardingComplete"`
	Preparation            string                             `json:"preparation,omitempty"`
	EmailVerification      string                             `json:"emailVerification,omitempty"`
	VerifiedAt             time.Time                          `json:"verifiedAt"`
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
	if connector := result.HomeAssistantConnector; connector != nil {
		issuer := connector.Issuer
		signature := issuer.Signature
		issuer.Signature = localevidence.OwnerPolicyStateSignature{}
		payload, _ := json.Marshal(issuer)
		if err := localevidence.VerifyOwnerPolicyState(store.Workspace, payload, signature); err != nil {
			return Evidence{}, err
		}
	}
	if result.ActionRef == VaultOwnerInviteActionRef && result.EmailVerification == "" {
		return Evidence{}, errors.New("Vaultwarden invitation setup result omitted owner email verification")
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
					run.HomeAssistantConnector = result.HomeAssistantConnector
				} else {
					run.Message = "setup receipt belongs to an earlier Plan or Apply"
				}
				if run.authenticated && !result.OnboardingComplete {
					run.Status, run.Phase = "waiting", "configured"
					if result.ActionRef == VaultOwnerInviteActionRef {
						if result.EmailVerification == OwnerEmailVerificationPending {
							run.Message = "Vaultwarden owner invitation and PocketID SMTP are prepared; confirm the owner email before native sign-in"
						} else if result.EmailVerification == OwnerEmailVerificationVerified {
							run.Message = "Vaultwarden owner invitation and owner email confirmation are verified; complete encrypted account setup in the official client"
						} else {
							run.Message = "Vaultwarden owner invitation is prepared; owner email confirmation is not recorded"
						}
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
	result, _, err := store.readSetupResultBytes(contract, operation, evidence)
	return result, err
}

// ExportSetupResult returns the exact immutable signed bytes after the same
// verification used by lifecycle projection. Historical or nonterminal results
// cannot become current dispatch evidence.
func (store Store) ExportSetupResult(contract Contract, operationID, applyResultHash string) (SetupResult, json.RawMessage, error) {
	state, err := store.Load(contract)
	if err != nil {
		return SetupResult{}, nil, err
	}
	for _, operation := range state.Operations {
		if operation.ID != operationID {
			continue
		}
		if operation.Stage != "setup" || operation.OperationRef != "stackkit.setup" || operation.Status != StatusSucceeded || operation.Authority != authorityFromContract(contract) {
			return SetupResult{}, nil, errors.New("setup receipt is not a current successful operation")
		}
		var result SetupResult
		var raw json.RawMessage
		for _, evidence := range operation.Evidence {
			if evidence.Kind != "setup-result" {
				continue
			}
			if raw != nil {
				return SetupResult{}, nil, errors.New("setup receipt is ambiguous")
			}
			result, raw, err = store.readSetupResultBytes(contract, operation, evidence)
			if err != nil {
				return SetupResult{}, nil, err
			}
		}
		if raw == nil || result.Authority != authorityFromContract(contract) || result.ApplyResultHash != applyResultHash {
			return SetupResult{}, nil, errors.New("setup receipt does not cover current Apply")
		}
		return result, raw, nil
	}
	return SetupResult{}, nil, errors.New("setup receipt is unavailable; do not repeat an uncertain effect")
}

func (store Store) readSetupResultBytes(contract Contract, operation Operation, evidence Evidence) (SetupResult, json.RawMessage, error) {
	if !digestPattern.MatchString(evidence.Digest) || evidence.Ref != setupResultPath(contract.WorkloadRef, evidence.Digest) {
		return SetupResult{}, nil, errors.New("setup evidence has an invalid immutable reference")
	}
	root, err := confinedfs.Open(store.Workspace)
	if err != nil {
		return SetupResult{}, nil, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return SetupResult{}, nil, err
	}
	defer transaction.Close()
	if err := backupcustody.RequirePrivatePath(filepath.Join(root.Name(), filepath.FromSlash(evidence.Ref)), false); err != nil {
		return SetupResult{}, nil, err
	}
	raw, _, err := transaction.ReadStable(evidence.Ref)
	if err != nil {
		return SetupResult{}, nil, err
	}
	if setupDigest(raw) != evidence.Digest {
		return SetupResult{}, nil, errors.New("setup evidence digest differs from its receipt")
	}
	var envelope signedSetupResult
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return SetupResult{}, nil, err
	}
	canonical, err := json.Marshal(envelope)
	if err != nil || !bytes.Equal(canonical, raw) {
		return SetupResult{}, nil, errors.New("setup evidence is not canonical")
	}
	payload, err := json.Marshal(envelope.Result)
	if err != nil {
		return SetupResult{}, nil, err
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(store.Workspace, payload, envelope.Signature); err != nil {
		return SetupResult{}, nil, err
	}
	result := envelope.Result
	// Historical receipts stay readable as historical evidence; they never
	// inherit the current contract authority merely from a matching workload.
	historical := contract
	historical.PlanHash, historical.ContractHash, historical.Version, historical.PackageRef = result.Authority.PlanHash, result.Authority.LifecycleContractHash, result.Authority.LifecycleVersion, result.Authority.PackageRef
	if err := validateSetupResult(historical, result); err != nil {
		return SetupResult{}, nil, err
	}
	if result.OperationID != operation.ID || result.Authority != operation.Authority {
		return SetupResult{}, nil, errors.New("setup receipt differs from its lifecycle operation")
	}
	return result, append(json.RawMessage(nil), raw...), nil
}

func validateSetupResult(contract Contract, result SetupResult) error {
	if connector := result.HomeAssistantConnector; connector != nil {
		local, issuer := connector.VerifiedLocalDeployment, connector.Issuer
		if result.ActionRef != "home-assistant-owner-bootstrap" || local.InstanceRef != result.InstanceRef || local.Node != issuer.RequestedBinding.Node || local.Node.SiteRef == "" || local.Node.NodeRef == "" || local.Node.ChannelRef == "" || local.ContainerID != issuer.RequestedBinding.ContainerID || local.ImageDigest != issuer.RequestedBinding.ImageDigest || !digestPattern.MatchString(local.ImageDigest) || !digestPattern.MatchString("sha256:"+local.ContainerID) || issuer.HAUserID != result.AccountRef || issuer.HAVersion != result.ApplicationVersion || !strings.HasPrefix(issuer.SecretRef, "secret://home-assistant/connector/") || issuer.ObservedAt.IsZero() || (issuer.Status != "active" && issuer.Status != "revoked") {
			return errors.New("Home Assistant connector observation differs from the verified setup deployment")
		}
	}
	if result.APIVersion != SetupResultAPIVersion || result.Authority != authorityFromContract(contract) || result.WorkloadRef != contract.WorkloadRef ||
		!operationIDPattern.MatchString(result.OperationID) || !contractIDPattern.MatchString(result.ActionRef) || !digestPattern.MatchString(result.ApplyResultHash) || !digestPattern.MatchString(result.ArtifactDigest) ||
		result.InstanceRef == "" || result.ApplicationVersion == "" || result.AccountRef == "" || result.VerifiedAt.IsZero() ||
		(!result.AdminLoginVerified && result.ActionRef != ComfyUIModelDownloadActionRef) {
		return errors.New("application setup result is incomplete or differs from the admitted authority")
	}
	switch result.ActionRef {
	case ImmichAddOnAPIKeyActionRef:
		if result.Preparation != ImmichAddOnAPIKeyPreparation || result.EmailVerification != "" || !result.Initialized {
			return errors.New("Immich add-on setup result has an invalid preparation state")
		}
		return nil
	case ComfyUIModelDownloadActionRef:
		preset, ok := strings.CutPrefix(result.Preparation, ComfyUIModelPresetPreparation)
		if !ok || !contractIDPattern.MatchString(preset) || result.AccountRef != "preset:"+preset ||
			result.EmailVerification != "" || !result.Initialized || result.AdminLoginVerified {
			return errors.New("ComfyUI model setup result has an invalid preparation state")
		}
		return nil
	}
	if result.ActionRef == VaultOwnerInviteActionRef {
		if result.Initialized || result.OnboardingComplete || (result.Preparation != VaultOwnerPreparationInvited && result.Preparation != VaultOwnerPreparationRegistered) {
			return errors.New("Vaultwarden invitation setup result has invalid personal-account state")
		}
		// Empty is accepted only when reading signed historical v1 receipts;
		// SaveSetupResult requires the field on every new Vault observation.
		if result.EmailVerification != "" && result.EmailVerification != OwnerEmailVerificationPending && result.EmailVerification != OwnerEmailVerificationVerified {
			return errors.New("Vaultwarden invitation setup result has invalid owner email verification state")
		}
	} else if result.Preparation != "" || result.EmailVerification != "" || !result.Initialized {
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
