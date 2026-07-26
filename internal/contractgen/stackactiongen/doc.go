// Package stackactiongen generates StackAction Go and OpenAPI projections from
// the canonical StackKits CUE authority.
//
// Authority: base/stack_action.cue owns the contract. This package owns only
// deterministic projection mechanics and drift detection.
//
// Non-authority: the generator does not admit or execute actions and does not
// own authentication, lifecycle, provider, or rollout policy.
//
// Side effects: Run writes generated files unless Options.Check is true. Check
// mode is read-only and fails when any requested projection is stale.
//
// Persistence: generated source files are build artifacts committed beside
// their consumers; the generator has no database or runtime state.
//
// Concurrency: Run provides no synchronization. Do not run multiple writers
// against the same outputs concurrently.
//
// Secrets: the generator processes schema metadata only and must never receive
// runtime credentials or secret values.
package stackactiongen
