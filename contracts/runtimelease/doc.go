// Package runtimelease defines the shared runtime-lease wire contract.
//
// # Authority
//
// The runtimelease wire is authoritative for stable JSON field names,
// snapshot signatures, and local validation rules. The owning runtime control
// plane, Techstack, is authoritative for lease validity,
// desired state, cancellation, revision issuance, enrollment admission, and
// durable persistence.
//
// # Non-authority
//
// The package does not own provider execution or provider state, adapter
// selection, provider-native resource identities or handles, cleanup evidence,
// server lifecycle, health, connection, inventory, billing, catalog, or access
// policy. A lease's desired state is input to separate reconciliation; it is
// not an observed provider or Guard state.
//
// # Side effects
//
// Lease, enrollment, and snapshot validation are in-memory operations. The
// package performs no network I/O.
//
// # Persistence
//
// This package persists no leases, snapshots, claims, or enrollment state.
//
// # Concurrency
//
// Values carry no synchronization or compare-and-swap implementation. Every
// durable mutation must use the exact non-zero Lease.Revision issued by the
// owning authority; stale revisions fail rather than merging projections.
//
// # Secrets
//
// Lease and enrollment fields are closed and contain no provider references,
// raw credentials, access material, or caller-defined metadata. Snapshot
// Ed25519 private signing keys remain inside the lease authority and must never
// be passed to consumers, persisted with a payload, or logged. Consumers hold
// only public verification keys selected by the signed key ID.
package runtimelease
