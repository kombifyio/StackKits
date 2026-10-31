# providerexecutor

Neutral `providerexecutor/v1beta1` contract for infrastructure-provider
lifecycle execution: the only provider-control wire, not a service contract. No
provider SDK, database, adapter registry or vendor configuration.

## Ownership

- **Techstack**: authorizes commands, selects adapters, owns leases, persists
  the append-only lifecycle and resource ledger, keeps cleanup handles until
  provider-native absence is proven.
- **This package**: the `Executor` interface, command/receipt wire types,
  idempotency hashes, resource graph, evidence contract and transition rules.
  A conformance harness may implement it; it is no required network hop.
- **StackKits** imports no provider adapters; it receives an external host
  binding and reports OS/host conformance separately.

## Safety invariants

- **References, never credentials**: commands carry opaque server-side lookup
  refs (`custody://`, `provider-connection://`, `desired-spec://`). They bind the
  positive `lease_revision`, `runtime_server_id`, lowercase `resource_generation_id`
  UUID, `provider_id`, `adapter_id`, capability snapshot, execution profile,
  custody, connection, resource graph and positive ledger revision. Receipts and
  evidence repeat the fences; digests, replay validation and receipt identity
  reject drift. `SealCommand` never invents a generation, and operation identity
  includes it.
- **Idempotency**: the same key within tenant, lease, generation and operation
  scope with different content fails closed.
- **Graphs**: closed, parent-complete, acyclic, with ownership and delete
  disposition. Bounds: 128 targets, 256 resources, 32 evidence entries per
  resource, 512 total, checked before normalizing, copying or marshaling.
  Revisions and sequences are positive and capped at JSON's safe integer.
  `SealCommand` derives `resource_graph_hash` from the normalized targets.
- **Receipts**: plan receipts are resource-free. Failed mutating operations may
  keep partial graphs so native refs stay available for cleanup. Denied receipts
  are resource-free, appended only from the durable `requested` head via
  `DenyReceipt` before invocation; executors cannot return `denied`; once
  `accepted`, even an ambiguous result is `failed` and reconciled.
- **Coverage**: successful observe and decommission receipts cover exactly their
  targets; successful reconcile retains exactly the target graph.
- **Absence** requires unique, definitive, attested provider API/event evidence
  bound to operation, lease revision, server, provider, capability snapshot,
  binding, native ref, connection, profile, graph and subject. Collection time
  lies between command request and receipt issue and `EvidenceVerifier` must
  accept it (honors cancellation; typed-nil fails closed). Adapter-local
  assertions never complete cleanup.
- **Chain**: Techstack's coordinator creates the sole initial receipt
  (`InitialReceipt`: sequence 1, `pending/requested`, no predecessor or
  resources) and persists it before invoking an executor. `ExecutionRequest`
  carries the immutable command and validated prior receipt; the executor
  returns an unchained `ExecutionResult` (one next transition; failures carry a
  secret-free `{code,retryable}` reason and partial bindings, diagnostics stay
  in Techstack logs). The coordinator calls `AssembleReceipt` with its durable
  head. Receipts hash-chain through `Sequence` and `PreviousReceiptDigest`;
  `phase_entered_at` is set on transition and preserved on same-phase polling.
  `ValidateAppend` enforces exact sequence increments, monotone issue time,
  legal phases and resource/evidence continuity. Pending `accepted`,
  `resources_bound`, `delete_accepted` and `absence_pending` may append safe
  polling receipts; `requested` and terminal phases may not self-loop.
- **Failure codes**: the `provider.*` namespace is closed to auth, permission,
  invalid spec, quota, rate limit, conflict, not found, transient, timeout,
  partial create and cleanup required.
- **Evidence** is append-only per resource; older envelopes keep their own
  observation. The newest definitive evidence decides the current observation
  (equal times with conflicting observations are rejected); current absence
  needs new definitive proof; successful observe needs definitive evidence.
- **`PhaseAbsent`** is operation-specific: observe uses it for "every target is
  absent"; decommission reaches it only via `delete_accepted -> absence_pending
  -> absent` with exact coverage, fresh post-`absence_pending` evidence and
  complete cleanup. Terminal absence evidence must be collected at or after the
  preceding `absence_pending` began.

## Validation entry points

`SealCommand` before execution, persist and execute only its canonical result;
`InitialReceipt`, `AssembleReceipt` (runs `ValidateAppend`; only its result may
be persisted) and `DenyReceipt` for the chain. There is deliberately no
standalone receipt-sealing helper. `Receipt.ValidateFor` is the read/replay
integrity check, not append authority. Direct validation rejects non-canonical
whitespace, digest case, ordering or timestamps. Validation, assembly and append
take a context; the verifier may be nil only when no absence claim exists.
