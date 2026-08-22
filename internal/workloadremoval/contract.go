// Package workloadremoval preserves the pre-public internal import path.
//
// Deprecated: import github.com/kombifyio/stackkits/pkg/workloadremoval. Every
// type and operation below delegates to that single public authority.
package workloadremoval

import (
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
	public "github.com/kombifyio/stackkits/pkg/workloadremoval"
)

const (
	APIVersion         = public.APIVersion
	ResultAPIVersion   = public.ResultAPIVersion
	EvidenceAPIVersion = public.EvidenceAPIVersion
	StatusRemoved      = public.StatusRemoved
)

type OwnerAuthorization = public.OwnerAuthorization
type AuthorizationPayload = public.AuthorizationPayload
type Request = public.Request
type Outcome = public.Outcome
type Result = public.Result
type EvidenceAuthority = public.EvidenceAuthority
type Evidence = public.Evidence
type AppliedPlacement = public.AppliedPlacement

func SelectAppliedWorkload(applied runtimeexecutor.ExecutionRequest, workloadRef string) (runtimeexecutor.ExecutionRequest, error) {
	return public.SelectAppliedWorkload(applied, workloadRef)
}

func SelectAppliedWorkloadPlacement(applied runtimeexecutor.ExecutionRequest, placement AppliedPlacement) (runtimeexecutor.ExecutionRequest, error) {
	return public.SelectAppliedWorkloadPlacement(applied, placement)
}

func AuthorizationBytes(applied runtimeexecutor.ExecutionRequest, workloadRef string, requestedAt, validUntil time.Time) ([]byte, error) {
	return public.AuthorizationBytes(applied, workloadRef, requestedAt, validUntil)
}

func SealRequest(applied runtimeexecutor.ExecutionRequest, workloadRef string, requestedAt, validUntil time.Time, authorization OwnerAuthorization) (Request, error) {
	return public.SealRequest(applied, workloadRef, requestedAt, validUntil, authorization)
}

func NewResult(request Request, removedAt time.Time, outcome Outcome) (Result, error) {
	return public.NewResult(request, removedAt, outcome)
}

func ParseResult(data []byte, request Request) (Result, error) {
	return public.ParseResult(data, request)
}

func NewEvidence(request Request, result Result) (Evidence, error) {
	return public.NewEvidence(request, result)
}

func ParseEvidence(data []byte) (Evidence, error) {
	return public.ParseEvidence(data)
}
