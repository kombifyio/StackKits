// Package resolvedplan compiles the Architecture v2 intent, immutable kit
// definition, observed inventory, and a governed contract catalog into the one
// deterministic plan consumed by later generators and runtimes.
//
// CUE remains the schema authority. The document types deliberately retain a
// JSON-shaped boundary instead of duplicating the full CUE type system in Go.
// Compile performs the cross-document and catalog resolution that CUE alone
// cannot perform, and MarshalCanonical emits a CUE-compatible JSON document.
package resolvedplan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const (
	// ResolvedPlanAPIVersion is the only plan contract emitted by the
	// Architecture v2 compiler. A StackSpec v1 document is never accepted as a
	// ResolvedPlan through the integrity helpers below.
	ResolvedPlanAPIVersion = "stackkit.resolved-plan/v1"
	ResolvedPlanKind       = "ResolvedPlan"
)

// KitDefinition is a JSON-decoded base.#KitDefinition document.
type KitDefinition map[string]any

// StackSpecV2 is a JSON-decoded base.#StackSpecV2 document.
type StackSpecV2 map[string]any

// InventoryFacts is a JSON-decoded base.#InventoryFacts document.
type InventoryFacts map[string]any

// ExternalHostBinding is the provider-free handoff for a host that has
// already been selected and supplied by the platform control plane.
type ExternalHostBinding map[string]any

// HostConformanceReceipt is StackKits-owned OS/host diagnostic evidence for
// one exact ExternalHostBinding. It is never server-provider compatibility.
type HostConformanceReceipt map[string]any

// HomeAccessRequirement is the StackKits-owned, provider-neutral Shadow-Plan
// contract for one Home access capability.
type HomeAccessRequirement map[string]any

// ExternalHomeAccessBinding is an opaque external realization binding. It
// carries no transport, endpoint, credential, or provider lifecycle data.
type ExternalHomeAccessBinding map[string]any

// CapabilityContract is a JSON-decoded base.#CapabilityContract document.
type CapabilityContract map[string]any

// CapabilityProvider is a JSON-decoded base.#CapabilityProvider document.
type CapabilityProvider map[string]any

// AddOnContract is a JSON-decoded base.#AddOnContract document.
type AddOnContract map[string]any

// ModuleContract is a JSON-decoded base.#ModuleContractV2 document.
type ModuleContract map[string]any

// WorkloadContract is a JSON-decoded base.#WorkloadContractV2 document. It
// owns logical workload and alternative selection; StackSpec never supplies
// the referenced provider or module implementation IDs.
type WorkloadContract map[string]any

// PrivilegedInterfaceApproval is centrally owned catalog authority for one
// narrowly scoped direct runtime-interface exception.
type PrivilegedInterfaceApproval map[string]any

// PlanArtifactContract is a JSON-decoded base.#CatalogPlanArtifactV2 document.
// It is CUE catalog authority, not caller-supplied compiler configuration.
type PlanArtifactContract map[string]any

// ResolvedPlan is a JSON-decoded base.#ResolvedPlan document. It must only be
// constructed through Compiler.Compile so its source and plan hashes agree.
type ResolvedPlan map[string]any

// Input contains the only deployment-specific compiler inputs. Catalog data is
// immutable compiler configuration and is therefore supplied to NewCompiler.
type Input struct {
	Definition KitDefinition
	Spec       StackSpecV2
	Inventory  InventoryFacts
}

// Catalog is the governed set of contracts the compiler may resolve. Unknown
// IDs and capabilities without exactly one selected realization fail closed.
type Catalog struct {
	Capabilities                 []CapabilityContract
	Providers                    []CapabilityProvider
	AddOns                       []AddOnContract
	Modules                      []ModuleContract
	Workloads                    []WorkloadContract
	PrivilegedInterfaceApprovals []PrivilegedInterfaceApproval
	PlanArtifacts                []PlanArtifactContract
}

// PlanAuthority is plan-bound provenance, not sidecar fixture metadata. The
// exact class/document/eligibility tuple is CUE constrained and carried into
// generation manifests and receipts so evidence consumers cannot graduate a
// contract fixture as a product kit.
type PlanAuthority struct {
	Class                string `json:"class"`
	Document             string `json:"document"`
	GraduationEligible   bool   `json:"graduationEligible"`
	Issuer               string `json:"issuer"`
	AuthorityFingerprint string `json:"authorityFingerprint,omitempty"`
	CatalogHash          string `json:"catalogHash,omitempty"`
	// DistributionFingerprint attests the exact source/catalog/Definition
	// bundle compiled into one binary. It is deliberately not serialized into
	// ResolvedPlan: plans carry a portable semantic authority derived only from
	// the selected Definition and selected catalog closure.
	DistributionFingerprint string `json:"-"`
}

func ProductPlanAuthority() PlanAuthority {
	return PlanAuthority{
		Class: "product", Document: "catalog", GraduationEligible: true,
		Issuer: "stackkits-product-authority/v1", DistributionFingerprint: pinnedProductDistributionFingerprint,
	}
}

func ContractFixturePlanAuthority() PlanAuthority {
	return PlanAuthority{
		Class: "contract-fixture", Document: "contractFixtureCatalog", GraduationEligible: false,
		Issuer: "stackkits-contract-fixture-authority/v1",
	}

}

