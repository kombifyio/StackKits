// Package federationbinding is the public producer contract for StackKits'
// provider-free external Federation link handshake. An external fabric
// authority builds the opaque, unsigned binding for one compiler-derived
// FederationLinkRequirement; the local Owner signs and adopts it with
// `stackkit federation binding adopt`. The package never creates a fabric, a
// key, an interface or an endpoint, and it accepts no provider, credential or
// lifecycle field.
package federationbinding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	RequirementAPIVersion = "stackkit.federation-link-requirement/v1"
	BindingAPIVersion     = "stackkit.external-federation-link-binding/v1"
	Capability            = "inter-site-link"
	MaxValidity           = 24 * time.Hour

	SchemeBinding = "federation-link-binding"
	SchemeFabric  = "federation-link-fabric"
	SchemeCustody = "federation-link-custody-attestation"
)

var (
	digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	semverPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
)

type Document map[string]any

// Input is everything the fabric authority adds to the requirement. The three
// evidence values are hashed into opaque references and never emitted.
type Input struct {
	BindingEvidence  []byte
	FabricEvidence   []byte
	CustodyEvidence  []byte
	StackKitsVersion string
	CandidateDigest  string
	IssuedAt         time.Time
	ValidUntil       time.Time
}

type targetNode struct {
	SiteRef string `json:"siteRef"`
	NodeRef string `json:"nodeRef"`
}

type requirementWire struct {
	APIVersion             string         `json:"apiVersion"`
	Kind                   string         `json:"kind"`
	StackID                string         `json:"stackId"`
	CapabilityRef          string         `json:"capabilityRef"`
	ContractOwnerRef       string         `json:"contractOwnerRef"`
	CapabilityContractHash string         `json:"capabilityContractHash"`
	HomeSiteRefs           []string       `json:"homeSiteRefs"`
	CloudSiteRefs          []string       `json:"cloudSiteRefs"`
	TargetNodes            []targetNode   `json:"targetNodes"`
	BridgeContractHash     string         `json:"bridgeContractHash"`
	Policy                 map[string]any `json:"policy"`
	SpecHash               string         `json:"specHash"`
	RequirementsHash       string         `json:"requirementsHash"`
}

type bindingWire struct {
	APIVersion             string       `json:"apiVersion"`
	Kind                   string       `json:"kind"`
	BindingRef             string       `json:"bindingRef"`
	FabricRef              string       `json:"fabricRef"`
	CustodyAttestationRef  string       `json:"custodyAttestationRef"`
	StackID                string       `json:"stackId"`
	CapabilityRef          string       `json:"capabilityRef"`
	ContractOwnerRef       string       `json:"contractOwnerRef"`
	CapabilityContractHash string       `json:"capabilityContractHash"`
	HomeSiteRefs           []string     `json:"homeSiteRefs"`
	CloudSiteRefs          []string     `json:"cloudSiteRefs"`
	TargetNodes            []targetNode `json:"targetNodes"`
	BridgeContractHash     string       `json:"bridgeContractHash"`
	RequirementsHash       string       `json:"requirementsHash"`
	StackKitsVersion       string       `json:"stackkitsVersion"`
	CandidateDigest        string       `json:"candidateDigest"`
	SpecHash               string       `json:"specHash"`
	IssuedAt               string       `json:"issuedAt"`
	ValidUntil             string       `json:"validUntil"`
	BindingHash            string       `json:"bindingHash"`
}

// Build returns the unsigned binding for one requirement. The result passes
// Validate against that same requirement.
func Build(requirement Document, input Input) (Document, error) {
	required, err := decodeRequirement(requirement)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for field, source := range map[string]struct {
		scheme   string
		evidence []byte
	}{
		"bindingRef": {SchemeBinding, input.BindingEvidence}, "fabricRef": {SchemeFabric, input.FabricEvidence},
		"custodyAttestationRef": {SchemeCustody, input.CustodyEvidence},
	} {
		ref, refErr := OpaqueReference(source.scheme, source.evidence)
		if refErr != nil {
			return nil, refErr
		}
		refs[field] = ref
	}
	wire := bindingWire{
		APIVersion: BindingAPIVersion, Kind: "ExternalFederationLinkBinding",
		BindingRef: refs["bindingRef"], FabricRef: refs["fabricRef"], CustodyAttestationRef: refs["custodyAttestationRef"],
		StackID: required.StackID, CapabilityRef: required.CapabilityRef, ContractOwnerRef: required.ContractOwnerRef,
		CapabilityContractHash: required.CapabilityContractHash, HomeSiteRefs: required.HomeSiteRefs,
		CloudSiteRefs: required.CloudSiteRefs, TargetNodes: required.TargetNodes,
		BridgeContractHash: required.BridgeContractHash, RequirementsHash: required.RequirementsHash,
		StackKitsVersion: input.StackKitsVersion, CandidateDigest: input.CandidateDigest, SpecHash: required.SpecHash,
		IssuedAt: canonicalTime(input.IssuedAt), ValidUntil: canonicalTime(input.ValidUntil),
	}
	document, err := documentFrom(wire)
	if err != nil {
		return nil, err
	}
	hash, err := ComputeHash(document)
	if err != nil {
		return nil, err
	}
	document["bindingHash"] = hash
	if err := Validate(document, requirement); err != nil {
		return nil, err
	}
	return document, nil
}

