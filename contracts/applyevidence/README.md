# applyevidence

Provider-neutral producer contract for StackKits Architecture-v2 pre-Apply
evidence: a consumer supplies an exact request; an enrolled host/device agent
collects observations, seals expectation-bound Ed25519 receipts and returns one
canonical exact-set bundle.

- `CollectionRequest` binds the sorted expectation request to the generated
  manifest, executor identity, one canonical UTC evaluation instant and its own
  digest: the value-only handoff between StackKits and the evidence producer.
- `Collector` is the minimal producer SPI (request in, canonical result bytes
  out); observation, enrollment, signing, endpoints, credentials, transport and
  retry stay private to the implementation.

No trust enrollment, key persistence, host inspection, provider adapter,
transport, authorization or runtime execution lives here.
