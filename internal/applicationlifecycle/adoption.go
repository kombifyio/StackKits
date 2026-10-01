package applicationlifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const AdoptionProfile = "home-assistant-container-in-place/v1"
const AdoptionSchemaVersion = "stackkit.application-adoption/v1"

type AdoptionContract struct {
	ProfileRef      string   `json:"profileRef"`
	Mode            string   `json:"mode"`
	SourceRuntime   string   `json:"sourceRuntime"`
	ImageRef        string   `json:"imageRef"`
	ImageDigest     string   `json:"imageDigest"`
	Version         string   `json:"version"`
	OwnedFields     []string `json:"ownedFields"`
	PreservedFields []string `json:"preservedFields"`
	Operations      []string `json:"operations"`
}

// AdoptionSource contains hashes of inspected configuration, never environment
// values or account material. Revision excludes changing runtime power state.
type AdoptionSource struct {
	ContainerID         string `json:"containerId"`
	ImageID             string `json:"imageId"`
	ImageDigest         string `json:"imageDigest"`
	Revision            string `json:"revision"`
	ConfigMount         string `json:"configMount"`
	ConfigurationDigest string `json:"configurationDigest"`
	AccountsDigest      string `json:"accountsDigest"`
}

type AdoptionPlan struct {
	SchemaVersion      string         `json:"schemaVersion"`
	APIVersion         string         `json:"apiVersion"`
	Authority          Authority      `json:"authority"`
	WorkloadRef        string         `json:"workloadRef"`
	ProfileRef         string         `json:"profileRef"`
	Mode               string         `json:"mode"`
	Source             AdoptionSource `json:"source"`
	OwnedFields        []string       `json:"ownedFields"`
	PreservedFields    []string       `json:"preservedFields"`
	AdmittedOperations []string       `json:"admittedOperations"`
	VerifiedOwnerRef   string         `json:"verifiedOwnerRef"`
	PlanDigest         string         `json:"planDigest,omitempty"`
}

type AdoptionResult struct {
	APIVersion         string         `json:"apiVersion"`
	Authority          Authority      `json:"authority"`
	WorkloadRef        string         `json:"workloadRef"`
	OperationID        string         `json:"operationId"`
	ProfileRef         string         `json:"profileRef"`
	Mode               string         `json:"mode"`
	PlanDigest         string         `json:"planDigest"`
	Source             AdoptionSource `json:"source"`
	OwnedFields        []string       `json:"ownedFields"`
	PreservedFields    []string       `json:"preservedFields"`
	AdmittedOperations []string       `json:"admittedOperations"`
	VerifiedOwnerRef   string         `json:"verifiedOwnerRef"`
	Status             string         `json:"status"`
	Action             string         `json:"action,omitempty"`
	PowerState         string         `json:"powerState,omitempty"`
	VerifiedAt         time.Time      `json:"verifiedAt"`
}

type AdoptionReceipt struct {
	SchemaVersion string                                        `json:"schemaVersion"`
	Result        AdoptionResult                                `json:"result"`
	Signature     localevidence.OwnerLifecycleMutationSignature `json:"signature"`
	Evidence      Evidence                                      `json:"evidence"`
}

func NewAdoptionPlan(contract Contract, source AdoptionSource, ownerRef string) (AdoptionPlan, error) {
	if contract.Adoption == nil || contract.Adoption.ProfileRef != AdoptionProfile || contract.Delivery.AdapterRef != "standalone-compose" || contract.WorkloadRef != "smart-home" {
		return AdoptionPlan{}, errors.New("application adoption is unsupported for the selected lifecycle and runtime adapter")
	}
	a := contract.Adoption
	p := AdoptionPlan{SchemaVersion: AdoptionSchemaVersion, APIVersion: "stackkit.application-adoption-plan/v1", Authority: authorityFromContract(contract), WorkloadRef: contract.WorkloadRef, ProfileRef: a.ProfileRef, Mode: a.Mode, Source: source, OwnedFields: a.OwnedFields, PreservedFields: a.PreservedFields, AdmittedOperations: a.Operations, VerifiedOwnerRef: ownerRef}
	raw, err := resolvedplan.CanonicalJSON(p)
	if contract.AdoptionBaselinePlanHash != "" {
		raw, err = resolvedplan.CanonicalJSON(map[string]any{"plan": p, "baselinePlanHash": contract.AdoptionBaselinePlanHash})
	}
	if err != nil {
		return AdoptionPlan{}, err
	}
	p.PlanDigest = setupDigest(raw)
	return p, nil
}

