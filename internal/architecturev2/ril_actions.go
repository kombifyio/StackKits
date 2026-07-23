package architecturev2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/rilactionpolicy"
	"github.com/kombifyio/stackkits/internal/rilactionv2"
)

// RILActionExecutorCatalogEntry is the exact CUE-owned provider-free identity
// of one action owner. ContractHash is derived from the complete closed CUE
// body; it is not a binary digest or an external runtime attestation.
type RILActionExecutorCatalogEntry struct {
	SchemaVersion string `json:"schemaVersion"`
	Ref           string `json:"ref"`
	Version       string `json:"version"`
	Owner         struct {
		Authority string `json:"authority"`
	} `json:"owner"`
	OperationClasses []string `json:"operationClasses"`
	Prohibitions     struct {
		ProviderLifecycle    bool `json:"providerLifecycle"`
		ProviderInputs       bool `json:"providerInputs"`
		LeaseAuthority       bool `json:"leaseAuthority"`
		CredentialResolution bool `json:"credentialResolution"`
		Transport            bool `json:"transport"`
		CallerCommands       bool `json:"callerCommands"`
		ArbitraryPaths       bool `json:"arbitraryPaths"`
	} `json:"prohibitions"`
	ContractHash string `json:"contractHash"`
}

func (e RILActionExecutorCatalogEntry) identity() rilaction.ExecutorIdentity {
	return rilaction.ExecutorIdentity{Ref: e.Ref, Version: e.Version, ContractHash: e.ContractHash}
}

// RILActionPrimitiveCatalogEntry is a read-only projection of one exact
// CUE-governed approved-action contract. ContractOnly entries are deliberately
// non-executable; their presence must never be interpreted as runtime support.
type RILActionPrimitiveCatalogEntry struct {
	SchemaVersion         string                         `json:"schemaVersion"`
	ID                    string                         `json:"id"`
	Version               string                         `json:"version"`
	Title                 string                         `json:"title"`
	Category              string                         `json:"category"`
	Support               string                         `json:"support"`
	Mutation              bool                           `json:"mutation"`
	Destructive           bool                           `json:"destructive"`
	Risk                  string                         `json:"risk"`
	Owner                 RILActionPrimitiveOwner        `json:"owner"`
	ExtensionAuthority    *RILActionExtensionAuthority   `json:"extensionAuthority,omitempty"`
	Approval              RILActionPrimitiveApproval     `json:"approval"`
	Grant                 RILActionPrimitiveGrant        `json:"grant"`
	Target                RILActionPrimitiveTarget       `json:"target"`
	Inputs                []RILActionPrimitiveInput      `json:"inputs"`
	Verification          RILActionPrimitiveVerification `json:"verification"`
	Recovery              RILActionPrimitiveRecovery     `json:"recovery"`
	Evidence              RILActionPrimitiveEvidence     `json:"evidence"`
	Prohibitions          RILActionPrimitiveProhibitions `json:"prohibitions"`
	ExecutorRef           string                         `json:"executorRef,omitempty"`
	ExecutorVersion       string                         `json:"executorVersion,omitempty"`
	ExecutorContractHash  string                         `json:"executorContractHash,omitempty"`
	PrimitiveContractHash string                         `json:"primitiveContractHash"`
}

func (e RILActionPrimitiveCatalogEntry) executorIdentity() rilaction.ExecutorIdentity {
	return rilaction.ExecutorIdentity{
		Ref: e.ExecutorRef, Version: e.ExecutorVersion, ContractHash: e.ExecutorContractHash,
	}
}

type RILActionPrimitiveOwner struct {
	Authority      string `json:"authority"`
	OperationClass string `json:"operationClass"`
}

// RILActionExtensionAuthority is derived only from the owning CUE module
// contract. It narrows target admission and grants no execution authority.
type RILActionExtensionAuthority struct {
	Kind        string `json:"kind"`
	ModuleRef   string `json:"moduleRef"`
	ProviderRef string `json:"providerRef"`
}

type RILActionPrimitiveApproval struct {
	Required        bool   `json:"required"`
	Authority       string `json:"authority"`
	PolicyAuthority string `json:"policyAuthority"`
	Class           string `json:"class"`
	ReceiptRequired bool   `json:"receiptRequired"`
}

type RILActionPrimitiveGrant struct {
	Required                 bool     `json:"required"`
	Audience                 string   `json:"audience"`
	Scopes                   []string `json:"scopes"`
	ConnectorBindingRequired bool     `json:"connectorBindingRequired"`
}

