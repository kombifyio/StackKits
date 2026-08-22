// Package workloadremoval defines the provider-neutral, owner-approved
// removal contract for one exact workload from a previously applied
// Architecture v2 runtime request.
package workloadremoval

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	APIVersion         = "stackkit.workload-removal/v1"
	ResultAPIVersion   = "stackkit.workload-removal-result/v1"
	EvidenceAPIVersion = "stackkit.workload-removal-evidence/v1"
	StatusRemoved      = "removed"
	maxValidity        = 5 * time.Minute
)

// OwnerAuthorization binds the destructive request to established local
// Owner custody. Signature is produced with localevidence's lifecycle
// mutation domain; the execution channel transports but never mints it.
type OwnerAuthorization struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

// AuthorizationPayload is the exact canonical value signed by the Owner.
type AuthorizationPayload struct {
	APIVersion           string `json:"apiVersion"`
	AppliedRequestDigest string `json:"appliedRequestDigest"`
	PlanHash             string `json:"planHash"`
	WorkloadRef          string `json:"workloadRef"`
	RequirementID        string `json:"requirementId"`
	InstanceRef          string `json:"instanceRef"`
	RequestedAt          string `json:"requestedAt"`
	ValidUntil           string `json:"validUntil"`
}

// Request carries one closed child request recovered from successful Product
// Apply. It cannot select a provider, host, artifact, or execution channel that
// was absent from that applied authority.
type Request struct {
	APIVersion    string                           `json:"apiVersion"`
	Applied       runtimeexecutor.ExecutionRequest `json:"applied"`
	WorkloadRef   string                           `json:"workloadRef"`
	RequestedAt   string                           `json:"requestedAt"`
	ValidUntil    string                           `json:"validUntil"`
	Authorization OwnerAuthorization               `json:"authorization"`
	RequestDigest string                           `json:"requestDigest"`
}

type Outcome struct {
	RequirementID     string `json:"requirementId"`
	WorkloadRef       string `json:"workloadRef"`
	InstanceRef       string `json:"instanceRef"`
	RuntimeOwnerRef   string `json:"runtimeOwnerRef"`
	ArtifactDigest    string `json:"artifactDigest"`
	Status            string `json:"status"`
	ObservedState     string `json:"observedState"`
	ObservationRef    string `json:"observationRef"`
	ObservationDigest string `json:"observationDigest"`
}

type Result struct {
	APIVersion    string  `json:"apiVersion"`
	RequestDigest string  `json:"requestDigest"`
	RemovedAt     string  `json:"removedAt"`
	Outcome       Outcome `json:"outcome"`
	ResultDigest  string  `json:"resultDigest"`
}

// EvidenceAuthority is the minimal non-secret projection required to verify
// terminal absence without exporting the applied request's artifact content.
// It is derived only after the complete Request and Result have passed the
// StackKits-owned validators.
type EvidenceAuthority struct {
	AppliedRequestDigest string             `json:"appliedRequestDigest"`
	PlanHash             string             `json:"planHash"`
	WorkloadRef          string             `json:"workloadRef"`
	RequirementID        string             `json:"requirementId"`
	InstanceRef          string             `json:"instanceRef"`
	RuntimeOwnerRef      string             `json:"runtimeOwnerRef"`
	ArtifactDigest       string             `json:"artifactDigest"`
	SiteRef              string             `json:"siteRef"`
	NodeRef              string             `json:"nodeRef"`
	ExecutionChannelRef  string             `json:"executionChannelRef"`
	RequestedAt          string             `json:"requestedAt"`
	ValidUntil           string             `json:"validUntil"`
	Authorization        OwnerAuthorization `json:"authorization"`
	RequestDigest        string             `json:"requestDigest"`
}

// Evidence is the transport-safe projection produced by the pinned StackKits
// process. Request remains the local Owner authority; this value preserves its
// exact successful outcome without reconstructing or exporting that authority.
type Evidence struct {
	APIVersion     string            `json:"apiVersion"`
	Authority      EvidenceAuthority `json:"authority"`
	Result         Result            `json:"result"`
	EvidenceDigest string            `json:"evidenceDigest"`
}

