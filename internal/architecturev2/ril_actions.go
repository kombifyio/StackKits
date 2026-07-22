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
)

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
	Approval              RILActionPrimitiveApproval     `json:"approval"`
	Grant                 RILActionPrimitiveGrant        `json:"grant"`
	Target                RILActionPrimitiveTarget       `json:"target"`
	Inputs                []RILActionPrimitiveInput      `json:"inputs"`
	Verification          RILActionPrimitiveVerification `json:"verification"`
	Recovery              RILActionPrimitiveRecovery     `json:"recovery"`
	Evidence              RILActionPrimitiveEvidence     `json:"evidence"`
	Prohibitions          RILActionPrimitiveProhibitions `json:"prohibitions"`
	ExecutorRef           string                         `json:"executorRef,omitempty"`
	PrimitiveContractHash string                         `json:"primitiveContractHash"`
}

type RILActionPrimitiveOwner struct {
	Authority      string `json:"authority"`
	OperationClass string `json:"operationClass"`
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

type RILActionPrimitiveRecovery struct {
	Kind              string `json:"kind"`
	RequiredOnFailure bool   `json:"requiredOnFailure"`
	PrimitiveRef      string `json:"primitiveRef,omitempty"`
}

type RILActionPrimitiveEvidence struct {
	RequiredFields   []string `json:"requiredFields"`
	RedactedLogsOnly bool     `json:"redactedLogsOnly"`
}

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
	entries, err := decodeRILActionPrimitiveCatalog(s.authority.catalog.RILActionPrimitives)
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, "decode Architecture v2 RIL action catalog: "+err.Error(), err)
	}
	return entries, nil
}

func decodeRILActionPrimitiveCatalog(contracts []resolvedplan.RILActionPrimitiveContract) ([]RILActionPrimitiveCatalogEntry, error) {
	entries := make([]RILActionPrimitiveCatalogEntry, 0, len(contracts))
	seen := make(map[string]struct{}, len(contracts))
	for index, contract := range contracts {
		entry, err := decodeRILActionPrimitiveContract(contract)
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

func decodeRILActionPrimitiveContract(contract resolvedplan.RILActionPrimitiveContract) (RILActionPrimitiveCatalogEntry, error) {
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
	switch entry.Support {
	case "contract-only":
		if entry.ExecutorRef != "" {
			return RILActionPrimitiveCatalogEntry{}, fmt.Errorf("contract-only primitive %q cannot name an executor", entry.ID)
		}
	case "executor-bound":
		if entry.ExecutorRef == "" {
			return RILActionPrimitiveCatalogEntry{}, fmt.Errorf("executor-bound primitive %q must name an executor", entry.ID)
		}
	default:
		return RILActionPrimitiveCatalogEntry{}, fmt.Errorf("primitive %q has unsupported execution support %q", entry.ID, entry.Support)
	}
	digest := sha256.Sum256(data)
	entry.PrimitiveContractHash = "sha256:" + hex.EncodeToString(digest[:])
	return entry, nil
}
