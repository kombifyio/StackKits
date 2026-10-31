# Consumer compatibility evidence

Evidence contract for consumers of the runtime contracts; not a Go package.
Each consumer keeps one lock matching `consumer-lock.schema.json` that binds
the exact `github.com/kombifyio/stackkits` module version and zip checksum
(`go.sum`'s non-`/go.mod` `h1:` line), lists only the imported contract
packages and hashes every consumer-owned fixture, so a module bump fails until
the consumer refreshes and exercises its fixtures. Rules:
[Compatibility](../README.md#compatibility).
