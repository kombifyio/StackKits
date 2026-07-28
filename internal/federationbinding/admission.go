// Package federationbinding owns account-free local admission of opaque,
// externally realized Federation-link bindings. It has no provider client,
// endpoint, credential, transport, or fabric lifecycle API.
package federationbinding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	AdmissionAPIVersion = "stackkit.owner-federation-link-binding-admission/v1"
	admissionKind       = "OwnerFederationLinkBindingAdmission"

	PurposeProductionImport = "production-import"
	PurposeHermeticProof    = "hermetic-live-proof"

	maxHermeticValidity = 15 * time.Minute
)

// Admission is the owner-signed import envelope. Only Binding crosses into
// Inventory; the envelope remains local custody evidence.
type Admission struct {
	APIVersion string                                                 `json:"apiVersion"`
	Kind       string                                                 `json:"kind"`
	Purpose    string                                                 `json:"purpose"`
	Binding    resolvedplan.ExternalFederationLinkBinding             `json:"binding"`
	Signature  localevidence.OwnerFederationBindingAdmissionSignature `json:"ownerSignature"`
}

type unsignedAdmission struct {
	APIVersion string                                     `json:"apiVersion"`
	Kind       string                                     `json:"kind"`
	Purpose    string                                     `json:"purpose"`
	Binding    resolvedplan.ExternalFederationLinkBinding `json:"binding"`
}

// ImportOptions makes hermetic admission an explicit test-lane decision.
// Production callers use the zero value and therefore reject proof bindings.
type ImportOptions struct {
	AllowHermeticProof bool
}

// HermeticIssueRequest contains only immutable release/requirement identity
// and a caller-owned nonce. The nonce is hashed and never emitted.
type HermeticIssueRequest struct {
	Requirement      resolvedplan.FederationLinkRequirement
	StackKitsVersion string
	CandidateDigest  string
	IssuedAt         time.Time
	Validity         time.Duration
	Nonce            []byte
}

type externalBindingWire struct {
	APIVersion             string   `json:"apiVersion"`
	Kind                   string   `json:"kind"`
	BindingRef             string   `json:"bindingRef"`
	FabricRef              string   `json:"fabricRef"`
	CustodyAttestationRef  string   `json:"custodyAttestationRef"`
	StackID                string   `json:"stackId"`
	CapabilityRef          string   `json:"capabilityRef"`
	ContractOwnerRef       string   `json:"contractOwnerRef"`
	CapabilityContractHash string   `json:"capabilityContractHash"`
	HomeSiteRefs           []string `json:"homeSiteRefs"`
	CloudSiteRefs          []string `json:"cloudSiteRefs"`
	TargetNodes            []string `json:"targetNodes"`
	BridgeContractHash     string   `json:"bridgeContractHash"`
	RequirementsHash       string   `json:"requirementsHash"`
	StackKitsVersion       string   `json:"stackkitsVersion"`
	CandidateDigest        string   `json:"candidateDigest"`
	SpecHash               string   `json:"specHash"`
	IssuedAt               string   `json:"issuedAt"`
	ValidUntil             string   `json:"validUntil"`
	BindingHash            string   `json:"bindingHash"`
}

// DecodeUnsignedProductionBinding accepts only the closed, unsigned external
// binding body. Admission envelopes, signatures, credentials, endpoints,
// provider fields, duplicate JSON names, and trailing values fail closed.
func DecodeUnsignedProductionBinding(raw []byte) (resolvedplan.ExternalFederationLinkBinding, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("federationbinding: unsigned external binding is empty")
	}
	if err := rejectDuplicateJSONNames(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var wire externalBindingWire
	if err := decoder.Decode(&wire); err != nil {
		return nil, fmt.Errorf("federationbinding: decode closed unsigned external binding: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("federationbinding: unsigned external binding contains trailing JSON")
	}
	canonical, err := resolvedplan.CanonicalJSON(wire)
	if err != nil {
		return nil, fmt.Errorf("federationbinding: canonicalize unsigned external binding: %w", err)
	}
	binding, err := resolvedplan.DecodeDocument[resolvedplan.ExternalFederationLinkBinding](canonical)
	if err != nil {
		return nil, fmt.Errorf("federationbinding: detach unsigned external binding: %w", err)
	}
	return binding, nil
}

func rejectDuplicateJSONNames(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkUniqueJSONValue(decoder, "binding"); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("federationbinding: unsigned external binding contains trailing JSON")
	}
	return nil
}

func walkUniqueJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("federationbinding: decode unsigned external binding at %s: %w", path, err)
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("federationbinding: decode object name at %s: %w", path, err)
			}
			name, ok := nameToken.(string)
			if !ok {
				return fmt.Errorf("federationbinding: non-string object name at %s", path)
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("federationbinding: duplicate JSON name %q at %s", name, path)
			}
			seen[name] = struct{}{}
			if err := walkUniqueJSONValue(decoder, path+"."+name); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("federationbinding: unterminated object at %s", path)
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := walkUniqueJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("federationbinding: unterminated array at %s", path)
		}
	default:
		return fmt.Errorf("federationbinding: invalid JSON delimiter at %s", path)
	}
	return nil
}

