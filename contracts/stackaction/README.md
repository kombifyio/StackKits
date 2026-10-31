# stackaction

Generated from `foundation/stack_action.cue`, the sole StackAction contract
authority; this package is not a second schema. `internal/contractgen/stackactiongen`
writes the bundle here:

- `stackaction_gen.go`: Go wire projection for dependency-light consumers;
- `contract.ir.json`: neutral, versioned generator input;
- `openapi.yaml`: standalone request, response, vocabulary and path contract;
- `manifest.json`: generator version, wire version, CUE source SHA-256 and
  SHA-256 per output.

Regenerate the local projections and this bundle together, never by hand:

```bash
mise run contracts:stackaction:generate
```

`mise run contracts:stackaction:check` verifies committed parity without
writing. The bundle grants no provider, lease, credential, transport,
inventory, orchestration or execution authority.