type RILActionPrimitiveTarget struct {
	Scope                      string `json:"scope"`
	RequiresStackID            bool   `json:"requiresStackID"`
	RequiresResolvedPlanHash   bool   `json:"requiresResolvedPlanHash"`
	RequiresNodeRef            bool   `json:"requiresNodeRef"`
	RequiresRuntimeInstanceRef bool   `json:"requiresRuntimeInstanceRef"`
}

type RILActionPrimitiveInput struct {
	ID                  string `json:"id"`
	Type                string `json:"type"`
	Required            bool   `json:"required"`
	Source              string `json:"source"`
	OpaqueReferenceOnly bool   `json:"opaqueReferenceOnly"`
	InlineMaterial      bool   `json:"inlineMaterial"`
}

type RILActionPrimitiveVerification struct {
	Required       bool     `json:"required"`
	EvidenceSchema string   `json:"evidenceSchema"`
	Phases         []string `json:"phases"`
}

type RILActionPrimitiveRecovery = rilactionpolicy.RecoveryContract

type RILActionPrimitiveEvidence struct {
	RequiredFields       []string                                   `json:"requiredFields"`
	RedactedLogsOnly     bool                                       `json:"redactedLogsOnly"`
	ProtectedDiagnostics RILActionProtectedDiagnosticEvidencePolicy `json:"protectedDiagnostics"`
}

type RILActionProtectedDiagnosticEvidencePolicy = rilactionpolicy.ProtectedDiagnosticPolicy

type RILActionPrimitiveProhibitions struct {
	RawSSH            bool `json:"rawSSH"`
	RawDocker         bool `json:"rawDocker"`
	RawOpenTofu       bool `json:"rawOpenTofu"`
	ProviderInputs    bool `json:"providerInputs"`
	CallerCommands    bool `json:"callerCommands"`
	ArbitraryPaths    bool `json:"arbitraryPaths"`
	ProviderLifecycle bool `json:"providerLifecycle"`
}

// ListRILActionPrimitives returns the exact embedded product action catalog in
// stable ID order. It is discovery/contract metadata only and grants no Apply,
// node handoff, connector, transport, or provider authority.
func (s *Service) ListRILActionPrimitives() ([]RILActionPrimitiveCatalogEntry, error) {
	if s == nil || s.authority == nil {
		return nil, resolveError(ErrResolveFailed, "service is not initialized", nil)
	}
	if len(s.authority.contractSources) == 0 || s.authority.planAuthority.Class != "product" || s.authority.planAuthority.Document != "catalog" {
		return nil, resolveError(ErrAuthorityLoad, "embedded Architecture v2 product catalog authority is required", nil)
	}
	executors, err := decodeRILActionExecutorCatalog(s.authority.catalog.RILActionExecutors)
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, "decode Architecture v2 RIL action executor catalog: "+err.Error(), err)
	}
	entries, err := decodeRILActionPrimitiveCatalog(s.authority.catalog.RILActionPrimitives, executors)
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, "decode Architecture v2 RIL action catalog: "+err.Error(), err)
	}
	return entries, nil
}