// SelectAppliedWorkload reduces a sealed Product Apply request to the exact
// workload child and its referenced immutable artifacts.
func SelectAppliedWorkload(applied runtimeexecutor.ExecutionRequest, workloadRef string) (runtimeexecutor.ExecutionRequest, error) {
	if err := applied.Validate(); err != nil {
		return runtimeexecutor.ExecutionRequest{}, fmt.Errorf("validate applied runtime request: %w", err)
	}
	workloadRef = strings.TrimSpace(workloadRef)
	if workloadRef == "" || workloadRef != strings.ToLower(workloadRef) {
		return runtimeexecutor.ExecutionRequest{}, errors.New("workload removal requires one canonical workload ref")
	}
	var selected []runtimeexecutor.RuntimeTarget
	for _, target := range applied.RuntimeTargets {
		if target.WorkloadRef == workloadRef {
			selected = append(selected, target)
		}
	}
	if len(selected) != 1 {
		return runtimeexecutor.ExecutionRequest{}, fmt.Errorf("applied request contains %d runtime targets for workload %q; exactly one is required", len(selected), workloadRef)
	}
	target := selected[0]
	artifactRefs := map[string]struct{}{}
	for _, ref := range target.ArtifactRefs {
		artifactRefs[ref] = struct{}{}
	}
	if target.RuntimeAdapter != nil {
		for _, ref := range target.RuntimeAdapter.ArtifactRefs {
			artifactRefs[ref] = struct{}{}
		}
		for _, agent := range target.RuntimeAdapter.Agents {
			for _, ref := range agent.ArtifactRefs {
				artifactRefs[ref] = struct{}{}
			}
		}
	}
	artifacts := make([]runtimeexecutor.Artifact, 0, len(artifactRefs))
	for _, artifact := range applied.Artifacts {
		if _, keep := artifactRefs[artifact.ID]; keep {
			artifacts = append(artifacts, artifact)
		}
	}
	selectedRequest := runtimeexecutor.CloneExecutionRequest(applied)
	selectedRequest.RuntimeTargets = []runtimeexecutor.RuntimeTarget{target}
	selectedRequest.HealthTargets = nil
	selectedRequest.AccessBindings = selectAccessBindings(applied.AccessBindings, target.AccessBindingRefs)
	selectedRequest.BackupTargetBindings = selectBackupBindings(applied.BackupTargetBindings, target.BackupTargetBindingRefs)
	selectedRequest.Artifacts = artifacts
	if len(selectedRequest.AccessBindings) == 0 && len(selectedRequest.BackupTargetBindings) == 0 {
		selectedRequest.AuthorizationTime = ""
	}
	selectedRequest.RequestDigest = ""
	sealed, err := runtimeexecutor.SealRequest(selectedRequest)
	if err != nil {
		return runtimeexecutor.ExecutionRequest{}, fmt.Errorf("seal applied workload authority: %w", err)
	}
	return sealed, nil
}

func AuthorizationBytes(applied runtimeexecutor.ExecutionRequest, workloadRef string, requestedAt, validUntil time.Time) ([]byte, error) {
	payload, err := authorizationPayload(applied, workloadRef, requestedAt, validUntil)
	if err != nil {
		return nil, err
	}
	return resolvedplan.CanonicalJSON(payload)
}

func SealRequest(applied runtimeexecutor.ExecutionRequest, workloadRef string, requestedAt, validUntil time.Time, authorization OwnerAuthorization) (Request, error) {
	payload, err := authorizationPayload(applied, workloadRef, requestedAt, validUntil)
	if err != nil {
		return Request{}, err
	}
	request := Request{
		APIVersion: APIVersion, Applied: runtimeexecutor.CloneExecutionRequest(applied),
		WorkloadRef: workloadRef, RequestedAt: payload.RequestedAt, ValidUntil: payload.ValidUntil,
		Authorization: authorization,
	}
	digest, err := requestHash(request)
	if err != nil {
		return Request{}, err
	}
	request.RequestDigest = digest
	if err := request.ValidateAt(requestedAt); err != nil {
		return Request{}, err
	}
	return request, nil
}

