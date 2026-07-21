// Package stackspecintent owns the only first-party persistence contract for
// canonical StackSpec v2 intent.
package stackspecintent

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

type Outcome string

const (
	OutcomeCreated        Outcome = "created"
	OutcomeAlreadyApplied Outcome = "already-applied"
	OutcomeReplaced       Outcome = "replaced"
)

type ErrorCode string

const (
	ErrInvalidCandidate ErrorCode = "invalid-candidate"
	ErrInvalidExpected  ErrorCode = "invalid-expected-hash"
	ErrCASRequired      ErrorCode = "compare-and-swap-required"
	ErrCASConflict      ErrorCode = "compare-and-swap-conflict"
	ErrMissingTarget    ErrorCode = "compare-and-swap-missing-target"
	ErrInvalidCurrent   ErrorCode = "invalid-current-intent"
)

type Error struct {
	Code              ErrorCode
	CurrentSpecHash   string
	CandidateSpecHash string
	Cause             error
}

func (e *Error) Error() string {
	if e == nil {
		return "StackSpec intent persistence failed"
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type Request struct {
	WorkspaceRoot    string
	SpecPath         string
	Candidate        []byte
	ExpectedSpecHash string
	BuildVersion     string
}

type Result struct {
	Outcome          Outcome
	KitProfile       stackspecmigration.KitProfile
	SpecHash         string
	PreviousSpecHash string
	Canonical        []byte
}

// Persist validates both candidate and current intent through the same
// embedded CUE authority, then performs a non-blocking exact-hash CAS beneath
// one held workspace root. It never accepts v1 as current or candidate intent.
func Persist(request Request) (result Result, returnErr error) {
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(request.BuildVersion))
	if err != nil {
		return result, fmt.Errorf("load embedded StackSpec v2 authority: %w", err)
	}
	candidate, err := service.ValidateStackSpec(request.Candidate)
	if err != nil {
		return result, &Error{Code: ErrInvalidCandidate, Cause: fmt.Errorf("validate candidate StackSpec v2: %w", err)}
	}
	result.KitProfile = candidate.KitProfile
	result.SpecHash = candidate.SpecHash
	result.Canonical = append([]byte(nil), candidate.CanonicalStackSpec...)

	expected := strings.TrimSpace(request.ExpectedSpecHash)
	if expected != "" && !IsCanonicalSHA256(expected) {
		return result, &Error{
			Code: ErrInvalidExpected, CandidateSpecHash: result.SpecHash,
			Cause: fmt.Errorf("expected_spec_hash must be lowercase sha256:<64-hex>"),
		}
	}

	absoluteRoot, err := filepath.Abs(request.WorkspaceRoot)
	if err != nil {
		return result, fmt.Errorf("resolve workspace root: %w", err)
	}
	absoluteSpec := request.SpecPath
	if !filepath.IsAbs(absoluteSpec) {
		absoluteSpec = filepath.Join(absoluteRoot, absoluteSpec)
	}
	absoluteSpec, err = filepath.Abs(absoluteSpec)
	if err != nil {
		return result, fmt.Errorf("resolve StackSpec path: %w", err)
	}
	relative, err := filepath.Rel(absoluteRoot, absoluteSpec)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return result, fmt.Errorf("StackSpec path must stay beneath the workspace: %s", request.SpecPath)
	}
	portable := filepath.ToSlash(relative)

	root, err := confinedfs.Open(absoluteRoot)
	if err != nil {
		return result, err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return result, err
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()

	lock, err := transaction.TryAcquireOutputLock(filepath.ToSlash(filepath.Join("intent-authoring", portable)))
	if err != nil {
		return result, fmt.Errorf("acquire non-blocking StackSpec intent lock: %w", err)
	}
	lockClosed := false
	defer func() {
		if !lockClosed {
			returnErr = errors.Join(returnErr, lock.Close())
		}
	}()

	parent := filepath.ToSlash(filepath.Dir(relative))
	parentExists := true
	if parent != "." {
		parentExists, _, err = transaction.Exists(parent)
		if err != nil {
			return result, err
		}
	}
	if !parentExists && expected != "" {
		return result, &Error{
			Code: ErrMissingTarget, CandidateSpecHash: result.SpecHash,
			Cause: fmt.Errorf("expected_spec_hash was supplied but no current intent exists"),
		}
	}
	if !parentExists {
		if err := transaction.MkdirAll(parent, 0o750); err != nil {
			return result, err
		}
	}

	view, err := root.View(".")
	if err != nil {
		return result, err
	}
	exists, info, err := transaction.Exists(portable)
	if err != nil {
		return result, err
	}
	if !exists {
		if expected != "" {
			return result, &Error{
				Code: ErrMissingTarget, CandidateSpecHash: result.SpecHash,
				Cause: fmt.Errorf("expected_spec_hash was supplied but no current intent exists"),
			}
		}
		if _, err := view.WriteAtomic0600NoReplace(portable, result.Canonical); err != nil {
			return result, fmt.Errorf("create canonical StackSpec v2 intent: %w", err)
		}
		result.Outcome = OutcomeCreated
	} else {
		if !info.Mode().IsRegular() {
			return result, &Error{Code: ErrInvalidCurrent, CandidateSpecHash: result.SpecHash, Cause: fmt.Errorf("existing intent target is not a regular file")}
		}
		currentRaw, _, err := transaction.ReadStable(portable)
		if err != nil {
			return result, fmt.Errorf("read stable current StackSpec intent: %w", err)
		}
		current, err := service.ValidateStackSpec(currentRaw)
		if err != nil {
			return result, &Error{
				Code: ErrInvalidCurrent, CandidateSpecHash: result.SpecHash,
				Cause: fmt.Errorf("existing intent is not CUE-valid StackSpec v2; migrate or repair it explicitly: %w", err),
			}
		}
		result.PreviousSpecHash = current.SpecHash
		switch {
		case current.SpecHash == result.SpecHash:
			result.Outcome = OutcomeAlreadyApplied
		case expected == "":
			return result, &Error{
				Code: ErrCASRequired, CurrentSpecHash: current.SpecHash, CandidateSpecHash: result.SpecHash,
				Cause: fmt.Errorf("expected_spec_hash is required to replace existing StackSpec v2 intent"),
			}
		case expected != current.SpecHash:
			return result, &Error{
				Code: ErrCASConflict, CurrentSpecHash: current.SpecHash, CandidateSpecHash: result.SpecHash,
				Cause: fmt.Errorf("expected_spec_hash does not match current CUE-normalized intent"),
			}
		default:
			if _, err := view.WriteAtomic0600(portable, result.Canonical); err != nil {
				return result, fmt.Errorf("replace canonical StackSpec v2 intent under CAS lock: %w", err)
			}
			result.Outcome = OutcomeReplaced
		}
	}

	if result.Outcome != OutcomeAlreadyApplied {
		installed, _, err := transaction.ReadStable(portable)
		if err != nil {
			return result, fmt.Errorf("re-read installed canonical StackSpec v2 intent: %w", err)
		}
		if !bytes.Equal(installed, result.Canonical) {
			return result, fmt.Errorf("re-read installed canonical StackSpec v2 intent: bytes differ from validated candidate")
		}
	}
	if err := lock.Close(); err != nil {
		return result, fmt.Errorf("release StackSpec intent lock: %w", err)
	}
	lockClosed = true
	return result, nil
}

func IsCanonicalSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
