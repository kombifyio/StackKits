// Package applyevidence defines the provider-neutral producer wire contract
// for StackKits Architecture-v2 pre-Apply evidence. Product authorization,
// trust enrollment, observation policy, and runtime execution stay with the
// consuming control plane.
package applyevidence

import "time"

const (
	RequestAPIVersion           = "stackkit.apply-requirements/v1"
	CollectionRequestAPIVersion = "stackkit.apply-evidence-collection/v1"
	CollectionRequestKind       = "ApplyEvidenceCollectionRequest"
	CollectionRequestMaxBytes   = 4 << 20
	BundleAPIVersion            = "stackkit.apply-evidence/v1"
	BundleKind                  = "ApplyEvidenceBundle"
	ReceiptAPIVersion           = "stackkit.apply-evidence-receipt/v1"
	ReceiptKind                 = "ApplyEvidenceReceipt"
	MaxValidity                 = 15 * time.Minute
)

type RendererIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type PlanAuthority struct {
	Class                string `json:"class"`
	Document             string `json:"document"`
	GraduationEligible   bool   `json:"graduationEligible"`
	Issuer               string `json:"issuer"`
	AuthorityFingerprint string `json:"authorityFingerprint,omitempty"`
	CatalogHash          string `json:"catalogHash,omitempty"`
}

type PlanBinding struct {
	PlanHash        string           `json:"planHash"`
	SpecHash        string           `json:"specHash"`
	InventoryHash   string           `json:"inventoryHash"`
	DefinitionHash  string           `json:"definitionHash"`
	CompilerVersion string           `json:"compilerVersion"`
	Renderer        RendererIdentity `json:"renderer"`
	Authority       PlanAuthority    `json:"authority"`
}

type ExecutorIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type Producer struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	KeyID   string `json:"keyId"`
}

type Subject struct {
	OwnerKind    string `json:"ownerKind"`
	OwnerRef     string `json:"ownerRef"`
	ProviderRef  string `json:"providerRef,omitempty"`
	ModuleRef    string `json:"moduleRef,omitempty"`
	UnitRef      string `json:"unitRef,omitempty"`
	InstanceRef  string `json:"instanceRef,omitempty"`
	NodeRef      string `json:"nodeRef,omitempty"`
	GateRef      string `json:"gateRef,omitempty"`
	ContractHash string `json:"contractHash,omitempty"`
}

type Expectation struct {
	ReceiptID       string  `json:"receiptId"`
	RequirementKind string  `json:"requirementKind"`
	RequirementID   string  `json:"requirementId"`
	RequirementHash string  `json:"requirementHash"`
	Subject         Subject `json:"subject"`
}

type Request struct {
	APIVersion       string        `json:"apiVersion"`
	Binding          PlanBinding   `json:"binding"`
	RequirementsHash string        `json:"requirementsHash"`
	Expectations     []Expectation `json:"expectations"`
}

// CollectionRequest is the canonical provider-neutral handoff from a Product
// Apply authority to an authenticated evidence producer. The producer owns
// observation and signing custody; this request grants no execution, provider,
// endpoint, credential, transport, or key authority.
type CollectionRequest struct {
	APIVersion       string           `json:"apiVersion"`
	Kind             string           `json:"kind"`
	Request          Request          `json:"request"`
	ManifestHash     string           `json:"manifestHash"`
	Executor         ExecutorIdentity `json:"executor"`
	EvaluatedAt      time.Time        `json:"evaluatedAt"`
	CollectionDigest string           `json:"collectionDigest"`
}

type Receipt struct {
	APIVersion      string           `json:"apiVersion"`
	Kind            string           `json:"kind"`
	ID              string           `json:"id"`
	RequirementKind string           `json:"requirementKind"`
	RequirementID   string           `json:"requirementId"`
	RequirementHash string           `json:"requirementHash"`
	Binding         PlanBinding      `json:"binding"`
	ManifestHash    string           `json:"manifestHash"`
	Executor        ExecutorIdentity `json:"executor"`
	Subject         Subject          `json:"subject"`
	Result          string           `json:"result"`
	Producer        Producer         `json:"producer"`
	ObservationRef  string           `json:"observationRef"`
	ObservedAt      string           `json:"observedAt"`
	ValidUntil      string           `json:"validUntil"`
	Signature       string           `json:"signature"`
	ReceiptDigest   string           `json:"receiptDigest"`
}

type Bundle struct {
	APIVersion       string           `json:"apiVersion"`
	Kind             string           `json:"kind"`
	Binding          PlanBinding      `json:"binding"`
	ManifestHash     string           `json:"manifestHash"`
	Executor         ExecutorIdentity `json:"executor"`
	RequirementsHash string           `json:"requirementsHash"`
	Receipts         []Receipt        `json:"receipts"`
	BundleHash       string           `json:"bundleHash"`
}

type ReceiptInput struct {
	Request        Request
	Expectation    Expectation
	ManifestHash   string
	Executor       ExecutorIdentity
	Producer       Producer
	ObservationRef string
	ObservedAt     time.Time
	ValidUntil     time.Time
}