// SignProductionImport validates an already externally supplied opaque
// binding, then records the local Owner's explicit adoption. It creates no
// binding, provider resource, fabric, endpoint, or credential.
func SignProductionImport(workspaceRoot string, binding resolvedplan.ExternalFederationLinkBinding, requirement resolvedplan.FederationLinkRequirement, at time.Time) ([]byte, error) {
	if err := validateBindingAt(binding, requirement, at); err != nil {
		return nil, fmt.Errorf("federationbinding: reject production binding: %w", err)
	}
	return sign(workspaceRoot, PurposeProductionImport, binding)
}

// IssueHermeticProof creates a deterministic, short-lived opaque projection
// for a hermetic live-proof fixture. Its distinct purpose is rejected by
// production Import unless the caller explicitly opts into the proof lane.
func IssueHermeticProof(workspaceRoot string, request HermeticIssueRequest) ([]byte, error) {
	if request.IssuedAt.IsZero() {
		return nil, errors.New("federationbinding: hermetic issue time is required")
	}
	if request.Validity <= 0 || request.Validity > maxHermeticValidity {
		return nil, fmt.Errorf("federationbinding: hermetic validity must be within %s", maxHermeticValidity)
	}
	if len(request.Nonce) < 16 || len(request.Nonce) > 256 {
		return nil, errors.New("federationbinding: hermetic nonce must contain 16..256 bytes")
	}
	issuedAt := request.IssuedAt.UTC().Truncate(time.Second)
	requirementCanonical, err := resolvedplan.CanonicalJSON(request.Requirement)
	if err != nil {
		return nil, fmt.Errorf("federationbinding: canonicalize requirement: %w", err)
	}
	opaque := func(label string) string {
		digest := sha256.New()
		_, _ = digest.Write([]byte("stackkit.hermetic-federation-binding/v1\x00"))
		_, _ = digest.Write([]byte(label))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(requirementCanonical)
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(request.Nonce)
		return hex.EncodeToString(digest.Sum(nil))
	}
	binding := resolvedplan.ExternalFederationLinkBinding{
		"apiVersion":             "stackkit.external-federation-link-binding/v1",
		"kind":                   "ExternalFederationLinkBinding",
		"bindingRef":             "federation-link-binding://sha256/" + opaque("binding"),
		"fabricRef":              "federation-link-fabric://sha256/" + opaque("fabric"),
		"custodyAttestationRef":  "federation-link-custody-attestation://sha256/" + opaque("custody"),
		"stackId":                request.Requirement["stackId"],
		"capabilityRef":          request.Requirement["capabilityRef"],
		"contractOwnerRef":       request.Requirement["contractOwnerRef"],
		"capabilityContractHash": request.Requirement["capabilityContractHash"],
		"homeSiteRefs":           request.Requirement["homeSiteRefs"],
		"cloudSiteRefs":          request.Requirement["cloudSiteRefs"],
		"targetNodes":            request.Requirement["targetNodes"],
		"bridgeContractHash":     request.Requirement["bridgeContractHash"],
		"requirementsHash":       request.Requirement["requirementsHash"],
		"stackkitsVersion":       request.StackKitsVersion,
		"candidateDigest":        request.CandidateDigest,
		"specHash":               request.Requirement["specHash"],
		"issuedAt":               issuedAt.Format(time.RFC3339),
		"validUntil":             issuedAt.Add(request.Validity).Format(time.RFC3339),
	}
	bindingHash, err := resolvedplan.ComputeExternalFederationLinkBindingHash(binding)
	if err != nil {
		return nil, fmt.Errorf("federationbinding: hash hermetic binding: %w", err)
	}
	binding["bindingHash"] = bindingHash
	if err := validateBindingAt(binding, request.Requirement, issuedAt); err != nil {
		return nil, fmt.Errorf("federationbinding: generated hermetic binding is invalid: %w", err)
	}
	return sign(workspaceRoot, PurposeHermeticProof, binding)
}

// Import verifies closed wire shape, purpose, exact compiler requirement,
// lifetime, binding hash, and current local Owner custody before returning a
// detached binding. No state or external system is mutated.
func Import(workspaceRoot string, raw []byte, requirement resolvedplan.FederationLinkRequirement, at time.Time, options ImportOptions) (resolvedplan.ExternalFederationLinkBinding, error) {
	admission, err := decode(raw)
	if err != nil {
		return nil, err
	}
	switch admission.Purpose {
	case PurposeProductionImport:
	case PurposeHermeticProof:
		if !options.AllowHermeticProof {
			return nil, errors.New("federationbinding: hermetic live-proof admission is forbidden in production import")
		}
	default:
		return nil, errors.New("federationbinding: unsupported admission purpose")
	}
	if err := validateBindingAt(admission.Binding, requirement, at); err != nil {
		return nil, fmt.Errorf("federationbinding: binding admission failed: %w", err)
	}
	unsigned, err := canonicalUnsigned(admission.Purpose, admission.Binding)
	if err != nil {
		return nil, err
	}
	if err := localevidence.VerifyOwnerFederationBindingAdmission(workspaceRoot, unsigned, admission.Signature); err != nil {
		return nil, fmt.Errorf("federationbinding: Owner signature does not verify: %w", err)
	}
	return cloneBinding(admission.Binding)
}

