// Package runtimeinventory defines the dependency-free, secret-free Go wire
// model for reading Kombify runtime inventory.
//
// # Authority
//
// The canonical Inventory schema in Techstack owns this model. This package
// owns its generated Go type identity and stable JSON field names.
//
// # Non-authority
//
// This package does not own server identity, lifecycle transitions, desired
// state, lease state, provider operations, cleanup evidence, freshness policy,
// authorization, pagination behavior, or access-link derivation. Techstack is
// the production authority for those decisions and projections.
//
// # Side effects
//
// The package contains data transfer objects only. It performs no I/O and has
// no side effects.
//
// # Persistence
//
// The package does not persist inventory or cursor state.
//
// # Concurrency
//
// Values contain no synchronization. Treat DTOs as immutable after publication
// and do not concurrently mutate shared slices or pointed-to timestamps.
//
// # Secrets
//
// These DTOs intentionally expose no metadata bags, credential references,
// provider credentials, tokens, SSH material, route IDs, endpoint references,
// commands, or logs. Producers must sanitize all strings and access URLs before
// constructing a value.
package runtimeinventory