func (request Request) ValidateAt(now time.Time) error {
	if request.APIVersion != APIVersion || !validDigest(request.RequestDigest) {
		return errors.New("workload removal request identity is invalid")
	}
	if err := request.Applied.Validate(); err != nil {
		return fmt.Errorf("validate applied workload authority: %w", err)
	}
	if len(request.Applied.RuntimeTargets) != 1 || request.Applied.RuntimeTargets[0].WorkloadRef != request.WorkloadRef {
		return errors.New("workload removal request is not bound to exactly one applied workload")
	}
	if !validDigest(appliedArtifactDigest(request.Applied, request.Applied.RuntimeTargets[0])) {
		return errors.New("workload removal request must bind exactly one applied executable artifact")
	}
	requestedAt, validUntil, err := parseValidity(request.RequestedAt, request.ValidUntil)
	if err != nil {
		return err
	}
	if now.IsZero() || now.Location() != time.UTC || now.Before(requestedAt) || !now.Before(validUntil) {
		return errors.New("workload removal authorization is not fresh")
	}
	if strings.TrimSpace(request.Authorization.OwnerRef) == "" || strings.TrimSpace(request.Authorization.KeyID) == "" {
		return errors.New("workload removal requires exact Owner authorization identity")
	}
	signature, err := base64.RawStdEncoding.DecodeString(request.Authorization.Value)
	if err != nil || len(signature) != 64 {
		return errors.New("workload removal Owner authorization signature is malformed")
	}
	digest, err := requestHash(request)
	if err != nil {
		return err
	}
	if digest != request.RequestDigest {
		return errors.New("workload removal request digest does not match its exact authority")
	}
	return nil
}

func (request Request) Canonical() ([]byte, error) {
	if !validDigest(request.RequestDigest) {
		return nil, errors.New("workload removal request is unsealed")
	}
	return resolvedplan.CanonicalJSON(request)
}

func NewResult(request Request, removedAt time.Time, outcome Outcome) (Result, error) {
	if err := request.ValidateAt(removedAt); err != nil {
		return Result{}, err
	}
	target := request.Applied.RuntimeTargets[0]
	if outcome.RequirementID != target.RequirementID || outcome.WorkloadRef != request.WorkloadRef ||
		outcome.InstanceRef != target.InstanceRef || outcome.RuntimeOwnerRef != runtimeOwnerRef(target) ||
		outcome.ArtifactDigest != appliedArtifactDigest(request.Applied, target) ||
		outcome.Status != StatusRemoved || outcome.ObservedState != "absent" ||
		!strings.HasPrefix(outcome.ObservationRef, "removal-observation://") || !validDigest(outcome.ObservationDigest) {
		return Result{}, errors.New("workload removal outcome does not prove the exact target is absent")
	}
	result := Result{
		APIVersion: ResultAPIVersion, RequestDigest: request.RequestDigest,
		RemovedAt: removedAt.Format(time.RFC3339Nano), Outcome: outcome,
	}
	digest, err := resultHash(result)
	if err != nil {
		return Result{}, err
	}
	result.ResultDigest = digest
	return result, result.Validate(request)
}

func (result Result) Validate(request Request) error {
	removedAt, err := time.Parse(time.RFC3339Nano, result.RemovedAt)
	if err != nil || removedAt.Location() != time.UTC || removedAt.Format(time.RFC3339Nano) != result.RemovedAt {
		return errors.New("workload removal result time is invalid")
	}
	if err := request.ValidateAt(removedAt); err != nil {
		return fmt.Errorf("validate workload removal request at result time: %w", err)
	}
	target := request.Applied.RuntimeTargets[0]
	return validateResult(result, EvidenceAuthority{
		WorkloadRef: request.WorkloadRef, RequirementID: target.RequirementID,
		InstanceRef: target.InstanceRef, RuntimeOwnerRef: runtimeOwnerRef(target),
		ArtifactDigest: appliedArtifactDigest(request.Applied, target),
		RequestedAt:    request.RequestedAt, ValidUntil: request.ValidUntil,
		RequestDigest: request.RequestDigest,
	})
}