// ImportIntoInventory performs the same fail-closed admission and injects only
// the closed binding body into a detached Inventory value.
func ImportIntoInventory(workspaceRoot string, raw []byte, requirement resolvedplan.FederationLinkRequirement, inventory resolvedplan.InventoryFacts, at time.Time, options ImportOptions) (resolvedplan.InventoryFacts, error) {
	binding, err := Import(workspaceRoot, raw, requirement, at, options)
	if err != nil {
		return nil, err
	}
	cloned, err := cloneInventory(inventory)
	if err != nil {
		return nil, err
	}
	if schema, _ := cloned["schemaVersion"].(string); schema != "stackkit.inventory/v1" {
		return nil, errors.New("federationbinding: Inventory must use stackkit.inventory/v1")
	}
	existing, exists := cloned["externalFederationLinkBindings"]
	if exists && existing != nil {
		object, ok := existing.(map[string]any)
		if !ok || len(object) != 0 {
			return nil, errors.New("federationbinding: Inventory already contains Federation-link binding state")
		}
	}
	cloned["externalFederationLinkBindings"] = map[string]any{"inter-site-link": map[string]any(binding)}
	return cloned, nil
}

func sign(workspaceRoot, purpose string, binding resolvedplan.ExternalFederationLinkBinding) ([]byte, error) {
	unsigned, err := canonicalUnsigned(purpose, binding)
	if err != nil {
		return nil, err
	}
	signature, err := localevidence.SignOwnerFederationBindingAdmission(workspaceRoot, unsigned)
	if err != nil {
		return nil, fmt.Errorf("federationbinding: sign local admission: %w", err)
	}
	return resolvedplan.CanonicalJSON(Admission{
		APIVersion: AdmissionAPIVersion, Kind: admissionKind, Purpose: purpose,
		Binding: binding, Signature: signature,
	})
}

func canonicalUnsigned(purpose string, binding resolvedplan.ExternalFederationLinkBinding) ([]byte, error) {
	return resolvedplan.CanonicalJSON(unsignedAdmission{
		APIVersion: AdmissionAPIVersion, Kind: admissionKind, Purpose: purpose, Binding: binding,
	})
}

func decode(raw []byte) (Admission, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return Admission{}, errors.New("federationbinding: admission document is empty")
	}
	var admission Admission
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&admission); err != nil {
		return Admission{}, fmt.Errorf("federationbinding: decode closed admission: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Admission{}, errors.New("federationbinding: admission contains trailing JSON")
	}
	if admission.APIVersion != AdmissionAPIVersion || admission.Kind != admissionKind ||
		strings.TrimSpace(admission.Signature.OwnerRef) == "" || strings.TrimSpace(admission.Signature.KeyID) == "" ||
		strings.TrimSpace(admission.Signature.Value) == "" || admission.Binding == nil {
		return Admission{}, errors.New("federationbinding: admission envelope is incomplete")
	}
	return admission, nil
}

func validateBindingAt(binding resolvedplan.ExternalFederationLinkBinding, requirement resolvedplan.FederationLinkRequirement, at time.Time) error {
	if at.IsZero() {
		return errors.New("trusted admission time is required")
	}
	if err := resolvedplan.ValidateExternalFederationLinkBinding(binding, requirement); err != nil {
		return err
	}
	return resolvedplan.ValidateExternalFederationLinkBindingsFreshness(resolvedplan.ResolvedPlan{
		"federationLinkRequirements":     map[string]any{"inter-site-link": map[string]any(requirement)},
		"externalFederationLinkBindings": map[string]any{"inter-site-link": map[string]any(binding)},
	}, at.UTC())
}

func cloneBinding(binding resolvedplan.ExternalFederationLinkBinding) (resolvedplan.ExternalFederationLinkBinding, error) {
	raw, err := resolvedplan.CanonicalJSON(binding)
	if err != nil {
		return nil, fmt.Errorf("federationbinding: clone binding: %w", err)
	}
	var cloned resolvedplan.ExternalFederationLinkBinding
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return nil, fmt.Errorf("federationbinding: decode cloned binding: %w", err)
	}
	return cloned, nil
}

func cloneInventory(inventory resolvedplan.InventoryFacts) (resolvedplan.InventoryFacts, error) {
	raw, err := resolvedplan.CanonicalJSON(inventory)
	if err != nil {
		return nil, fmt.Errorf("federationbinding: clone Inventory: %w", err)
	}
	var cloned resolvedplan.InventoryFacts
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return nil, fmt.Errorf("federationbinding: decode cloned Inventory: %w", err)
	}
	return cloned, nil
}