// SaveAdoptionResult seals evidence before terminal journal publication. A
// membership consumer must accept this receipt, never a successful exit alone.
func (store Store) SaveAdoptionResult(contract Contract, result AdoptionResult) (AdoptionReceipt, error) {
	state, err := store.Load(contract)
	if err != nil {
		return AdoptionReceipt{}, err
	}
	op := currentOperation(state)
	if op == nil || op.ID != result.OperationID || op.Stage != "adopt" || op.Status != StatusRunning || result.Authority != authorityFromContract(contract) || result.WorkloadRef != contract.WorkloadRef || (result.Status != "verified" && result.Status != "released") {
		return AdoptionReceipt{}, errors.New("adoption receipt has no matching running lifecycle operation")
	}
	payload, err := resolvedplan.CanonicalJSON(result)
	if err != nil {
		return AdoptionReceipt{}, err
	}
	signature, err := localevidence.SignOwnerLifecycleMutation(store.Workspace, payload)
	if err != nil {
		return AdoptionReceipt{}, err
	}
	receipt := AdoptionReceipt{SchemaVersion: AdoptionSchemaVersion, Result: result, Signature: signature}
	raw, err := resolvedplan.CanonicalJSON(receipt)
	if err != nil {
		return AdoptionReceipt{}, err
	}
	digest := setupDigest(raw)
	ref := adoptionReceiptPath(contract.WorkloadRef, digest)
	root, err := confinedfs.Open(store.Workspace)
	if err != nil {
		return AdoptionReceipt{}, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return AdoptionReceipt{}, err
	}
	defer tx.Close()
	dir := filepath.ToSlash(filepath.Dir(ref))
	if err := tx.MkdirAll(dir, 0700); err != nil {
		return AdoptionReceipt{}, err
	}
	if err := backupcustody.ProtectPrivatePath(filepath.Join(root.Name(), filepath.FromSlash(dir)), true); err != nil {
		return AdoptionReceipt{}, err
	}
	view, err := root.View(".")
	if err != nil {
		return AdoptionReceipt{}, err
	}
	written, err := view.WriteAtomic0600NoReplace(ref, raw)
	if err != nil || !written.Installed || !written.FileSynced {
		return AdoptionReceipt{}, fmt.Errorf("persist adoption receipt: %w", errors.Join(err, errors.New("receipt was not durably installed")))
	}
	if err := backupcustody.ProtectPrivatePath(filepath.Join(root.Name(), filepath.FromSlash(ref)), false); err != nil {
		return AdoptionReceipt{}, err
	}
	receipt.Evidence = Evidence{Kind: "adoption-result", Ref: ref, Digest: digest}
	return receipt, nil
}