func ParseResult(data []byte, request Request) (Result, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return Result{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Result{}, errors.New("workload removal result contains trailing JSON")
	}
	if err := result.Validate(request); err != nil {
		return Result{}, err
	}
	canonical, err := resolvedplan.CanonicalJSON(result)
	if err != nil || !bytes.Equal(canonical, data) {
		return Result{}, errors.New("workload removal result is not canonical JSON")
	}
	return result, nil
}

func (result Result) Canonical() ([]byte, error) {
	if !validDigest(result.ResultDigest) {
		return nil, errors.New("workload removal result is unsealed")
	}
	return resolvedplan.CanonicalJSON(result)
}

// NewEvidence projects a fully validated local Request and Result into the
// bounded terminal proof that may cross the execution channel.
func NewEvidence(request Request, result Result) (Evidence, error) {
	if err := result.Validate(request); err != nil {
		return Evidence{}, err
	}
	target := request.Applied.RuntimeTargets[0]
	if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" {
		return Evidence{}, errors.New("workload removal evidence requires one exact Site, node, and execution channel")
	}
	evidence := Evidence{
		APIVersion: EvidenceAPIVersion,
		Authority: EvidenceAuthority{
			AppliedRequestDigest: request.Applied.RequestDigest, PlanHash: request.Applied.PlanHash,
			WorkloadRef: request.WorkloadRef, RequirementID: target.RequirementID,
			InstanceRef: target.InstanceRef, RuntimeOwnerRef: runtimeOwnerRef(target),
			ArtifactDigest: appliedArtifactDigest(request.Applied, target), SiteRef: target.SiteRefs[0],
			NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
			RequestedAt: request.RequestedAt, ValidUntil: request.ValidUntil,
			Authorization: request.Authorization, RequestDigest: request.RequestDigest,
		},
		Result: result,
	}
	digest, err := evidenceHash(evidence)
	if err != nil {
		return Evidence{}, err
	}
	evidence.EvidenceDigest = digest
	return evidence, evidence.Validate()
}

// Validate verifies canonical integrity and internal consistency. Authenticity
// still comes from the pinned producer channel and previously established
// Owner custody; EvidenceDigest is not a signature or a new trust root.
func (evidence Evidence) Validate() error {
	authority := evidence.Authority
	if evidence.APIVersion != EvidenceAPIVersion || !validDigest(evidence.EvidenceDigest) ||
		!validDigest(authority.AppliedRequestDigest) || !validDigest(authority.PlanHash) ||
		!validDigest(authority.ArtifactDigest) || !validDigest(authority.RequestDigest) {
		return errors.New("workload removal evidence identity is invalid")
	}
	for _, value := range []string{
		authority.RequirementID, authority.InstanceRef, authority.RuntimeOwnerRef,
		authority.SiteRef, authority.NodeRef, authority.ExecutionChannelRef,
		authority.Authorization.OwnerRef, authority.Authorization.KeyID,
	} {
		if value == "" || value != strings.TrimSpace(value) {
			return errors.New("workload removal evidence authority is incomplete")
		}
	}
	if authority.WorkloadRef == "" || authority.WorkloadRef != strings.ToLower(authority.WorkloadRef) ||
		authority.WorkloadRef != strings.TrimSpace(authority.WorkloadRef) {
		return errors.New("workload removal evidence workload ref is not canonical")
	}
	signature, err := base64.RawStdEncoding.DecodeString(authority.Authorization.Value)
	if err != nil || len(signature) != 64 {
		return errors.New("workload removal evidence Owner authorization signature is malformed")
	}
	if err := validateResult(evidence.Result, authority); err != nil {
		return err
	}
	digest, err := evidenceHash(evidence)
	if err != nil {
		return err
	}
	if digest != evidence.EvidenceDigest {
		return errors.New("workload removal evidence digest does not match")
	}
	return nil
}

