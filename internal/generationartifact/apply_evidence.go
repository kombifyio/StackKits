package generationartifact

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	ApplyRequirementsAPIVersion    = "stackkit.apply-requirements/v1"
	ApplyEvidenceBundleAPIVersion  = "stackkit.apply-evidence/v1"
	ApplyEvidenceBundleKind        = "ApplyEvidenceBundle"
	ApplyEvidenceReceiptAPIVersion = "stackkit.apply-evidence-receipt/v1"
	ApplyEvidenceReceiptKind       = "ApplyEvidenceReceipt"

	// MaxApplyEvidenceValidity is intentionally short and versioned with the
	// receipt contract. Producers cannot mint arbitrarily long-lived Apply
	// evidence. A later CUE policy may narrow this window, never widen v1.
	MaxApplyEvidenceValidity = 15 * time.Minute
)

var (
	applyEvidenceIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	applyEvidenceSemVerPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
	applyEvidenceKeyPattern    = regexp.MustCompile(`^ed25519://sha256/[a-f0-9]{64}$`)
)

type ApplyExecutorIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// ValidateApplyExecutorIdentity validates the complete immutable identity used
// by an Apply authorizer and its evidence producers.
func ValidateApplyExecutorIdentity(identity ApplyExecutorIdentity) error {
	return validateApplyExecutorIdentity(identity, "applyExecutor")
}

type ApplyEvidenceProducer struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	KeyID   string `json:"keyId"`
}