// AdoptionBinding derives membership from the existing lifecycle journal and
// verifies the Owner signature using local trusted custody. It also reads old
// authority receipts so changing the Plan cannot evade a preservation guard.
func (store Store) AdoptionBinding(workload string) (AdoptionReceipt, bool, error) {
	state, exists, err := store.load(workload)
	if err != nil || !exists {
		return AdoptionReceipt{}, false, err
	}
	if state.StateDigest != stateDigest(state) {
		return AdoptionReceipt{}, false, errors.New("application lifecycle state digest is invalid")
	}
	for i := len(state.Operations) - 1; i >= 0; i-- {
		op := state.Operations[i]
		if op.Stage != "adopt" || op.Status != StatusSucceeded {
			continue
		}
		if op.Digest != operationDigest(op) {
			return AdoptionReceipt{}, false, errors.New("adoption operation digest is invalid")
		}
		if len(op.Evidence) != 1 {
			return AdoptionReceipt{}, false, errors.New("adoption operation evidence is invalid")
		}
		e := op.Evidence[0]
		if e.Kind != "adoption-result" || e.Ref != adoptionReceiptPath(workload, e.Digest) || !digestPattern.MatchString(e.Digest) {
			return AdoptionReceipt{}, false, errors.New("adoption receipt reference is invalid")
		}
		root, err := confinedfs.Open(store.Workspace)
		if err != nil {
			return AdoptionReceipt{}, false, err
		}
		tx, err := root.BeginTransaction()
		if err != nil {
			root.Close()
			return AdoptionReceipt{}, false, err
		}
		raw, _, err := tx.ReadStable(e.Ref)
		tx.Close()
		root.Close()
		if err != nil {
			return AdoptionReceipt{}, false, err
		}
		var receipt AdoptionReceipt
		if setupDigest(raw) != e.Digest || json.Unmarshal(raw, &receipt) != nil {
			return AdoptionReceipt{}, false, errors.New("adoption receipt digest is invalid")
		}
		payload, err := resolvedplan.CanonicalJSON(receipt.Result)
		if err != nil {
			return AdoptionReceipt{}, false, err
		}
		if err := localevidence.VerifyOwnerLifecycleMutation(store.Workspace, payload, receipt.Signature); err != nil {
			return AdoptionReceipt{}, false, err
		}
		if receipt.Result.Authority != op.Authority || receipt.Result.OperationID != op.ID || receipt.Result.WorkloadRef != workload || receipt.Result.ProfileRef != AdoptionProfile || (receipt.Result.Status != "verified" && receipt.Result.Status != "released") {
			return AdoptionReceipt{}, false, errors.New("adoption receipt does not bind its lifecycle operation")
		}
		receipt.Evidence = e
		return receipt, receipt.Result.Status == "verified", nil
	}
	return AdoptionReceipt{}, false, nil
}

func adoptionReceiptPath(workload, digest string) string {
	return filepath.ToSlash(filepath.Join(stateRoot, workload, "adoption", strings.TrimPrefix(digest, "sha256:")+".json"))
}

func AdoptionIntentDigest(verb, action string, result AdoptionResult) (string, error) {
	raw, err := resolvedplan.CanonicalJSON(map[string]any{"verb": verb, "action": action, "authority": result.Authority, "source": result.Source, "planDigest": result.PlanDigest})
	if err != nil {
		return "", err
	}
	return setupDigest(raw), nil
}

func (store Store) RequireNoAdoption() error {
	// Only this explicitly supported module can currently acquire a binding.
	_, bound, err := store.AdoptionBinding("smart-home")
	if err != nil {
		return err
	}
	if bound {
		return errors.New("application_binding_preserved: release the adopted smart-home lifecycle before any fresh apply, generate, setup or removal; release retains its container, accounts and data")
	}
	return nil
}

// MarkAdoptionDispatch persists uncertainty before the only source side effect.
// A retry reconciles read-only; it may never send the action a second time.
func (store Store) MarkAdoptionDispatch(contract Contract, id, intent string) error {
	_, err := store.mutate(contract, func(state *State) error {
		op := currentOperation(*state)
		if op == nil || op.ID != id || op.OperationRef != "stackkit.application.control" || op.Status != StatusRunning || op.IntentDigest != intent || op.Dispatched {
			return errors.New("application dispatch has no exact undispatched intent")
		}
		op.Dispatched = true
		op.UpdatedAt = time.Now().UTC()
		op.Digest = operationDigest(*op)
		return nil
	})
	return err
}
