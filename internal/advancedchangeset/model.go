// Package advancedchangeset owns the immutable, owner-signed description of
// one already-rendered advanced change. It deliberately does not resolve,
// render, execute, or discover anything.
package advancedchangeset

import (
	"errors"
	"fmt"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

const (
	SchemaVersion = "stackkit.advanced-change-set/v1"
	MaxLifetime   = 24 * time.Hour
)

const (
	StatusAdded    = "added"
	StatusModified = "modified"
	StatusRemoved  = "removed"
)

// OwnerSignature is supplied by the local owner-custody composition seam. The
// private key and signing implementation never enter this package.
type OwnerSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

// ArtifactChange is a deterministic path-keyed transition. Equal hashes with
// MetadataChanged=true represent a governed metadata-only modification.
type ArtifactChange struct {
	Path            string `json:"path"`
	Status          string `json:"status"`
	BeforeSHA256    string `json:"beforeSha256,omitempty"`
	AfterSHA256     string `json:"afterSha256,omitempty"`
	MetadataChanged bool   `json:"metadataChanged,omitempty"`
}

// Record is content-addressed by all unsigned claims except ChangeSetID
// itself. OwnerSignature then authenticates the complete unsigned record,
// including that derived ID.
type Record struct {
	SchemaVersion         string           `json:"schemaVersion"`
	ChangeSetID           string           `json:"changeSetId"`
	CapabilityID          string           `json:"capabilityId"`
	CapabilitySHA256      string           `json:"capabilitySha256"`
	KeyID                 string           `json:"keyId"`
	StackID               string           `json:"stackId"`
	OwnerRef              string           `json:"ownerRef"`
	UIManagerRef          string           `json:"uiManagerRef"`
	RILRef                string           `json:"rilRef"`
	GenerationTarget      string           `json:"generationTarget"`
	CreatedAt             string           `json:"createdAt"`
	ExpiresAt             string           `json:"expiresAt"`
	CapabilityExpiresAt   string           `json:"capabilityExpiresAt"`
	BaselinePlanHash      string           `json:"baselinePlanHash"`
	CandidatePlanHash     string           `json:"candidatePlanHash"`
	BaselineRenderSHA256  string           `json:"baselineRenderSha256"`
	CandidateRenderSHA256 string           `json:"candidateRenderSha256"`
	Changes               []ArtifactChange `json:"changes"`
	OwnerSignature        OwnerSignature   `json:"ownerSignature"`
}

// OwnerSigner signs the canonical unsigned bytes using current local custody.
type OwnerSigner func(canonicalUnsigned []byte) (OwnerSignature, error)

// OwnerVerifier verifies an owner signature and its binding to current local
// custody. Implementations are injected by the composition root.
type OwnerVerifier func(canonicalUnsigned []byte, signature OwnerSignature) error

type CreateRequest struct {
	Baseline             architecturev2renderer.RenderResult
	Candidate            architecturev2renderer.RenderResult
	CapabilityID         string
	CapabilitySHA256     string
	KeyID                string
	StackID              string
	OwnerRef             string
	UIManagerRef         string
	RILRef               string
	BaselinePlanHash     string
	CandidatePlanHash    string
	CreatedAt            time.Time
	ExpiresAt            time.Time
	CapabilityExpiresAt  time.Time
	Sign                 OwnerSigner
	VerifyOwnerSignature OwnerVerifier
}

type VerificationRequest struct {
	Now                  time.Time
	CapabilityID         string
	CapabilitySHA256     string
	KeyID                string
	StackID              string
	OwnerRef             string
	UIManagerRef         string
	RILRef               string
	BaselinePlanHash     string
	CandidatePlanHash    string
	CapabilityExpiresAt  time.Time
	VerifyOwnerSignature OwnerVerifier
}

type ErrorCode string

const (
	ErrInvalid ErrorCode = "advanced_change_set_invalid"
	ErrStale   ErrorCode = "advanced_change_set_stale"
	ErrIO      ErrorCode = "advanced_change_set_io"
)

type Error struct {
	Code   ErrorCode
	Field  string
	Detail string
	Err    error
}

func (e *Error) Error() string {
	field := ""
	if e.Field != "" {
		field = ": " + e.Field
	}
	if e.Err != nil {
		return fmt.Sprintf("%s%s: %s: %v", e.Code, field, e.Detail, e.Err)
	}
	return fmt.Sprintf("%s%s: %s", e.Code, field, e.Detail)
}

func (e *Error) Unwrap() error { return e.Err }

func Reason(err error) (ErrorCode, bool) {
	var typed *Error
	if !errors.As(err, &typed) {
		return "", false
	}
	return typed.Code, true
}

func fail(code ErrorCode, field, detail string) error {
	return &Error{Code: code, Field: field, Detail: detail}
}

func wrap(code ErrorCode, field, detail string, err error) error {
	return &Error{Code: code, Field: field, Detail: detail, Err: err}
}