// ApplyEvidenceSubject is an explicit, human-auditable projection of the
// requirement identity. RequirementHash still binds the complete requirement.
type ApplyEvidenceSubject struct {
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

type ApplyEvidenceReceipt struct {
	APIVersion      string                `json:"apiVersion"`
	Kind            string                `json:"kind"`
	ID              string                `json:"id"`
	RequirementKind string                `json:"requirementKind"`
	RequirementID   string                `json:"requirementId"`
	RequirementHash string                `json:"requirementHash"`
	Binding         PlanBinding           `json:"binding"`
	ManifestHash    string                `json:"manifestHash"`
	Executor        ApplyExecutorIdentity `json:"executor"`
	Subject         ApplyEvidenceSubject  `json:"subject"`
	Result          string                `json:"result"`
	Producer        ApplyEvidenceProducer `json:"producer"`
	ObservationRef  string                `json:"observationRef"`
	ObservedAt      string                `json:"observedAt"`
	ValidUntil      string                `json:"validUntil"`
	Signature       string                `json:"signature"`
	ReceiptDigest   string                `json:"receiptDigest"`
}

type ApplyEvidenceBundle struct {
	APIVersion       string                 `json:"apiVersion"`
	Kind             string                 `json:"kind"`
	Binding          PlanBinding            `json:"binding"`
	ManifestHash     string                 `json:"manifestHash"`
	Executor         ApplyExecutorIdentity  `json:"executor"`
	RequirementsHash string                 `json:"requirementsHash"`
	Receipts         []ApplyEvidenceReceipt `json:"receipts"`
	BundleHash       string                 `json:"bundleHash"`
}

type ApplyEvidenceProducerTrust struct {
	Producer         ApplyEvidenceProducer
	PublicKey        ed25519.PublicKey
	RequirementKinds []string
	ReceiptIDs       []string
}

// ValidateApplyEvidenceProducerAnchor validates one public producer identity
// before product code expands its plan-independent scope into exact receipt
// IDs. Private signing material is never part of this contract.
func ValidateApplyEvidenceProducerAnchor(producer ApplyEvidenceProducer, publicKey ed25519.PublicKey) error {
	if !applyEvidenceIDPattern.MatchString(producer.ID) || !applyEvidenceSemVerPattern.MatchString(producer.Version) || !applyEvidenceKeyPattern.MatchString(producer.KeyID) {
		return fail(ErrInvalidContract, "applyEvidence.producer", "must contain canonical id, SemVer, and Ed25519 key ID")
	}
	if len(publicKey) != ed25519.PublicKeySize || ApplyEvidenceProducerKeyID(publicKey) != producer.KeyID {
		return fail(ErrInvalidContract, "applyEvidence.producer.keyId", "must identify the exact Ed25519 public key")
	}
	return nil
}

// ValidateApplyEvidenceProducerTrust validates the exact, plan-scoped trust
// entry consumed by evidence verification.
func ValidateApplyEvidenceProducerTrust(trust ApplyEvidenceProducerTrust) error {
	if err := ValidateApplyEvidenceProducerAnchor(trust.Producer, trust.PublicKey); err != nil {
		return err
	}
	if len(trust.RequirementKinds) == 0 || len(trust.ReceiptIDs) == 0 {
		return fail(ErrInvalidContract, "applyEvidence.producer.scope", "requirement kinds and exact receipt IDs are required")
	}
	previous := ""
	allowedKinds := make(map[string]struct{}, len(trust.RequirementKinds))
	for _, kind := range trust.RequirementKinds {
		if kind <= previous {
			return fail(ErrInvalidContract, "applyEvidence.producer.requirementKinds", "must be strictly sorted and unique")
		}
		if _, valid := applyEvidenceObservationPrefix(kind); !valid {
			return fail(ErrInvalidContract, "applyEvidence.producer.requirementKinds", "contains unsupported requirement kind %q", kind)
		}
		allowedKinds[kind] = struct{}{}
		previous = kind
	}
	previous = ""
	for _, id := range trust.ReceiptIDs {
		if id <= previous {
			return fail(ErrInvalidContract, "applyEvidence.producer.receiptIds", "must be strictly sorted and unique")
		}
		kind, _, found := strings.Cut(id, "/")
		if !found {
			return fail(ErrInvalidContract, "applyEvidence.producer.receiptIds", "contains invalid typed receipt ID %q", id)
		}
		if _, allowed := allowedKinds[kind]; !allowed {
			return fail(ErrInvalidContract, "applyEvidence.producer.receiptIds", "receipt %q exceeds the producer kind scope", id)
		}
		previous = id
	}
	return nil
}

type ApplyEvidenceVerificationInput struct {
	Plan              VerifiedPlan
	Manifest          ArtifactManifest
	GenerationReceipt GenerationReceipt
	Executor          ApplyExecutorIdentity
	Bundle            []byte
	TrustedProducers  map[string]ApplyEvidenceProducerTrust
}

// VerifiedApplyEvidenceBundle is evidence only. It is deliberately not an
// executor capability and cannot be upgraded without the later held-lock
// Architecture v2 authorization boundary.
type VerifiedApplyEvidenceBundle struct {
	binding          PlanBinding
	manifestHash     string
	executor         ApplyExecutorIdentity
	requirementsHash string
	bundleHash       string
	evaluatedAt      time.Time
	expiresAt        time.Time
}

func (v VerifiedApplyEvidenceBundle) BundleHash() string       { return v.bundleHash }
func (v VerifiedApplyEvidenceBundle) RequirementsHash() string { return v.requirementsHash }
func (v VerifiedApplyEvidenceBundle) EvaluatedAt() time.Time   { return v.evaluatedAt }
func (v VerifiedApplyEvidenceBundle) ExpiresAt() time.Time     { return v.expiresAt }

type ApplyEvidenceExpectation struct {
	ReceiptID       string               `json:"receiptId"`
	RequirementKind string               `json:"requirementKind"`
	RequirementID   string               `json:"requirementId"`
	RequirementHash string               `json:"requirementHash"`
	Subject         ApplyEvidenceSubject `json:"subject"`
}

type ApplyEvidenceRequest struct {
	APIVersion       string                     `json:"apiVersion"`
	Binding          PlanBinding                `json:"binding"`
	RequirementsHash string                     `json:"requirementsHash"`
	Expectations     []ApplyEvidenceExpectation `json:"expectations"`
}

// ApplyEvidenceRequest returns the single canonical producer-facing request.
// It contains opaque secret references only through their requirement hashes;
// no external producer has to reimplement StackKits requirement hashing.
func (p VerifiedPlan) ApplyEvidenceRequest() (ApplyEvidenceRequest, error) {
	requirementsHash, err := ComputeApplyRequirementsHash(p.applyRequirements)
	if err != nil {
		return ApplyEvidenceRequest{}, err
	}
	indexed, err := applyEvidenceExpectations(p.applyRequirements)
	if err != nil {
		return ApplyEvidenceRequest{}, err
	}
	ids := make([]string, 0, len(indexed))
	for id := range indexed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	request := ApplyEvidenceRequest{APIVersion: ApplyRequirementsAPIVersion, Binding: p.binding, RequirementsHash: requirementsHash}
	for _, id := range ids {
		request.Expectations = append(request.Expectations, indexed[id])
	}
	return request, nil
}

func VerifyApplyEvidenceBundle(input ApplyEvidenceVerificationInput) (VerifiedApplyEvidenceBundle, error) {
	return verifyApplyEvidenceBundleAt(input, time.Now().UTC())
}

func verifyApplyEvidenceBundleAt(input ApplyEvidenceVerificationInput, at time.Time) (VerifiedApplyEvidenceBundle, error) {
	if at.IsZero() || at.Location() != time.UTC {
		return VerifiedApplyEvidenceBundle{}, fail(ErrEvidenceFreshness, "applyEvidence.at", "a non-zero UTC evaluation time is required")
	}
	if err := validateApplyExecutorIdentity(input.Executor, "applyEvidence.executor"); err != nil {
		return VerifiedApplyEvidenceBundle{}, err
	}
	if err := VerifyReceipt(input.Plan, input.Manifest, input.GenerationReceipt); err != nil {
		return VerifiedApplyEvidenceBundle{}, err
	}
	var bundle ApplyEvidenceBundle
	if err := decodeStrictJSON(input.Bundle, &bundle); err != nil {
		return VerifiedApplyEvidenceBundle{}, wrap(ErrInvalidContract, "applyEvidence.bundle", "strictly decode bundle", err)
	}
	canonical, err := bundle.MarshalCanonical()
	if err != nil {
		return VerifiedApplyEvidenceBundle{}, err
	}
	if !bytes.Equal(input.Bundle, canonical) {
		return VerifiedApplyEvidenceBundle{}, fail(ErrNonCanonical, "applyEvidence.bundle", "must be byte-for-byte canonical JSON")
	}
	manifestHash, err := input.Manifest.Hash()
	if err != nil {
		return VerifiedApplyEvidenceBundle{}, err
	}
	if bundle.Binding != input.Plan.Binding() || bundle.Binding != input.GenerationReceipt.Binding {
		return VerifiedApplyEvidenceBundle{}, fail(ErrBindingMismatch, "applyEvidence.bundle.binding", "does not match the verified plan and generation receipt")
	}
	if bundle.ManifestHash != manifestHash || bundle.ManifestHash != input.GenerationReceipt.ManifestHash {
		return VerifiedApplyEvidenceBundle{}, fail(ErrBindingMismatch, "applyEvidence.bundle.manifestHash", "does not match the verified generation manifest")
	}
	if bundle.Executor != input.Executor {
		return VerifiedApplyEvidenceBundle{}, fail(ErrBindingMismatch, "applyEvidence.bundle.executor", "does not match the selected executor")
	}
	requirementsHash, err := ComputeApplyRequirementsHash(input.Plan.applyRequirements)
	if err != nil {
		return VerifiedApplyEvidenceBundle{}, err
	}
	if bundle.RequirementsHash != requirementsHash {
		return VerifiedApplyEvidenceBundle{}, fail(ErrBindingMismatch, "applyEvidence.bundle.requirementsHash", "does not match the verified plan requirements")
	}
	expected, err := applyEvidenceExpectations(input.Plan.applyRequirements)
	if err != nil {
		return VerifiedApplyEvidenceBundle{}, err
	}
	if len(bundle.Receipts) != len(expected) {
		return VerifiedApplyEvidenceBundle{}, fail(ErrEvidenceSetMismatch, "applyEvidence.bundle.receipts", "got %d receipts, require exact set of %d", len(bundle.Receipts), len(expected))
	}
	seen := make(map[string]struct{}, len(bundle.Receipts))
	previous := ""
	for index, receipt := range bundle.Receipts {
		path := fmt.Sprintf("applyEvidence.bundle.receipts[%d]", index)
		if previous != "" && receipt.ID <= previous {
			return VerifiedApplyEvidenceBundle{}, fail(ErrDuplicateEvidence, path+".id", "receipts must be strictly sorted and unique")
		}
		previous = receipt.ID
		if _, duplicate := seen[receipt.ID]; duplicate {
			return VerifiedApplyEvidenceBundle{}, fail(ErrDuplicateEvidence, path+".id", "duplicate receipt %q", receipt.ID)
		}
		seen[receipt.ID] = struct{}{}
		expectation, exists := expected[receipt.ID]
		if !exists {
			return VerifiedApplyEvidenceBundle{}, fail(ErrEvidenceSetMismatch, path+".id", "receipt is not required by the verified plan")
		}
		if err := verifyApplyEvidenceReceipt(receipt, expectation, bundle, at, input.TrustedProducers, path); err != nil {
			return VerifiedApplyEvidenceBundle{}, err
		}
		delete(expected, receipt.ID)
	}
	if len(expected) != 0 {
		missing := make([]string, 0, len(expected))
		for id := range expected {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return VerifiedApplyEvidenceBundle{}, fail(ErrEvidenceSetMismatch, "applyEvidence.bundle.receipts", "missing exact receipts %v", missing)
	}
	resolved, err := resolvedplan.DecodeCanonicalPlan(input.Plan.canonical)
	if err != nil {
		return VerifiedApplyEvidenceBundle{}, wrap(ErrInvalidPlan, "resolvedPlan", "decode verified plan for external-host freshness", err)
	}
	if err := resolvedplan.ValidateHostConformanceReceiptsForApply(resolved, at); err != nil {
		return VerifiedApplyEvidenceBundle{}, wrap(ErrEvidenceFreshness, "resolvedPlan.hostConformanceReceipts", "external-host evidence is not fresh and conformant", err)
	}
	expiresAt, err := earliestApplyEvidenceExpiry(bundle, resolved)
	if err != nil {
		return VerifiedApplyEvidenceBundle{}, err
	}
	return VerifiedApplyEvidenceBundle{
		binding: bundle.Binding, manifestHash: bundle.ManifestHash, executor: bundle.Executor,
		requirementsHash: bundle.RequirementsHash, bundleHash: bundle.BundleHash, evaluatedAt: at, expiresAt: expiresAt,
	}, nil
}

func earliestApplyEvidenceExpiry(bundle ApplyEvidenceBundle, plan resolvedplan.ResolvedPlan) (time.Time, error) {
	var earliest time.Time
	include := func(value, path string) error {
		expiresAt, err := parseApplyEvidenceTimestamp(value, path)
		if err != nil {
			return err
		}
		if earliest.IsZero() || expiresAt.Before(earliest) {
			earliest = expiresAt
		}
		return nil
	}
	for index, receipt := range bundle.Receipts {
		if err := include(receipt.ValidUntil, fmt.Sprintf("applyEvidence.bundle.receipts[%d].validUntil", index)); err != nil {
			return time.Time{}, err
		}
	}
	for _, collection := range []string{"externalHostBindings", "hostConformanceReceipts"} {
		values, ok := plan[collection].(map[string]any)
		if !ok {
			return time.Time{}, fail(ErrInvalidPlan, "resolvedPlan."+collection, "must be an object")
		}
		for ref, raw := range values {
			value, ok := raw.(map[string]any)
			if !ok {
				return time.Time{}, fail(ErrInvalidPlan, "resolvedPlan."+collection+"."+ref, "must be an object")
			}
			validUntil, ok := value["validUntil"].(string)
			if !ok || validUntil == "" {
				return time.Time{}, fail(ErrInvalidPlan, "resolvedPlan."+collection+"."+ref+".validUntil", "must be a canonical timestamp")
			}
			if err := include(validUntil, "resolvedPlan."+collection+"."+ref+".validUntil"); err != nil {
				return time.Time{}, err
			}
		}
	}
	homeSites, ok := plan["externalHomeAccessBindings"].(map[string]any)
	if !ok {
		return time.Time{}, fail(ErrInvalidPlan, "resolvedPlan.externalHomeAccessBindings", "must be an object")
	}
	for siteRef, rawCapabilities := range homeSites {
		capabilities, ok := rawCapabilities.(map[string]any)
		if !ok {
			return time.Time{}, fail(ErrInvalidPlan, "resolvedPlan.externalHomeAccessBindings."+siteRef, "must be an object")
		}
		for capabilityRef, rawBinding := range capabilities {
			binding, ok := rawBinding.(map[string]any)
			if !ok {
				return time.Time{}, fail(ErrInvalidPlan, "resolvedPlan.externalHomeAccessBindings."+siteRef+"."+capabilityRef, "must be an object")
			}
			validUntil, ok := binding["validUntil"].(string)
			if !ok || validUntil == "" {
				return time.Time{}, fail(ErrInvalidPlan, "resolvedPlan.externalHomeAccessBindings."+siteRef+"."+capabilityRef+".validUntil", "must be a canonical timestamp")
			}
			if err := include(validUntil, "resolvedPlan.externalHomeAccessBindings."+siteRef+"."+capabilityRef+".validUntil"); err != nil {
				return time.Time{}, err
			}
		}
	}
	if earliest.IsZero() {
		return time.Time{}, fail(ErrEvidenceFreshness, "applyEvidence.bundle.receipts", "no finite evidence expiry was projected")
	}
	return earliest, nil
}

func verifyApplyEvidenceReceipt(receipt ApplyEvidenceReceipt, expected ApplyEvidenceExpectation, bundle ApplyEvidenceBundle, at time.Time, trust map[string]ApplyEvidenceProducerTrust, path string) error {
	if err := validateApplyEvidenceReceiptContract(receipt, path); err != nil {
		return err
	}
	if receipt.ID != expected.ReceiptID || receipt.RequirementKind != expected.RequirementKind || receipt.RequirementID != expected.RequirementID {
		return fail(ErrEvidenceSetMismatch, path, "receipt kind/id does not match its exact requirement")
	}
	if receipt.RequirementHash != expected.RequirementHash || receipt.Subject != expected.Subject {
		return fail(ErrBindingMismatch, path+".requirementHash", "receipt subject does not match the exact requirement")
	}
	if receipt.Binding != bundle.Binding || receipt.ManifestHash != bundle.ManifestHash || receipt.Executor != bundle.Executor {
		return fail(ErrBindingMismatch, path, "receipt plan, manifest, or executor identity differs from the bundle")
	}
	observedAt, err := parseApplyEvidenceTimestamp(receipt.ObservedAt, path+".observedAt")
	if err != nil {
		return err
	}
	validUntil, err := parseApplyEvidenceTimestamp(receipt.ValidUntil, path+".validUntil")
	if err != nil {
		return err
	}
	if !observedAt.Before(validUntil) || validUntil.Sub(observedAt) > MaxApplyEvidenceValidity || at.Before(observedAt) || !at.Before(validUntil) {
		return fail(ErrEvidenceFreshness, path, "receipt must satisfy observedAt <= at < validUntil within %s", MaxApplyEvidenceValidity)
	}
	trusted, exists := trust[receipt.Producer.KeyID]
	if !exists || trusted.Producer != receipt.Producer || len(trusted.PublicKey) != ed25519.PublicKeySize || ApplyEvidenceProducerKeyID(trusted.PublicKey) != receipt.Producer.KeyID || !applyEvidenceTrustAllows(trusted, receipt.RequirementKind, receipt.ID) {
		return fail(ErrEvidenceUntrusted, path+".producer", "receipt producer is not an exact trusted Ed25519 identity")
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(receipt.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.RawStdEncoding.EncodeToString(signature) != receipt.Signature {
		return fail(ErrInvalidContract, path+".signature", "must be an unpadded base64 Ed25519 signature")
	}
	signingBytes, err := ApplyEvidenceReceiptSigningBytes(receipt)
	if err != nil {
		return err
	}
	if !ed25519.Verify(trusted.PublicKey, signingBytes, signature) {
		return fail(ErrEvidenceUntrusted, path+".signature", "signature does not authenticate the receipt")
	}
	return nil
}

func ComputeApplyRequirementsHash(requirements ApplyRequirements) (string, error) {
	if err := requirements.Binding.validate("applyRequirements.binding"); err != nil {
		return "", err
	}
	if err := validateApplyRequirementIDs(requirements); err != nil {
		return "", err
	}
	return resolvedplan.CanonicalSHA256(struct {
		APIVersion   string            `json:"apiVersion"`
		Requirements ApplyRequirements `json:"requirements"`
	}{ApplyRequirementsAPIVersion, requirements})
}

func ApplyEvidenceReceiptSigningBytes(receipt ApplyEvidenceReceipt) ([]byte, error) {
	clone := receipt
	clone.Signature = ""
	clone.ReceiptDigest = ""
	if err := validateApplyEvidenceReceiptUnsigned(clone, "applyEvidence.receipt"); err != nil {
		return nil, err
	}
	return resolvedplan.CanonicalJSON(clone)
}

func ComputeApplyEvidenceReceiptDigest(receipt ApplyEvidenceReceipt) (string, error) {
	clone := receipt
	clone.ReceiptDigest = ""
	if err := validateApplyEvidenceReceiptSigned(clone, "applyEvidence.receipt"); err != nil {
		return "", err
	}
	return resolvedplan.CanonicalSHA256(clone)
}

func ComputeApplyEvidenceBundleHash(bundle ApplyEvidenceBundle) (string, error) {
	clone := bundle
	clone.BundleHash = ""
	if err := validateApplyEvidenceBundleUnsigned(clone); err != nil {
		return "", err
	}
	return resolvedplan.CanonicalSHA256(clone)
}

func (bundle ApplyEvidenceBundle) MarshalCanonical() ([]byte, error) {
	if err := validateApplyEvidenceBundleContract(bundle); err != nil {
		return nil, err
	}
	return resolvedplan.CanonicalJSON(bundle)
}

func applyEvidenceExpectations(requirements ApplyRequirements) (map[string]ApplyEvidenceExpectation, error) {
	result := map[string]ApplyEvidenceExpectation{}
	add := func(kind, id string, value any, subject ApplyEvidenceSubject) error {
		hash, err := resolvedplan.CanonicalSHA256(struct {
			APIVersion  string `json:"apiVersion"`
			Kind        string `json:"kind"`
			Requirement any    `json:"requirement"`
		}{ApplyRequirementsAPIVersion, kind, value})
		if err != nil {
			return err
		}
		key := kind + "/" + id
		if _, duplicate := result[key]; duplicate {
			return fail(ErrDuplicateEvidence, "applyRequirements", "duplicate typed requirement %q", key)
		}
		result[key] = ApplyEvidenceExpectation{ReceiptID: key, RequirementKind: kind, RequirementID: id, RequirementHash: hash, Subject: subject}
		return nil
	}
	for _, value := range requirements.Workloads {
		if err := add("workload", value.ID, value, ApplyEvidenceSubject{OwnerKind: "workload", OwnerRef: value.ID, ProviderRef: value.ProviderRef, ModuleRef: value.ModuleRef, ContractHash: value.ContractHash}); err != nil {
			return nil, err
		}
	}
	for _, value := range requirements.Secrets {
		if err := add("secret", value.ID, value, ApplyEvidenceSubject{OwnerKind: value.OwnerKind, OwnerRef: value.OwnerRef, ModuleRef: value.ModuleRef, UnitRef: value.UnitRef, InstanceRef: value.InstanceRef}); err != nil {
			return nil, err
		}
	}
	for _, value := range requirements.RuntimeInstances {
		if err := add("runtime", value.ID, value, ApplyEvidenceSubject{OwnerKind: value.OwnerKind, OwnerRef: value.OwnerRef, ProviderRef: value.ProviderRef, ModuleRef: value.ModuleRef, UnitRef: value.UnitRef, InstanceRef: value.InstanceRef, ContractHash: value.OwnerContractHash}); err != nil {
			return nil, err
		}
	}
	for _, value := range requirements.Hosts {
		if err := add("host", value.NodeRef, value, ApplyEvidenceSubject{OwnerKind: "host", OwnerRef: value.NodeRef, NodeRef: value.NodeRef, ContractHash: value.BindingHash}); err != nil {
			return nil, err
		}
	}
	for _, value := range requirements.ProviderOwners {
		if err := add("provider-owner", value.ID, value, ApplyEvidenceSubject{OwnerKind: "provider-owner", OwnerRef: value.Ref, ProviderRef: value.ID, ContractHash: value.ContractHash}); err != nil {
			return nil, err
		}
	}
	for _, value := range requirements.EvidenceRequirements {
		if err := add("evidence", value.ID, value, ApplyEvidenceSubject{OwnerKind: value.OwnerKind, OwnerRef: value.OwnerRef, GateRef: value.GateRef}); err != nil {
			return nil, err
		}
	}
	for _, value := range requirements.HealthRequirements {
		if err := add("health", value.ID, value, ApplyEvidenceSubject{OwnerKind: value.TargetKind, OwnerRef: value.TargetRef, GateRef: value.ID, ContractHash: value.ContractHash}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func validateApplyEvidenceBundleContract(bundle ApplyEvidenceBundle) error {
	if err := validateApplyEvidenceBundleUnsigned(func() ApplyEvidenceBundle { clone := bundle; clone.BundleHash = ""; return clone }()); err != nil {
		return err
	}
	if !validSHA256(bundle.BundleHash) {
		return fail(ErrInvalidContract, "applyEvidence.bundle.bundleHash", "must be a lowercase sha256 digest")
	}
	want, err := ComputeApplyEvidenceBundleHash(bundle)
	if err != nil {
		return err
	}
	if bundle.BundleHash != want {
		return fail(ErrHashMismatch, "applyEvidence.bundle.bundleHash", "does not match the canonical bundle body")
	}
	return nil
}

func validateApplyEvidenceBundleUnsigned(bundle ApplyEvidenceBundle) error {
	if bundle.APIVersion != ApplyEvidenceBundleAPIVersion || bundle.Kind != ApplyEvidenceBundleKind {
		return fail(ErrInvalidContract, "applyEvidence.bundle", "must be %s %s", ApplyEvidenceBundleAPIVersion, ApplyEvidenceBundleKind)
	}
	if err := bundle.Binding.validate("applyEvidence.bundle.binding"); err != nil {
		return err
	}
	if !validSHA256(bundle.ManifestHash) || !validSHA256(bundle.RequirementsHash) {
		return fail(ErrInvalidContract, "applyEvidence.bundle", "manifestHash and requirementsHash must be lowercase sha256 digests")
	}
	if err := validateApplyExecutorIdentity(bundle.Executor, "applyEvidence.bundle.executor"); err != nil {
		return err
	}
	if len(bundle.Receipts) == 0 {
		return fail(ErrInvalidContract, "applyEvidence.bundle.receipts", "at least one receipt is required")
	}
	for index, receipt := range bundle.Receipts {
		if err := validateApplyEvidenceReceiptContract(receipt, fmt.Sprintf("applyEvidence.bundle.receipts[%d]", index)); err != nil {
			return err
		}
	}
	return nil
}

func validateApplyEvidenceReceiptContract(receipt ApplyEvidenceReceipt, path string) error {
	if err := validateApplyEvidenceReceiptSigned(func() ApplyEvidenceReceipt { clone := receipt; clone.ReceiptDigest = ""; return clone }(), path); err != nil {
		return err
	}
	if !validSHA256(receipt.ReceiptDigest) {
		return fail(ErrInvalidContract, path+".receiptDigest", "must be a lowercase sha256 digest")
	}
	want, err := ComputeApplyEvidenceReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fail(ErrHashMismatch, path+".receiptDigest", "does not match the canonical signed receipt")
	}
	return nil
}

func validateApplyEvidenceReceiptSigned(receipt ApplyEvidenceReceipt, path string) error {
	clone := receipt
	clone.Signature = ""
	if err := validateApplyEvidenceReceiptUnsigned(clone, path); err != nil {
		return err
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(receipt.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.RawStdEncoding.EncodeToString(signature) != receipt.Signature {
		return fail(ErrInvalidContract, path+".signature", "must be one canonical unpadded base64 Ed25519 signature")
	}
	return nil
}

func validateApplyEvidenceReceiptUnsigned(receipt ApplyEvidenceReceipt, path string) error {
	if receipt.APIVersion != ApplyEvidenceReceiptAPIVersion || receipt.Kind != ApplyEvidenceReceiptKind {
		return fail(ErrInvalidContract, path, "must be %s %s", ApplyEvidenceReceiptAPIVersion, ApplyEvidenceReceiptKind)
	}
	if strings.TrimSpace(receipt.ID) == "" || strings.TrimSpace(receipt.RequirementID) == "" || receipt.ID != receipt.RequirementKind+"/"+receipt.RequirementID {
		return fail(ErrInvalidContract, path+".id", "must be the typed requirement ID")
	}
	if _, exists := applyEvidenceObservationPrefix(receipt.RequirementKind); !exists {
		return fail(ErrInvalidContract, path+".requirementKind", "unsupported requirement kind %q", receipt.RequirementKind)
	}
	if !validSHA256(receipt.RequirementHash) || !validSHA256(receipt.ManifestHash) {
		return fail(ErrInvalidContract, path, "requirementHash and manifestHash must be lowercase sha256 digests")
	}
	if err := receipt.Binding.validate(path + ".binding"); err != nil {
		return err
	}
	if err := validateApplyExecutorIdentity(receipt.Executor, path+".executor"); err != nil {
		return err
	}
	if strings.TrimSpace(receipt.Subject.OwnerKind) == "" || strings.TrimSpace(receipt.Subject.OwnerRef) == "" {
		return fail(ErrInvalidContract, path+".subject", "ownerKind and ownerRef are required")
	}
	if receipt.Result != "satisfied" {
		return fail(ErrInvalidContract, path+".result", "must be satisfied")
	}
	if !applyEvidenceIDPattern.MatchString(receipt.Producer.ID) || !applyEvidenceSemVerPattern.MatchString(receipt.Producer.Version) || !applyEvidenceKeyPattern.MatchString(receipt.Producer.KeyID) {
		return fail(ErrInvalidContract, path+".producer", "must contain canonical id, SemVer, and Ed25519 key ID")
	}
	prefix, _ := applyEvidenceObservationPrefix(receipt.RequirementKind)
	wantObservation := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `://sha256/[a-f0-9]{64}$`)
	if !wantObservation.MatchString(receipt.ObservationRef) {
		return fail(ErrInvalidContract, path+".observationRef", "must be a typed opaque observation reference")
	}
	if _, err := parseApplyEvidenceTimestamp(receipt.ObservedAt, path+".observedAt"); err != nil {
		return err
	}
	if _, err := parseApplyEvidenceTimestamp(receipt.ValidUntil, path+".validUntil"); err != nil {
		return err
	}
	return nil
}

func validateApplyExecutorIdentity(identity ApplyExecutorIdentity, path string) error {
	if !applyEvidenceIDPattern.MatchString(identity.ID) || !applyEvidenceSemVerPattern.MatchString(identity.Version) || !validSHA256(identity.Digest) {
		return fail(ErrInvalidContract, path, "must contain canonical id, SemVer, and lowercase sha256 digest")
	}
	return nil
}

func parseApplyEvidenceTimestamp(value, path string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fail(ErrInvalidContract, path, "must be canonical UTC RFC3339Nano")
	}
	return parsed, nil
}

func applyEvidenceObservationPrefix(kind string) (string, bool) {
	prefix, exists := map[string]string{
		"workload": "workload-observation", "secret": "secret-materialization", "runtime": "runtime-observation",
		"host": "host-observation", "provider-owner": "provider-owner-observation", "evidence": "evidence-observation", "health": "health-observation",
	}[kind]
	return prefix, exists
}

// ApplyEvidenceProducerKeyID returns the canonical provider-free identity for
// one trusted Ed25519 producer key.
func ApplyEvidenceProducerKeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return "ed25519://sha256/" + hex.EncodeToString(digest[:])
}

func applyEvidenceTrustAllows(trust ApplyEvidenceProducerTrust, requiredKind, requiredID string) bool {
	previous := ""
	kindAllowed := false
	for _, kind := range trust.RequirementKinds {
		if kind <= previous {
			return false
		}
		if _, valid := applyEvidenceObservationPrefix(kind); !valid {
			return false
		}
		if kind == requiredKind {
			kindAllowed = true
		}
		previous = kind
	}
	previous = ""
	idAllowed := false
	for _, id := range trust.ReceiptIDs {
		if id <= previous {
			return false
		}
		if id == requiredID {
			idAllowed = true
		}
		previous = id
	}
	return kindAllowed && idAllowed
}