// ParseEvidence accepts only canonical, complete terminal evidence.
func ParseEvidence(data []byte) (Evidence, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var evidence Evidence
	if err := decoder.Decode(&evidence); err != nil {
		return Evidence{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Evidence{}, errors.New("workload removal evidence contains trailing JSON")
	}
	if err := evidence.Validate(); err != nil {
		return Evidence{}, err
	}
	canonical, err := resolvedplan.CanonicalJSON(evidence)
	if err != nil || !bytes.Equal(canonical, data) {
		return Evidence{}, errors.New("workload removal evidence is not canonical JSON")
	}
	return evidence, nil
}

func (evidence Evidence) Canonical() ([]byte, error) {
	if !validDigest(evidence.EvidenceDigest) {
		return nil, errors.New("workload removal evidence is unsealed")
	}
	return resolvedplan.CanonicalJSON(evidence)
}

// AuthorizationBytes returns the original Owner-signed authorization payload.
// A consumer with a separately pinned Owner public key can authenticate it
// without receiving or reconstructing the complete local Request.
func (evidence Evidence) AuthorizationBytes() ([]byte, error) {
	if err := evidence.Validate(); err != nil {
		return nil, err
	}
	authority := evidence.Authority
	return resolvedplan.CanonicalJSON(AuthorizationPayload{
		APIVersion: APIVersion, AppliedRequestDigest: authority.AppliedRequestDigest,
		PlanHash: authority.PlanHash, WorkloadRef: authority.WorkloadRef,
		RequirementID: authority.RequirementID, InstanceRef: authority.InstanceRef,
		RequestedAt: authority.RequestedAt, ValidUntil: authority.ValidUntil,
	})
}

func validateResult(result Result, authority EvidenceAuthority) error {
	if result.APIVersion != ResultAPIVersion || result.RequestDigest != authority.RequestDigest || !validDigest(result.ResultDigest) {
		return errors.New("workload removal result identity is invalid")
	}
	removedAt, err := time.Parse(time.RFC3339Nano, result.RemovedAt)
	if err != nil || removedAt.Location() != time.UTC || removedAt.Format(time.RFC3339Nano) != result.RemovedAt {
		return errors.New("workload removal result time is invalid")
	}
	requestedAt, validUntil, err := parseValidity(authority.RequestedAt, authority.ValidUntil)
	if err != nil || removedAt.Before(requestedAt) || !removedAt.Before(validUntil) {
		return errors.New("workload removal result is outside the fresh Owner authorization window")
	}
	if result.Outcome.RequirementID != authority.RequirementID || result.Outcome.WorkloadRef != authority.WorkloadRef ||
		result.Outcome.InstanceRef != authority.InstanceRef || result.Outcome.RuntimeOwnerRef != authority.RuntimeOwnerRef ||
		result.Outcome.ArtifactDigest != authority.ArtifactDigest || result.Outcome.Status != StatusRemoved ||
		result.Outcome.ObservedState != "absent" || !strings.HasPrefix(result.Outcome.ObservationRef, "removal-observation://") ||
		!validDigest(result.Outcome.ObservationDigest) {
		return errors.New("workload removal result is not exact absence evidence")
	}
	digest, err := resultHash(result)
	if err != nil {
		return err
	}
	if digest != result.ResultDigest {
		return errors.New("workload removal result digest does not match")
	}
	return nil
}

func authorizationPayload(applied runtimeexecutor.ExecutionRequest, workloadRef string, requestedAt, validUntil time.Time) (AuthorizationPayload, error) {
	if err := applied.Validate(); err != nil {
		return AuthorizationPayload{}, fmt.Errorf("validate selected applied workload: %w", err)
	}
	if len(applied.RuntimeTargets) != 1 || applied.RuntimeTargets[0].WorkloadRef != workloadRef {
		return AuthorizationPayload{}, errors.New("Owner authorization must bind exactly one selected workload")
	}
	if requestedAt.IsZero() || validUntil.IsZero() || requestedAt.Location() != time.UTC || validUntil.Location() != time.UTC ||
		!requestedAt.Before(validUntil) || validUntil.Sub(requestedAt) > maxValidity {
		return AuthorizationPayload{}, errors.New("workload removal authorization validity must be positive and at most five minutes")
	}
	target := applied.RuntimeTargets[0]
	return AuthorizationPayload{
		APIVersion: APIVersion, AppliedRequestDigest: applied.RequestDigest, PlanHash: applied.PlanHash,
		WorkloadRef: workloadRef, RequirementID: target.RequirementID, InstanceRef: target.InstanceRef,
		RequestedAt: requestedAt.Format(time.RFC3339Nano), ValidUntil: validUntil.Format(time.RFC3339Nano),
	}, nil
}

func parseValidity(requested, valid string) (time.Time, time.Time, error) {
	requestedAt, err := time.Parse(time.RFC3339Nano, requested)
	if err != nil || requestedAt.Location() != time.UTC || requestedAt.Format(time.RFC3339Nano) != requested {
		return time.Time{}, time.Time{}, errors.New("workload removal requestedAt is not canonical UTC")
	}
	validUntil, err := time.Parse(time.RFC3339Nano, valid)
	if err != nil || validUntil.Location() != time.UTC || validUntil.Format(time.RFC3339Nano) != valid ||
		!requestedAt.Before(validUntil) || validUntil.Sub(requestedAt) > maxValidity {
		return time.Time{}, time.Time{}, errors.New("workload removal validUntil is invalid")
	}
	return requestedAt, validUntil, nil
}

func requestHash(request Request) (string, error) {
	request.RequestDigest = ""
	canonical, err := resolvedplan.CanonicalJSON(request)
	if err != nil {
		return "", err
	}
	return hash(canonical), nil
}

func resultHash(result Result) (string, error) {
	result.ResultDigest = ""
	canonical, err := resolvedplan.CanonicalJSON(result)
	if err != nil {
		return "", err
	}
	return hash(canonical), nil
}

func evidenceHash(evidence Evidence) (string, error) {
	evidence.EvidenceDigest = ""
	canonical, err := resolvedplan.CanonicalJSON(evidence)
	if err != nil {
		return "", err
	}
	return hash(canonical), nil
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func runtimeOwnerRef(target runtimeexecutor.RuntimeTarget) string {
	if target.RuntimeAdapter != nil {
		return target.RuntimeAdapter.ID
	}
	return target.OwnerRef
}

func appliedArtifactDigest(request runtimeexecutor.ExecutionRequest, target runtimeexecutor.RuntimeTarget) string {
	if len(target.ArtifactRefs) != 1 {
		return ""
	}
	for _, artifact := range request.Artifacts {
		if artifact.ID == target.ArtifactRefs[0] {
			return artifact.Digest
		}
	}
	return ""
}

func selectAccessBindings(values []runtimeexecutor.AccessBinding, refs []string) []runtimeexecutor.AccessBinding {
	allowed := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		allowed[ref] = struct{}{}
	}
	result := make([]runtimeexecutor.AccessBinding, 0, len(refs))
	for _, value := range values {
		if _, ok := allowed[value.ID]; ok {
			result = append(result, value)
		}
	}
	return result
}

func selectBackupBindings(values []runtimeexecutor.BackupTargetBinding, refs []string) []runtimeexecutor.BackupTargetBinding {
	allowed := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		allowed[ref] = struct{}{}
	}
	result := make([]runtimeexecutor.BackupTargetBinding, 0, len(refs))
	for _, value := range values {
		if _, ok := allowed[value.ID]; ok {
			result = append(result, value)
		}
	}
	return result
}