func decodeRILActionExecutorCatalog(contracts []resolvedplan.RILActionExecutorContract) ([]RILActionExecutorCatalogEntry, error) {
	entries := make([]RILActionExecutorCatalogEntry, 0, len(contracts))
	seen := make(map[string]struct{}, len(contracts))
	for index, contract := range contracts {
		if _, supplied := contract["contractHash"]; supplied {
			return nil, fmt.Errorf("rilActionExecutors[%d]: contractHash is derived and cannot be supplied", index)
		}
		data, err := json.Marshal(contract)
		if err != nil {
			return nil, fmt.Errorf("rilActionExecutors[%d]: %w", index, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var entry RILActionExecutorCatalogEntry
		if err := decoder.Decode(&entry); err != nil {
			return nil, fmt.Errorf("rilActionExecutors[%d]: %w", index, err)
		}
		if entry.Ref == "" || entry.Version == "" || entry.Owner.Authority != "stackkits" || len(entry.OperationClasses) == 0 {
			return nil, fmt.Errorf("rilActionExecutors[%d]: exact StackKits identity and operation classes are required", index)
		}
		if _, duplicate := seen[entry.Ref]; duplicate {
			return nil, fmt.Errorf("RIL action executor %q is duplicated", entry.Ref)
		}
		hash, err := resolvedplan.CanonicalSHA256(contract)
		if err != nil {
			return nil, fmt.Errorf("rilActionExecutors[%d]: derive contract hash: %w", index, err)
		}
		entry.ContractHash = hash
		if err := rilaction.ValidateExecutorIdentity(entry.identity()); err != nil {
			return nil, fmt.Errorf("rilActionExecutors[%d]: invalid executor identity: %w", index, err)
		}
		seen[entry.Ref] = struct{}{}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Ref < entries[j].Ref })
	return entries, nil
}

func decodeRILActionPrimitiveCatalog(contracts []resolvedplan.RILActionPrimitiveContract, executors []RILActionExecutorCatalogEntry) ([]RILActionPrimitiveCatalogEntry, error) {
	entries := make([]RILActionPrimitiveCatalogEntry, 0, len(contracts))
	seen := make(map[string]struct{}, len(contracts))
	executorsByRef := make(map[string]RILActionExecutorCatalogEntry, len(executors))
	for _, executor := range executors {
		executorsByRef[executor.Ref] = executor
	}
	for index, contract := range contracts {
		entry, err := decodeRILActionPrimitiveContract(contract, executorsByRef)
		if err != nil {
			return nil, fmt.Errorf("rilActionPrimitives[%d]: %w", index, err)
		}
		if _, duplicate := seen[entry.ID]; duplicate {
			return nil, fmt.Errorf("RIL action primitive %q is duplicated", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		entries = append(entries, entry)
	}
	for _, entry := range entries {
		if entry.Recovery.Kind == "primitive" {
			if _, exists := seen[entry.Recovery.PrimitiveRef]; !exists {
				return nil, fmt.Errorf("RIL action primitive %q references unknown recovery primitive %q", entry.ID, entry.Recovery.PrimitiveRef)
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries, nil
}

func decodeRILActionPrimitiveContract(contract resolvedplan.RILActionPrimitiveContract, executors map[string]RILActionExecutorCatalogEntry) (RILActionPrimitiveCatalogEntry, error) {
	if _, supplied := contract["primitiveContractHash"]; supplied {
		return RILActionPrimitiveCatalogEntry{}, fmt.Errorf("primitiveContractHash is derived and cannot be supplied")
	}
	data, err := json.Marshal(contract)
	if err != nil {
		return RILActionPrimitiveCatalogEntry{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var entry RILActionPrimitiveCatalogEntry
	if err := decoder.Decode(&entry); err != nil {
		return RILActionPrimitiveCatalogEntry{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return RILActionPrimitiveCatalogEntry{}, fmt.Errorf("contract contains multiple JSON values")
		}
		return RILActionPrimitiveCatalogEntry{}, err
	}
	if entry.ID == "" || entry.Support == "" || entry.Owner.OperationClass == "" {
		return RILActionPrimitiveCatalogEntry{}, fmt.Errorf("contract identity, support, and operation class are required")
	}
	if entry.ExtensionAuthority != nil &&
		(entry.ExtensionAuthority.Kind != "module" || entry.ExtensionAuthority.ModuleRef == "" || entry.ExtensionAuthority.ProviderRef == "") {
		return RILActionPrimitiveCatalogEntry{}, fmt.Errorf("primitive %q has an invalid module extension authority", entry.ID)
	}
	switch entry.Support {
	case "contract-only":
		if entry.ExecutorRef != "" {
			return RILActionPrimitiveCatalogEntry{}, fmt.Errorf("contract-only primitive %q cannot name an executor", entry.ID)
		}
	case "executor-bound":
		if entry.ExecutorRef == "" {
			return RILActionPrimitiveCatalogEntry{}, fmt.Errorf("executor-bound primitive %q must name an executor", entry.ID)
		}
		executor, exists := executors[entry.ExecutorRef]
		if !exists || !containsRILActionString(executor.OperationClasses, entry.Owner.OperationClass) {
			return RILActionPrimitiveCatalogEntry{}, fmt.Errorf("executor-bound primitive %q has no exact CUE executor contract", entry.ID)
		}
		entry.ExecutorVersion = executor.Version
		entry.ExecutorContractHash = executor.ContractHash
	default:
		return RILActionPrimitiveCatalogEntry{}, fmt.Errorf("primitive %q has unsupported execution support %q", entry.ID, entry.Support)
	}
	if entry.Support == "executor-bound" {
		hash, err := resolvedplan.CanonicalSHA256(map[string]any{
			"primitive":            contract,
			"executorContractHash": entry.ExecutorContractHash,
		})
		if err != nil {
			return RILActionPrimitiveCatalogEntry{}, fmt.Errorf("derive executor-bound primitive contract hash: %w", err)
		}
		entry.PrimitiveContractHash = hash
	} else {
		digest := sha256.Sum256(data)
		entry.PrimitiveContractHash = "sha256:" + hex.EncodeToString(digest[:])
	}
	return entry, nil
}

func containsRILActionString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
