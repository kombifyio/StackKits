# Runtime contracts

Dependency-light Go contracts for the runtime boundary between StackKits and
Techstack: stable type identities, closed validation rules, deterministic
digests and Golden JSON. Not a service, control plane, provider adapter or
state store. The packages are part of the StackKits module
(`github.com/kombifyio/stackkits/contracts/...`) and ship with its versions;
they have no release line of their own.

## Packages

| Import path (below `contracts/`) | Contract | Owner of the authority behind the wire |
| --- | --- | --- |
| `runtimelease` | Secret-free lease identity, validation, enrollment and signed snapshot DTOs | Techstack: validity, cancellation, desired state, enrollment, persistence |
| `providerexecutor/v1beta1` | Provider-neutral commands, receipts, evidence, digesting and lifecycle validation | Techstack: adapter selection, side effects, custody, durable ledger |
| `runtimeinventory` | Secret-free inventory read model generated from the Techstack inventory schema | Techstack: schema, collection, freshness, authorization |
| `stackaction` | Action wire generated from `foundation/stack_action.cue` | StackKits: lifecycle meaning, validation, execution, evidence |
| `runtimeexecutor` | Execution SPI for an authorized Apply grant: sealed request, artifact transport, invocation, result digest | Control plane: authorization, registration, retries, state, policy |
| `runtimeapply` | Durable operation journal and recovery contract for a sealed `runtimeexecutor` Apply | Consumer: persistence |
| `applyevidence` | Producer contract for Ed25519-sealed pre-Apply evidence bundles | Evidence producer: enrollment, keys, observation |
| `datasubject` | Account export and erasure request, signed receipt and completion rule | Coordinator and participants |
| `advancedcapability` | Generated Advanced-capability and trust-bundle wire from `contract.json` | Techstack issues, StackKits verifies |

`compatibility/` holds the consumer lock schema; it is not a Go package.

## Boundary

- Contract sources import only the standard library, `github.com/google/uuid`
  and other packages below `contracts/`. They carry no transport, database or
  process execution and name only reserved example hosts in URL literals.
  `mise run contracts:check` enforces this together with the generation
  checks below.
- No credential, provider SDK, adapter registry, database or product state
  belongs here. Wallet host delivery is outside these packages: no secret
  belongs in a fixture, command, log or receipt.
- No alias or wrapper exists for `vmlease`, `runtimeaction`, `serverruntime`
  or a previous provider-executor wire.

## Generated packages

- `stackaction`: edit `foundation/stack_action.cue` and run
  `mise run contracts:stackaction:generate`; the generator writes the bundle
  (`contract.ir.json`, `openapi.yaml`, `stackaction_gen.go`, `manifest.json`)
  in place. `mise run contracts:stackaction:check` fails on drift.
- `advancedcapability`: edit `advancedcapability/contract.json` and run
  `mise run contracts:advancedcapability:generate`; never edit the generated
  outputs.
- `runtimeinventory`: generated in Techstack from its inventory schema; the
  checked-in `manifest.json` binds the schema and `types.go` digests.

## Compatibility

JSON names, enum values, API and wire version strings, validation outcomes,
digest inputs and Golden fixtures are compatibility contracts. An additive
change keeps all of them and adds only optional fields. A breaking change
introduces a new contract version (a new import path) with before and after
Golden evidence and migrates every consumer directly, with no retired alias.
A consumer binds the exact StackKits module version and checksum in its lock
(`compatibility/consumer-lock.schema.json`) and exercises the fixtures it uses.

Apache-2.0, see [LICENSE](LICENSE).
