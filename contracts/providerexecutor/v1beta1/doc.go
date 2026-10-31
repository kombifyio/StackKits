// Package providerexecutor defines the provider-neutral execution contract for
// infrastructure lifecycle operations.
//
// # Authority
//
// The providerexecutor/v1beta1 wire format is authoritative for canonical
// digests, envelope validation, resource-graph invariants, and lifecycle
// transitions. The owning control plane, Techstack, authorizes commands and is authoritative
// for the operation ledger and its projections.
//
// # Non-authority
//
// The package does not select adapters, define provider capabilities, resolve
// credentials, own desired state, or decide product and billing policy.
// Executor is a seam for a control-plane-owned adapter, not a transfer of
// lifecycle or ledger authority to this package or to a conformance harness.
//
// # Side effects
//
// Validation, hashing, receipt assembly, and transition helpers perform no I/O.
// An injected EvidenceVerifier may perform bounded verification and must honor
// its context. An Executor implementation may perform provider side effects;
// the caller must persist the command and initial receipt before invoking it.
//
// # Persistence
//
// This package has no database, cache, or durable storage.
//
// # Concurrency
//
// Commands and receipts are immutable envelopes. Lease revision, runtime
// server, resource generation, provider, and capability snapshot fence every
// entry. The package provides no synchronization: the owning control plane
// must serialize or CAS ledger-head advancement and reject stale results.
//
// # Secrets
//
// Contract values must contain opaque lookup references and hashes only. Raw
// credentials, private keys, human diagnostics, provider responses, and
// secret-bearing desired specs must not be placed in commands, receipts,
// reasons, or evidence. Reasons contain only a machine-readable code and
// retryability; the provider.* namespace is closed by this contract.
package providerexecutor
