// Package stackaction contains the generated Go projection of StackKits'
// canonical CUE StackAction contract.
//
// # Authority
//
// foundation/stack_action.cue owns wire fields, vocabularies, paths, and
// validation constraints. This package is generated output and must not be
// edited as an independent contract.
//
// # Non-authority
//
// Callers own authentication, admission, orchestration,
// persistence, concurrency, and action execution. Importing this package does
// not grant action authority.
//
// # Side effects
//
// Helpers in this package perform no I/O. Action handlers live in internal/api.
//
// # Persistence
//
// This package has no persistence. Callers must durably record
// admitted operations before performing side effects.
//
// # Concurrency
//
// Values contain mutable maps and slices and provide no locking.
// Callers must copy or synchronize values shared between goroutines.
//
// # Secrets
//
// Public values contain only opaque, versioned, scope- and
// expiry-bound references. Raw SSH, enrollment, onboarding, and backup
// credentials are forbidden from this package and from action evidence.
package stackaction