// Validate checks a binding against its exact requirement, the closed v1
// shape, opaque references, canonical UTC timestamps, the 24 hour maximum
// validity and the binding hash.
func Validate(binding, requirement Document) error {
	required, err := decodeRequirement(requirement)
	if err != nil {
		return err
	}
	var wire bindingWire
	if err := decodeClosed(binding, &wire); err != nil {
		return fmt.Errorf("external federation binding is outside the closed v1 contract: %w", err)
	}
	if wire.APIVersion != BindingAPIVersion || wire.Kind != "ExternalFederationLinkBinding" {
		return errors.New("unsupported external federation link binding contract")
	}
	for field, source := range map[string]struct{ value, scheme string }{
		"bindingRef": {wire.BindingRef, SchemeBinding}, "fabricRef": {wire.FabricRef, SchemeFabric},
		"custodyAttestationRef": {wire.CustodyAttestationRef, SchemeCustody},
	} {
		if !validOpaqueRef(source.scheme, source.value) {
			return fmt.Errorf("%s must be an opaque sha256 reference", field)
		}
	}
	if err := requireSame(wire, required); err != nil {
		return err
	}
	if !semverPattern.MatchString(wire.StackKitsVersion) {
		return errors.New("stackkitsVersion must be a semantic version")
	}
	if !digestPattern.MatchString(wire.CandidateDigest) || !digestPattern.MatchString(wire.BindingHash) {
		return errors.New("candidateDigest and bindingHash must be sha256 content hashes")
	}
	issuedAt, err := parseCanonicalTime(wire.IssuedAt)
	if err != nil {
		return fmt.Errorf("issuedAt: %w", err)
	}
	validUntil, err := parseCanonicalTime(wire.ValidUntil)
	if err != nil {
		return fmt.Errorf("validUntil: %w", err)
	}
	if !issuedAt.Before(validUntil) || validUntil.Sub(issuedAt) > MaxValidity {
		return fmt.Errorf("validUntil must be after issuedAt and no more than %s later", MaxValidity)
	}
	wantHash, err := ComputeHash(binding)
	if err != nil {
		return err
	}
	if wire.BindingHash != wantHash {
		return errors.New("bindingHash does not match the canonical binding body")
	}
	return nil
}

func requireSame(wire bindingWire, required requirementWire) error {
	pairs := map[string][2]any{
		"stackId": {wire.StackID, required.StackID}, "capabilityRef": {wire.CapabilityRef, required.CapabilityRef},
		"contractOwnerRef":       {wire.ContractOwnerRef, required.ContractOwnerRef},
		"capabilityContractHash": {wire.CapabilityContractHash, required.CapabilityContractHash},
		"bridgeContractHash":     {wire.BridgeContractHash, required.BridgeContractHash},
		"requirementsHash":       {wire.RequirementsHash, required.RequirementsHash}, "specHash": {wire.SpecHash, required.SpecHash},
		"homeSiteRefs": {wire.HomeSiteRefs, required.HomeSiteRefs}, "cloudSiteRefs": {wire.CloudSiteRefs, required.CloudSiteRefs},
		"targetNodes": {wire.TargetNodes, required.TargetNodes},
	}
	for field, values := range pairs {
		left, _ := json.Marshal(values[0])
		right, _ := json.Marshal(values[1])
		if !bytes.Equal(left, right) {
			return fmt.Errorf("%s does not match the exact federation link requirement", field)
		}
	}
	return nil
}

// ComputeHash is the canonical hash of the binding body without bindingHash.
func ComputeHash(binding Document) (string, error) {
	clone := make(Document, len(binding))
	for key, value := range binding {
		clone[key] = value
	}
	delete(clone, "bindingHash")
	encoded, err := json.Marshal(clone)
	if err != nil {
		return "", fmt.Errorf("encode external federation binding: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// OpaqueReference hashes evidence into an opaque reference of one scheme.
func OpaqueReference(scheme string, evidence []byte) (string, error) {
	switch scheme {
	case SchemeBinding, SchemeFabric, SchemeCustody:
	default:
		return "", fmt.Errorf("unsupported external federation reference scheme %q", scheme)
	}
	if len(evidence) == 0 {
		return "", errors.New("external federation reference evidence is empty")
	}
	digest := sha256.Sum256(evidence)
	return scheme + "://sha256/" + hex.EncodeToString(digest[:]), nil
}

func decodeRequirement(document Document) (requirementWire, error) {
	var requirement requirementWire
	if err := decodeClosed(document, &requirement); err != nil {
		return requirement, fmt.Errorf("federation link requirement is outside the closed v1 contract: %w", err)
	}
	if requirement.APIVersion != RequirementAPIVersion || requirement.Kind != "FederationLinkRequirement" ||
		requirement.CapabilityRef != Capability || strings.TrimSpace(requirement.StackID) == "" ||
		strings.TrimSpace(requirement.ContractOwnerRef) == "" || len(requirement.HomeSiteRefs) == 0 ||
		len(requirement.CloudSiteRefs) == 0 || len(requirement.TargetNodes) == 0 || len(requirement.Policy) == 0 ||
		!digestPattern.MatchString(requirement.CapabilityContractHash) || !digestPattern.MatchString(requirement.BridgeContractHash) ||
		!digestPattern.MatchString(requirement.SpecHash) || !digestPattern.MatchString(requirement.RequirementsHash) {
		return requirement, errors.New("federation link requirement is incomplete or unsupported")
	}
	return requirement, nil
}

func decodeClosed(document Document, target any) error {
	encoded, err := json.Marshal(document)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("document contains trailing JSON")
	}
	return nil
}

func documentFrom(value any) (Document, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document Document
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, err
	}
	return document, nil
}

func validOpaqueRef(scheme, value string) bool {
	prefix := scheme + "://sha256/"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil
}

func canonicalTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseCanonicalTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, errors.New("must be a canonical RFC3339Nano UTC timestamp")
	}
	return parsed, nil
}
