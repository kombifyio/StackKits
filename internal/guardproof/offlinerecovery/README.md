# Offline recovery producer

`internal/guardproof/offlinerecovery` is the bounded Guard H source slice. It admits and executes one already-authorized local
recovery recipe from cached artifacts while the node is offline.

## Contract

1. A `Plan` names one operation, the current node custody, a bounded sorted
   artifact set, and ordered `materialize`, `restart`, or `verify` steps.
2. An `Authority` signs the exact plan digest and `Plan.ArtifactSetDigest()` through
   the existing local Owner policy-state custody. Its lifetime is at most five
   minutes. A plan without that signature is not authority.
3. `Run` re-reads local Owner custody, verifies the authority, checks the local
   revocation projection and continuing wall/monotonic clock, and verifies every
   cache file's exact SHA-256 digest and size before any effect.
4. A pending receipt is durably journaled before the first effect. Every later
   step and terminal result extends the same hash chain. A retry returns the
   stored receipt; pending or unknown outcomes are never reexecuted. A denial
   before admission (malformed, unsigned or mismatched authority, expiry,
   revocation, artifact or executor refusal) is returned to the caller and never
   journaled, so an unauthenticated call cannot occupy an operation ID.
5. `LoadReceipt` reads durable evidence without the original authority or cache,
   so reconnection reconciliation does not run the recipe again.

## Standard Mode boundary

The package has no account client, HTTP transport, control-plane client,
provider client, shell, command, URL, or generic executor. Artifact
materialization is confined to the operation-owned workspace directory. The
only external seam is `Executor`, which accepts typed local restart/verify
steps and must identify the same node, Standard Mode, a digest-pinned build,
and disabled network/shell access.

This is a producer source slice, not a standalone Advanced executor or a claim
that the live Guard H test window has passed.

## Receipt

`stackkit.offline-recovery-receipt/v1` records exact artifact digests and sizes,
plan and authority versions, authority and execution timing, cache evidence age,
the complete hash chain, explicit `unknowns`, and explicit `not_claimed` bounds.
`Receipt.Verify` recomputes the binding, genesis, every link, verdict, timing,
and mandatory bounds. A recovered receipt still requires reconnection
reconciliation; it never claims WAN or control-plane availability.
