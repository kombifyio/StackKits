// Package backupbinding is the public producer contract for StackKits'
// provider-free external Cloud backup target handshake. It never provisions a
// provider resource or accepts provider, endpoint, account, bucket, credential,
// lease, or resource lifecycle fields.
package backupbinding

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
	RequirementAPIVersion = "stackkit.backup-target-requirement/v1"
	BindingAPIVersion     = "stackkit.external-backup-target-binding/v1"
	Capability            = "offsite-object-backup"
	MaxValidity           = 24 * time.Hour
)

var (
	digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	semverPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
)

type Document map[string]any

type Input struct {
	BindingRef            string
	BackupTargetRef       string
	CustodyAttestationRef string
	StackKitsVersion      string
	CandidateDigest       string
	IssuedAt              time.Time
	ValidUntil            time.Time
}

type requirementWire struct {
	APIVersion             string         `json:"apiVersion"`
	Kind                   string         `json:"kind"`
	StackID                string         `json:"stackId"`
	SiteRef                string         `json:"siteRef"`
	CapabilityRef          string         `json:"capabilityRef"`
	ContractOwnerRef       string         `json:"contractOwnerRef"`
	CapabilityContractHash string         `json:"capabilityContractHash"`
	TargetNodeRefs         []string       `json:"targetNodeRefs"`
	Policy                 map[string]any `json:"policy"`
	SpecHash               string         `json:"specHash"`
	RequirementsHash       string         `json:"requirementsHash"`
}

type bindingWire struct {
	APIVersion             string `json:"apiVersion"`
	Kind                   string `json:"kind"`
	BindingRef             string `json:"bindingRef"`
	BackupTargetRef        string `json:"backupTargetRef"`
	CustodyAttestationRef  string `json:"custodyAttestationRef"`
	StackID                string `json:"stackId"`
	SiteRef                string `json:"siteRef"`
	CapabilityRef          string `json:"capabilityRef"`
	ContractOwnerRef       string `json:"contractOwnerRef"`
	CapabilityContractHash string `json:"capabilityContractHash"`
	RequirementsHash       string `json:"requirementsHash"`
	StackKitsVersion       string `json:"stackkitsVersion"`
	CandidateDigest        string `json:"candidateDigest"`
	SpecHash               string `json:"specHash"`
	IssuedAt               string `json:"issuedAt"`
	ValidUntil             string `json:"validUntil"`
	BindingHash            string `json:"bindingHash"`
}

func Build(requirement Document, input Input) (Document, error) {
	required, err := decodeRequirement(requirement)
	if err != nil {
		return nil, err
	}
	wire := bindingWire{
		APIVersion: BindingAPIVersion, Kind: "ExternalBackupTargetBinding",
		BindingRef: input.BindingRef, BackupTargetRef: input.BackupTargetRef,
		CustodyAttestationRef: input.CustodyAttestationRef,
		StackID:               required.StackID, SiteRef: required.SiteRef, CapabilityRef: required.CapabilityRef,
		ContractOwnerRef: required.ContractOwnerRef, CapabilityContractHash: required.CapabilityContractHash,
		RequirementsHash: required.RequirementsHash, StackKitsVersion: input.StackKitsVersion,
		CandidateDigest: input.CandidateDigest, SpecHash: required.SpecHash,
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

func Validate(binding, requirement Document) error {
	required, err := decodeRequirement(requirement)
	if err != nil {
		return err
	}
	var wire bindingWire
	if err := decodeClosed(binding, &wire); err != nil {
		return fmt.Errorf("external backup binding is outside the closed v1 contract: %w", err)
	}
	if wire.APIVersion != BindingAPIVersion || wire.Kind != "ExternalBackupTargetBinding" {
		return errors.New("unsupported external backup target binding contract")
	}
	for field, value := range map[string]string{
		"bindingRef": wire.BindingRef, "backupTargetRef": wire.BackupTargetRef,
		"custodyAttestationRef": wire.CustodyAttestationRef,
	} {
		if !validOpaqueRef(field, value) {
			return fmt.Errorf("%s must be an opaque sha256 reference", field)
		}
	}
	for field, values := range map[string][2]string{
		"stackId": {wire.StackID, required.StackID}, "siteRef": {wire.SiteRef, required.SiteRef},
		"capabilityRef":          {wire.CapabilityRef, required.CapabilityRef},
		"contractOwnerRef":       {wire.ContractOwnerRef, required.ContractOwnerRef},
		"capabilityContractHash": {wire.CapabilityContractHash, required.CapabilityContractHash},
		"requirementsHash":       {wire.RequirementsHash, required.RequirementsHash}, "specHash": {wire.SpecHash, required.SpecHash},
	} {
		if values[0] != values[1] {
			return fmt.Errorf("%s does not match the exact backup target requirement", field)
		}
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

func ComputeHash(binding Document) (string, error) {
	clone := make(Document, len(binding))
	for key, value := range binding {
		clone[key] = value
	}
	delete(clone, "bindingHash")
	encoded, err := json.Marshal(clone)
	if err != nil {
		return "", fmt.Errorf("encode external backup binding: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func OpaqueReference(scheme string, evidence []byte) (string, error) {
	switch scheme {
	case "backup-target-binding", "backup-target", "backup-custody-attestation":
	default:
		return "", fmt.Errorf("unsupported external backup reference scheme %q", scheme)
	}
	if len(evidence) == 0 {
		return "", errors.New("external backup reference evidence is empty")
	}
	digest := sha256.Sum256(evidence)
	return scheme + "://sha256/" + hex.EncodeToString(digest[:]), nil
}

func decodeRequirement(document Document) (requirementWire, error) {
	var requirement requirementWire
	if err := decodeClosed(document, &requirement); err != nil {
		return requirement, fmt.Errorf("backup target requirement is outside the closed v1 contract: %w", err)
	}
	if requirement.APIVersion != RequirementAPIVersion || requirement.Kind != "BackupTargetRequirement" ||
		requirement.CapabilityRef != Capability || strings.TrimSpace(requirement.StackID) == "" ||
		strings.TrimSpace(requirement.SiteRef) == "" || strings.TrimSpace(requirement.ContractOwnerRef) == "" ||
		len(requirement.TargetNodeRefs) == 0 || len(requirement.Policy) == 0 ||
		!digestPattern.MatchString(requirement.CapabilityContractHash) ||
		!digestPattern.MatchString(requirement.SpecHash) || !digestPattern.MatchString(requirement.RequirementsHash) {
		return requirement, errors.New("backup target requirement is incomplete or unsupported")
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

func validOpaqueRef(field, value string) bool {
	scheme := map[string]string{
		"bindingRef": "backup-target-binding", "backupTargetRef": "backup-target",
		"custodyAttestationRef": "backup-custody-attestation",
	}[field]
	if !strings.HasPrefix(value, scheme+"://sha256/") || len(value) != len(scheme+"://sha256/")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, scheme+"://sha256/"))
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
