# runtimelease

Secret-free contract for the Runtime Lease Authority. Techstack owns the durable
lease state; this package defines the wire and local validation. The authority
owns lease identity and validity window, caller-owned desired state (`running`,
`stopped`, `absent`), cancellation and a monotonic non-zero `revision` for CAS.
Provider execution, observations, cleanup, server lifecycle, Guard, billing,
catalog policy and access custody are separate authorities.

## Identity

- `server_id`: one durable RuntimeServer, unchanged when infrastructure is replaced.
- `resource_generation_id`: one replaceable infrastructure generation; non-nil
  canonical lowercase UUID.
- `tenant_id` and `owner_id` are explicit. Provider/adapter names, native
  handles, simulation or VM identifiers, provisioning specs, observed state,
  metadata, credentials and access material are absent.

Validation and enrollment fence both identities plus the exact lease revision.

## Wire

No `vmlease` compatibility package. Validation requests carry `tenant_id`,
`owner_id`, `lease_id`, `lease_revision`, `server_id`, `resource_generation_id`.
Closed failure reasons: `lease_cancelled`, `lease_not_yet_valid`,
`lease_expired`, `binding_mismatch`, `revision_mismatch`, `invalid_lease`.
Enrollment requests and acknowledgements repeat the fence; the enrollment
idempotency key is not a provider idempotency key.

## Snapshots

Signed with the authority's Ed25519 key; the wire fixes
`runtimelease-snapshot/v1`, `Ed25519` and a signed `key_id`. Consumers hold only
a rotation-capable public-key set. `expires_at` is at most five minutes after
issuance; optional authority-signed `grace_until` (max five minutes) is accepted
only by `VerifySnapshotFallback` when `FallbackEligible` classifies the failure
as an outage. An explicit invalid result from the authority never falls back to
a cached snapshot. Consumers verify against a public-key resolver, issuer,
audience, owner, revision, server and generation. HTTP transport and routing
belong to the owning runtime.

The package has no provider SDK, database, durable claim, adapter configuration,
private module or product-policy dependency.
