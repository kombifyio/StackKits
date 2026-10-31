# runtimeexecutor

`runtimeexecutor/v1beta1` is the provider-neutral execution SPI for an already
authorized StackKits Architecture v2 Apply grant. The control plane owns
authorization, registration, retries, durable state and policy; this package
seals and validates the immutable grant projection, transports content-addressed
artifact bytes without workspace paths, invokes one selected adapter, checks
exact runtime and health outcomes, and derives a canonical result digest bound
to the sealed request digest.

## Targets

- **Runtime targets** keep exact site, node, workload, paired image ref and
  digest, daemon and artifact refs. Provider-owned runtimes also keep the exact
  versioned owner adapter identity; module runtimes cannot carry it.
- **`selected-paas` workloads** keep one exact runtime adapter binding: adapter
  ID, provider/module versions and contract hashes, companion-agent module
  identities and only the content-addressed handoff artifacts of those modules.
  Executable artifacts and adapter handoffs are different classes.
- **Health targets** keep the materialized requirement ID and the logical source
  contract ref. Executable route health binds one runtime requirement, route,
  backend pool, placement and a closed address-free HTTP or TCP probe (protocol,
  port, bounded timeout, fixed method/path/status policy); no URL, host,
  redirect, credential, TLS identity or trust material is representable.
- **Home access bindings** (optional) keep the StackKits requirement, scope,
  contract hashes, opaque external binding and fabric refs and a validity of at
  most 24 hours. Each binding names and is referenced by exactly one runtime
  requirement, has a projection hash and is part of the sealed request digest.
- **Cloud backup-target bindings** (optional) follow the same envelope without
  Home authority: one runtime requirement, Site, node set,
  `offsite-object-backup` capability and contract hash, bound to opaque target
  and custody-attestation refs; own content hash, 24-hour maximum. No bucket,
  endpoint, credential, account, region, lease or lifecycle is representable.
- **Freshness**: both binding kinds are rechecked against the actual `InvokeAt`
  instant before the adapter runs. The control plane captures one UTC instant
  for authorization and calls `InvokeAt` with it; the serialized authorization
  timestamp is a digest-bound consistency value, never freshness proof.
- **Artifacts**: render-instance artifacts bind provider, module, unit,
  instance, logical output, site and node and are referenced exactly once;
  plan-owned artifacts bind owner ref and owner contract hash to the plan digest
  and are never runtime-referenced. A daemon's absolute runtime socket is the
  only path-shaped value.

## Execution channel

`ExecutionChannelRequest` and its factory/admission interfaces bind one opaque
channel to an exact single-Site, single-node Runtime+Health closure. Remote
admission can return its authenticated transport executor without local
Operations.

No provider SDK/configuration, credentials, database, network transport,
StackSpec type, endpoint, address, provider resource/account/region, discovery,
lease, generation, lifecycle or retry policy is representable.