// DevelopmentPlanAuthority is the non-graduating default for filesystem and
// caller-supplied CUE sources. Product eligibility is reserved for a binary
// whose exact source/catalog/Definition distribution matches its generated
// distribution fingerprint pin.
func DevelopmentPlanAuthority() PlanAuthority {
	return PlanAuthority{
		Class: "development", Document: "catalog", GraduationEligible: false,
		Issuer: "stackkits-development-authority/v1",
	}
}

// Options records compiler identity in every plan. CompilerVersion must be a
// stable release/build identifier and must not contain timestamps.
type Options struct {
	CompilerVersion   string
	ContractValidator *CUEContractValidator
	PlanAuthority     PlanAuthority
	// AuthorityDefinitions is the complete CUE-exported Definition set owned
	// by the service constructing this compiler. When present, persisted-plan
	// verification is bound to those exact normalized definition hashes.
	AuthorityDefinitions    []KitDefinition
	MinimumCLIVersion       string
	MinimumRuntimeVersion   string
	MinimumGeneratorVersion string
	RendererID              string
	RendererVersion         string
}

// DecodeDocument decodes a CUE-exported JSON object into a typed document map.
// It rejects trailing JSON values and preserves numbers as json.Number so
// canonical hashes do not depend on float conversion.
func DecodeDocument[T ~map[string]any](data []byte) (T, error) {
	var document T
	decoder := json.NewDecoder(bytesReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode architecture document: %w", err)
	}
	if document == nil {
		return nil, fmt.Errorf("decode architecture document: expected JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("decode architecture document: multiple JSON values")
	} else if err != io.EOF {
		return nil, fmt.Errorf("decode architecture document: invalid trailing data: %w", err)
	}
	return document, nil
}

// MarshalCanonical emits stable JSON suitable for persisting or handing to
// CUE. It never applies secret redaction; plans are already sanitized before
// they are constructed.
func (p ResolvedPlan) MarshalCanonical() ([]byte, error) {
	return canonicalJSON(p, false)
}

// CanonicalJSON exposes the compiler's canonical JSON implementation for
// versioned downstream contracts such as generation manifests. Callers must
// not introduce a second JSON-normalization or object-hashing algorithm.
func CanonicalJSON(value any) ([]byte, error) {
	return canonicalJSON(value, false)
}

// CanonicalSHA256 returns the compiler canonical hash without applying secret
// redaction. ResolvedPlan integrity must instead use VerifyPlanHash, which
// applies the exact compiler rules and rejects unsafe secret material first.
func CanonicalSHA256(value any) (string, error) {
	return canonicalHash(value, false)
}

// VerifyPlanHash verifies the self-declared hash against the canonical plan
// with planHash removed. It also rejects plaintext secret material, the wrong
// contract kind/version, malformed hashes, and never mutates the caller's map.
func VerifyPlanHash(plan ResolvedPlan) (string, error) {
	if plan == nil {
		return "", fail(ErrInvalidInput, "resolvedPlan", "plan is required")
	}
	if plan["apiVersion"] != ResolvedPlanAPIVersion {
		return "", fail(ErrInvalidInput, "resolvedPlan.apiVersion", "must be %q", ResolvedPlanAPIVersion)
	}
	if plan["kind"] != ResolvedPlanKind {
		return "", fail(ErrInvalidInput, "resolvedPlan.kind", "must be %q", ResolvedPlanKind)
	}
	declared, ok := plan["planHash"].(string)
	if !ok || !canonicalSHA256Pattern.MatchString(declared) {
		return "", fail(ErrInvalidInput, "resolvedPlan.planHash", "must be a lowercase sha256:<64-hex> digest")
	}
	actual, err := CanonicalPlanHash(plan)
	if err != nil {
		return "", err
	}
	if declared != actual {
		return "", fail(ErrPlanHashMismatch, "resolvedPlan.planHash", "declared %s, canonical hashless plan is %s", declared, actual)
	}
	return actual, nil
}

// CanonicalPlanHash returns the compiler-compatible hash of a plan with its
// declared planHash omitted. It preserves the established secret-redaction
// semantics and never mutates the supplied map.
func CanonicalPlanHash(plan ResolvedPlan) (string, error) {
	if plan == nil {
		return "", fail(ErrInvalidInput, "resolvedPlan", "plan is required")
	}
	if err := validateSecretReferences(map[string]any(plan), "resolvedPlan", ""); err != nil {
		return "", err
	}
	clone, err := cloneObject(map[string]any(plan), false)
	if err != nil {
		return "", fmt.Errorf("clone ResolvedPlan for hashing: %w", err)
	}
	delete(clone, "planHash")
	actual, err := canonicalHash(clone, true)
	if err != nil {
		return "", fmt.Errorf("hash canonical ResolvedPlan: %w", err)
	}
	return actual, nil
}

// DecodeCanonicalPlan accepts only byte-for-byte canonical JSON. Requiring the
// canonical representation rejects duplicate object keys and representation
// ambiguity before a persisted plan reaches a renderer or apply gate.
func DecodeCanonicalPlan(data []byte) (ResolvedPlan, error) {
	plan, err := DecodeDocument[ResolvedPlan](data)
	if err != nil {
		return nil, err
	}
	if _, err := VerifyPlanHash(plan); err != nil {
		return nil, err
	}
	canonical, err := plan.MarshalCanonical()
	if err != nil {
		return nil, fmt.Errorf("marshal canonical ResolvedPlan: %w", err)
	}
	if !bytes.Equal(data, canonical) {
		return nil, fail(ErrNonCanonicalPlan, "resolvedPlan", "input is not byte-for-byte canonical JSON")
	}
	return plan, nil
}
