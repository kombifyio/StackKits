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
	APIVersion       = "stackkit.workload-removal/v1"
	ResultAPIVersion = "stackkit.workload-removal-result/v1"
	StatusRemoved    = "removed"
	maxValidity      = 5 * time.Minute
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
	if result.APIVersion != ResultAPIVersion || result.RequestDigest != request.RequestDigest || !validDigest(result.ResultDigest) {
		return errors.New("workload removal result identity is invalid")
	}
	removedAt, err := time.Parse(time.RFC3339Nano, result.RemovedAt)
	if err != nil || removedAt.Location() != time.UTC || removedAt.Format(time.RFC3339Nano) != result.RemovedAt {
		return errors.New("workload removal result time is invalid")
	}
	requestedAt, validUntil, err := parseValidity(request.RequestedAt, request.ValidUntil)
	if err != nil || removedAt.Before(requestedAt) || !removedAt.Before(validUntil) {
		return errors.New("workload removal result is outside the fresh Owner authorization window")
	}
	target := request.Applied.RuntimeTargets[0]
	if result.Outcome.RequirementID != target.RequirementID || result.Outcome.WorkloadRef != request.WorkloadRef ||
		result.Outcome.InstanceRef != target.InstanceRef ||
		result.Outcome.RuntimeOwnerRef != runtimeOwnerRef(target) ||
		result.Outcome.ArtifactDigest != appliedArtifactDigest(request.Applied, target) ||
		result.Outcome.Status != StatusRemoved || result.Outcome.ObservedState != "absent" ||
		!strings.HasPrefix(result.Outcome.ObservationRef, "removal-observation://") || !validDigest(result.Outcome.ObservationDigest) {
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
